package room

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
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

func TestRoomRuntimeCompactsSlowConsumerQueueToSnapshot(t *testing.T) {
	runtime, cancel, done := startTestRuntimeWithConfig(t, RuntimeConfig{
		InputQueueSize:          8,
		SlowConsumerStrikeLimit: 2,
	})
	defer stopTestRuntime(t, cancel, done)

	session := newBoundedTestSink("player-1", 1)

	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()

	if _, err := runtime.Join(ctx, session); err != nil {
		t.Fatalf("Join returned error: %v", err)
	}

	first, err := runtime.Submit(ctx, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationAdd,
		Value:     1,
	})
	if err != nil {
		t.Fatalf("first Submit returned error: %v", err)
	}
	if first.BroadcastCount != 1 {
		t.Fatalf("first BroadcastCount = %d, want 1", first.BroadcastCount)
	}

	second, err := runtime.Submit(ctx, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationAdd,
		Value:     1,
	})
	if err != nil {
		t.Fatalf("second Submit returned error: %v", err)
	}
	if second.SnapshotCompactions != 1 {
		t.Fatalf("SnapshotCompactions = %d, want 1", second.SnapshotCompactions)
	}
	if session.IsClosed() {
		t.Fatal("session closed after first slow-consumer strike")
	}

	env := session.read(t)
	snapshot := env.GetStateSnapshot()
	if snapshot == nil {
		t.Fatalf("payload = nil, want StateSnapshot")
	}
	if snapshot.GetValue() != 2 || snapshot.GetRevision() != 2 {
		t.Fatalf("snapshot = value %d revision %d, want 2/2", snapshot.GetValue(), snapshot.GetRevision())
	}
}

func TestRoomRuntimeDisconnectsPersistentSlowConsumer(t *testing.T) {
	runtime, cancel, done := startTestRuntimeWithConfig(t, RuntimeConfig{
		InputQueueSize:          8,
		SlowConsumerStrikeLimit: 2,
	})
	defer stopTestRuntime(t, cancel, done)

	session := newBoundedTestSink("player-1", 1)

	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()

	if _, err := runtime.Join(ctx, session); err != nil {
		t.Fatalf("Join returned error: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, err := runtime.Submit(ctx, IntCommand{
			RoomID:    "room-1",
			PlayerID:  "player-1",
			Operation: OperationAdd,
			Value:     1,
		})
		if err != nil {
			t.Fatalf("Submit %d returned error: %v", i, err)
		}
		if i == 2 && result.DisconnectedSlowConsumers != 1 {
			t.Fatalf("DisconnectedSlowConsumers = %d, want 1", result.DisconnectedSlowConsumers)
		}
	}

	if !session.IsClosed() {
		t.Fatal("session is open, want closed")
	}
	if session.CloseReason() != CloseReasonSlowConsumer {
		t.Fatalf("CloseReason = %q, want %q", session.CloseReason(), CloseReasonSlowConsumer)
	}
}

func TestRoomRuntimeShutdownClosesJoinedSessions(t *testing.T) {
	runtime, cancel, done := startTestRuntime(t)

	session := newCollectingSink("player-1")
	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()

	if _, err := runtime.Join(ctx, session); err != nil {
		t.Fatalf("Join returned error: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for room runtime shutdown")
	}

	if !session.IsClosed() {
		t.Fatal("session is open after runtime shutdown")
	}
	if session.closeReason != CloseReasonShutdown {
		t.Fatalf("closeReason = %q, want %q", session.closeReason, CloseReasonShutdown)
	}
}

func TestRoomRuntimeReplacesSessionAndIgnoresOldCommands(t *testing.T) {
	runtime, cancel, done := startTestRuntime(t)
	defer stopTestRuntime(t, cancel, done)

	oldSession := newCollectingSink("player-1")
	newSession := newCollectingSink("player-1")

	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()

	if _, err := runtime.Join(ctx, oldSession); err != nil {
		t.Fatalf("join old session returned error: %v", err)
	}
	if _, err := runtime.Join(ctx, newSession); err != nil {
		t.Fatalf("join new session returned error: %v", err)
	}

	if !oldSession.IsClosed() {
		t.Fatal("old session is open, want closed after replacement")
	}
	if oldSession.CloseReason() != CloseReasonReplaced {
		t.Fatalf("old close reason = %q, want %q", oldSession.CloseReason(), CloseReasonReplaced)
	}

	_, err := runtime.SubmitFrom(ctx, oldSession, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationAdd,
		Value:     10,
	})
	if !errors.Is(err, ErrStalePlayerSession) {
		t.Fatalf("old SubmitFrom error = %v, want ErrStalePlayerSession", err)
	}

	result, err := runtime.SubmitFrom(ctx, newSession, IntCommand{
		RoomID:    "room-1",
		PlayerID:  "player-1",
		Operation: OperationAdd,
		Value:     3,
	})
	if err != nil {
		t.Fatalf("new SubmitFrom returned error: %v", err)
	}
	if result.Snapshot.Value != 3 || result.Snapshot.Revision != 1 {
		t.Fatalf("snapshot = value %d revision %d, want 3/1", result.Snapshot.Value, result.Snapshot.Revision)
	}

	snapshot, err := runtime.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if snapshot.Value != 3 || snapshot.Revision != 1 {
		t.Fatalf("runtime snapshot = value %d revision %d, want 3/1", snapshot.Value, snapshot.Revision)
	}
}

func startTestRuntime(t *testing.T) (*RoomRuntime, context.CancelFunc, <-chan error) {
	t.Helper()

	return startTestRuntimeWithConfig(t, RuntimeConfig{InputQueueSize: 128})
}

func startTestRuntimeWithConfig(t *testing.T, cfg RuntimeConfig) (*RoomRuntime, context.CancelFunc, <-chan error) {
	t.Helper()

	runtime, err := NewRuntime("room-1", cfg)
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
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for room runtime shutdown")
	}
}

type collectingSink struct {
	sessionID   uint64
	playerID    string
	mu          sync.Mutex
	received    []StateDelta
	closed      bool
	closeReason string
}

func newCollectingSink(playerID string) *collectingSink {
	return &collectingSink{sessionID: nextTestSessionID(), playerID: playerID}
}

func (s *collectingSink) SessionID() uint64 {
	return s.sessionID
}

func (s *collectingSink) PlayerID() string {
	return s.playerID
}

func (s *collectingSink) Send(ctx context.Context, msg *ruleshiftv1.ServerEnvelope) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrPlayerSinkClosed
	}
	delta := msg.GetStateDelta()
	if delta == nil {
		return nil
	}
	s.received = append(s.received, stateDeltaFromProto(delta))
	return nil
}

func (s *collectingSink) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *collectingSink) CloseReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeReason
}

func (s *collectingSink) Close(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.closeReason = reason
}

func (s *collectingSink) deltas() []StateDelta {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make([]StateDelta, len(s.received))
	copy(copied, s.received)
	return copied
}

type boundedTestSink struct {
	sessionID   uint64
	playerID    string
	capacity    int
	mu          sync.Mutex
	queue       []*ruleshiftv1.ServerEnvelope
	closed      bool
	closeReason string
}

func newBoundedTestSink(playerID string, capacity int) *boundedTestSink {
	return &boundedTestSink{
		sessionID: nextTestSessionID(),
		playerID:  playerID,
		capacity:  capacity,
		queue:     make([]*ruleshiftv1.ServerEnvelope, 0, capacity),
	}
}

func (s *boundedTestSink) SessionID() uint64 {
	return s.sessionID
}

func (s *boundedTestSink) PlayerID() string {
	return s.playerID
}

var testSessionID atomic.Uint64

func nextTestSessionID() uint64 {
	return testSessionID.Add(1)
}

func (s *boundedTestSink) Send(ctx context.Context, msg *ruleshiftv1.ServerEnvelope) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrPlayerSinkClosed
	}
	if msg.GetStateSnapshot() != nil {
		s.queue = s.queue[:0]
	}
	if len(s.queue) >= s.capacity {
		return ErrPlayerSinkFull
	}
	s.queue = append(s.queue, msg)
	return nil
}

func (s *boundedTestSink) Close(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true
	s.closeReason = reason
}

func (s *boundedTestSink) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *boundedTestSink) CloseReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeReason
}

func (s *boundedTestSink) read(t *testing.T) *ruleshiftv1.ServerEnvelope {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.queue) == 0 {
		t.Fatal("sink queue is empty")
	}
	env := s.queue[0]
	copy(s.queue, s.queue[1:])
	s.queue = s.queue[:len(s.queue)-1]
	return env
}

func stateDeltaFromProto(delta *ruleshiftv1.StateDelta) StateDelta {
	return StateDelta{
		RoomID:            delta.GetRoomId(),
		PreviousValue:     delta.GetPreviousValue(),
		NewValue:          delta.GetNewValue(),
		PreviousRevision:  delta.GetPreviousRevision(),
		NewRevision:       delta.GetNewRevision(),
		ChangedByPlayerID: delta.GetChangedByPlayerId(),
		Operation:         operationFromProto(delta.GetOperation()),
		Operand:           delta.GetOperand(),
	}
}

func operationFromProto(operation ruleshiftv1.IntOperation) Operation {
	switch operation {
	case ruleshiftv1.IntOperation_INT_OPERATION_ADD:
		return OperationAdd
	case ruleshiftv1.IntOperation_INT_OPERATION_SET:
		return OperationSet
	default:
		return OperationUnspecified
	}
}
