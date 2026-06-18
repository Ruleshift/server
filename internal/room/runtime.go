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
	EventStore              EventStore
	GameModule              game.Module
}

type RuntimeCommand struct {
	Command      GameCommand
	CommandSink  PlayerSink
	SnapshotOnly bool
	SnapshotSent *snapshotSentRecord
	JoinSink     PlayerSink
	LeaveSink    PlayerSink
	LeaveReason  string
	Reply        chan<- CommandResult
}

type snapshotSentRecord struct {
	PlayerID string
}

type CommandResult struct {
	Snapshot                  StateSnapshot
	Delta                     StateDelta
	BroadcastCount            int
	SnapshotCompactions       int
	DisconnectedSlowConsumers int
	BroadcastFailures         []BroadcastFailure
	Err                       error
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
	subscribers map[string]PlayerSink
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

	fmt.Println("THIS GAME TYPE IS", cfg.GameModule.Type())

	now := time.Now().UTC()
	gameState, err := cfg.GameModule.NewState(now)
	if err != nil {
		return nil, fmt.Errorf("create game state: %w", err)
	}
	state := NewState(roomID, cfg.GameModule.Type(), gameState, now)
	runtime := &RoomRuntime{
		state:       state,
		input:       make(chan RuntimeCommand, cfg.InputQueueSize),
		done:        make(chan struct{}),
		cfg:         cfg,
		subscribers: make(map[string]PlayerSink),
		slowStrikes: make(map[string]int),
		eventStore:  cfg.EventStore,
		gameModule:  cfg.GameModule,
		clock: func() time.Time {
			return time.Now().UTC()
		},
	}
	if err := runtime.appendEvent(NewRoomCreatedEvent(state)); err != nil {
		return nil, err
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

func (r *RoomRuntime) Join(ctx context.Context, sink PlayerSink) (StateSnapshot, error) {
	if sink == nil {
		return StateSnapshot{}, ErrNilPlayerSink
	}
	if r.isClosed() {
		return StateSnapshot{}, ErrRuntimeClosed
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{JoinSink: sink, Reply: reply}

	select {
	case r.input <- request:
	case <-r.done:
		return StateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("submit room join request: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result.Snapshot, result.Err
	case <-r.done:
		return StateSnapshot{}, ErrRuntimeClosed
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("wait room join result: %w", ctx.Err())
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
				err := r.join(request.JoinSink)
				snapshot, snapshotErr := r.snapshot()
				if err == nil {
					err = snapshotErr
				}
				request.Reply <- CommandResult{Snapshot: snapshot, Err: err}
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

			next, delta, err := ApplyGameCommand(r.gameModule, r.state, request.Command, r.clock())
			outcome := broadcastOutcome{}
			if err == nil {
				event, eventErr := NewGameCommandEvent(delta)
				if eventErr != nil {
					err = eventErr
				} else if eventErr := r.appendEvent(event); eventErr != nil {
					err = eventErr
				} else {
					r.state = next
					outcome = r.broadcast(delta)
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

func (r *RoomRuntime) join(sink PlayerSink) error {
	playerID := sink.PlayerID()
	if playerID == "" {
		return ErrEmptyPlayerID
	}
	if sink.SessionID() == 0 {
		return ErrStalePlayerSession
	}

	if previous := r.subscribers[playerID]; previous != nil && previous.SessionID() != sink.SessionID() {
		previous.Close(CloseReasonReplaced)
		if err := r.recordPlayerDisconnected(previous.PlayerID(), CloseReasonReplaced); err != nil {
			return err
		}
	}
	gameState, err := r.gameModule.PlayerJoined(r.state.GameState, playerID)
	if err != nil {
		return fmt.Errorf("game player joined: %w", err)
	}
	r.state.GameState = gameState
	r.state.UpdatedAt = r.clock()
	r.subscribers[playerID] = sink
	r.slowStrikes[playerID] = 0
	return r.appendEvent(NewPlayerJoinedEvent(r.state, playerID, r.clock()))
}

func (r *RoomRuntime) leave(sink PlayerSink, reason string) error {
	playerID := sink.PlayerID()
	if playerID == "" {
		return ErrEmptyPlayerID
	}
	if reason == "" {
		reason = CloseReasonDisconnected
	}

	current := r.subscribers[playerID]
	if current == nil || current.SessionID() != sink.SessionID() {
		return nil
	}

	current.Close(reason)
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
	current := r.subscribers[playerID]
	if current == nil || current.SessionID() != sink.SessionID() {
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

func (r *RoomRuntime) broadcast(delta StateDelta) broadcastOutcome {
	outcome := broadcastOutcome{failures: make([]BroadcastFailure, 0)}
	snapshot, snapshotErr := r.snapshot()

	for playerID, subscriber := range r.subscribers {
		err := subscriber.Send(context.Background(), DeltaEnvelope(delta))
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
			subscriber.Close(CloseReasonSlowConsumer)
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

		if snapshotErr != nil {
			outcome.failures = append(outcome.failures, BroadcastFailure{
				PlayerID: playerID,
				Err:      fmt.Errorf("build compacted snapshot for player %q: %w", playerID, snapshotErr),
			})
			subscriber.Close(CloseReasonSlowConsumer)
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

		if err := subscriber.Send(context.Background(), SnapshotEnvelope(snapshot)); err != nil {
			outcome.failures = append(outcome.failures, BroadcastFailure{
				PlayerID: playerID,
				Err:      fmt.Errorf("compact delta queue to snapshot for player %q: %w", playerID, err),
			})
			subscriber.Close(CloseReasonSlowConsumer)
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
		subscriber.Close(reason)
		_ = r.recordPlayerDisconnected(playerID, reason)
		r.removeSubscriber(playerID)
	}
}

func (r *RoomRuntime) recordSnapshotSent(playerID string) error {
	snapshot, err := r.snapshot()
	if err != nil {
		return err
	}
	return r.appendEvent(NewSnapshotSentEvent(snapshot, playerID, r.clock()))
}

func (r *RoomRuntime) recordPlayerDisconnected(playerID string, reason string) error {
	return r.appendEvent(NewPlayerDisconnectedEvent(r.state, playerID, reason, r.clock()))
}

func (r *RoomRuntime) appendEvent(event RoomEvent) error {
	if r.eventStore == nil {
		return nil
	}
	if _, err := r.eventStore.Append(context.Background(), event); err != nil {
		return fmt.Errorf("append room event: %w", err)
	}
	return nil
}

func (r *RoomRuntime) snapshot() (StateSnapshot, error) {
	return BuildSnapshot(r.gameModule, r.state)
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
