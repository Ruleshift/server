package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/xiangqi"
	"github.com/Ruleshift/server/internal/protocol"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"github.com/Ruleshift/server/internal/room"
	"github.com/gorilla/websocket"
)

func TestGatewayRejectsCommandBeforeAuth(t *testing.T) {
	server, _ := newTestGatewayServer(t)
	defer server.Close()

	client := dialTestWebSocket(t, server.URL)
	defer client.Close()

	client.Send(t, &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: protocol.CurrentVersion,
		ClientSequence:  1,
		Payload: &ruleshiftv1.ClientEnvelope_JoinRoom{
			JoinRoom: &ruleshiftv1.JoinRoomRequest{RoomId: "room-1"},
		},
	})

	errMsg := client.Read(t).GetError()
	if errMsg == nil {
		t.Fatalf("payload = nil, want ErrorMessage")
	}
	if errMsg.GetCode() != "auth_required" {
		t.Fatalf("error code = %q, want auth_required", errMsg.GetCode())
	}
}

func TestGatewayAuthJoinAndBroadcastDelta(t *testing.T) {
	server, _ := newTestGatewayServer(t)
	defer server.Close()

	player1 := dialTestWebSocket(t, server.URL)
	defer player1.Close()
	player2 := dialTestWebSocket(t, server.URL)
	defer player2.Close()

	authAndJoin(t, player1, "player-1", "room-1")
	authAndJoin(t, player2, "player-2", "room-1")

	player1.Send(t, gameMoveEnvelope(3, "room-1", "a0a1"))

	delta1 := readDelta(t, player1)
	delta2 := readDelta(t, player2)
	if delta1.String() != delta2.String() {
		t.Fatalf("players received different deltas:\n%#v\n%#v", delta1, delta2)
	}
	if delta1.GetXiangqi().GetStateHash() != 1 || delta1.GetNewRevision() != 1 || delta1.GetChangedByPlayerId() != "player-1" {
		t.Fatalf("delta = %#v, want hash 1 revision 1 by player-1", delta1)
	}
}

func TestGatewayJoinAtCurrentRevisionSkipsSnapshot(t *testing.T) {
	server, _ := newTestGatewayServer(t)
	defer server.Close()

	client := dialTestWebSocket(t, server.URL)
	defer client.Close()

	client.Send(t, authEnvelope(1, "player-1"))
	if client.Read(t).GetAuthOk() == nil {
		t.Fatalf("auth response payload = nil, want AuthOk")
	}

	client.Send(t, joinEnvelopeWithLastSeen(2, "room-1", 0))
	join := client.Read(t).GetJoinRoomOk()
	if join == nil {
		t.Fatalf("join response payload = nil, want JoinRoomOk")
	}
	if join.GetCurrentRevision() != 0 {
		t.Fatalf("join revision = %d, want 0", join.GetCurrentRevision())
	}

	client.Send(t, &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: protocol.CurrentVersion,
		ClientSequence:  3,
		Payload: &ruleshiftv1.ClientEnvelope_Ping{
			Ping: &ruleshiftv1.Ping{ClientTimeUnixMs: 123},
		},
	})
	if pong := client.Read(t).GetPong(); pong == nil {
		t.Fatalf("payload = nil, want Pong; a stale queued snapshot would be read first here")
	}
}

func TestGatewayNewClientReceivesSnapshotAfterStateChange(t *testing.T) {
	server, _ := newTestGatewayServer(t)
	defer server.Close()

	player1 := dialTestWebSocket(t, server.URL)
	defer player1.Close()

	authAndJoin(t, player1, "player-1", "room-1")
	player1.Send(t, gameMoveEnvelope(3, "room-1", "a0a1"))
	_ = readDelta(t, player1)

	player2 := dialTestWebSocket(t, server.URL)
	defer player2.Close()

	player2.Send(t, authEnvelope(1, "player-2"))
	authOk := player2.Read(t).GetAuthOk()
	if authOk == nil {
		t.Fatalf("auth response payload = nil, want AuthOk")
	}

	player2.Send(t, joinEnvelope(2, "room-1"))
	join := player2.Read(t).GetJoinRoomOk()
	if join == nil {
		t.Fatalf("join response payload = nil, want JoinRoomOk")
	}
	if join.GetCurrentRevision() != 1 {
		t.Fatalf("join revision = %d, want 1", join.GetCurrentRevision())
	}
	snapshot := player2.Read(t).GetStateSnapshot()
	if snapshot == nil {
		t.Fatalf("snapshot payload = nil, want StateSnapshot")
	}
	if snapshot.GetXiangqi().GetStateHash() != 1 || snapshot.GetRevision() != 1 {
		t.Fatalf("snapshot = hash %d revision %d, want 1/1", snapshot.GetXiangqi().GetStateHash(), snapshot.GetRevision())
	}
}

func TestGatewayReconnectReplacesOldSessionAndSendsSnapshot(t *testing.T) {
	server, _ := newTestGatewayServer(t)
	defer server.Close()

	oldConnection := dialTestWebSocket(t, server.URL)
	defer oldConnection.Close()

	authAndJoin(t, oldConnection, "player-1", "room-1")
	oldConnection.Send(t, gameMoveEnvelope(3, "room-1", "a0a1"))
	originalDelta := readDelta(t, oldConnection)
	if originalDelta.GetXiangqi().GetStateHash() != 1 || originalDelta.GetNewRevision() != 1 {
		t.Fatalf("delta = hash %d revision %d, want 1/1", originalDelta.GetXiangqi().GetStateHash(), originalDelta.GetNewRevision())
	}

	resumedConnection := dialTestWebSocket(t, server.URL)
	defer resumedConnection.Close()

	resumedConnection.Send(t, authEnvelope(1, "player-1"))
	authOk := resumedConnection.Read(t).GetAuthOk()
	if authOk == nil {
		t.Fatalf("resume auth response payload = nil, want AuthOk")
	}

	resumedConnection.Send(t, joinEnvelopeWithLastSeen(2, "room-1", 0))
	join := resumedConnection.Read(t).GetJoinRoomOk()
	if join == nil {
		t.Fatalf("resume join response payload = nil, want JoinRoomOk")
	}
	if join.GetCurrentRevision() != 1 {
		t.Fatalf("resume join revision = %d, want 1", join.GetCurrentRevision())
	}

	snapshot := resumedConnection.Read(t).GetStateSnapshot()
	if snapshot == nil {
		t.Fatalf("resume payload = nil, want StateSnapshot")
	}
	if snapshot.GetXiangqi().GetStateHash() != 1 || snapshot.GetRevision() != 1 {
		t.Fatalf("resume snapshot = hash %d revision %d, want 1/1", snapshot.GetXiangqi().GetStateHash(), snapshot.GetRevision())
	}

	oldConnection.ExpectClosed(t)
	_ = oldConnection.Write(gameMoveEnvelope(4, "room-1", "a0a1"))

	resumedConnection.Send(t, gameMoveEnvelope(3, "room-1", "a0a1"))
	resumedDelta := readDelta(t, resumedConnection)
	if resumedDelta.GetXiangqi().GetStateHash() != 2 || resumedDelta.GetNewRevision() != 2 {
		t.Fatalf("resumed delta = hash %d revision %d, want 2/2", resumedDelta.GetXiangqi().GetStateHash(), resumedDelta.GetNewRevision())
	}
}

func TestGatewayPingPong(t *testing.T) {
	server, _ := newTestGatewayServer(t)
	defer server.Close()

	client := dialTestWebSocket(t, server.URL)
	defer client.Close()

	client.Send(t, authEnvelope(1, "player-1"))
	if client.Read(t).GetAuthOk() == nil {
		t.Fatalf("auth response payload = nil, want AuthOk")
	}

	client.Send(t, &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: protocol.CurrentVersion,
		ClientSequence:  2,
		Payload: &ruleshiftv1.ClientEnvelope_Ping{
			Ping: &ruleshiftv1.Ping{ClientTimeUnixMs: 123},
		},
	})
	pong := client.Read(t).GetPong()
	if pong == nil {
		t.Fatalf("pong response payload = nil, want Pong")
	}
	if pong.GetClientTimeUnixMs() != 123 {
		t.Fatalf("pong client time = %d, want 123", pong.GetClientTimeUnixMs())
	}
}

func newTestGatewayServer(t *testing.T) (*httptest.Server, *Gateway) {
	t.Helper()

	registry := room.NewRegistry(room.RuntimeConfig{InputQueueSize: 128, GameModule: gatewayTestModule{}})
	gateway, err := New(Config{
		MaxMessageBytes:      64 * 1024,
		SessionSendQueueSize: 16,
		AuthTimeout:          time.Second,
	}, auth.NewMockProvider(), registry, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New gateway returned error: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(gateway.HandleWebSocket))
	t.Cleanup(gateway.Close)
	return server, gateway
}

func authAndJoin(t *testing.T, client *testWebSocketClient, playerID string, roomID string) {
	t.Helper()

	client.Send(t, authEnvelope(1, playerID))
	authOk := client.Read(t).GetAuthOk()
	if authOk == nil {
		t.Fatalf("auth response payload = nil, want AuthOk")
	}
	if authOk.GetPlayerId() != playerID {
		t.Fatalf("auth player = %q, want %q", authOk.GetPlayerId(), playerID)
	}

	client.Send(t, joinEnvelope(2, roomID))
	join := client.Read(t).GetJoinRoomOk()
	if join == nil {
		t.Fatalf("join response payload = nil, want JoinRoomOk")
	}
	if join.GetRoomId() != roomID {
		t.Fatalf("join room = %q, want %q", join.GetRoomId(), roomID)
	}
}

func authEnvelope(sequence uint64, playerID string) *ruleshiftv1.ClientEnvelope {
	return &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: protocol.CurrentVersion,
		ClientSequence:  sequence,
		Payload: &ruleshiftv1.ClientEnvelope_AuthRequest{
			AuthRequest: &ruleshiftv1.AuthRequest{Ticket: "mock:" + playerID},
		},
	}
}

func joinEnvelope(sequence uint64, roomID string) *ruleshiftv1.ClientEnvelope {
	return joinEnvelopeWithLastSeen(sequence, roomID, 0)
}

func joinEnvelopeWithLastSeen(sequence uint64, roomID string, lastSeenRevision uint64) *ruleshiftv1.ClientEnvelope {
	return &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: protocol.CurrentVersion,
		ClientSequence:  sequence,
		Payload: &ruleshiftv1.ClientEnvelope_JoinRoom{
			JoinRoom: &ruleshiftv1.JoinRoomRequest{
				RoomId:           roomID,
				LastSeenRevision: lastSeenRevision,
			},
		},
	}
}

func gameMoveEnvelope(sequence uint64, roomID string, move string) *ruleshiftv1.ClientEnvelope {
	return &ruleshiftv1.ClientEnvelope{
		ProtocolVersion: protocol.CurrentVersion,
		ClientSequence:  sequence,
		Payload: &ruleshiftv1.ClientEnvelope_GameCommand{
			GameCommand: &ruleshiftv1.GameCommand{
				RoomId: roomID,
				Command: &ruleshiftv1.GameCommand_DoMove{
					DoMove: &ruleshiftv1.DoMove{MoveUci: move},
				},
			},
		},
	}
}

func readDelta(t *testing.T, client *testWebSocketClient) *ruleshiftv1.StateDelta {
	t.Helper()
	delta := client.Read(t).GetStateDelta()
	if delta == nil {
		t.Fatalf("payload = nil, want StateDelta")
	}
	return delta
}

type testWebSocketClient struct {
	conn *websocket.Conn
}

func dialTestWebSocket(t *testing.T, rawURL string) *testWebSocketClient {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}

	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	default:
		parsed.Scheme = "ws"
	}

	conn, _, err := websocket.DefaultDialer.Dial(parsed.String(), nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}

	return &testWebSocketClient{conn: conn}
}

func (c *testWebSocketClient) Send(t *testing.T, env *ruleshiftv1.ClientEnvelope) {
	t.Helper()

	if err := c.Write(env); err != nil {
		t.Fatalf("write websocket frame: %v", err)
	}
}

func (c *testWebSocketClient) Write(env *ruleshiftv1.ClientEnvelope) error {
	payload, err := protocol.EncodeClientEnvelope(env)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func (c *testWebSocketClient) Read(t *testing.T) *ruleshiftv1.ServerEnvelope {
	t.Helper()

	if err := c.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	opcode, payload, err := c.conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket frame: %v", err)
	}
	if opcode != websocket.BinaryMessage {
		t.Fatalf("opcode = %d, want binary", opcode)
	}

	env, err := protocol.DecodeServerEnvelope(payload)
	if err != nil {
		t.Fatalf("decode server envelope: %v", err)
	}
	return env
}

func (c *testWebSocketClient) Close() {
	_ = c.conn.Close()
}

func (c *testWebSocketClient) ExpectClosed(t *testing.T) {
	t.Helper()

	if err := c.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err := c.conn.ReadMessage()
	if err == nil {
		t.Fatal("read websocket frame succeeded, want closed connection")
	}
}

type gatewayTestModule struct{}

type gatewayTestState struct {
	moves uint64
}

func (gatewayTestModule) Type() game.Type {
	return game.TypeXiangqi
}

func (gatewayTestModule) NewState(time.Time) (any, error) {
	return &gatewayTestState{}, nil
}

func (gatewayTestModule) PlayerJoined(state any, _ string) (any, error) {
	return state, nil
}

func (gatewayTestModule) Snapshot(state any) (game.Snapshot, error) {
	testState := state.(*gatewayTestState)
	base := game.Snapshot{
		Type:      game.TypeXiangqi,
		Status:    game.StatusActive,
		StateHash: testState.moves,
	}
	base.Payload = xiangqi.Snapshot{
		Snapshot:   base,
		FEN:        "gateway-test",
		SideToMove: xiangqi.SideRed,
	}
	return base, nil
}

func (gatewayTestModule) Apply(state any, command game.Command) (any, game.Delta, error) {
	testState := state.(*gatewayTestState)
	if command.Type != game.CommandDoMove {
		return state, game.Delta{}, game.ErrInvalidCommand
	}
	move, ok := command.Payload.(xiangqi.Move)
	if !ok {
		return state, game.Delta{}, game.ErrInvalidCommand
	}
	testState.moves++
	base := game.Delta{
		Type:        game.TypeXiangqi,
		CommandType: game.CommandDoMove,
		Status:      game.StatusActive,
		StateHash:   testState.moves,
	}
	base.CommandPayload = move
	base.Payload = xiangqi.Delta{
		Delta:      base,
		MoveUCI:    move.UCI,
		SideToMove: xiangqi.SideRed,
	}
	return testState, base, nil
}
