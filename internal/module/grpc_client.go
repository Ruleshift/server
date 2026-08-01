package module

import (
	"context"
	"fmt"
	"time"

	modulev2 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCClientConfig struct {
	TransitionDeadline  time.Duration
	CreateStateDeadline time.Duration
	StateTypeURL        string
	CommandTypeURLs     map[string]struct{}
}

type GRPCClient struct {
	client modulev2.ModuleRuntimeClient
	cfg    GRPCClientConfig
}

func NewGRPCClient(client modulev2.ModuleRuntimeClient, cfg GRPCClientConfig) (*GRPCClient, error) {
	if client == nil {
		return nil, fmt.Errorf("module runtime client must not be nil")
	}
	if cfg.TransitionDeadline <= 0 {
		cfg.TransitionDeadline = DefaultDeadline
	}
	if cfg.TransitionDeadline > MaxTransitionDeadline {
		return nil, fmt.Errorf("transition deadline exceeds %s", MaxTransitionDeadline)
	}
	if cfg.CreateStateDeadline <= 0 {
		cfg.CreateStateDeadline = CreateStateDeadline
	}
	if cfg.CreateStateDeadline > CreateStateDeadline {
		return nil, fmt.Errorf("create-state deadline exceeds %s", CreateStateDeadline)
	}
	return &GRPCClient{client: client, cfg: cfg}, nil
}

func (c *GRPCClient) CreateState(ctx context.Context, op DeterministicContext, setup GameSetup) (Transition, error) {
	if setup.PlayerCount == 0 || setup.PlayerCount > MaxPlayers {
		return Transition{}, fmt.Errorf("%w: player count must be between 1 and %d", ErrProtocolViolation, MaxPlayers)
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.CreateStateDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev2.TransitionResponse, error) {
		return c.client.CreateState(callCtx, &modulev2.CreateStateRequest{
			Context: deterministicContextProto(op),
			Setup:   &modulev2.GameSetup{PlayerCount: setup.PlayerCount},
		})
	})
	return c.transition(response, err)
}

func (c *GRPCClient) Apply(ctx context.Context, op DeterministicContext, state OpaqueState, actor Actor, command OpaqueState) (Transition, error) {
	if actor.PlayerID == "" {
		return Transition{}, fmt.Errorf("%w: actor player_id is required", ErrProtocolViolation)
	}
	if len(c.cfg.CommandTypeURLs) > 0 {
		if _, ok := c.cfg.CommandTypeURLs[command.TypeURL]; !ok {
			return Transition{}, fmt.Errorf("%w: unsupported command type %q", ErrCommandRejected, command.TypeURL)
		}
	}
	request := &modulev2.ApplyRequest{
		Context: deterministicContextProto(op),
		State:   state.Any(),
		Actor:   &modulev2.Actor{PlayerId: actor.PlayerID, SeatIndex: actor.SeatIndex},
		Command: command.Any(),
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.TransitionDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev2.TransitionResponse, error) {
		return c.client.Apply(callCtx, request)
	})
	return c.transition(response, err)
}

func (c *GRPCClient) ProjectSnapshot(ctx context.Context, op DeterministicContext, state OpaqueState, viewer Viewer) (Projection, error) {
	request := &modulev2.ProjectRequest{Context: deterministicContextProto(op), State: state.Any(), Viewer: viewerProto(viewer)}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.TransitionDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev2.ProjectionResponse, error) {
		return c.client.ProjectSnapshot(callCtx, request)
	})
	return projection(response, err)
}

func (c *GRPCClient) ProjectDelta(ctx context.Context, op DeterministicContext, before, after, delta OpaqueState, viewer Viewer) (Projection, error) {
	request := &modulev2.ProjectDeltaRequest{Context: deterministicContextProto(op), BeforeState: before.Any(), AfterState: after.Any(), Delta: delta.Any(), Viewer: viewerProto(viewer)}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.TransitionDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev2.ProjectionResponse, error) {
		return c.client.ProjectDelta(callCtx, request)
	})
	return projection(response, err)
}

func (c *GRPCClient) transition(response *modulev2.TransitionResponse, err error) (Transition, error) {
	if err != nil {
		return Transition{}, classifyRPCError(err)
	}
	if response == nil {
		return Transition{}, fmt.Errorf("%w: empty transition response", ErrProtocolViolation)
	}
	next, err := StateFromAny(response.NextState)
	if err != nil {
		return Transition{}, err
	}
	if c.cfg.StateTypeURL != "" && next.TypeURL != c.cfg.StateTypeURL {
		return Transition{}, fmt.Errorf("%w: next state type %q, expected %q", ErrProtocolViolation, next.TypeURL, c.cfg.StateTypeURL)
	}
	delta, err := MessageFromAny(response.Delta)
	if err != nil {
		return Transition{}, err
	}
	return Transition{Changed: response.Changed, NextState: next, Delta: delta}, nil
}

func projection(response *modulev2.ProjectionResponse, err error) (Projection, error) {
	if err != nil {
		return Projection{}, classifyRPCError(err)
	}
	if response == nil {
		return Projection{}, fmt.Errorf("%w: empty projection response", ErrProtocolViolation)
	}
	payload, payloadErr := MessageFromAny(response.Payload)
	if payloadErr != nil {
		return Projection{}, payloadErr
	}
	return Projection{Payload: payload, NoVisibleChange: response.NoVisibleChange}, nil
}

func deterministicContextProto(op DeterministicContext) *modulev2.DeterministicContext {
	return &modulev2.DeterministicContext{Revision: op.Revision, NowUnixMs: op.Now.UnixMilli(), Seed: op.Seed}
}

func viewerProto(viewer Viewer) *modulev2.Viewer {
	scope := modulev2.ViewScope_VIEW_SCOPE_PLAYER
	switch viewer.Scope {
	case ViewScopePublic:
		scope = modulev2.ViewScope_VIEW_SCOPE_PUBLIC
	case ViewScopeFull:
		scope = modulev2.ViewScope_VIEW_SCOPE_FULL
	}
	result := &modulev2.Viewer{PlayerId: viewer.PlayerID, Scope: scope}
	if viewer.Seated {
		result.SeatIndex = &viewer.SeatIndex
	}
	return result
}

func retryUnavailable[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	response, err := call(ctx)
	if status.Code(err) != codes.Unavailable || ctx.Err() != nil {
		return response, err
	}
	return call(ctx)
}

func classifyRPCError(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied:
		return fmt.Errorf("%w: %v", ErrCommandRejected, err)
	case codes.Unavailable, codes.DeadlineExceeded:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	default:
		return fmt.Errorf("%w: %v", ErrProtocolViolation, err)
	}
}
