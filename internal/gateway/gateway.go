package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Ruleshift/server/internal/auth"
	netx "github.com/Ruleshift/server/internal/net"
	"github.com/Ruleshift/server/internal/protocol"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"github.com/Ruleshift/server/internal/room"
)

const directSendQueueSize = 64

type Config struct {
	MaxMessageBytes      int
	SessionSendQueueSize int
	AuthTimeout          time.Duration
}

type Gateway struct {
	cfg    Config
	auth   auth.Provider
	rooms  *room.Registry
	logger *slog.Logger
	frame  protocol.FrameCodec
	ctx    context.Context
	cancel context.CancelFunc
}

func New(cfg Config, provider auth.Provider, registry *room.Registry, logger *slog.Logger) (*Gateway, error) {
	if provider == nil {
		return nil, fmt.Errorf("auth provider must not be nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("room registry must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.MaxMessageBytes <= 0 {
		return nil, fmt.Errorf("max message bytes must be positive")
	}
	if cfg.SessionSendQueueSize <= 0 {
		return nil, fmt.Errorf("session send queue size must be positive")
	}
	if cfg.AuthTimeout <= 0 {
		return nil, fmt.Errorf("auth timeout must be positive")
	}

	frame, err := protocol.NewFrameCodec(cfg.MaxMessageBytes)
	if err != nil {
		return nil, fmt.Errorf("create frame codec: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Gateway{
		cfg:    cfg,
		auth:   provider,
		rooms:  registry,
		logger: logger,
		frame:  frame,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (g *Gateway) Close() {
	g.cancel()
}

func (g *Gateway) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := netx.AcceptWebSocket(w, r, g.cfg.MaxMessageBytes+4)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	direct := make(chan *ruleshiftv1.ServerEnvelope, directSendQueueSize)
	writerDone := make(chan error, 1)
	go g.writeLoop(ctx, conn, direct, writerDone)

	state := connectionState{direct: direct}
	authDeadlineCleared := false

	if err := conn.SetReadDeadline(time.Now().Add(g.cfg.AuthTimeout)); err != nil {
		g.logger.Debug("set auth read deadline", "error", err)
	}

	for {
		env, err := g.readClientEnvelope(conn)
		if err != nil {
			if !errors.Is(err, netx.ErrWebSocketClosed) {
				g.logger.Debug("read client envelope", "error", err)
			}
			break
		}

		if err := state.acceptClientSequence(env.GetClientSequence()); err != nil {
			_ = g.send(ctx, direct, errorEnvelope("bad_sequence", err.Error()))
			continue
		}

		if err := g.handleEnvelope(ctx, &state, env); err != nil {
			g.logger.Debug("handle client envelope", "error", err)
			_ = g.send(ctx, direct, errorEnvelope("bad_request", err.Error()))
		}
		if state.authenticated && !authDeadlineCleared {
			if err := conn.SetReadDeadline(time.Time{}); err != nil {
				g.logger.Debug("clear auth read deadline", "error", err)
			}
			authDeadlineCleared = true
		}
	}

	if state.playerSession != nil {
		state.playerSession.Close(room.CloseReasonShutdown)
	}
	cancel()
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		g.logger.Debug("timed out waiting for websocket writer")
	}
}

func (g *Gateway) readClientEnvelope(conn *netx.WebSocketConn) (*ruleshiftv1.ClientEnvelope, error) {
	for {
		opcode, payload, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case netx.OpcodePing:
			if err := conn.WritePong(payload); err != nil {
				return nil, err
			}
			continue
		case netx.OpcodeBinary:
			var env ruleshiftv1.ClientEnvelope
			if err := g.frame.DecodeMessage(payload, &env); err != nil {
				return nil, err
			}
			if err := protocol.ValidateClientEnvelope(&env); err != nil {
				return nil, err
			}
			return &env, nil
		default:
			return nil, fmt.Errorf("unsupported websocket opcode %d", opcode)
		}
	}
}

func (g *Gateway) handleEnvelope(ctx context.Context, state *connectionState, env *ruleshiftv1.ClientEnvelope) error {
	if !state.authenticated {
		authRequest := env.GetAuthRequest()
		if authRequest == nil {
			return g.send(ctx, state.direct, errorEnvelope("auth_required", "AuthRequest must be the first client envelope"))
		}
		return g.handleAuth(ctx, state, authRequest)
	}

	switch payload := env.GetPayload().(type) {
	case *ruleshiftv1.ClientEnvelope_AuthRequest:
		return g.send(ctx, state.direct, errorEnvelope("already_authenticated", "connection is already authenticated"))
	case *ruleshiftv1.ClientEnvelope_JoinRoom:
		return g.handleJoinRoom(ctx, state, payload.JoinRoom)
	case *ruleshiftv1.ClientEnvelope_IntCommand:
		return g.handleIntCommand(ctx, state, payload.IntCommand)
	case *ruleshiftv1.ClientEnvelope_SnapshotRequest:
		return g.handleSnapshotRequest(ctx, state, payload.SnapshotRequest)
	case *ruleshiftv1.ClientEnvelope_Ping:
		return g.send(ctx, state.direct, pongEnvelope(payload.Ping.GetClientTimeUnixMs(), time.Now().UnixMilli()))
	default:
		return fmt.Errorf("unknown client payload %T", env.GetPayload())
	}
}

func (g *Gateway) handleAuth(ctx context.Context, state *connectionState, payload *ruleshiftv1.AuthRequest) error {
	authCtx, cancel := context.WithTimeout(ctx, g.cfg.AuthTimeout)
	defer cancel()

	identity, err := g.auth.AuthenticateTicket(authCtx, payload.GetTicket())
	if err != nil {
		_ = g.send(ctx, state.direct, authFailedEnvelope("invalid auth ticket"))
		return fmt.Errorf("authenticate ticket: %w", err)
	}

	session, err := room.NewBoundedPlayerSession(identity.PlayerID, g.cfg.SessionSendQueueSize)
	if err != nil {
		return fmt.Errorf("create player session: %w", err)
	}

	state.identity = identity
	state.playerSession = session
	state.authenticated = true

	go g.bridgeRoomMessages(ctx, session.Outbound(), state.direct)

	return g.send(ctx, state.direct, authOkEnvelope(identity.PlayerID, identity.DisplayName))
}

func (g *Gateway) handleJoinRoom(ctx context.Context, state *connectionState, payload *ruleshiftv1.JoinRoomRequest) error {
	runtime, created, err := g.rooms.GetOrCreate(payload.GetRoomId())
	if err != nil {
		return fmt.Errorf("get room: %w", err)
	}
	if created {
		go func() {
			if err := runtime.Run(g.ctx); err != nil {
				g.logger.Error("room runtime stopped", "room_id", payload.GetRoomId(), "error", err)
			}
		}()
	}

	snapshot, err := runtime.Join(ctx, state.playerSession)
	if err != nil {
		return fmt.Errorf("join room: %w", err)
	}

	state.room = runtime
	state.roomID = payload.GetRoomId()

	if err := g.send(ctx, state.direct, joinRoomOkEnvelope(payload.GetRoomId(), snapshot.Revision)); err != nil {
		return err
	}
	return g.send(ctx, state.direct, snapshotEnvelope(snapshot))
}

func (g *Gateway) handleIntCommand(ctx context.Context, state *connectionState, payload *ruleshiftv1.IntCommand) error {
	if state.room == nil {
		return g.send(ctx, state.direct, errorEnvelope("not_in_room", "join a room before sending commands"))
	}
	if payload.GetRoomId() != state.roomID {
		return g.send(ctx, state.direct, errorEnvelope("wrong_room", "command room_id does not match joined room"))
	}

	_, err := state.room.Submit(ctx, room.IntCommand{
		RoomID:           payload.GetRoomId(),
		PlayerID:         state.identity.PlayerID,
		Operation:        toRoomOperation(payload.GetOperation()),
		Value:            payload.GetValue(),
		ExpectedRevision: payload.GetExpectedRevision(),
		ReceivedAt:       time.Now().UTC(),
	})
	if err != nil {
		return g.send(ctx, state.direct, errorEnvelope("command_rejected", err.Error()))
	}
	return nil
}

func (g *Gateway) handleSnapshotRequest(ctx context.Context, state *connectionState, payload *ruleshiftv1.SnapshotRequest) error {
	if state.room == nil || payload.GetRoomId() != state.roomID {
		return g.send(ctx, state.direct, errorEnvelope("not_in_room", "join the requested room before snapshots"))
	}

	snapshot, err := state.room.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("room snapshot: %w", err)
	}
	return g.send(ctx, state.direct, snapshotEnvelope(snapshot))
}

func (g *Gateway) bridgeRoomMessages(ctx context.Context, roomMessages <-chan room.RoomMessage, direct chan<- *ruleshiftv1.ServerEnvelope) {
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-roomMessages:
			if !ok {
				return
			}

			var payload *ruleshiftv1.ServerEnvelope
			switch message.Kind {
			case room.MessageKindStateDelta:
				payload = deltaEnvelope(message.Delta)
			case room.MessageKindStateSnapshot:
				payload = snapshotEnvelope(message.Snapshot)
			default:
				continue
			}

			if err := g.send(ctx, direct, payload); err != nil {
				return
			}
		}
	}
}

func (g *Gateway) writeLoop(ctx context.Context, conn *netx.WebSocketConn, direct <-chan *ruleshiftv1.ServerEnvelope, done chan<- error) {
	var serverSequence uint64
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case env := <-direct:
			serverSequence++
			env.ProtocolVersion = protocol.CurrentVersion
			env.ServerSequence = serverSequence

			frame, err := g.frame.EncodeMessage(env)
			if err != nil {
				done <- err
				return
			}
			if err := conn.WriteBinary(frame); err != nil {
				done <- err
				return
			}
		}
	}
}

func (g *Gateway) send(ctx context.Context, direct chan<- *ruleshiftv1.ServerEnvelope, payload *ruleshiftv1.ServerEnvelope) error {
	select {
	case direct <- payload:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("send server payload: %w", ctx.Err())
	}
}

type connectionState struct {
	authenticated bool
	identity      *auth.Identity
	playerSession *room.BoundedPlayerSession
	room          *room.RoomRuntime
	roomID        string
	lastSequence  uint64
	direct        chan *ruleshiftv1.ServerEnvelope
}

func (s *connectionState) acceptClientSequence(sequence uint64) error {
	if sequence == 0 {
		return fmt.Errorf("client_sequence must be positive")
	}
	if s.lastSequence != 0 && sequence <= s.lastSequence {
		return fmt.Errorf("client_sequence must increase: got=%d last=%d", sequence, s.lastSequence)
	}
	s.lastSequence = sequence
	return nil
}

func toRoomOperation(operation ruleshiftv1.IntOperation) room.Operation {
	switch operation {
	case ruleshiftv1.IntOperation_INT_OPERATION_ADD:
		return room.OperationAdd
	case ruleshiftv1.IntOperation_INT_OPERATION_SET:
		return room.OperationSet
	default:
		return room.OperationUnspecified
	}
}

func fromRoomOperation(operation room.Operation) ruleshiftv1.IntOperation {
	switch operation {
	case room.OperationAdd:
		return ruleshiftv1.IntOperation_INT_OPERATION_ADD
	case room.OperationSet:
		return ruleshiftv1.IntOperation_INT_OPERATION_SET
	default:
		return ruleshiftv1.IntOperation_INT_OPERATION_UNSPECIFIED
	}
}

func authOkEnvelope(playerID string, displayName string) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_AuthOk{AuthOk: &ruleshiftv1.AuthOk{
		PlayerId:    playerID,
		DisplayName: displayName,
	}}}
}

func authFailedEnvelope(reason string) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_AuthFailed{AuthFailed: &ruleshiftv1.AuthFailed{Reason: reason}}}
}

func joinRoomOkEnvelope(roomID string, revision uint64) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_JoinRoomOk{JoinRoomOk: &ruleshiftv1.JoinRoomOk{
		RoomId:          roomID,
		CurrentRevision: revision,
	}}}
}

func snapshotEnvelope(snapshot room.StateSnapshot) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateSnapshot{StateSnapshot: &ruleshiftv1.StateSnapshot{
		RoomId:   snapshot.RoomID,
		Value:    snapshot.Value,
		Revision: snapshot.Revision,
	}}}
}

func deltaEnvelope(delta room.StateDelta) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_StateDelta{StateDelta: &ruleshiftv1.StateDelta{
		RoomId:            delta.RoomID,
		PreviousValue:     delta.PreviousValue,
		NewValue:          delta.NewValue,
		PreviousRevision:  delta.PreviousRevision,
		NewRevision:       delta.NewRevision,
		ChangedByPlayerId: delta.ChangedByPlayerID,
		Operation:         fromRoomOperation(delta.Operation),
		Operand:           delta.Operand,
	}}}
}

func errorEnvelope(code string, message string) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_Error{Error: &ruleshiftv1.ErrorMessage{
		Code:    code,
		Message: message,
	}}}
}

func pongEnvelope(clientTime int64, serverTime int64) *ruleshiftv1.ServerEnvelope {
	return &ruleshiftv1.ServerEnvelope{Payload: &ruleshiftv1.ServerEnvelope_Pong{Pong: &ruleshiftv1.Pong{
		ClientTimeUnixMs: clientTime,
		ServerTimeUnixMs: serverTime,
	}}}
}
