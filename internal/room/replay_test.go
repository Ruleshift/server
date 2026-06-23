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

func TestRegistryRestoresRoomFromEventStore(t *testing.T) {
	store := NewInMemoryEventStore()
	module := testGameModule{}
	first, cancelFirst, firstDone := startTestRuntimeWithConfig(t, RuntimeConfig{
		InputQueueSize: 16,
		EventStore:     store,
		GameModule:     module,
	})

	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()
	if _, err := first.Submit(ctx, GameCommand{
		RoomID:   "room-1",
		PlayerID: "player-1",
		Type:     game.CommandDoMove,
		Payload:  xiangqi.Move{UCI: "a0a1"},
	}); err != nil {
		t.Fatalf("first Submit returned error: %v", err)
	}
	stopTestRuntime(t, cancelFirst, firstDone)

	registry := NewRegistry(RuntimeConfig{
		InputQueueSize: 16,
		EventStore:     store,
		GameModule:     module,
	})
	restored, created, err := registry.GetOrCreate("room-1")
	if err != nil {
		t.Fatalf("GetOrCreate returned error: %v", err)
	}
	if !created {
		t.Fatal("restored runtime should be newly installed in the registry")
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- restored.Run(runCtx) }()
	defer stopTestRuntime(t, cancelRun, done)

	snapshot, err := restored.Snapshot(ctx)
	if err != nil {
		t.Fatalf("restored Snapshot returned error: %v", err)
	}
	if snapshot.Revision != 1 || snapshot.Game.StateHash != 1 {
		t.Fatalf("restored snapshot = revision %d hash %d, want 1/1", snapshot.Revision, snapshot.Game.StateHash)
	}
}
