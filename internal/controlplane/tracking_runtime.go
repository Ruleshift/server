package controlplane

import (
	"context"
	"errors"

	"github.com/Ruleshift/server/internal/module"
)

type TrackingResolver struct {
	Resolver module.Resolver
	Tracker  *ProtocolViolationTracker
}

func (r TrackingResolver) Resolve(ctx context.Context, ref module.ModuleRef) (module.Runtime, error) {
	runtime, err := r.Resolver.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &trackingRuntime{Runtime: runtime, ref: ref, tracker: r.Tracker}, nil
}

type trackingRuntime struct {
	module.Runtime
	ref     module.ModuleRef
	tracker *ProtocolViolationTracker
}

func (r *trackingRuntime) track(ctx context.Context, err error) error {
	if r.tracker != nil && errors.Is(err, module.ErrProtocolViolation) {
		_, _ = r.tracker.Record(ctx, r.ref)
	}
	return err
}
func (r *trackingRuntime) CreateState(ctx context.Context, op module.DeterministicContext, setup module.GameSetup) (module.Transition, error) {
	v, e := r.Runtime.CreateState(ctx, op, setup)
	return v, r.track(ctx, e)
}
func (r *trackingRuntime) Apply(ctx context.Context, op module.DeterministicContext, s module.OpaqueState, actor module.Actor, c module.OpaqueState) (module.Transition, error) {
	v, e := r.Runtime.Apply(ctx, op, s, actor, c)
	return v, r.track(ctx, e)
}
func (r *trackingRuntime) ProjectSnapshot(ctx context.Context, op module.DeterministicContext, s module.OpaqueState, v module.Viewer) (module.Projection, error) {
	result, e := r.Runtime.ProjectSnapshot(ctx, op, s, v)
	return result, r.track(ctx, e)
}
func (r *trackingRuntime) ProjectDelta(ctx context.Context, op module.DeterministicContext, b, a, d module.OpaqueState, v module.Viewer) (module.Projection, error) {
	result, e := r.Runtime.ProjectDelta(ctx, op, b, a, d, v)
	return result, r.track(ctx, e)
}
