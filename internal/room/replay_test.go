package room

import (
	"context"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/xiangqi"
)

func TestReplayEventsRestoresGameStateFromEventLog(t *testing.T) {
	store := NewInMemoryEventStore()
	module := testGameModule{}
	runtime, cancel, done := startTestRuntimeWithConfig(t, RuntimeConfig{
		InputQueueSize: 128,
		EventStore:     store,
		GameModule:     module,
	})
	defer stopTestRuntime(t, cancel, done)

	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()

	commands := []GameCommand{
		{RoomID: "room-1", PlayerID: "player-1", Type: game.CommandDoMove, Payload: xiangqi.Move{UCI: "a0a1"}},
		{RoomID: "room-1", PlayerID: "player-1", Type: game.CommandDoMove, Payload: xiangqi.Move{UCI: "a0a1"}},
		{RoomID: "room-1", PlayerID: "player-1", Type: game.CommandDoMove, Payload: xiangqi.Move{UCI: "a0a1"}},
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
	wantTypes := []EventType{EventTypeRoomCreated, EventTypeGameMoveApplied, EventTypeGameMoveApplied, EventTypeGameMoveApplied}
	for i, wantType := range wantTypes {
		if events[i].Type != wantType {
			t.Fatalf("event[%d] type = %s, want %s", i, events[i].Type, wantType)
		}
	}

	replayed, err := ReplayEvents(module, events)
	if err != nil {
		t.Fatalf("ReplayEvents returned error: %v", err)
	}
	replayedSnapshot, err := BuildSnapshot(module, replayed)
	if err != nil {
		t.Fatalf("BuildSnapshot returned error: %v", err)
	}

	if replayedSnapshot.Game.StateHash != snapshot.Game.StateHash {
		t.Fatalf("replayed state hash = %d, want %d", replayedSnapshot.Game.StateHash, snapshot.Game.StateHash)
	}
	if replayed.Revision != snapshot.Revision {
		t.Fatalf("replayed revision = %d, want %d", replayed.Revision, snapshot.Revision)
	}
	if replayedSnapshot.Game.StateHash != 3 || replayed.Revision != 3 {
		t.Fatalf("replayed state = hash %d revision %d, want 3/3", replayedSnapshot.Game.StateHash, replayed.Revision)
	}
}
