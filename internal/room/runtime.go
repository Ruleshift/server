package room

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/game"
)

var ErrNilPlayerSink = errors.New("player sink must not be nil")
var ErrRuntimeClosed = errors.New("room runtime is closed")
var ErrStalePlayerSession = errors.New("player session is no longer current")

type RuntimeConfig struct {
	InputQueueSize          int
	SlowConsumerStrikeLimit int
	EventStoreTimeout       time.Duration
	EventStore              EventStore
	GameModule              game.Module
}

type RuntimeCommand struct {
	Command      GameCommand
	CommandSink  PlayerSink
	SnapshotOnly bool
	SnapshotSink PlayerSink
	SnapshotSent *snapshotSentRecord
	JoinSink     PlayerSink
	JoinViewer   game.Viewer
	LeaveSink    PlayerSink
	LeaveReason  string
	Reply        chan<- CommandResult
}

type snapshotSentRecord struct {
	PlayerID string
}

type CommandResult struct {
	Snapshot                  StateSnapshot
	ProjectedSnapshot         ProjectedStateSnapshot
	Delta                     StateDelta
	BroadcastCount            int
	SnapshotCompactions       int
	DisconnectedSlowConsumers int
	BroadcastFailures         []BroadcastFailure
	Err                       error
}

type roomSubscriber struct {
	sink   PlayerSink
	viewer game.Viewer
}

type BroadcastFailure struct {
	PlayerID string
	Err      error
}

type RoomRuntime struct {
	state       RoomState
	input       chan RuntimeCommand
	done        chan struct{}
	closeOnce   sync.Once
	cfg         RuntimeConfig
	subscribers map[string]roomSubscriber
	slowStrikes map[string]int
	eventStore  EventStore
	gameModule  game.Module
	clock       func() time.Time
}

func NewRuntime(roomID string, cfg RuntimeConfig) (*RoomRuntime, error) {
	if roomID == "" {
		return nil, ErrEmptyRoomID
	}
	if cfg.InputQueueSize <= 0 {
		return nil, fmt.Errorf("room input queue size must be positive")
	}
	if cfg.SlowConsumerStrikeLimit <= 0 {
		cfg.SlowConsumerStrikeLimit = 2
	}
	if cfg.GameModule == nil {
		return nil, ErrNilGameModule
	}

	now := time.Now().UTC()
	gameState, err := cfg.GameModule.NewState(now)
	if err != nil {
		return nil, fmt.Errorf("create game state: %w", err)
	}
	state := NewState(roomID, cfg.GameModule.Type(), gameState, now)
	return newRuntime(state, cfg, true)
}

// NewRuntimeFromState restores a runtime without appending another RoomCreated
// event. Callers must obtain state by replaying the configured event store.
func NewRuntimeFromState(state RoomState, cfg RuntimeConfig) (*RoomRuntime, error) {
	if state.RoomID == "" {
		return nil, ErrEmptyRoomID
	}
	if cfg.GameModule == nil {
		return nil, ErrNilGameModule
	}
	if state.GameType != cfg.GameModule.Type() {
		return nil, fmt.Errorf("restored game type mismatch: state=%d module=%d", state.GameType, cfg.GameModule.Type())
	}
	if state.GameState == nil {
		return nil, fmt.Errorf("restored game state must not be nil")
	}
	return newRuntime(state, cfg, false)
}

func newRuntime(state RoomState, cfg RuntimeConfig, appendCreated bool) (*RoomRuntime, error) {
	if cfg.InputQueueSize <= 0 {
		return nil, fmt.Errorf("room input queue size must be positive")
	}
	if cfg.SlowConsumerStrikeLimit <= 0 {
		cfg.SlowConsumerStrikeLimit = 2
	}
	if cfg.EventStoreTimeout <= 0 {
		cfg.EventStoreTimeout = 5 * time.Second
	}
	runtime := &RoomRuntime{
		state:       state,
		input:       make(chan RuntimeCommand, cfg.InputQueueSize),
		done:        make(chan struct{}),
		cfg:         cfg,
		subscribers: make(map[string]roomSubscriber),
		slowStrikes: make(map[string]int),
		eventStore:  cfg.EventStore,
		gameModule:  cfg.GameModule,
		clock: func() time.Time {
			return time.Now().UTC()
		},
	}
	if appendCreated {
		if err := runtime.appendEvent(NewRoomCreatedEvent(state)); err != nil {
			return nil, err
		}
	}
	return runtime, nil
}

func (r *RoomRuntime) Submit(ctx context.Context, cmd GameCommand) (CommandResult, error) {
	return r.submit(ctx, nil, cmd)
}

func (r *RoomRuntime) SubmitFrom(ctx context.Context, sink PlayerSink, cmd GameCommand) (CommandResult, error) {
	if sink == nil {
		return CommandResult{}, ErrNilPlayerSink
	}
	return r.submit(ctx, sink, cmd)
}

func (r *RoomRuntime) submit(ctx context.Context, sink PlayerSink, cmd GameCommand) (CommandResult, error) {
	if r.isClosed() {
		return CommandResult{}, ErrRuntimeClosed
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{Command: cmd, CommandSink: sink, Reply: reply}

	select {
	case r.input <- request:
	case <-r.done:
		return CommandResult{}, ErrRuntimeClosed
	case <-ctx.Done():
		return CommandResult{}, fmt.Errorf("submit room command: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result, result.Err
	case <-r.done:
		return CommandResult{}, ErrRuntimeClosed
	case <-ctx.Done():
		return CommandResult{}, fmt.Errorf("wait room command result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) Snapshot(ctx context.Context) (StateSnapshot, error) {
	if r.isClosed() {
		return StateSnapshot{}, ErrRuntimeClosed
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{SnapshotOnly: true, Reply: reply}

	select {
	case r.input <- request:
	case <-r.done:
		return StateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("submit room snapshot request: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result.Snapshot, result.Err
	case <-r.done:
		return StateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("wait room snapshot result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) SnapshotFor(ctx context.Context, sink PlayerSink) (ProjectedStateSnapshot, error) {
	if sink == nil {
		return ProjectedStateSnapshot{}, ErrNilPlayerSink
	}
	if r.isClosed() {
		return ProjectedStateSnapshot{}, ErrRuntimeClosed
	}
	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{SnapshotSink: sink, Reply: reply}
	select {
	case r.input <- request:
	case <-r.done:
		return ProjectedStateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return ProjectedStateSnapshot{}, fmt.Errorf("submit projected snapshot request: %w", ctx.Err())
	}
	select {
	case result := <-reply:
		return result.ProjectedSnapshot, result.Err
	case <-r.done:
		return ProjectedStateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return ProjectedStateSnapshot{}, fmt.Errorf("wait projected snapshot result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) RecordSnapshotSent(ctx context.Context, playerID string) error {
	if r.isClosed() {
		return ErrRuntimeClosed
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{SnapshotSent: &snapshotSentRecord{PlayerID: playerID}, Reply: reply}

	select {
	case r.input <- request:
	case <-r.done:
		return ErrRuntimeClosed
	case <-ctx.Done():
		return fmt.Errorf("submit snapshot-sent event: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result.Err
	case <-r.done:
		return ErrRuntimeClosed
	case <-ctx.Done():
		return fmt.Errorf("wait snapshot-sent event: %w", ctx.Err())
	}
}

func (r *RoomRuntime) Join(ctx context.Context, sink PlayerSink, viewer game.Viewer) (ProjectedStateSnapshot, error) {
	if sink == nil {
		return ProjectedStateSnapshot{}, ErrNilPlayerSink
	}
	if r.isClosed() {
		return ProjectedStateSnapshot{}, ErrRuntimeClosed
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{JoinSink: sink, JoinViewer: viewer, Reply: reply}

	select {
	case r.input <- request:
	case <-r.done:
		return ProjectedStateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return ProjectedStateSnapshot{}, fmt.Errorf("submit room join request: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result.ProjectedSnapshot, result.Err
	case <-r.done:
		return ProjectedStateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return ProjectedStateSnapshot{}, fmt.Errorf("wait room join result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) Leave(ctx context.Context, sink PlayerSink, reason string) error {
	if sink == nil {
		return ErrNilPlayerSink
	}
	if r.isClosed() {
		return ErrRuntimeClosed
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{LeaveSink: sink, LeaveReason: reason, Reply: reply}

	select {
	case r.input <- request:
	case <-r.done:
		return ErrRuntimeClosed
	case <-ctx.Done():
		return fmt.Errorf("submit room leave request: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result.Err
	case <-r.done:
		return ErrRuntimeClosed
	case <-ctx.Done():
		return fmt.Errorf("wait room leave result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) Run(ctx context.Context) error {
	defer r.closeDone()

	for {
		select {
		case <-ctx.Done():
			r.shutdown(CloseReasonShutdown)
			return nil
		case request := <-r.input:
			if request.SnapshotOnly {
				snapshot, err := r.snapshot()
				request.Reply <- CommandResult{Snapshot: snapshot, Err: err}
				continue
			}
			if request.SnapshotSink != nil {
				projected, err := r.snapshotFor(request.SnapshotSink)
				request.Reply <- CommandResult{ProjectedSnapshot: projected, Err: err}
				continue
			}
			if request.SnapshotSent != nil {
				err := r.recordSnapshotSent(request.SnapshotSent.PlayerID)
				snapshot, snapshotErr := r.snapshot()
				if err == nil {
					err = snapshotErr
				}
				request.Reply <- CommandResult{Snapshot: snapshot, Err: err}
				continue
			}
			if request.JoinSink != nil {
				projected, err := r.join(request.JoinSink, request.JoinViewer)
				request.Reply <- CommandResult{ProjectedSnapshot: projected, Err: err}
				continue
			}
			if request.LeaveSink != nil {
				err := r.leave(request.LeaveSink, request.LeaveReason)
				snapshot, snapshotErr := r.snapshot()
				if err == nil {
					err = snapshotErr
				}
				request.Reply <- CommandResult{Snapshot: snapshot, Err: err}
				continue
			}

			if err := r.validateCommandSink(request.CommandSink, request.Command.PlayerID); err != nil {
				snapshot, snapshotErr := r.snapshot()
				if snapshotErr != nil {
					err = fmt.Errorf("%w; build snapshot: %v", err, snapshotErr)
				}
				request.Reply <- CommandResult{Snapshot: snapshot, Err: err}
				continue
			}

			before := r.state
			next, delta, err := ApplyGameCommand(r.gameModule, before, request.Command, r.clock())
			outcome := broadcastOutcome{}
			projected := make(map[string]ProjectedStateDelta, len(r.subscribers))
			if err == nil {
				for playerID, subscriber := range r.subscribers {
					viewDelta, projectErr := ProjectDelta(r.gameModule, before, next, delta, subscriber.viewer)
					if projectErr != nil {
						err = fmt.Errorf("project delta for player %q: %w", playerID, projectErr)
						break
					}
					projected[playerID] = viewDelta
				}
			}
			if err == nil {
				event, eventErr := NewGameCommandEvent(delta)
				if eventErr != nil {
					err = eventErr
				} else if eventErr := r.appendEvent(event); eventErr != nil {
					err = eventErr
				} else {
					r.state = next
					outcome = r.broadcast(projected)
				}
			}
			snapshot, snapshotErr := r.snapshot()
			if err == nil {
				err = snapshotErr
			}
			request.Reply <- CommandResult{
				Snapshot:                  snapshot,
				Delta:                     delta,
				BroadcastCount:            outcome.delivered,
				SnapshotCompactions:       outcome.snapshotCompactions,
				DisconnectedSlowConsumers: outcome.disconnected,
				BroadcastFailures:         outcome.failures,
				Err:                       err,
			}
		}
	}
}

func (r *RoomRuntime) QueueDepth() int {
	return len(r.input)
}

func (r *RoomRuntime) join(sink PlayerSink, viewer game.Viewer) (ProjectedStateSnapshot, error) {
	playerID := sink.PlayerID()
	if playerID == "" {
		return ProjectedStateSnapshot{}, ErrEmptyPlayerID
	}
	if sink.SessionID() == 0 {
		return ProjectedStateSnapshot{}, ErrStalePlayerSession
	}
	if viewer.PlayerID != playerID {
		return ProjectedStateSnapshot{}, ErrStalePlayerSession
	}

	if previous, ok := r.subscribers[playerID]; ok && previous.sink.SessionID() != sink.SessionID() {
		previous.sink.Close(CloseReasonReplaced)
		if err := r.recordPlayerDisconnected(previous.sink.PlayerID(), CloseReasonReplaced); err != nil {
			return ProjectedStateSnapshot{}, err
		}
	}

	nextState := r.state
	changed := false
	if viewer.JoinMode == game.JoinModePlayer {
		gameState, joined, err := r.gameModule.PlayerJoined(r.state.GameState, playerID)
		if err != nil {
			return ProjectedStateSnapshot{}, fmt.Errorf("game player joined: %w", err)
		}
		nextState.GameState = gameState
		changed = joined
		if changed {
			nextState.Revision++
			nextState.UpdatedAt = r.clock()
		}
	}
	joiningSnapshot, err := BuildProjectedSnapshot(r.gameModule, nextState, viewer)
	if err != nil {
		return ProjectedStateSnapshot{}, fmt.Errorf("project join snapshot: %w", err)
	}
	existingSnapshots := make(map[string]ProjectedStateSnapshot, len(r.subscribers))
	if changed {
		for existingID, existing := range r.subscribers {
			projected, projectErr := BuildProjectedSnapshot(r.gameModule, nextState, existing.viewer)
			if projectErr != nil {
				return ProjectedStateSnapshot{}, fmt.Errorf("project join snapshot for player %q: %w", existingID, projectErr)
			}
			existingSnapshots[existingID] = projected
		}
		canonical, snapshotErr := BuildSnapshot(r.gameModule, nextState)
		if snapshotErr != nil {
			return ProjectedStateSnapshot{}, snapshotErr
		}
		if err := r.appendEvent(NewPlayerJoinedEvent(r.state, nextState, canonical, playerID, nextState.UpdatedAt)); err != nil {
			return ProjectedStateSnapshot{}, err
		}
		r.state = nextState
	}
	r.subscribers[playerID] = roomSubscriber{sink: sink, viewer: viewer}
	r.slowStrikes[playerID] = 0
	for existingID, projected := range existingSnapshots {
		existing := r.subscribers[existingID]
		if sendErr := existing.sink.Send(context.Background(), SnapshotEnvelope(projected)); sendErr == nil {
			_ = r.recordSnapshotSent(existingID)
		}
	}
	return joiningSnapshot, nil
}

func (r *RoomRuntime) leave(sink PlayerSink, reason string) error {
	playerID := sink.PlayerID()
	if playerID == "" {
		return ErrEmptyPlayerID
	}
	if reason == "" {
		reason = CloseReasonDisconnected
	}

	current, ok := r.subscribers[playerID]
	if !ok || current.sink.SessionID() != sink.SessionID() {
		return nil
	}

	current.sink.Close(reason)
	if err := r.recordPlayerDisconnected(playerID, reason); err != nil {
		return err
	}
	r.removeSubscriber(playerID)
	return nil
}

func (r *RoomRuntime) validateCommandSink(sink PlayerSink, playerID string) error {
	if sink == nil {
		return nil
	}
	if playerID == "" {
		return ErrEmptyPlayerID
	}
	if sink.PlayerID() != playerID {
		return ErrStalePlayerSession
	}
	current, ok := r.subscribers[playerID]
	if !ok || current.sink.SessionID() != sink.SessionID() {
		return ErrStalePlayerSession
	}
	return nil
}

type broadcastOutcome struct {
	delivered           int
	snapshotCompactions int
	disconnected        int
	failures            []BroadcastFailure
}

func (r *RoomRuntime) broadcast(deltas map[string]ProjectedStateDelta) broadcastOutcome {
	outcome := broadcastOutcome{failures: make([]BroadcastFailure, 0)}

	for playerID, subscriber := range r.subscribers {
		delta, ok := deltas[playerID]
		if !ok {
			outcome.failures = append(outcome.failures, BroadcastFailure{PlayerID: playerID, Err: fmt.Errorf("missing projected delta")})
			continue
		}
		err := subscriber.sink.Send(context.Background(), DeltaEnvelope(delta))
		if err == nil {
			outcome.delivered++
			r.slowStrikes[playerID] = 0
			continue
		}

		outcome.failures = append(outcome.failures, BroadcastFailure{
			PlayerID: playerID,
			Err:      fmt.Errorf("send delta to player %q: %w", playerID, err),
		})

		if errors.Is(err, ErrPlayerSinkClosed) {
			if eventErr := r.recordPlayerDisconnected(playerID, CloseReasonDisconnected); eventErr != nil {
				outcome.failures = append(outcome.failures, BroadcastFailure{
					PlayerID: playerID,
					Err:      fmt.Errorf("record disconnect for player %q: %w", playerID, eventErr),
				})
			}
			r.removeSubscriber(playerID)
			continue
		}

		r.slowStrikes[playerID]++
		if r.slowStrikes[playerID] >= r.cfg.SlowConsumerStrikeLimit {
			subscriber.sink.Close(CloseReasonSlowConsumer)
			if eventErr := r.recordPlayerDisconnected(playerID, CloseReasonSlowConsumer); eventErr != nil {
				outcome.failures = append(outcome.failures, BroadcastFailure{
					PlayerID: playerID,
					Err:      fmt.Errorf("record slow-consumer disconnect for player %q: %w", playerID, eventErr),
				})
			}
			r.removeSubscriber(playerID)
			outcome.disconnected++
			continue
		}

		snapshot, snapshotErr := BuildProjectedSnapshot(r.gameModule, r.state, subscriber.viewer)
		if snapshotErr != nil {
			outcome.failures = append(outcome.failures, BroadcastFailure{
				PlayerID: playerID,
				Err:      fmt.Errorf("build compacted snapshot for player %q: %w", playerID, snapshotErr),
			})
			subscriber.sink.Close(CloseReasonSlowConsumer)
			if eventErr := r.recordPlayerDisconnected(playerID, CloseReasonSlowConsumer); eventErr != nil {
				outcome.failures = append(outcome.failures, BroadcastFailure{
					PlayerID: playerID,
					Err:      fmt.Errorf("record slow-consumer disconnect for player %q: %w", playerID, eventErr),
				})
			}
			r.removeSubscriber(playerID)
			outcome.disconnected++
			continue
		}

		if err := subscriber.sink.Send(context.Background(), SnapshotEnvelope(snapshot)); err != nil {
			outcome.failures = append(outcome.failures, BroadcastFailure{
				PlayerID: playerID,
				Err:      fmt.Errorf("compact delta queue to snapshot for player %q: %w", playerID, err),
			})
			subscriber.sink.Close(CloseReasonSlowConsumer)
			if eventErr := r.recordPlayerDisconnected(playerID, CloseReasonSlowConsumer); eventErr != nil {
				outcome.failures = append(outcome.failures, BroadcastFailure{
					PlayerID: playerID,
					Err:      fmt.Errorf("record slow-consumer disconnect for player %q: %w", playerID, eventErr),
				})
			}
			r.removeSubscriber(playerID)
			outcome.disconnected++
			continue
		}
		if eventErr := r.recordSnapshotSent(playerID); eventErr != nil {
			outcome.failures = append(outcome.failures, BroadcastFailure{
				PlayerID: playerID,
				Err:      fmt.Errorf("record snapshot sent for player %q: %w", playerID, eventErr),
			})
		}
		outcome.snapshotCompactions++
	}
	return outcome
}

func (r *RoomRuntime) shutdown(reason string) {
	for playerID, subscriber := range r.subscribers {
		subscriber.sink.Close(reason)
		_ = r.recordPlayerDisconnected(playerID, reason)
		r.removeSubscriber(playerID)
	}
}

func (r *RoomRuntime) recordSnapshotSent(playerID string) error {
	snapshot, err := r.snapshot()
	if err != nil {
		return err
	}
	viewScope := game.ViewScopeUnspecified
	if subscriber, ok := r.subscribers[playerID]; ok {
		viewScope = subscriber.viewer.Scope
	}
	return r.appendEvent(NewSnapshotSentEvent(snapshot, playerID, viewScope, r.clock()))
}

func (r *RoomRuntime) recordPlayerDisconnected(playerID string, reason string) error {
	return r.appendEvent(NewPlayerDisconnectedEvent(r.state, playerID, reason, r.clock()))
}

func (r *RoomRuntime) appendEvent(event RoomEvent) error {
	if r.eventStore == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.EventStoreTimeout)
	defer cancel()
	if _, err := r.eventStore.Append(ctx, event); err != nil {
		return fmt.Errorf("append room event: %w", err)
	}
	return nil
}

func (r *RoomRuntime) snapshot() (StateSnapshot, error) {
	return BuildSnapshot(r.gameModule, r.state)
}

func (r *RoomRuntime) snapshotFor(sink PlayerSink) (ProjectedStateSnapshot, error) {
	if err := r.validateCommandSink(sink, sink.PlayerID()); err != nil {
		return ProjectedStateSnapshot{}, err
	}
	subscriber := r.subscribers[sink.PlayerID()]
	return BuildProjectedSnapshot(r.gameModule, r.state, subscriber.viewer)
}

func (r *RoomRuntime) removeSubscriber(playerID string) {
	delete(r.subscribers, playerID)
	delete(r.slowStrikes, playerID)
}

func (r *RoomRuntime) closeDone() {
	r.closeOnce.Do(func() {
		close(r.done)
	})
}

func (r *RoomRuntime) isClosed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
