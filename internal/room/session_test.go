package room

import (
	"errors"
	"testing"
)

func TestBoundedPlayerSessionRejectsOverflow(t *testing.T) {
	session, err := NewBoundedPlayerSession("player-1", 1)
	if err != nil {
		t.Fatalf("NewBoundedPlayerSession returned error: %v", err)
	}

	if err := session.TrySend(RoomMessage{Kind: MessageKindStateDelta}); err != nil {
		t.Fatalf("first TrySend returned error: %v", err)
	}
	if err := session.TrySend(RoomMessage{Kind: MessageKindStateDelta}); !errors.Is(err, ErrSessionQueueFull) {
		t.Fatalf("second TrySend error = %v, want ErrSessionQueueFull", err)
	}
}

func TestBoundedPlayerSessionCompactsQueueToSnapshot(t *testing.T) {
	session, err := NewBoundedPlayerSession("player-1", 2)
	if err != nil {
		t.Fatalf("NewBoundedPlayerSession returned error: %v", err)
	}

	if err := session.TrySend(RoomMessage{Kind: MessageKindStateDelta}); err != nil {
		t.Fatalf("first TrySend returned error: %v", err)
	}
	if err := session.TrySend(RoomMessage{Kind: MessageKindStateDelta}); err != nil {
		t.Fatalf("second TrySend returned error: %v", err)
	}

	snapshot := StateSnapshot{RoomID: "room-1", Value: 7, Revision: 3}
	if err := session.TrySendSnapshot(snapshot); err != nil {
		t.Fatalf("TrySendSnapshot returned error: %v", err)
	}

	if depth := session.QueueDepth(); depth != 1 {
		t.Fatalf("QueueDepth = %d, want 1", depth)
	}

	message := readRoomMessage(t, session.Outbound())
	if message.Kind != MessageKindStateSnapshot {
		t.Fatalf("message kind = %d, want MessageKindStateSnapshot", message.Kind)
	}
	if message.Snapshot != snapshot {
		t.Fatalf("snapshot = %#v, want %#v", message.Snapshot, snapshot)
	}
}

func TestBoundedPlayerSessionCloseRejectsSends(t *testing.T) {
	session, err := NewBoundedPlayerSession("player-1", 1)
	if err != nil {
		t.Fatalf("NewBoundedPlayerSession returned error: %v", err)
	}

	session.Close(CloseReasonShutdown)

	if !session.IsClosed() {
		t.Fatal("session is open, want closed")
	}
	if session.CloseReason() != CloseReasonShutdown {
		t.Fatalf("CloseReason = %q, want %q", session.CloseReason(), CloseReasonShutdown)
	}
	if err := session.TrySend(RoomMessage{Kind: MessageKindStateDelta}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("TrySend error = %v, want ErrSessionClosed", err)
	}
}
