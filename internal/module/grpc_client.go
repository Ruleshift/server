package module

import (
	"context"
	"fmt"
	"time"

	modulev1 "github.com/Ruleshift/server/internal/moduleruntime/generated/moduleruntimev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCClientConfig struct {
	TransitionDeadline time.Duration
	NewStateDeadline   time.Duration
	StateTypeURL       string
	CommandTypeURLs    map[string]struct{}
}

type GRPCClient struct {
	client modulev1.ModuleRuntimeClient
	cfg    GRPCClientConfig
}

func NewGRPCClient(client modulev1.ModuleRuntimeClient, cfg GRPCClientConfig) (*GRPCClient, error) {
	if client == nil {
		return nil, fmt.Errorf("module runtime client must not be nil")
	}
	if cfg.TransitionDeadline <= 0 {
		cfg.TransitionDeadline = DefaultDeadline
	}
	if cfg.TransitionDeadline > MaxTransitionDeadline {
		return nil, fmt.Errorf("transition deadline exceeds %s", MaxTransitionDeadline)
	}
	if cfg.NewStateDeadline <= 0 {
		cfg.NewStateDeadline = NewStateDeadline
	}
	if cfg.NewStateDeadline > NewStateDeadline {
		return nil, fmt.Errorf("new-state deadline exceeds %s", NewStateDeadline)
	}
	return &GRPCClient{client: client, cfg: cfg}, nil
}

func (c *GRPCClient) NewState(ctx context.Context, op Operation) (Transition, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.NewStateDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev1.TransitionResponse, error) {
		return c.client.NewState(callCtx, &modulev1.NewStateRequest{Context: operationProto(op)})
	})
	return c.transition(response, err)
}

func (c *GRPCClient) PlayerJoined(ctx context.Context, op Operation, state OpaqueState, playerID string) (Transition, error) {
	return c.playerTransition(ctx, op, state, playerID, true)
}

func (c *GRPCClient) PlayerLeft(ctx context.Context, op Operation, state OpaqueState, playerID string) (Transition, error) {
	return c.playerTransition(ctx, op, state, playerID, false)
}

func (c *GRPCClient) playerTransition(ctx context.Context, op Operation, state OpaqueState, playerID string, joined bool) (Transition, error) {
	request := &modulev1.PlayerTransitionRequest{Context: operationProto(op), State: state.Any(), PlayerId: playerID}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.TransitionDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev1.TransitionResponse, error) {
		if joined {
			return c.client.PlayerJoined(callCtx, request)
		}
		return c.client.PlayerLeft(callCtx, request)
	})
	return c.transition(response, err)
}

func (c *GRPCClient) Apply(ctx context.Context, op Operation, state OpaqueState, playerID string, command OpaqueState) (Transition, error) {
	if len(c.cfg.CommandTypeURLs) > 0 {
		if _, ok := c.cfg.CommandTypeURLs[command.TypeURL]; !ok {
			return Transition{}, fmt.Errorf("%w: unsupported command type %q", ErrCommandRejected, command.TypeURL)
		}
	}
	request := &modulev1.ApplyRequest{Context: operationProto(op), State: state.Any(), PlayerId: playerID, Command: command.Any()}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.TransitionDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev1.TransitionResponse, error) {
		return c.client.Apply(callCtx, request)
	})
	return c.transition(response, err)
}

func (c *GRPCClient) ProjectSnapshot(ctx context.Context, op Operation, state OpaqueState, viewer Viewer) (Projection, error) {
	request := &modulev1.ProjectRequest{Context: operationProto(op), State: state.Any(), Viewer: viewerProto(viewer)}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.TransitionDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev1.ProjectionResponse, error) {
		return c.client.ProjectSnapshot(callCtx, request)
	})
	return projection(response, err)
}

func (c *GRPCClient) ProjectDelta(ctx context.Context, op Operation, before, after, delta OpaqueState, viewer Viewer) (Projection, error) {
	request := &modulev1.ProjectDeltaRequest{Context: operationProto(op), BeforeState: before.Any(), AfterState: after.Any(), Delta: delta.Any(), Viewer: viewerProto(viewer)}
	deadlineCtx, cancel := context.WithTimeout(ctx, c.cfg.TransitionDeadline)
	defer cancel()
	response, err := retryUnavailable(deadlineCtx, func(callCtx context.Context) (*modulev1.ProjectionResponse, error) {
		return c.client.ProjectDelta(callCtx, request)
	})
	return projection(response, err)
}

func (c *GRPCClient) transition(response *modulev1.TransitionResponse, err error) (Transition, error) {
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

func projection(response *modulev1.ProjectionResponse, err error) (Projection, error) {
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

func operationProto(op Operation) *modulev1.RoomContext {
	return &modulev1.RoomContext{OperationId: op.OperationID, RoomId: op.RoomID, Revision: op.Revision, NowUnixMs: op.Now.UnixMilli(), Seed: op.Seed}
}

func viewerProto(viewer Viewer) *modulev1.Viewer {
	joinMode := modulev1.JoinMode_JOIN_MODE_PLAYER
	if viewer.JoinMode == JoinModeSpectator {
		joinMode = modulev1.JoinMode_JOIN_MODE_SPECTATOR
	}
	scope := modulev1.ViewScope_VIEW_SCOPE_PLAYER
	switch viewer.Scope {
	case ViewScopePublic:
		scope = modulev1.ViewScope_VIEW_SCOPE_PUBLIC
	case ViewScopeFull:
		scope = modulev1.ViewScope_VIEW_SCOPE_FULL
	}
	return &modulev1.Viewer{PlayerId: viewer.PlayerID, JoinMode: joinMode, Scope: scope}
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
