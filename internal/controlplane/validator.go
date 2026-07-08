package controlplane

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Ruleshift/server/internal/module"
)

type Description struct {
	ModuleID           string
	Version            string
	ABIVersion         uint32
	StateTypeURL       string
	CommandTypeURLs    []string
	DescriptorDigest   string
	SupportsPlayerLeft bool
}

type RuntimeConnector interface {
	Connect(context.Context, RuntimeDeployment, Version) (module.Runtime, Description, error)
}

type ConformanceRunner interface {
	Run(context.Context, module.Runtime, Version, []byte) ([]byte, error)
}

type MigrationApplier interface {
	ApplyAdditive(context.Context, Version) error
}

type Validator struct {
	Store      Store
	Scheduler  Scheduler
	Connector  RuntimeConnector
	Runner     ConformanceRunner
	Migrations MigrationApplier
	Clock      func() time.Time
}

const validationCleanupTimeout = 10 * time.Second

func (v Validator) Publish(ctx context.Context, request PublishRequest) (Version, error) {
	if err := request.Validate(); err != nil {
		return Version{}, err
	}
	version, created, err := v.Store.PutVersion(ctx, request.Version())
	if err != nil {
		return Version{}, err
	}
	if !created {
		return version, nil
	}
	if err = v.validate(ctx, version, request.Vectors); err != nil {
		_ = v.Store.MarkStatus(context.Background(), version.Ref.DeveloperID, version.Ref.ModuleID, version.Ref.Version, StatusFailed)
		v.finishValidation(context.Background(), version, StatusFailed, err.Error())
		return Version{}, err
	}
	if err = v.Store.Activate(ctx, version.Ref.DeveloperID, version.Ref.ModuleID, version.Ref.Version); err != nil {
		return Version{}, err
	}
	v.finishValidation(ctx, version, StatusActive, "validation passed; version activated")
	return v.Store.GetVersion(ctx, version.Ref.DeveloperID, version.Ref.ModuleID, version.Ref.Version)
}

func (v Validator) validate(ctx context.Context, version Version, vectors []byte) (err error) {
	if v.Store == nil || v.Scheduler == nil || v.Connector == nil || v.Runner == nil {
		return fmt.Errorf("validator dependencies are incomplete")
	}
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), validationCleanupTimeout)
		defer cancel()
		if cleanupErr := v.Scheduler.Cleanup(cleanupCtx, version, 0); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
	}()
	now := time.Now().UTC()
	if v.Clock != nil {
		now = v.Clock()
	}
	_ = v.Store.SetValidation(ctx, ValidationRun{DeveloperID: version.Ref.DeveloperID, ModuleID: version.Ref.ModuleID, Version: version.Ref.Version, StartedAt: now, Result: StatusValidating, Logs: "validation started"})
	if err := v.Scheduler.EnsureTenant(ctx, version.Ref.DeveloperID); err != nil {
		return fmt.Errorf("ensure tenant: %w", err)
	}
	deployment, err := v.Scheduler.Deploy(ctx, version)
	if err != nil {
		return fmt.Errorf("deploy immutable image: %w", err)
	}
	if err = v.Scheduler.WaitReady(ctx, version, deployment); err != nil {
		return fmt.Errorf("wait for ready replicas: %w", err)
	}
	runtime, description, err := v.Connector.Connect(ctx, deployment, version)
	if err != nil {
		return fmt.Errorf("describe module: %w", err)
	}
	if err = matchDescription(version, description); err != nil {
		return err
	}
	first, err := v.Runner.Run(ctx, runtime, version, vectors)
	if err != nil {
		return fmt.Errorf("first conformance run: %w", err)
	}
	second, err := v.Runner.Run(ctx, runtime, version, vectors)
	if err != nil {
		return fmt.Errorf("second conformance run: %w", err)
	}
	if !bytes.Equal(first, second) {
		return fmt.Errorf("module is nondeterministic: conformance output differs between identical runs")
	}
	if v.Migrations != nil {
		if err = v.Migrations.ApplyAdditive(ctx, version); err != nil {
			return fmt.Errorf("apply additive database migrations: %w", err)
		}
	}
	version.Endpoint = deployment.Endpoint
	return nil
}

func matchDescription(version Version, description Description) error {
	if description.ModuleID != version.Ref.ModuleID || description.Version != version.Ref.Version {
		return fmt.Errorf("Describe identity does not match manifest")
	}
	if description.ABIVersion != version.ABIVersion {
		return fmt.Errorf("Describe ABI does not match manifest")
	}
	if description.StateTypeURL != version.Manifest.StateTypeURL {
		return fmt.Errorf("Describe state type URL does not match manifest")
	}
	if description.DescriptorDigest != version.DescriptorDigest {
		return fmt.Errorf("Describe descriptor digest does not match published descriptor")
	}
	for _, capability := range version.Manifest.Capabilities {
		if capability == "player_lifecycle" && !description.SupportsPlayerLeft {
			return fmt.Errorf("Describe capabilities do not match manifest: player_lifecycle requires PlayerLeft")
		}
	}
	if len(description.CommandTypeURLs) != len(version.Manifest.CommandTypeURLs) {
		return fmt.Errorf("Describe command type URLs do not match manifest")
	}
	expected := map[string]struct{}{}
	for _, value := range version.Manifest.CommandTypeURLs {
		expected[value] = struct{}{}
	}
	for _, value := range description.CommandTypeURLs {
		if _, ok := expected[value]; !ok {
			return fmt.Errorf("Describe command type URL %q is not in manifest", value)
		}
	}
	return nil
}

func (v Validator) finishValidation(ctx context.Context, version Version, result VersionStatus, logs string) {
	now := time.Now().UTC()
	if v.Clock != nil {
		now = v.Clock()
	}
	_ = v.Store.SetValidation(ctx, ValidationRun{DeveloperID: version.Ref.DeveloperID, ModuleID: version.Ref.ModuleID, Version: version.Ref.Version, StartedAt: version.CreatedAt, FinishedAt: &now, Result: result, Logs: logs})
}

type ProtocolViolationTracker struct {
	Store      Store
	Window     time.Duration
	Threshold  int
	now        func() time.Time
	mu         sync.Mutex
	violations map[string][]time.Time
}

func NewProtocolViolationTracker(store Store) *ProtocolViolationTracker {
	return &ProtocolViolationTracker{Store: store, Window: time.Minute, Threshold: 3, now: time.Now, violations: map[string][]time.Time{}}
}

// Record quarantines a version after three malformed/oversized/wrong-type
// responses in the rolling one-minute window. It is called only for protocol
// violations, never for ordinary command rejection or temporary unavailability.
func (t *ProtocolViolationTracker) Record(ctx context.Context, ref module.ModuleRef) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	k := ref.DeveloperID + "\x00" + ref.ModuleID + "\x00" + ref.Version
	cutoff := now.Add(-t.Window)
	items := t.violations[k][:0]
	for _, at := range t.violations[k] {
		if at.After(cutoff) {
			items = append(items, at)
		}
	}
	items = append(items, now)
	t.violations[k] = items
	if len(items) < t.Threshold {
		return false, nil
	}
	if err := t.Store.MarkStatus(ctx, ref.DeveloperID, ref.ModuleID, ref.Version, StatusDegraded); err != nil {
		return false, err
	}
	return true, nil
}
