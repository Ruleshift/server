package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/xiangqi"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"github.com/Ruleshift/server/internal/room"
)

func TestWebSocketSessionRejectsOverflow(t *testing.T) {
	session := newTestWebSocketSession(t, 1)

	if err := session.Send(context.Background(), testDeltaEnvelope(1)); err != nil {
		t.Fatalf("first Send returned error: %v", err)
	}
	if err := session.Send(context.Background(), testDeltaEnvelope(2)); !errors.Is(err, room.ErrPlayerSinkFull) {
		t.Fatalf("second Send error = %v, want ErrPlayerSinkFull", err)
	}
}

func TestWebSocketSessionCompactsQueueToSnapshot(t *testing.T) {
	session := newTestWebSocketSession(t, 2)

	if err := session.Send(context.Background(), testDeltaEnvelope(1)); err != nil {
		t.Fatalf("first Send returned error: %v", err)
	}
	if err := session.Send(context.Background(), testDeltaEnvelope(2)); err != nil {
		t.Fatalf("second Send returned error: %v", err)
	}
	if err := session.Send(context.Background(), testSnapshotEnvelope()); err != nil {
		t.Fatalf("snapshot Send returned error: %v", err)
	}

	if depth := session.QueueDepth(); depth != 1 {
		t.Fatalf("QueueDepth = %d, want 1", depth)
	}

	env := readQueuedEnvelope(t, session)
	snapshot := env.GetStateSnapshot()
	if snapshot == nil {
		t.Fatalf("payload = nil, want StateSnapshot")
	}
	if snapshot.GetXiangqi().GetStateHash() != 7 || snapshot.GetRevision() != 3 {
		t.Fatalf("snapshot = hash %d revision %d, want 7/3", snapshot.GetXiangqi().GetStateHash(), snapshot.GetRevision())
	}
}

func TestWebSocketSessionCloseRejectsSends(t *testing.T) {
	session := newTestWebSocketSession(t, 1)

	session.Close(room.CloseReasonShutdown)

	if !session.IsClosed() {
		t.Fatal("session is open, want closed")
	}
	if session.CloseReason() != room.CloseReasonShutdown {
		t.Fatalf("CloseReason = %q, want %q", session.CloseReason(), room.CloseReasonShutdown)
	}
	if err := session.Send(context.Background(), testDeltaEnvelope(1)); !errors.Is(err, room.ErrPlayerSinkClosed) {
		t.Fatalf("Send error = %v, want ErrPlayerSinkClosed", err)
	}
}

func newTestWebSocketSession(t *testing.T, queueSize int) *websocketSession {
	t.Helper()

	session, err := newWebSocketSession(queueSize)
	if err != nil {
		t.Fatalf("newWebSocketSession returned error: %v", err)
	}
	if err := session.Bind("player-1"); err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	return session
}

func testDeltaEnvelope(revision uint64) *ruleshiftv1.ServerEnvelope {
	base := game.Delta{
		Type:        game.TypeXiangqi,
		CommandType: game.CommandDoMove,
		StateHash:   revision,
		Status:      game.StatusActive,
	}
	base.CommandPayload = xiangqi.Move{UCI: "a0a1"}
	base.Payload = xiangqi.Delta{
		Delta:   base,
		MoveUCI: "a0a1",
	}

	return room.DeltaEnvelope(room.StateDelta{
		RoomID:            "room-1",
		PreviousRevision:  revision - 1,
		NewRevision:       revision,
		ChangedByPlayerID: "player-1",
		Game:              base,
	})
}

func testSnapshotEnvelope() *ruleshiftv1.ServerEnvelope {
	base := game.Snapshot{
		Type:      game.TypeXiangqi,
		StateHash: 7,
		Status:    game.StatusActive,
	}
	base.Payload = xiangqi.Snapshot{
		Snapshot: base,
		FEN:      "test:7",
	}

	return room.SnapshotEnvelope(room.StateSnapshot{
		RoomID:   "room-1",
		Revision: 3,
		Game:     base,
	})
}

func readQueuedEnvelope(t *testing.T, session *websocketSession) *ruleshiftv1.ServerEnvelope {
	t.Helper()

	select {
	case env, ok := <-session.Outbound():
		if !ok {
			t.Fatal("outbound channel is closed")
		}
		return env
	default:
		t.Fatal("outbound channel is empty")
		return nil
	}
}
