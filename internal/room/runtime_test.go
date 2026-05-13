package room

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestRoomRuntimeBroadcastsSameDeltasToAllJoinedClients(t *testing.T) {
	runtime, cancel, done := startTestRuntime(t)
	defer stopTestRuntime(t, cancel, done)

	player1 := newCollectingSink("player-1")
	player2 := newCollectingSink("player-2")

	ctx, cancelJoin := context.WithTimeout(context.Background(), time.Second)
	defer cancelJoin()

	snapshot, err := runtime.Join(ctx, player1)
	if err != nil {
		t.Fatalf("Join player-1 returned error: %v", err)
	}
	if snapshot.Revision != 0 || snapshot.Value != 0 {
		t.Fatalf("initial snapshot = value %d revision %d, want 0/0", snapshot.Value, snapshot.Revision)
	}
	if _, err := runtime.Join(ctx, player2); err != nil {
		t.Fatalf("Join player-2 returned error: %v", err)
	}

	result, err := runtime.Submit(ctx, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationAdd,
		Value:     5,
	})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if result.BroadcastCount != 2 {
		t.Fatalf("BroadcastCount = %d, want 2", result.BroadcastCount)
	}

	got1 := player1.deltas()
	got2 := player2.deltas()
	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("delta counts = %d/%d, want 1/1", len(got1), len(got2))
	}
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("joined clients received different deltas:\n%#v\n%#v", got1, got2)
	}
	if got1[0].NewRevision != 1 || got1[0].NewValue != 5 {
		t.Fatalf("delta = value %d revision %d, want 5/1", got1[0].NewValue, got1[0].NewRevision)
	}
}

func TestRoomRuntimeConcurrentCommandsHaveLinearRevisions(t *testing.T) {
	runtime, cancel, done := startTestRuntime(t)
	defer stopTestRuntime(t, cancel, done)

	const commands = 64
	results := make(chan CommandResult, commands)
	errs := make(chan error, commands)

	for i := 0; i < commands; i++ {
		go func(i int) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			result, err := runtime.Submit(ctx, IntCommand{
				RoomID:    "room-1",
				PlayerID:  "player-concurrent",
				Operation: OperationAdd,
				Value:     1,
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(i)
	}

	revisions := make([]int, 0, commands)
	for i := 0; i < commands; i++ {
		select {
		case err := <-errs:
			t.Fatalf("Submit returned error: %v", err)
		case result := <-results:
			revisions = append(revisions, int(result.Delta.NewRevision))
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent command result")
		}
	}

	sort.Ints(revisions)
	for i, revision := range revisions {
		want := i + 1
		if revision != want {
			t.Fatalf("revision[%d] = %d, want %d; all revisions=%v", i, revision, want, revisions)
		}
	}

	ctx, cancelSnapshot := context.WithTimeout(context.Background(), time.Second)
	defer cancelSnapshot()

	snapshot, err := runtime.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if snapshot.Value != commands {
		t.Fatalf("snapshot value = %d, want %d", snapshot.Value, commands)
	}
	if snapshot.Revision != commands {
		t.Fatalf("snapshot revision = %d, want %d", snapshot.Revision, commands)
	}
}

func TestRoomRuntimeRejectsInvalidCommandWithoutBroadcast(t *testing.T) {
	runtime, cancel, done := startTestRuntime(t)
	defer stopTestRuntime(t, cancel, done)

	player := newCollectingSink("player-1")
	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()

	if _, err := runtime.Join(ctx, player); err != nil {
		t.Fatalf("Join returned error: %v", err)
	}

	result, err := runtime.Submit(ctx, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationUnspecified,
	})
	if !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("Submit error = %v, want ErrInvalidOperation", err)
	}
	if result.BroadcastCount != 0 {
		t.Fatalf("BroadcastCount = %d, want 0", result.BroadcastCount)
	}
	if got := len(player.deltas()); got != 0 {
		t.Fatalf("deltas sent = %d, want 0", got)
	}
}

func startTestRuntime(t *testing.T) (*RoomRuntime, context.CancelFunc, <-chan error) {
	t.Helper()

	runtime, err := NewRuntime("room-1", RuntimeConfig{InputQueueSize: 128})
	if err != nil {
		t.Fatalf("NewRuntime returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runtime.Run(ctx)
	}()

	return runtime, cancel, done
}

func stopTestRuntime(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for room runtime shutdown")
	}
}

type collectingSink struct {
	playerID string
	mu       sync.Mutex
	received []StateDelta
}

func newCollectingSink(playerID string) *collectingSink {
	return &collectingSink{playerID: playerID}
}

func (s *collectingSink) PlayerID() string {
	return s.playerID
}

func (s *collectingSink) TrySendDelta(delta StateDelta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, delta)
	return nil
}

func (s *collectingSink) deltas() []StateDelta {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make([]StateDelta, len(s.received))
	copy(copied, s.received)
	return copied
}
