package controlplane

import (
	"context"
	"errors"
	"testing"

	"github.com/Ruleshift/server/internal/module"
)

type cleanupTrackingScheduler struct {
	waitErr           error
	cleanupCalls      int
	cleanupContextErr error
}

func (s *cleanupTrackingScheduler) EnsureTenant(context.Context, string) error { return nil }
func (s *cleanupTrackingScheduler) PutRegistryCredential(context.Context, string, string, string, string, string) error {
	return nil
}
func (s *cleanupTrackingScheduler) Deploy(context.Context, Version) (RuntimeDeployment, error) {
	return RuntimeDeployment{Endpoint: "module.test:50051"}, nil
}
func (s *cleanupTrackingScheduler) WaitReady(context.Context, Version, RuntimeDeployment) error {
	return s.waitErr
}
func (s *cleanupTrackingScheduler) Cleanup(ctx context.Context, _ Version, _ int) error {
	s.cleanupCalls++
	s.cleanupContextErr = ctx.Err()
	return nil
}

type unusedConnector struct{}

func (unusedConnector) Connect(context.Context, RuntimeDeployment, Version) (module.Runtime, Description, error) {
	return nil, Description{}, errors.New("unexpected Connect call")
}

type unusedRunner struct{}

func (unusedRunner) Run(context.Context, module.Runtime, Version, []byte) ([]byte, error) {
	return nil, errors.New("unexpected conformance call")
}

func TestWaitReadyFailureCleansUpExactlyOnce(t *testing.T) {
	scheduler := &cleanupTrackingScheduler{waitErr: errors.New("exceeded quota: ruleshift-module-quota")}
	validator := Validator{Store: NewMemoryStore(), Scheduler: scheduler, Connector: unusedConnector{}, Runner: unusedRunner{}}

	err := validator.validate(context.Background(), Version{}, nil)
	if err == nil || !errors.Is(err, scheduler.waitErr) {
		t.Fatalf("validate error = %v, want quota error", err)
	}
	if scheduler.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", scheduler.cleanupCalls)
	}
}

func TestCanceledValidationContextDoesNotCancelCleanup(t *testing.T) {
	scheduler := &cleanupTrackingScheduler{waitErr: context.Canceled}
	validator := Validator{Store: NewMemoryStore(), Scheduler: scheduler, Connector: unusedConnector{}, Runner: unusedRunner{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := validator.validate(ctx, Version{}, nil); err == nil {
		t.Fatal("validate unexpectedly succeeded")
	}
	if scheduler.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", scheduler.cleanupCalls)
	}
	if scheduler.cleanupContextErr != nil {
		t.Fatalf("cleanup context error = %v, want nil", scheduler.cleanupContextErr)
	}
}
