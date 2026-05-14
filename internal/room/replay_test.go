package room

import (
	"context"
	"testing"
	"time"
)

func TestReplayEventsRestoresIntegerStateFromEventLog(t *testing.T) {
	store := NewInMemoryEventStore()
	runtime, cancel, done := startTestRuntimeWithConfig(t, RuntimeConfig{
		InputQueueSize: 128,
		EventStore:     store,
	})
	defer stopTestRuntime(t, cancel, done)

	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()

	commands := []IntCommand{
		{RoomID: "room-1", PlayerID: "player-1", Operation: OperationAdd, Value: 5},
		{RoomID: "room-1", PlayerID: "player-1", Operation: OperationAdd, Value: 8},
		{RoomID: "room-1", PlayerID: "player-1", Operation: OperationSet, Value: 21},
	}

	for i, command := range commands {
		if _, err := runtime.Submit(ctx, command); err != nil {
			t.Fatalf("Submit %d returned error: %v", i, err)
		}
	}

	snapshot, err := runtime.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}

	events, err := store.List(ctx, "room-1")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4", len(events))
	}
	wantTypes := []EventType{EventTypeRoomCreated, EventTypeIntAdded, EventTypeIntAdded, EventTypeIntSet}
	for i, wantType := range wantTypes {
		if events[i].Type != wantType {
			t.Fatalf("event[%d] type = %s, want %s", i, events[i].Type, wantType)
		}
	}

	replayed, err := ReplayEvents(events)
	if err != nil {
		t.Fatalf("ReplayEvents returned error: %v", err)
	}

	if replayed.Value != snapshot.Value {
		t.Fatalf("replayed value = %d, want %d", replayed.Value, snapshot.Value)
	}
	if replayed.Revision != snapshot.Revision {
		t.Fatalf("replayed revision = %d, want %d", replayed.Revision, snapshot.Revision)
	}
	if replayed.Value != 21 || replayed.Revision != 3 {
		t.Fatalf("replayed state = value %d revision %d, want 21/3", replayed.Value, replayed.Revision)
	}
}
