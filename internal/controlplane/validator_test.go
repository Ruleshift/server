package controlplane

import (
	"context"
	"errors"
	"testing"

	"github.com/Ruleshift/server/internal/module"
)

func TestPublishFailurePersistsOriginalErrorAndCleansDeployment(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateModule(ctx, Module{DeveloperID: "dev", Key: "sample", DisplayName: "Sample"}); err != nil {
		t.Fatal(err)
	}
	waitErr := errors.New("workload failed")
	cleanupErr := errors.New("cleanup failed")
	scheduler := &failureCleanupScheduler{store: store, waitErr: waitErr, cleanupErr: cleanupErr}
	validator := Validator{
		Store:     store,
		Scheduler: scheduler,
		Connector: unusedRuntimeConnector{},
		Runner:    unusedConformanceRunner{},
	}

	if _, err := validator.Publish(ctx, validPublish()); !errors.Is(err, waitErr) {
		t.Fatalf("Publish error = %v, want original wait error", err)
	}

	version, err := store.GetVersion(ctx, "dev", "sample", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if version.Status != StatusFailed {
		t.Fatalf("version status = %q, want %q", version.Status, StatusFailed)
	}
	validation, err := store.GetValidation(ctx, "dev", "sample", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	wantLog := "wait for module readiness: workload failed"
	if validation.Result != StatusFailed || validation.FinishedAt == nil || validation.Logs != wantLog {
		t.Fatalf("validation = %+v, want failed result with log %q", validation, wantLog)
	}
	if scheduler.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", scheduler.cleanupCalls)
	}
	if scheduler.cleanupVersion.Status != StatusFailed || scheduler.cleanupVersion.Ref != version.Ref {
		t.Fatalf("cleanup version = %+v, want failed version %+v", scheduler.cleanupVersion, version.Ref)
	}
	if scheduler.cleanupPinnedRooms != 0 {
		t.Fatalf("cleanup pinned rooms = %d, want 0", scheduler.cleanupPinnedRooms)
	}
	if !scheduler.cleanupHadDeadline {
		t.Fatal("cleanup context did not have a deadline")
	}
	if scheduler.statusAtCleanup != StatusFailed || scheduler.logAtCleanup != wantLog {
		t.Fatalf("state at cleanup = status %q log %q, want failed status and original log", scheduler.statusAtCleanup, scheduler.logAtCleanup)
	}
}

type failureCleanupScheduler struct {
	store              *MemoryStore
	waitErr            error
	cleanupErr         error
	cleanupCalls       int
	cleanupVersion     Version
	cleanupPinnedRooms int
	cleanupHadDeadline bool
	statusAtCleanup    VersionStatus
	logAtCleanup       string
}

func (s *failureCleanupScheduler) EnsureTenant(context.Context, string) error { return nil }
func (s *failureCleanupScheduler) PutRegistryCredential(context.Context, string, string, string, string, string) error {
	return nil
}
func (s *failureCleanupScheduler) Deploy(context.Context, Version) (RuntimeDeployment, error) {
	return RuntimeDeployment{}, nil
}
func (s *failureCleanupScheduler) WaitReady(context.Context, Version, RuntimeDeployment) error {
	return s.waitErr
}
func (s *failureCleanupScheduler) Cleanup(ctx context.Context, version Version, pinnedRooms int) error {
	s.cleanupCalls++
	s.cleanupVersion = version
	s.cleanupPinnedRooms = pinnedRooms
	_, s.cleanupHadDeadline = ctx.Deadline()
	storedVersion, versionErr := s.store.GetVersion(ctx, version.Ref.DeveloperID, version.Ref.ModuleID, version.Ref.Version)
	if versionErr == nil {
		s.statusAtCleanup = storedVersion.Status
	}
	validation, validationErr := s.store.GetValidation(ctx, version.Ref.DeveloperID, version.Ref.ModuleID, version.Ref.Version)
	if validationErr == nil {
		s.logAtCleanup = validation.Logs
	}
	return s.cleanupErr
}

type unusedRuntimeConnector struct{}

func (unusedRuntimeConnector) Connect(context.Context, RuntimeDeployment, Version) (module.Runtime, Description, error) {
	panic("Connect must not be called after readiness failure")
}

type unusedConformanceRunner struct{}

func (unusedConformanceRunner) Run(context.Context, module.Runtime, Version, []byte) ([]byte, error) {
	panic("Run must not be called after readiness failure")
}
