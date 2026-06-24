package controlplane

import (
	"context"
	"sync"
	"time"
)

type MemoryStore struct {
	mu          sync.Mutex
	modules     map[string]Module
	versions    map[string]Version
	validations map[string]ValidationRun
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{modules: map[string]Module{}, versions: map[string]Version{}, validations: map[string]ValidationRun{}}
}
func key(parts ...string) string {
	value := ""
	for _, part := range parts {
		value += "\x00" + part
	}
	return value
}

func (s *MemoryStore) CreateModule(ctx context.Context, value Module) (Module, error) {
	if err := ctx.Err(); err != nil {
		return Module{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(value.DeveloperID, value.Key)
	if existing, ok := s.modules[k]; ok {
		return existing, nil
	}
	now := time.Now().UTC()
	value.CreatedAt = now
	value.UpdatedAt = now
	s.modules[k] = value
	return value, nil
}
func (s *MemoryStore) GetModule(ctx context.Context, developerID, moduleID string) (Module, error) {
	if err := ctx.Err(); err != nil {
		return Module{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.modules[key(developerID, moduleID)]
	if !ok {
		return Module{}, ErrModuleNotFound
	}
	return value, nil
}
func (s *MemoryStore) PutVersion(ctx context.Context, value Version) (Version, bool, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(value.Ref.DeveloperID, value.Ref.ModuleID, value.Ref.Version)
	if existing, ok := s.versions[k]; ok {
		if existing.Ref.ImageDigest != value.Ref.ImageDigest {
			return Version{}, false, ErrVersionConflict
		}
		return existing, false, nil
	}
	if _, ok := s.modules[key(value.Ref.DeveloperID, value.Ref.ModuleID)]; !ok {
		return Version{}, false, ErrModuleNotFound
	}
	s.versions[k] = value
	return value, true, nil
}
func (s *MemoryStore) GetVersion(ctx context.Context, d, m, v string) (Version, error) {
	if err := ctx.Err(); err != nil {
		return Version{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.versions[key(d, m, v)]
	if !ok {
		return Version{}, ErrVersionNotFound
	}
	return value, nil
}
func (s *MemoryStore) SetValidation(ctx context.Context, value ValidationRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(value.Logs) > MaxValidationLogBytes {
		value.Logs = value.Logs[:MaxValidationLogBytes]
	}
	s.validations[key(value.DeveloperID, value.ModuleID, value.Version)] = value
	return nil
}
func (s *MemoryStore) GetValidation(ctx context.Context, d, m, v string) (ValidationRun, error) {
	if err := ctx.Err(); err != nil {
		return ValidationRun{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.validations[key(d, m, v)]
	if !ok {
		return ValidationRun{}, ErrVersionNotFound
	}
	return value, nil
}
func (s *MemoryStore) Activate(ctx context.Context, d, m, v string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mk := key(d, m)
	mod, ok := s.modules[mk]
	if !ok {
		return ErrModuleNotFound
	}
	targetKey := key(d, m, v)
	target, ok := s.versions[targetKey]
	if !ok {
		return ErrVersionNotFound
	}
	for k, current := range s.versions {
		if current.Ref.DeveloperID == d && current.Ref.ModuleID == m && current.Status == StatusActive {
			current.Status = StatusInactive
			current.UpdatedAt = time.Now().UTC()
			s.versions[k] = current
		}
	}
	target.Status = StatusActive
	target.UpdatedAt = time.Now().UTC()
	s.versions[targetKey] = target
	mod.ActiveVersion = v
	mod.UpdatedAt = time.Now().UTC()
	s.modules[mk] = mod
	return nil
}
func (s *MemoryStore) MarkStatus(ctx context.Context, d, m, v string, status VersionStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(d, m, v)
	value, ok := s.versions[k]
	if !ok {
		return ErrVersionNotFound
	}
	value.Status = status
	value.UpdatedAt = time.Now().UTC()
	s.versions[k] = value
	return nil
}
func (s *MemoryStore) ResolveForNewRoom(ctx context.Context, d, m, v string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v != "" {
		value, ok := s.versions[key(d, m, v)]
		if !ok {
			return Version{}, ErrVersionNotFound
		}
		if value.Status != StatusActive && value.Status != StatusInactive {
			return Version{}, ErrVersionNotFound
		}
		return value, nil
	}
	mod, ok := s.modules[key(d, m)]
	if !ok {
		return Version{}, ErrModuleNotFound
	}
	if mod.ActiveVersion != "" {
		active := s.versions[key(d, m, mod.ActiveVersion)]
		if active.Status == StatusActive {
			return active, nil
		}
	}
	var fallback Version
	for _, candidate := range s.versions {
		if candidate.Ref.DeveloperID == d && candidate.Ref.ModuleID == m && candidate.Status == StatusInactive && (fallback.CreatedAt.IsZero() || candidate.UpdatedAt.After(fallback.UpdatedAt)) {
			fallback = candidate
		}
	}
	if fallback.CreatedAt.IsZero() {
		return Version{}, ErrVersionNotFound
	}
	return fallback, nil
}
