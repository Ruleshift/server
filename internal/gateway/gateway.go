package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/protocol"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"github.com/Ruleshift/server/internal/room"
	"github.com/gorilla/websocket"
)

const WebSocketPath = "/ws"

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

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

	ctx, cancel := context.WithCancel(context.Background())
	return &Gateway{
		cfg:    cfg,
		auth:   provider,
		rooms:  registry,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (g *Gateway) Close() {
	g.cancel()
}

func (g *Gateway) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := g.upgrade(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	session, err := newWebSocketSession(g.cfg.SessionSendQueueSize)
	if err != nil {
		g.logger.Debug("create websocket session", "error", err)
		return
	}
	writerDone := make(chan error, 1)
	go g.writeLoop(ctx, conn, session, writerDone)

	state := connectionState{session: session}
	authDeadlineCleared := false

	if err := conn.SetReadDeadline(time.Now().Add(g.cfg.AuthTimeout)); err != nil {
		g.logger.Debug("set auth read deadline", "error", err)
	}

	for {
		env, err := g.readClientEnvelope(conn)
		if err != nil {
			if !isWebSocketClosed(err) {
				g.logger.Debug("read client envelope", "error", err)
			}
			break
		}

		if err := state.acceptClientSequence(env.GetClientSequence()); err != nil {
			_ = g.send(ctx, state.session, errorEnvelope("bad_sequence", err.Error()))
			continue
		}

		if err := g.handleEnvelope(ctx, &state, env); err != nil {
			g.logger.Debug("handle client envelope", "error", err)
			_ = g.send(ctx, state.session, errorEnvelope("bad_request", err.Error()))
		}
		if state.authenticated && !authDeadlineCleared {
			if err := conn.SetReadDeadline(time.Time{}); err != nil {
				g.logger.Debug("clear auth read deadline", "error", err)
			}
			authDeadlineCleared = true
		}
	}

	state.session.Close(room.CloseReasonShutdown)
	cancel()
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		g.logger.Debug("timed out waiting for websocket writer")
	}
}

func (g *Gateway) upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	if !websocket.IsWebSocketUpgrade(r) {
		return nil, fmt.Errorf("websocket upgrade required")
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, fmt.Errorf("upgrade websocket: %w", err)
	}
	conn.SetReadLimit(int64(g.cfg.MaxMessageBytes))
	return conn, nil
}

func (g *Gateway) readClientEnvelope(conn *websocket.Conn) (*ruleshiftv1.ClientEnvelope, error) {
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		if errors.Is(err, websocket.ErrReadLimit) {
			return nil, fmt.Errorf("websocket message exceeds %d bytes", g.cfg.MaxMessageBytes)
		}
		return nil, err
	}
	if messageType != websocket.BinaryMessage {
		return nil, fmt.Errorf("unsupported websocket message type %d", messageType)
	}

	return protocol.DecodeClientEnvelope(payload)
}

func (g *Gateway) handleEnvelope(ctx context.Context, state *connectionState, env *ruleshiftv1.ClientEnvelope) error {
	if !state.authenticated {
		authRequest := env.GetAuthRequest()
		if authRequest == nil {
			return g.send(ctx, state.session, errorEnvelope("auth_required", "AuthRequest must be the first client envelope"))
		}
		return g.handleAuth(ctx, state, authRequest)
	}

	switch payload := env.GetPayload().(type) {
	case *ruleshiftv1.ClientEnvelope_AuthRequest:
		return g.send(ctx, state.session, errorEnvelope("already_authenticated", "connection is already authenticated"))
	case *ruleshiftv1.ClientEnvelope_JoinRoom:
		return g.handleJoinRoom(ctx, state, payload.JoinRoom)
	case *ruleshiftv1.ClientEnvelope_IntCommand:
		return g.handleIntCommand(ctx, state, payload.IntCommand)
	case *ruleshiftv1.ClientEnvelope_SnapshotRequest:
		return g.handleSnapshotRequest(ctx, state, payload.SnapshotRequest)
	case *ruleshiftv1.ClientEnvelope_Ping:
		return g.send(ctx, state.session, pongEnvelope(payload.Ping.GetClientTimeUnixMs(), time.Now().UnixMilli()))
	default:
		return fmt.Errorf("unknown client payload %T", env.GetPayload())
	}
}

func (g *Gateway) handleAuth(ctx context.Context, state *connectionState, payload *ruleshiftv1.AuthRequest) error {
	authCtx, cancel := context.WithTimeout(ctx, g.cfg.AuthTimeout)
	defer cancel()

	identity, err := g.auth.AuthenticateTicket(authCtx, payload.GetTicket())
	if err != nil {
		_ = g.send(ctx, state.session, authFailedEnvelope("invalid auth ticket"))
		return fmt.Errorf("authenticate ticket: %w", err)
	}

	if err := state.session.Bind(identity.PlayerID); err != nil {
		return fmt.Errorf("bind player session: %w", err)
	}

	state.identity = identity
	state.authenticated = true

	return g.send(ctx, state.session, authOkEnvelope(identity.PlayerID, identity.DisplayName))
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

	snapshot, err := runtime.Join(ctx, state.session)
	if err != nil {
		return fmt.Errorf("join room: %w", err)
	}

	state.room = runtime
	state.roomID = payload.GetRoomId()

	if err := g.send(ctx, state.session, joinRoomOkEnvelope(payload.GetRoomId(), snapshot.Revision)); err != nil {
		return err
	}
	return g.send(ctx, state.session, room.SnapshotEnvelope(snapshot))
}

func (g *Gateway) handleIntCommand(ctx context.Context, state *connectionState, payload *ruleshiftv1.IntCommand) error {
	if state.room == nil {
		return g.send(ctx, state.session, errorEnvelope("not_in_room", "join a room before sending commands"))
	}
	if payload.GetRoomId() != state.roomID {
		return g.send(ctx, state.session, errorEnvelope("wrong_room", "command room_id does not match joined room"))
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
		return g.send(ctx, state.session, errorEnvelope("command_rejected", err.Error()))
	}
	return nil
}

func (g *Gateway) handleSnapshotRequest(ctx context.Context, state *connectionState, payload *ruleshiftv1.SnapshotRequest) error {
	if state.room == nil || payload.GetRoomId() != state.roomID {
		return g.send(ctx, state.session, errorEnvelope("not_in_room", "join the requested room before snapshots"))
	}

	snapshot, err := state.room.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("room snapshot: %w", err)
	}
	return g.send(ctx, state.session, room.SnapshotEnvelope(snapshot))
}

func (g *Gateway) writeLoop(ctx context.Context, conn *websocket.Conn, session *websocketSession, done chan<- error) {
	var serverSequence uint64
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case env, ok := <-session.Outbound():
			if !ok {
				done <- nil
				return
			}
			serverSequence++
			env.ProtocolVersion = protocol.CurrentVersion
			env.ServerSequence = serverSequence

			payload, err := protocol.EncodeServerEnvelope(env)
			if err != nil {
				done <- err
				return
			}
			if len(payload) > g.cfg.MaxMessageBytes {
				done <- fmt.Errorf("server envelope exceeds %d bytes", g.cfg.MaxMessageBytes)
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				done <- err
				return
			}
		}
	}
}

func isWebSocketClosed(err error) bool {
	var closeErr *websocket.CloseError
	return errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent) || errors.As(err, &closeErr)
}

func (g *Gateway) send(ctx context.Context, session *websocketSession, payload *ruleshiftv1.ServerEnvelope) error {
	return session.Send(ctx, payload)
}

type connectionState struct {
	authenticated bool
	identity      *auth.Identity
	room          *room.RoomRuntime
	roomID        string
	lastSequence  uint64
	session       *websocketSession
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
