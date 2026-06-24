package gatewayv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/module"
	"github.com/Ruleshift/server/internal/protocol"
	ruleshiftv2 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv2"
	"github.com/Ruleshift/server/internal/roomcore"
	"github.com/gorilla/websocket"
)

const WebSocketPath = "/v2/ws"

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

type Config struct {
	MaxMessageBytes      int
	SessionSendQueueSize int
	AuthTimeout          time.Duration
}
type member struct {
	session *session
	viewer  module.Viewer
}
type hub struct{ members map[string]member }
type Gateway struct {
	cfg    Config
	auth   auth.Provider
	rooms  *roomcore.Registry
	logger *slog.Logger
	mu     sync.Mutex
	hubs   map[string]*hub
	ctx    context.Context
	cancel context.CancelFunc
}

func New(cfg Config, provider auth.Provider, rooms *roomcore.Registry, logger *slog.Logger) (*Gateway, error) {
	if provider == nil || rooms == nil {
		return nil, fmt.Errorf("auth provider and room registry are required")
	}
	if cfg.MaxMessageBytes <= 0 || cfg.SessionSendQueueSize <= 0 || cfg.AuthTimeout <= 0 {
		return nil, fmt.Errorf("gateway limits must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Gateway{cfg: cfg, auth: provider, rooms: rooms, logger: logger, hubs: map[string]*hub{}, ctx: ctx, cancel: cancel}, nil
}
func (g *Gateway) Close() {
	g.cancel()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, h := range g.hubs {
		for _, m := range h.members {
			m.session.Close()
		}
	}
}

func (g *Gateway) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(int64(g.cfg.MaxMessageBytes))
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	state := connection{session: newSession("", g.cfg.SessionSendQueueSize)}
	writerDone := make(chan error, 1)
	go g.write(ctx, conn, state.session, writerDone)
	defer func() {
		if state.room != nil && state.session != nil {
			g.remove(state.roomID, state.session.playerID, state.session.id)
			leaveCtx, leaveCancel := context.WithTimeout(context.Background(), time.Second)
			if _, leaveErr := state.room.Leave(leaveCtx, state.viewer); leaveErr == nil {
				_ = g.broadcastSnapshots(leaveCtx, state.roomID, state.room)
			}
			leaveCancel()
		}
		if state.session != nil {
			state.session.Close()
		}
		select {
		case <-writerDone:
		case <-time.After(time.Second):
		}
	}()
	_ = conn.SetReadDeadline(time.Now().Add(g.cfg.AuthTimeout))
	for {
		env, readErr := g.read(conn)
		if readErr != nil {
			return
		}
		if env.ClientSequence == 0 || env.ClientSequence <= state.lastSequence {
			g.sendError(ctx, state.session, "bad_sequence", "client_sequence must increase")
			continue
		}
		state.lastSequence = env.ClientSequence
		if !state.authenticated {
			request := env.GetAuthRequest()
			if request == nil {
				g.sendError(ctx, state.session, "auth_required", "AuthRequest must be first")
				continue
			}
			authCtx, authCancel := context.WithTimeout(ctx, g.cfg.AuthTimeout)
			identity, authErr := g.auth.AuthenticateTicket(authCtx, request.Ticket)
			authCancel()
			if authErr != nil {
				_ = state.session.Send(ctx, &ruleshiftv2.ServerEnvelope{Payload: &ruleshiftv2.ServerEnvelope_AuthFailed{AuthFailed: &ruleshiftv2.AuthFailed{Reason: "invalid auth ticket"}}})
				return
			}
			state.identity = identity
			state.authenticated = true
			state.session.playerID = identity.PlayerID
			_ = conn.SetReadDeadline(time.Time{})
			_ = state.session.Send(ctx, &ruleshiftv2.ServerEnvelope{Payload: &ruleshiftv2.ServerEnvelope_AuthOk{AuthOk: &ruleshiftv2.AuthOk{PlayerId: identity.PlayerID, DisplayName: identity.DisplayName}}})
			continue
		}
		if handleErr := g.handle(ctx, &state, env); handleErr != nil {
			code := "bad_request"
			if errors.Is(handleErr, roomcore.ErrRoomNotFound) {
				code = "room_not_found"
			}
			if errors.Is(handleErr, module.ErrUnavailable) {
				code = "module_unavailable"
			}
			if errors.Is(handleErr, module.ErrCommandRejected) {
				code = "command_rejected"
			}
			g.sendError(ctx, state.session, code, handleErr.Error())
		}
	}
}

type connection struct {
	authenticated bool
	identity      *auth.Identity
	session       *session
	room          *roomcore.Runtime
	roomID        string
	viewer        module.Viewer
	lastSequence  uint64
}

func (g *Gateway) handle(ctx context.Context, state *connection, env *ruleshiftv2.ClientEnvelope) error {
	switch payload := env.Payload.(type) {
	case *ruleshiftv2.ClientEnvelope_JoinRoom:
		return g.join(ctx, state, payload.JoinRoom)
	case *ruleshiftv2.ClientEnvelope_GameCommand:
		return g.apply(ctx, state, payload.GameCommand)
	case *ruleshiftv2.ClientEnvelope_SnapshotRequest:
		return g.snapshot(ctx, state, payload.SnapshotRequest)
	case *ruleshiftv2.ClientEnvelope_Ping:
		return state.session.Send(ctx, &ruleshiftv2.ServerEnvelope{Payload: &ruleshiftv2.ServerEnvelope_Pong{Pong: &ruleshiftv2.Pong{ClientTimeUnixMs: payload.Ping.ClientTimeUnixMs, ServerTimeUnixMs: time.Now().UnixMilli()}}})
	case *ruleshiftv2.ClientEnvelope_AuthRequest:
		return fmt.Errorf("already authenticated")
	default:
		return fmt.Errorf("unsupported payload")
	}
}
func (g *Gateway) join(ctx context.Context, state *connection, request *ruleshiftv2.JoinRoomRequest) error {
	if request == nil || request.RoomId == "" {
		return fmt.Errorf("room_id is required")
	}
	runtime, err := g.rooms.Get(ctx, request.RoomId)
	if err != nil {
		return err
	}
	joinMode := module.JoinModePlayer
	scope := module.ViewScopePlayer
	if request.JoinMode == ruleshiftv2.JoinMode_JOIN_MODE_SPECTATOR {
		joinMode = module.JoinModeSpectator
		scope = module.ViewScopePublic
		if state.identity.Permissions.Has(auth.PermissionViewFullState) {
			scope = module.ViewScopeFull
		}
	}
	viewer := module.Viewer{PlayerID: state.identity.PlayerID, JoinMode: joinMode, Scope: scope}
	snapshot, _, err := runtime.Join(ctx, viewer)
	if err != nil {
		return err
	}
	state.room = runtime
	state.roomID = request.RoomId
	state.viewer = viewer
	g.add(request.RoomId, state.session, viewer)
	if err = state.session.Send(ctx, joinOK(request.RoomId, snapshot.Revision, viewer, snapshot.Module)); err != nil {
		return err
	}
	return g.broadcastSnapshots(ctx, request.RoomId, runtime)
}
func (g *Gateway) apply(ctx context.Context, state *connection, request *ruleshiftv2.GameCommand) error {
	if state.room == nil || request == nil || request.RoomId != state.roomID {
		return fmt.Errorf("join the target room before sending commands")
	}
	command, err := module.MessageFromAny(request.Command)
	if err != nil {
		return err
	}
	members := g.members(state.roomID)
	viewers := make([]module.Viewer, 0, len(members))
	for _, m := range members {
		viewers = append(viewers, m.viewer)
	}
	deltas, err := state.room.Apply(ctx, roomcore.Command{PlayerID: state.identity.PlayerID, ExpectedRevision: request.ExpectedRevision, Payload: command}, viewers)
	if err != nil {
		return err
	}
	for index, delta := range deltas {
		if index >= len(members) {
			break
		}
		_ = members[index].session.Send(ctx, deltaEnvelope(delta))
	}
	return nil
}
func (g *Gateway) snapshot(ctx context.Context, state *connection, request *ruleshiftv2.SnapshotRequest) error {
	if state.room == nil || request == nil || request.RoomId != state.roomID {
		return fmt.Errorf("join the target room before requesting a snapshot")
	}
	snapshot, err := state.room.Snapshot(ctx, state.viewer)
	if err != nil {
		return err
	}
	return state.session.Send(ctx, snapshotEnvelope(snapshot))
}

func (g *Gateway) read(conn *websocket.Conn) (*ruleshiftv2.ClientEnvelope, error) {
	kind, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if kind != websocket.BinaryMessage {
		return nil, fmt.Errorf("binary protobuf frame required")
	}
	return protocol.DecodeClientEnvelope(payload)
}
func (g *Gateway) write(ctx context.Context, conn *websocket.Conn, s *session, done chan<- error) {
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case env, ok := <-s.send:
			if !ok {
				done <- nil
				return
			}
			sequence++
			env.ProtocolVersion = protocol.CurrentVersion
			env.ServerSequence = sequence
			payload, err := protocol.EncodeServerEnvelope(env)
			if err == nil && len(payload) > g.cfg.MaxMessageBytes {
				err = fmt.Errorf("server envelope too large")
			}
			if err == nil {
				err = conn.WriteMessage(websocket.BinaryMessage, payload)
			}
			if err != nil {
				done <- err
				return
			}
		}
	}
}
func (g *Gateway) sendError(ctx context.Context, s *session, code, message string) {
	if s != nil {
		_ = s.Send(ctx, &ruleshiftv2.ServerEnvelope{Payload: &ruleshiftv2.ServerEnvelope_Error{Error: &ruleshiftv2.ErrorMessage{Code: code, Message: message}}})
	}
}

func (g *Gateway) add(roomID string, s *session, viewer module.Viewer) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := g.hubs[roomID]
	if h == nil {
		h = &hub{members: map[string]member{}}
		g.hubs[roomID] = h
	}
	if old, ok := h.members[s.playerID]; ok && old.session.id != s.id {
		old.session.Close()
	}
	h.members[s.playerID] = member{session: s, viewer: viewer}
}
func (g *Gateway) remove(roomID, playerID string, id uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if h := g.hubs[roomID]; h != nil {
		if current, ok := h.members[playerID]; ok && current.session.id == id {
			delete(h.members, playerID)
		}
	}
}
func (g *Gateway) members(roomID string) []member {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := g.hubs[roomID]
	if h == nil {
		return nil
	}
	values := make([]member, 0, len(h.members))
	for _, value := range h.members {
		values = append(values, value)
	}
	return values
}
func (g *Gateway) broadcastSnapshots(ctx context.Context, roomID string, runtime *roomcore.Runtime) error {
	for _, m := range g.members(roomID) {
		snapshot, err := runtime.Snapshot(ctx, m.viewer)
		if err != nil {
			return err
		}
		if err = m.session.Send(ctx, snapshotEnvelope(snapshot)); err != nil && !errors.Is(err, errSessionFull) {
			return err
		}
	}
	return nil
}

func moduleRef(value module.ModuleRef) *ruleshiftv2.ModuleRef {
	return &ruleshiftv2.ModuleRef{ModuleId: value.ModuleID, Version: value.Version}
}
func snapshotEnvelope(value roomcore.SnapshotView) *ruleshiftv2.ServerEnvelope {
	return &ruleshiftv2.ServerEnvelope{Payload: &ruleshiftv2.ServerEnvelope_StateSnapshot{StateSnapshot: &ruleshiftv2.StateSnapshot{RoomId: value.RoomID, Revision: value.Revision, Module: moduleRef(value.Module), ViewDigest: value.View.Digest[:], State: value.View.Any()}}}
}
func deltaEnvelope(value roomcore.DeltaView) *ruleshiftv2.ServerEnvelope {
	return &ruleshiftv2.ServerEnvelope{Payload: &ruleshiftv2.ServerEnvelope_StateDelta{StateDelta: &ruleshiftv2.StateDelta{RoomId: value.RoomID, PreviousRevision: value.PreviousRevision, NewRevision: value.NewRevision, ChangedByPlayerId: value.ChangedBy, Module: moduleRef(value.Module), ViewDigest: value.View.Digest[:], NoVisibleChange: value.NoVisibleChange, Delta: value.View.Any()}}}
}
func joinOK(roomID string, revision uint64, viewer module.Viewer, ref module.ModuleRef) *ruleshiftv2.ServerEnvelope {
	mode := ruleshiftv2.JoinMode_JOIN_MODE_PLAYER
	if viewer.JoinMode == module.JoinModeSpectator {
		mode = ruleshiftv2.JoinMode_JOIN_MODE_SPECTATOR
	}
	scope := ruleshiftv2.ViewScope_VIEW_SCOPE_PLAYER
	if viewer.Scope == module.ViewScopePublic {
		scope = ruleshiftv2.ViewScope_VIEW_SCOPE_PUBLIC
	} else if viewer.Scope == module.ViewScopeFull {
		scope = ruleshiftv2.ViewScope_VIEW_SCOPE_FULL
	}
	return &ruleshiftv2.ServerEnvelope{Payload: &ruleshiftv2.ServerEnvelope_JoinRoomOk{JoinRoomOk: &ruleshiftv2.JoinRoomOk{RoomId: roomID, CurrentRevision: revision, JoinMode: mode, ViewScope: scope, Module: moduleRef(ref)}}}
}
