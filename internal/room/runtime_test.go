package room

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/internal/game/hiddennumber"
	"github.com/Ruleshift/server/internal/game/xiangqi"
	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

func testViewer(playerID string) game.Viewer {
	return game.Viewer{PlayerID: playerID, JoinMode: game.JoinModePlayer, Scope: game.ViewScopePlayer}
}

func TestRoomRuntimeBroadcastsSameDeltasToAllJoinedClients(t *testing.T) {
	runtime, cancel, done := startTestRuntime(t)
	defer stopTestRuntime(t, cancel, done)

	player1 := newCollectingSink("player-1")
	player2 := newCollectingSink("player-2")

	ctx, cancelJoin := context.WithTimeout(context.Background(), time.Second)
	defer cancelJoin()

	snapshot, err := runtime.Join(ctx, player1, testViewer(player1.PlayerID()))
	if err != nil {
		t.Fatalf("Join player-1 returned error: %v", err)
	}
	if snapshot.Revision != 0 || snapshot.Game.ViewHash != 0 {
		t.Fatalf("initial snapshot = hash %d revision %d, want 0/0", snapshot.Game.ViewHash, snapshot.Revision)
	}
	if _, err := runtime.Join(ctx, player2, testViewer(player2.PlayerID())); err != nil {
		t.Fatalf("Join player-2 returned error: %v", err)
	}

	result, err := runtime.Submit(ctx, testMoveCommand("player-1", "a0a1"))
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
	if got1[0].NewRevision != 1 || got1[0].Game.StateHash != 1 {
		t.Fatalf("delta = hash %d revision %d, want 1/1", got1[0].Game.StateHash, got1[0].NewRevision)
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

			result, err := runtime.Submit(ctx, testMoveCommand("player-concurrent", "a0a1"))
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
	if snapshot.Game.StateHash != commands {
		t.Fatalf("snapshot hash = %d, want %d", snapshot.Game.StateHash, commands)
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

	if _, err := runtime.Join(ctx, player, testViewer(player.PlayerID())); err != nil {
		t.Fatalf("Join returned error: %v", err)
	}

	result, err := runtime.Submit(ctx, GameCommand{
		RoomID:   "room-1",
		PlayerID: "player-1",
		Type:     game.CommandUnspecified,
	})
	if !errors.Is(err, game.ErrInvalidCommand) {
		t.Fatalf("Submit error = %v, want game.ErrInvalidCommand", err)
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

	if _, err := runtime.Join(ctx, session, testViewer(session.PlayerID())); err != nil {
		t.Fatalf("Join returned error: %v", err)
	}

	first, err := runtime.Submit(ctx, testMoveCommand("player-1", "a0a1"))
	if err != nil {
		t.Fatalf("first Submit returned error: %v", err)
	}
	if first.BroadcastCount != 1 {
		t.Fatalf("first BroadcastCount = %d, want 1", first.BroadcastCount)
	}

	second, err := runtime.Submit(ctx, testMoveCommand("player-1", "a0a1"))
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
	if snapshot.GetXiangqi().GetStateHash() != 2 || snapshot.GetRevision() != 2 {
		t.Fatalf("snapshot = hash %d revision %d, want 2/2", snapshot.GetXiangqi().GetStateHash(), snapshot.GetRevision())
	}
}

func TestRoomRuntimePersonalizesHiddenCompactionAndNoVisibleDelta(t *testing.T) {
	runtime, cancel, done := startTestRuntimeWithConfig(t, RuntimeConfig{
		InputQueueSize: 16, SlowConsumerStrikeLimit: 2, GameModule: hiddennumber.NewModule(),
	})
	defer stopTestRuntime(t, cancel, done)
	ctx, cancelCtx := context.WithTimeout(context.Background(), time.Second)
	defer cancelCtx()
	owner := newCollectingSink("owner")
	opponent := newCollectingSink("opponent")
	if _, err := runtime.Join(ctx, owner, testViewer("owner")); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Join(ctx, opponent, testViewer("opponent")); err != nil {
		t.Fatal(err)
	}
	slow := newBoundedTestSink("slow-watcher", 1)
	fast := newBoundedTestSink("fast-watcher", 4)
	publicSlow := game.Viewer{PlayerID: slow.PlayerID(), JoinMode: game.JoinModeSpectator, Scope: game.ViewScopePublic}
	publicFast := game.Viewer{PlayerID: fast.PlayerID(), JoinMode: game.JoinModeSpectator, Scope: game.ViewScopePublic}
	if _, err := runtime.Join(ctx, slow, publicSlow); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Join(ctx, fast, publicFast); err != nil {
		t.Fatal(err)
	}

	command := func(value int64) GameCommand {
		return GameCommand{RoomID: "room-1", PlayerID: "owner", Type: game.CommandSetSecret, Payload: hiddennumber.SetSecret{Value: value}}
	}
	if _, err := runtime.Submit(ctx, command(10)); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Submit(ctx, command(20))
	if err != nil {
		t.Fatal(err)
	}
	if second.SnapshotCompactions != 1 {
		t.Fatalf("snapshot compactions=%d want 1", second.SnapshotCompactions)
	}
	compacted := slow.read(t).GetStateSnapshot()
	if compacted == nil || compacted.GetRevision() != 4 {
		t.Fatalf("compacted snapshot=%v want revision 4", compacted)
	}
	if compacted.GetHiddenNumber().GetPlayers()[0].Secret != nil {
		t.Fatal("public compacted snapshot leaked owner secret")
	}
	firstDelta := fast.read(t).GetStateDelta()
	secondDelta := fast.read(t).GetStateDelta()
	if firstDelta == nil || firstDelta.GetNoVisibleChange() {
		t.Fatal("first has_secret change should be visible")
	}
	if secondDelta == nil || !secondDelta.GetNoVisibleChange() || secondDelta.GetHiddenNumber() != nil {
		t.Fatalf("second public delta=%v want no_visible_change without payload", secondDelta)
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

	if _, err := runtime.Join(ctx, session, testViewer(session.PlayerID())); err != nil {
		t.Fatalf("Join returned error: %v", err)
	}

	for i := 0; i < 3; i++ {
		result, err := runtime.Submit(ctx, testMoveCommand("player-1", "a0a1"))
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

	if _, err := runtime.Join(ctx, session, testViewer(session.PlayerID())); err != nil {
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

	if _, err := runtime.Join(ctx, oldSession, testViewer(oldSession.PlayerID())); err != nil {
		t.Fatalf("join old session returned error: %v", err)
	}
	if _, err := runtime.Join(ctx, newSession, testViewer(newSession.PlayerID())); err != nil {
		t.Fatalf("join new session returned error: %v", err)
	}

	if !oldSession.IsClosed() {
		t.Fatal("old session is open, want closed after replacement")
	}
	if oldSession.CloseReason() != CloseReasonReplaced {
		t.Fatalf("old close reason = %q, want %q", oldSession.CloseReason(), CloseReasonReplaced)
	}

	_, err := runtime.SubmitFrom(ctx, oldSession, testMoveCommand("player-1", "a0a1"))
	if !errors.Is(err, ErrStalePlayerSession) {
		t.Fatalf("old SubmitFrom error = %v, want ErrStalePlayerSession", err)
	}

	result, err := runtime.SubmitFrom(ctx, newSession, testMoveCommand("player-1", "a0a1"))
	if err != nil {
		t.Fatalf("new SubmitFrom returned error: %v", err)
	}
	if result.Snapshot.Game.StateHash != 1 || result.Snapshot.Revision != 1 {
		t.Fatalf("snapshot = hash %d revision %d, want 1/1", result.Snapshot.Game.StateHash, result.Snapshot.Revision)
	}

	snapshot, err := runtime.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if snapshot.Game.StateHash != 1 || snapshot.Revision != 1 {
		t.Fatalf("runtime snapshot = hash %d revision %d, want 1/1", snapshot.Game.StateHash, snapshot.Revision)
	}
}

func testMoveCommand(playerID string, move string) GameCommand {
	return GameCommand{
		RoomID:   "room-1",
		PlayerID: playerID,
		Type:     game.CommandDoMove,
		Payload:  xiangqi.Move{FromSquare: 1, ToSquare: 2, UCI: move},
	}
}

func startTestRuntime(t *testing.T) (*RoomRuntime, context.CancelFunc, <-chan error) {
	t.Helper()

	return startTestRuntimeWithConfig(t, RuntimeConfig{InputQueueSize: 128})
}

func startTestRuntimeWithConfig(t *testing.T, cfg RuntimeConfig) (*RoomRuntime, context.CancelFunc, <-chan error) {
	t.Helper()

	if cfg.GameModule == nil {
		cfg.GameModule = testGameModule{}
	}

	fmt.Println("CREATED RUNTIME WITH module ", cfg.GameModule, cfg.GameModule.Type())

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
	gameDelta := game.Delta{
		Type: game.TypeXiangqi,
	}
	if protoDelta := delta.GetXiangqi(); protoDelta != nil {
		gameDelta.CommandType = commandTypeFromProto(protoDelta.GetCommandType())
		gameDelta.Status = statusFromProto(protoDelta.GetStatus())
		gameDelta.StateHash = protoDelta.GetStateHash()

		move := xiangqi.Move{
			FromSquare: protoDelta.GetFromSquare(),
			ToSquare:   protoDelta.GetToSquare(),
			UCI:        protoDelta.GetMoveUci(),
		}
		if gameDelta.CommandType == game.CommandDoMove {
			gameDelta.CommandPayload = move
		}

		payload := xiangqi.Delta{
			Delta:                 gameDelta,
			MoveUCI:               protoDelta.GetMoveUci(),
			FromSquare:            protoDelta.GetFromSquare(),
			ToSquare:              protoDelta.GetToSquare(),
			SideToMove:            sideFromProto(protoDelta.GetSideToMove()),
			WinnerPlayerID:        protoDelta.GetWinnerPlayerId(),
			DrawOfferedByPlayerID: protoDelta.GetDrawOfferedByPlayerId(),
		}
		for _, update := range protoDelta.GetSquareUpdates() {
			payload.SquareUpdates = append(payload.SquareUpdates, xiangqi.SquareUpdate{
				Square: update.GetSquare(),
				Piece:  update.GetPiece(),
			})
		}
		gameDelta.Payload = payload
	}

	return StateDelta{
		RoomID:            delta.GetRoomId(),
		PreviousRevision:  delta.GetPreviousRevision(),
		NewRevision:       delta.GetNewRevision(),
		ChangedByPlayerID: delta.GetChangedByPlayerId(),
		Game:              gameDelta,
	}
}

func commandTypeFromProto(commandType ruleshiftv1.GameCommandType) game.CommandType {
	switch commandType {
	case ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_DO_MOVE:
		return game.CommandDoMove
	case ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_RESIGN:
		return game.CommandResign
	case ruleshiftv1.GameCommandType_GAME_COMMAND_TYPE_OFFER_DRAW:
		return game.CommandOfferDraw
	default:
		return game.CommandUnspecified
	}
}

func sideFromProto(side ruleshiftv1.XiangqiSide) xiangqi.Side {
	switch side {
	case ruleshiftv1.XiangqiSide_XIANGQI_SIDE_RED:
		return xiangqi.SideRed
	case ruleshiftv1.XiangqiSide_XIANGQI_SIDE_BLACK:
		return xiangqi.SideBlack
	default:
		return xiangqi.SideUnspecified
	}
}

func statusFromProto(status ruleshiftv1.GameStatus) game.Status {
	switch status {
	case ruleshiftv1.GameStatus_GAME_STATUS_ACTIVE:
		return game.StatusActive
	case ruleshiftv1.GameStatus_GAME_STATUS_RESIGNED:
		return game.StatusResigned
	case ruleshiftv1.GameStatus_GAME_STATUS_DRAW_OFFERED:
		return game.StatusDrawOffered
	case ruleshiftv1.GameStatus_GAME_STATUS_DRAWN:
		return game.StatusDrawn
	default:
		return game.StatusUnspecified
	}
}
