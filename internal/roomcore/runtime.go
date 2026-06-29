package roomcore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Ruleshift/server/internal/metrics"
	"github.com/Ruleshift/server/internal/module"
)

const SnapshotInterval uint64 = 100

type RuntimeConfig struct {
	QueueSize int
	Store     Store
	Module    module.Runtime
	Clock     func() time.Time
	Metrics   metrics.Observer
}

type request struct {
	ctx        context.Context
	operation  string
	queuedAt   time.Time
	queueRatio float64
	fn         func(context.Context) (any, error)
	reply      chan result
}
type result struct {
	value any
	err   error
}

type Runtime struct {
	state   State
	store   Store
	module  module.Runtime
	clock   func() time.Time
	input   chan request
	done    chan struct{}
	metrics metrics.Observer

	roomID    string
	moduleID  string
	version   string
	status    string
	createdAt time.Time
	revision  atomic.Uint64
	updatedAt atomic.Int64
}

func Create(ctx context.Context, route Route, cfg RuntimeConfig) (*Runtime, error) {
	if route.RoomID == "" {
		return nil, fmt.Errorf("room id must not be empty")
	}
	if err := route.Module.Validate(); err != nil {
		return nil, err
	}
	if cfg.Store == nil || cfg.Module == nil {
		return nil, fmt.Errorf("room store and module runtime are required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	now := clock()
	route.CreatedAt = now
	transition, err := cfg.Module.NewState(ctx, operation(route, 0, now))
	if err != nil {
		return nil, err
	}
	if !transition.Changed {
		return nil, fmt.Errorf("%w: NewState returned changed=false", module.ErrProtocolViolation)
	}
	state := State{Route: route, Opaque: transition.NextState, Status: "active", CreatedAt: now, UpdatedAt: now}
	event := Event{RoomID: route.RoomID, Kind: EventRoomCreated, Delta: transition.Delta, StateDigest: state.Opaque.Digest, OccurredAt: now}
	snapshot := Snapshot{RoomID: route.RoomID, State: state.Opaque, SavedAt: now}
	if err := cfg.Store.Create(ctx, state, event, snapshot); err != nil {
		return nil, err
	}
	return newRuntime(state, cfg), nil
}

func Restore(ctx context.Context, route Route, cfg RuntimeConfig) (*Runtime, error) {
	if cfg.Store == nil || cfg.Module == nil {
		return nil, fmt.Errorf("room store and module runtime are required")
	}
	loadedRoute, snapshot, events, err := cfg.Store.Load(ctx, route.RoomID)
	if err != nil {
		return nil, err
	}
	if loadedRoute.Module != route.Module {
		return nil, fmt.Errorf("stored route does not match pinned module reference")
	}
	if snapshot == nil {
		return nil, fmt.Errorf("room %q has no snapshot", route.RoomID)
	}
	state := State{Route: route, Revision: snapshot.Revision, Opaque: snapshot.State, Status: "active", CreatedAt: route.CreatedAt, UpdatedAt: snapshot.SavedAt}
	for _, event := range events {
		if event.PreviousRevision != state.Revision {
			return nil, fmt.Errorf("event %d has discontinuous revision", event.Sequence)
		}
		transition, transitionErr := replayTransition(ctx, cfg.Module, state, event)
		if transitionErr != nil {
			return nil, fmt.Errorf("replay event %d: %w", event.Sequence, transitionErr)
		}
		if transition.NextState.Digest != event.StateDigest {
			return nil, fmt.Errorf("replay event %d state digest mismatch", event.Sequence)
		}
		state.Opaque, state.Revision, state.UpdatedAt = transition.NextState, event.NewRevision, event.OccurredAt
	}
	return newRuntime(state, cfg), nil
}

func newRuntime(state State, cfg RuntimeConfig) *Runtime {
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = 1024
	}
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	observer := cfg.Metrics
	if observer == nil {
		observer = metrics.NopRecorder{}
	}
	runtime := &Runtime{state: state, store: cfg.Store, module: cfg.Module, clock: clock, input: make(chan request, queueSize), done: make(chan struct{}), metrics: observer, roomID: state.Route.RoomID, moduleID: state.Route.Module.ModuleID, version: state.Route.Module.Version, status: state.Status, createdAt: state.CreatedAt}
	runtime.revision.Store(state.Revision)
	runtime.updatedAt.Store(state.UpdatedAt.UnixMilli())
	return runtime
}

func (r *Runtime) Run(ctx context.Context) error {
	defer r.persistEvictionSnapshot()
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return nil
		case req := <-r.input:
			started := r.clock()
			value, err := req.fn(req.ctx)
			r.metrics.RoomOperation(req.operation, metricResult(err), r.clock().Sub(started), started.Sub(req.queuedAt), req.queueRatio)
			select {
			case req.reply <- result{value: value, err: err}:
			case <-req.ctx.Done():
			}
		}
	}
}

func (r *Runtime) persistEvictionSnapshot() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := r.store.SaveSnapshot(ctx, Snapshot{RoomID: r.state.Route.RoomID, Revision: r.state.Revision, State: r.state.Opaque, SavedAt: r.clock()})
	r.metrics.Snapshot("eviction", metricResult(err))
}

func (r *Runtime) State(ctx context.Context) (State, error) {
	value, err := r.submit(ctx, "state", func(context.Context) (any, error) { return cloneState(r.state), nil })
	if err != nil {
		return State{}, err
	}
	return value.(State), nil
}

func (r *Runtime) Join(ctx context.Context, viewer module.Viewer) (SnapshotView, *DeltaView, error) {
	value, err := r.submit(ctx, "join", func(callCtx context.Context) (any, error) {
		var delta *DeltaView
		if viewer.JoinMode == module.JoinModePlayer {
			before := r.state
			transition, callErr := r.module.PlayerJoined(callCtx, operation(r.state.Route, r.state.Revision, r.clock()), r.state.Opaque, viewer.PlayerID)
			if callErr != nil {
				return nil, callErr
			}
			if transition.Changed {
				if callErr = r.commit(callCtx, before, transition, EventPlayerJoined, viewer.PlayerID, module.OpaqueState{}); callErr != nil {
					return nil, callErr
				}
				projected, projectErr := r.projectDelta(callCtx, before, transition.Delta, viewer.PlayerID, viewer)
				if projectErr != nil {
					return nil, projectErr
				}
				delta = &projected
			}
		}
		snapshot, callErr := r.snapshot(callCtx, viewer)
		return struct {
			Snapshot SnapshotView
			Delta    *DeltaView
		}{snapshot, delta}, callErr
	})
	if err != nil {
		return SnapshotView{}, nil, err
	}
	response := value.(struct {
		Snapshot SnapshotView
		Delta    *DeltaView
	})
	return response.Snapshot, response.Delta, nil
}

func (r *Runtime) Leave(ctx context.Context, viewer module.Viewer) (*DeltaView, error) {
	value, err := r.submit(ctx, "leave", func(callCtx context.Context) (any, error) {
		if viewer.JoinMode != module.JoinModePlayer {
			return (*DeltaView)(nil), nil
		}
		before := r.state
		transition, callErr := r.module.PlayerLeft(callCtx, operation(r.state.Route, r.state.Revision, r.clock()), r.state.Opaque, viewer.PlayerID)
		if callErr != nil {
			return nil, callErr
		}
		if !transition.Changed {
			return (*DeltaView)(nil), nil
		}
		if callErr = r.commit(callCtx, before, transition, EventPlayerLeft, viewer.PlayerID, module.OpaqueState{}); callErr != nil {
			return nil, callErr
		}
		projected, callErr := r.projectDelta(callCtx, before, transition.Delta, viewer.PlayerID, viewer)
		if callErr != nil {
			return nil, callErr
		}
		return &projected, nil
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	return value.(*DeltaView), nil
}

func (r *Runtime) Apply(ctx context.Context, command Command, viewers []module.Viewer) ([]DeltaView, error) {
	value, err := r.submit(ctx, "apply", func(callCtx context.Context) (any, error) {
		if command.PlayerID == "" {
			return nil, fmt.Errorf("player id must not be empty")
		}
		if command.ExpectedRevision != 0 && command.ExpectedRevision != r.state.Revision {
			return nil, ErrRevisionMismatch
		}
		before := r.state
		transition, callErr := r.module.Apply(callCtx, operation(r.state.Route, r.state.Revision, r.clock()), r.state.Opaque, command.PlayerID, command.Payload)
		if callErr != nil {
			return nil, callErr
		}
		if !transition.Changed {
			return []DeltaView{}, nil
		}
		if callErr = r.commit(callCtx, before, transition, EventCommandApplied, command.PlayerID, command.Payload); callErr != nil {
			return nil, callErr
		}
		deltas := make([]DeltaView, 0, len(viewers))
		for _, viewer := range viewers {
			projected, projectErr := r.projectDelta(callCtx, before, transition.Delta, command.PlayerID, viewer)
			if projectErr != nil {
				return nil, projectErr
			}
			deltas = append(deltas, projected)
		}
		return deltas, nil
	})
	if err != nil {
		return nil, err
	}
	return value.([]DeltaView), nil
}

func (r *Runtime) Snapshot(ctx context.Context, viewer module.Viewer) (SnapshotView, error) {
	value, err := r.submit(ctx, "snapshot", func(callCtx context.Context) (any, error) { return r.snapshot(callCtx, viewer) })
	if err != nil {
		return SnapshotView{}, err
	}
	return value.(SnapshotView), nil
}

func (r *Runtime) submit(ctx context.Context, operation string, fn func(context.Context) (any, error)) (any, error) {
	queuedAt := r.clock()
	ratio := float64(len(r.input)) / float64(cap(r.input))
	req := request{ctx: ctx, operation: operation, queuedAt: queuedAt, queueRatio: ratio, fn: fn, reply: make(chan result, 1)}
	select {
	case <-r.done:
		return nil, ErrRuntimeClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case r.input <- req:
	}
	select {
	case <-r.done:
		return nil, ErrRuntimeClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-req.reply:
		return result.value, result.err
	}
}

func (r *Runtime) commit(ctx context.Context, before State, transition module.Transition, kind EventKind, playerID string, input module.OpaqueState) error {
	now := r.clock()
	next := before
	next.Revision++
	next.Opaque = transition.NextState
	next.UpdatedAt = now
	event := Event{RoomID: before.Route.RoomID, Kind: kind, PlayerID: playerID, PreviousRevision: before.Revision, NewRevision: next.Revision, Input: input, Delta: transition.Delta, StateDigest: transition.NextState.Digest, OccurredAt: now}
	var snapshot *Snapshot
	if next.Revision%SnapshotInterval == 0 {
		snapshot = &Snapshot{RoomID: next.Route.RoomID, Revision: next.Revision, State: next.Opaque, SavedAt: now}
	}
	if err := r.store.Commit(ctx, next, event, snapshot); err != nil {
		return err
	}
	r.state = next
	r.revision.Store(next.Revision)
	r.updatedAt.Store(next.UpdatedAt.UnixMilli())
	r.metrics.Revision(string(kind))
	if snapshot != nil {
		r.metrics.Snapshot("interval", "ok")
	}
	return nil
}

func (r *Runtime) snapshot(ctx context.Context, viewer module.Viewer) (SnapshotView, error) {
	projection, err := r.module.ProjectSnapshot(ctx, operation(r.state.Route, r.state.Revision, r.clock()), r.state.Opaque, viewer)
	if err != nil {
		return SnapshotView{}, err
	}
	return SnapshotView{RoomID: r.state.Route.RoomID, Revision: r.state.Revision, Module: r.state.Route.Module, View: projection.Payload}, nil
}

func (r *Runtime) projectDelta(ctx context.Context, before State, delta module.OpaqueState, changedBy string, viewer module.Viewer) (DeltaView, error) {
	projection, err := r.module.ProjectDelta(ctx, operation(r.state.Route, r.state.Revision, r.clock()), before.Opaque, r.state.Opaque, delta, viewer)
	if err != nil {
		return DeltaView{}, err
	}
	return DeltaView{RoomID: r.state.Route.RoomID, PreviousRevision: before.Revision, NewRevision: r.state.Revision, ChangedBy: changedBy, Module: r.state.Route.Module, View: projection.Payload, NoVisibleChange: projection.NoVisibleChange}, nil
}

func operation(route Route, revision uint64, now time.Time) module.Operation {
	return module.Operation{OperationID: newOperationID(), RoomID: route.RoomID, Revision: revision, Now: now, Seed: route.Seed}
}
func newOperationID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func replayTransition(ctx context.Context, runtime module.Runtime, state State, event Event) (module.Transition, error) {
	op := operation(state.Route, state.Revision, event.OccurredAt)
	switch event.Kind {
	case EventPlayerJoined:
		return runtime.PlayerJoined(ctx, op, state.Opaque, event.PlayerID)
	case EventPlayerLeft:
		return runtime.PlayerLeft(ctx, op, state.Opaque, event.PlayerID)
	case EventCommandApplied:
		return runtime.Apply(ctx, op, state.Opaque, event.PlayerID, event.Input)
	default:
		return module.Transition{}, errors.New("unsupported replay event")
	}
}

func metricResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrRevisionMismatch):
		return "revision_mismatch"
	case errors.Is(err, module.ErrCommandRejected):
		return "command_rejected"
	case errors.Is(err, module.ErrUnavailable):
		return "module_unavailable"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}
