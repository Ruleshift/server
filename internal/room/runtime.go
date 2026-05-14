package room

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrNilPlayerSink = errors.New("player sink must not be nil")
var ErrRuntimeClosed = errors.New("room runtime is closed")

type RuntimeConfig struct {
	InputQueueSize          int
	SlowConsumerStrikeLimit int
}

type RuntimeCommand struct {
	Command      IntCommand
	SnapshotOnly bool
	JoinSink     PlayerSink
	Reply        chan<- CommandResult
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
	clock       func() time.Time
}

func NewRuntime(roomID string, cfg RuntimeConfig) (*RoomRuntime, error) {
	if cfg.InputQueueSize <= 0 {
		return nil, fmt.Errorf("room input queue size must be positive")
	}
	if cfg.SlowConsumerStrikeLimit <= 0 {
		cfg.SlowConsumerStrikeLimit = 2
	}

	now := time.Now().UTC()
	return &RoomRuntime{
		state:       NewState(roomID, now),
		input:       make(chan RuntimeCommand, cfg.InputQueueSize),
		done:        make(chan struct{}),
		cfg:         cfg,
		subscribers: make(map[string]PlayerSink),
		slowStrikes: make(map[string]int),
		clock: func() time.Time {
			return time.Now().UTC()
		},
	}, nil
}

func (r *RoomRuntime) Submit(ctx context.Context, cmd IntCommand) (CommandResult, error) {
	if r.isClosed() {
		return CommandResult{}, ErrRuntimeClosed
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{Command: cmd, Reply: reply}

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

func (r *RoomRuntime) Run(ctx context.Context) error {
	defer r.closeDone()

	for {
		select {
		case <-ctx.Done():
			r.shutdown(CloseReasonShutdown)
			return nil
		case request := <-r.input:
			if request.SnapshotOnly {
				request.Reply <- CommandResult{Snapshot: BuildSnapshot(r.state)}
				continue
			}
			if request.JoinSink != nil {
				err := r.join(request.JoinSink)
				request.Reply <- CommandResult{Snapshot: BuildSnapshot(r.state), Err: err}
				continue
			}

			next, delta, err := ApplyIntCommand(r.state, request.Command, r.clock())
			outcome := broadcastOutcome{}
			if err == nil {
				r.state = next
				outcome = r.broadcast(delta)
			}
			request.Reply <- CommandResult{
				Snapshot:                  BuildSnapshot(r.state),
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

	if previous := r.subscribers[playerID]; previous != nil && previous != sink {
		previous.Close(CloseReasonReplaced)
	}
	r.subscribers[playerID] = sink
	r.slowStrikes[playerID] = 0
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
	snapshot := BuildSnapshot(r.state)

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
			r.removeSubscriber(playerID)
			continue
		}

		r.slowStrikes[playerID]++
		if r.slowStrikes[playerID] >= r.cfg.SlowConsumerStrikeLimit {
			subscriber.Close(CloseReasonSlowConsumer)
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
			r.removeSubscriber(playerID)
			outcome.disconnected++
			continue
		}
		outcome.snapshotCompactions++
	}
	return outcome
}

func (r *RoomRuntime) shutdown(reason string) {
	for playerID, subscriber := range r.subscribers {
		subscriber.Close(reason)
		r.removeSubscriber(playerID)
	}
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
