package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Ruleshift/server/internal/controlplane"
	"github.com/jackc/pgx/v5"
)

// ControlStore exposes the v2 control-plane model without colliding with the
// temporary v1 data-browser methods on Platform.
type ControlStore struct{ platform *Platform }

func (p *Platform) V2ControlStore() *ControlStore { return &ControlStore{platform: p} }

func (s *ControlStore) CreateModule(ctx context.Context, value controlplane.Module) (controlplane.Module, error) {
	if value.DeveloperID == "" || value.Key == "" || value.DisplayName == "" {
		return controlplane.Module{}, fmt.Errorf("developer, key and display name are required")
	}
	now := time.Now().UTC()
	databaseName, err := moduleDatabaseName(s.platform.cfg.ModuleDatabasePrefix, value.DeveloperID, value.Key)
	if err != nil {
		return controlplane.Module{}, err
	}
	id := value.DeveloperID + ":" + value.Key
	_, err = s.platform.control.Exec(ctx, `INSERT INTO modules(id,developer_id,module_key,display_name,database_name,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6) ON CONFLICT(developer_id,module_key) DO UPDATE SET display_name=EXCLUDED.display_name,updated_at=EXCLUDED.updated_at`, id, value.DeveloperID, value.Key, value.DisplayName, databaseName, now)
	if err != nil {
		return controlplane.Module{}, err
	}
	return s.GetModule(ctx, value.DeveloperID, value.Key)
}
func (s *ControlStore) GetModule(ctx context.Context, d, m string) (controlplane.Module, error) {
	var value controlplane.Module
	var active *string
	err := s.platform.control.QueryRow(ctx, `SELECT developer_id,module_key,display_name,active_version,created_at,updated_at FROM modules WHERE developer_id=$1 AND module_key=$2`, d, m).Scan(&value.DeveloperID, &value.Key, &value.DisplayName, &active, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return controlplane.Module{}, controlplane.ErrModuleNotFound
	}
	if err != nil {
		return controlplane.Module{}, err
	}
	if active != nil {
		value.ActiveVersion = *active
	}
	return value, nil
}
func (s *ControlStore) PutVersion(ctx context.Context, value controlplane.Version) (controlplane.Version, bool, error) {
	manifest, err := json.Marshal(value.Manifest)
	if err != nil {
		return controlplane.Version{}, false, err
	}
	result, err := s.platform.control.Exec(ctx, `INSERT INTO module_versions(developer_id,module_id,version,image_ref,image_digest,abi_version,descriptor_digest,descriptor_set,manifest,credential_name,lifecycle_status,endpoint,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13) ON CONFLICT(developer_id,module_id,version) DO NOTHING`, value.Ref.DeveloperID, value.Ref.ModuleID, value.Ref.Version, value.ImageRef, value.Ref.ImageDigest, value.ABIVersion, value.DescriptorDigest, value.DescriptorSet, manifest, value.CredentialName, string(value.Status), value.Endpoint, value.CreatedAt)
	if err != nil {
		return controlplane.Version{}, false, err
	}
	rows := result.RowsAffected()
	if rows == 0 {
		existing, getErr := s.GetVersion(ctx, value.Ref.DeveloperID, value.Ref.ModuleID, value.Ref.Version)
		if getErr != nil {
			return controlplane.Version{}, false, getErr
		}
		if existing.Ref.ImageDigest != value.Ref.ImageDigest {
			return controlplane.Version{}, false, controlplane.ErrVersionConflict
		}
		return existing, false, nil
	}
	return value, true, nil
}
func (s *ControlStore) GetVersion(ctx context.Context, d, m, v string) (controlplane.Version, error) {
	var value controlplane.Version
	var manifest []byte
	var status string
	err := s.platform.control.QueryRow(ctx, `SELECT image_ref,image_digest,abi_version,descriptor_digest,descriptor_set,manifest,credential_name,lifecycle_status,endpoint,created_at,updated_at FROM module_versions WHERE developer_id=$1 AND module_id=$2 AND version=$3`, d, m, v).Scan(&value.ImageRef, &value.Ref.ImageDigest, &value.ABIVersion, &value.DescriptorDigest, &value.DescriptorSet, &manifest, &value.CredentialName, &status, &value.Endpoint, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return controlplane.Version{}, controlplane.ErrVersionNotFound
	}
	if err != nil {
		return controlplane.Version{}, err
	}
	value.Ref.DeveloperID = d
	value.Ref.ModuleID = m
	value.Ref.Version = v
	value.Status = controlplane.VersionStatus(status)
	if err = json.Unmarshal(manifest, &value.Manifest); err != nil {
		return controlplane.Version{}, err
	}
	return value, nil
}
func (s *ControlStore) SetValidation(ctx context.Context, value controlplane.ValidationRun) error {
	if len(value.Logs) > controlplane.MaxValidationLogBytes {
		value.Logs = value.Logs[:controlplane.MaxValidationLogBytes]
	}
	_, err := s.platform.control.Exec(ctx, `INSERT INTO module_validation_runs(developer_id,module_id,version,started_at,finished_at,result,logs) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(developer_id,module_id,version) DO UPDATE SET started_at=EXCLUDED.started_at,finished_at=EXCLUDED.finished_at,result=EXCLUDED.result,logs=EXCLUDED.logs`, value.DeveloperID, value.ModuleID, value.Version, value.StartedAt, value.FinishedAt, string(value.Result), value.Logs)
	return err
}
func (s *ControlStore) GetValidation(ctx context.Context, d, m, v string) (controlplane.ValidationRun, error) {
	var value controlplane.ValidationRun
	var status string
	err := s.platform.control.QueryRow(ctx, `SELECT started_at,finished_at,result,logs FROM module_validation_runs WHERE developer_id=$1 AND module_id=$2 AND version=$3`, d, m, v).Scan(&value.StartedAt, &value.FinishedAt, &status, &value.Logs)
	if errors.Is(err, pgx.ErrNoRows) {
		return controlplane.ValidationRun{}, controlplane.ErrVersionNotFound
	}
	if err != nil {
		return controlplane.ValidationRun{}, err
	}
	value.DeveloperID = d
	value.ModuleID = m
	value.Version = v
	value.Result = controlplane.VersionStatus(status)
	return value, nil
}
func (s *ControlStore) Activate(ctx context.Context, d, m, v string) error {
	return pgx.BeginFunc(ctx, s.platform.control, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE module_versions SET lifecycle_status='inactive',updated_at=NOW() WHERE developer_id=$1 AND module_id=$2 AND lifecycle_status='active'`, d, m); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE module_versions SET lifecycle_status='active',updated_at=NOW() WHERE developer_id=$1 AND module_id=$2 AND version=$3 AND lifecycle_status='validating'`, d, m, v)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return controlplane.ErrVersionNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE modules SET active_version=$3,updated_at=NOW() WHERE developer_id=$1 AND module_key=$2`, d, m, v)
		return err
	})
}
func (s *ControlStore) MarkStatus(ctx context.Context, d, m, v string, status controlplane.VersionStatus) error {
	result, err := s.platform.control.Exec(ctx, `UPDATE module_versions SET lifecycle_status=$4,updated_at=NOW() WHERE developer_id=$1 AND module_id=$2 AND version=$3`, d, m, v, string(status))
	if err != nil {
		return err
	}
	rows := result.RowsAffected()
	if rows != 1 {
		return controlplane.ErrVersionNotFound
	}
	return nil
}
func (s *ControlStore) ResolveForNewRoom(ctx context.Context, d, m, v string) (controlplane.Version, error) {
	if v != "" {
		value, err := s.GetVersion(ctx, d, m, v)
		if err != nil {
			return controlplane.Version{}, err
		}
		if value.Status != controlplane.StatusActive && value.Status != controlplane.StatusInactive {
			return controlplane.Version{}, controlplane.ErrVersionNotFound
		}
		return value, nil
	}
	var active *string
	if err := s.platform.control.QueryRow(ctx, `SELECT active_version FROM modules WHERE developer_id=$1 AND module_key=$2`, d, m).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
		return controlplane.Version{}, controlplane.ErrModuleNotFound
	} else if err != nil {
		return controlplane.Version{}, err
	}
	if active != nil {
		value, err := s.GetVersion(ctx, d, m, *active)
		if err == nil && value.Status == controlplane.StatusActive {
			return value, nil
		}
	}
	var fallback string
	err := s.platform.control.QueryRow(ctx, `SELECT version FROM module_versions WHERE developer_id=$1 AND module_id=$2 AND lifecycle_status='inactive' ORDER BY updated_at DESC LIMIT 1`, d, m).Scan(&fallback)
	if errors.Is(err, pgx.ErrNoRows) {
		return controlplane.Version{}, controlplane.ErrVersionNotFound
	}
	if err != nil {
		return controlplane.Version{}, err
	}
	return s.GetVersion(ctx, d, m, fallback)
}
