package room

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrNilDeltaSink = errors.New("delta sink must not be nil")

type RuntimeConfig struct {
	InputQueueSize int
}

type RuntimeCommand struct {
	Command      IntCommand
	SnapshotOnly bool
	JoinSession  DeltaSink
	Reply        chan<- CommandResult
}

type CommandResult struct {
	Snapshot          StateSnapshot
	Delta             StateDelta
	BroadcastCount    int
	BroadcastFailures []BroadcastFailure
	Err               error
}

type DeltaSink interface {
	PlayerID() string
	TrySendDelta(delta StateDelta) error
}

type BroadcastFailure struct {
	PlayerID string
	Err      error
}

type RoomRuntime struct {
	state       RoomState
	input       chan RuntimeCommand
	subscribers map[string]DeltaSink
	clock       func() time.Time
}

func NewRuntime(roomID string, cfg RuntimeConfig) (*RoomRuntime, error) {
	if cfg.InputQueueSize <= 0 {
		return nil, fmt.Errorf("room input queue size must be positive")
	}

	now := time.Now().UTC()
	return &RoomRuntime{
		state:       NewState(roomID, now),
		input:       make(chan RuntimeCommand, cfg.InputQueueSize),
		subscribers: make(map[string]DeltaSink),
		clock: func() time.Time {
			return time.Now().UTC()
		},
	}, nil
}

func (r *RoomRuntime) Submit(ctx context.Context, cmd IntCommand) (CommandResult, error) {
	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{Command: cmd, Reply: reply}

	select {
	case r.input <- request:
	case <-ctx.Done():
		return CommandResult{}, fmt.Errorf("submit room command: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result, result.Err
	case <-ctx.Done():
		return CommandResult{}, fmt.Errorf("wait room command result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) Snapshot(ctx context.Context) (StateSnapshot, error) {
	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{SnapshotOnly: true, Reply: reply}

	select {
	case r.input <- request:
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("submit room snapshot request: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result.Snapshot, result.Err
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("wait room snapshot result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) Join(ctx context.Context, session DeltaSink) (StateSnapshot, error) {
	if session == nil {
		return StateSnapshot{}, ErrNilDeltaSink
	}

	reply := make(chan CommandResult, 1)
	request := RuntimeCommand{JoinSession: session, Reply: reply}

	select {
	case r.input <- request:
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("submit room join request: %w", ctx.Err())
	}

	select {
	case result := <-reply:
		return result.Snapshot, result.Err
	case <-ctx.Done():
		return StateSnapshot{}, fmt.Errorf("wait room join result: %w", ctx.Err())
	}
}

func (r *RoomRuntime) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("room runtime stopped: %w", ctx.Err())
		case request := <-r.input:
			if request.SnapshotOnly {
				request.Reply <- CommandResult{Snapshot: BuildSnapshot(r.state)}
				continue
			}
			if request.JoinSession != nil {
				err := r.join(request.JoinSession)
				request.Reply <- CommandResult{Snapshot: BuildSnapshot(r.state), Err: err}
				continue
			}

			next, delta, err := ApplyIntCommand(r.state, request.Command, r.clock())
			var failures []BroadcastFailure
			broadcastCount := 0
			if err == nil {
				r.state = next
				broadcastCount, failures = r.broadcast(delta)
			}
			request.Reply <- CommandResult{
				Snapshot:          BuildSnapshot(r.state),
				Delta:             delta,
				BroadcastCount:    broadcastCount,
				BroadcastFailures: failures,
				Err:               err,
			}
		}
	}
}

func (r *RoomRuntime) QueueDepth() int {
	return len(r.input)
}

func (r *RoomRuntime) join(session DeltaSink) error {
	playerID := session.PlayerID()
	if playerID == "" {
		return ErrEmptyPlayerID
	}
	r.subscribers[playerID] = session
	return nil
}

func (r *RoomRuntime) broadcast(delta StateDelta) (int, []BroadcastFailure) {
	failures := make([]BroadcastFailure, 0)
	delivered := 0
	for playerID, subscriber := range r.subscribers {
		if err := subscriber.TrySendDelta(delta); err != nil {
			failures = append(failures, BroadcastFailure{
				PlayerID: playerID,
				Err:      fmt.Errorf("send delta to player %q: %w", playerID, err),
			})
			continue
		}
		delivered++
	}
	return delivered, failures
}
