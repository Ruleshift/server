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
func (r *trackingRuntime) NewState(ctx context.Context, op module.Operation) (module.Transition, error) {
	v, e := r.Runtime.NewState(ctx, op)
	return v, r.track(ctx, e)
}
func (r *trackingRuntime) PlayerJoined(ctx context.Context, op module.Operation, s module.OpaqueState, p string) (module.Transition, error) {
	v, e := r.Runtime.PlayerJoined(ctx, op, s, p)
	return v, r.track(ctx, e)
}
func (r *trackingRuntime) PlayerLeft(ctx context.Context, op module.Operation, s module.OpaqueState, p string) (module.Transition, error) {
	v, e := r.Runtime.PlayerLeft(ctx, op, s, p)
	return v, r.track(ctx, e)
}
func (r *trackingRuntime) Apply(ctx context.Context, op module.Operation, s module.OpaqueState, p string, c module.OpaqueState) (module.Transition, error) {
	v, e := r.Runtime.Apply(ctx, op, s, p, c)
	return v, r.track(ctx, e)
}
func (r *trackingRuntime) ProjectSnapshot(ctx context.Context, op module.Operation, s module.OpaqueState, v module.Viewer) (module.Projection, error) {
	result, e := r.Runtime.ProjectSnapshot(ctx, op, s, v)
	return result, r.track(ctx, e)
}
func (r *trackingRuntime) ProjectDelta(ctx context.Context, op module.Operation, b, a, d module.OpaqueState, v module.Viewer) (module.Projection, error) {
	result, e := r.Runtime.ProjectDelta(ctx, op, b, a, d, v)
	return result, r.track(ctx, e)
}
