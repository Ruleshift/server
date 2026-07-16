// Package controlplane owns module publication, validation, activation and room
// routing metadata. Registry credentials intentionally do not appear in these
// persisted models.
package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/Ruleshift/server/internal/module"
	"github.com/distribution/reference"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	MaxDescriptorBytes    = 4 << 20
	MaxValidationLogBytes = 64 << 10
)

var (
	ErrModuleNotFound  = errors.New("module not found")
	ErrVersionNotFound = errors.New("module version not found")
	ErrVersionConflict = errors.New("module version already exists with different digest")
	ErrUnauthorized    = errors.New("resource belongs to another developer")
)

type VersionStatus string

const (
	StatusValidating VersionStatus = "validating"
	StatusActive     VersionStatus = "active"
	StatusInactive   VersionStatus = "inactive"
	StatusFailed     VersionStatus = "failed"
	StatusDegraded   VersionStatus = "degraded"
)

type Manifest struct {
	ModuleID             string              `json:"module_id"`
	Version              string              `json:"version"`
	ABIVersion           uint32              `json:"abi_version"`
	StateTypeURL         string              `json:"state_type_url"`
	CommandTypeURLs      []string            `json:"command_type_urls"`
	TransitionDeadlineMS int                 `json:"transition_deadline_ms,omitempty"`
	Capabilities         []string            `json:"capabilities,omitempty"`
	DatabaseMigrations   []DatabaseMigration `json:"database_migrations,omitempty"`
}

type DatabaseMigration struct {
	Version uint64  `json:"version"`
	Name    string  `json:"name"`
	Tables  []Table `json:"tables"`
}

type Table struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}
type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable,omitempty"`
	PrimaryKey bool   `json:"primary_key,omitempty"`
}

type Module struct {
	DeveloperID   string    `json:"developer_id"`
	Key           string    `json:"key"`
	DisplayName   string    `json:"display_name"`
	ActiveVersion string    `json:"active_version,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Version struct {
	Ref              module.ModuleRef `json:"ref"`
	ImageRef         string           `json:"image_ref"`
	ABIVersion       uint32           `json:"abi_version"`
	DescriptorDigest string           `json:"descriptor_digest"`
	DescriptorSet    []byte           `json:"-"`
	Manifest         Manifest         `json:"manifest"`
	CredentialName   string           `json:"credential_name,omitempty"`
	Status           VersionStatus    `json:"status"`
	Endpoint         string           `json:"endpoint,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type ValidationRun struct {
	DeveloperID string        `json:"developer_id"`
	ModuleID    string        `json:"module_id"`
	Version     string        `json:"version"`
	StartedAt   time.Time     `json:"started_at"`
	FinishedAt  *time.Time    `json:"finished_at,omitempty"`
	Result      VersionStatus `json:"result"`
	Logs        string        `json:"logs"`
}

type ConformanceVectors struct {
	Vectors []json.RawMessage `json:"vectors"`
}

type PublishRequest struct {
	DeveloperID    string
	ModuleID       string
	ImageRef       string
	CredentialName string
	Manifest       Manifest
	DescriptorSet  []byte
	Vectors        []byte
}

func (r PublishRequest) Validate() error {
	if r.DeveloperID == "" || r.ModuleID == "" {
		return fmt.Errorf("developer and module are required")
	}
	if r.Manifest.ModuleID != r.ModuleID {
		return fmt.Errorf("manifest module_id does not match URL")
	}
	if _, err := semver.StrictNewVersion(r.Manifest.Version); err != nil {
		return fmt.Errorf("version %q is not SemVer", r.Manifest.Version)
	}
	if r.Manifest.ABIVersion != module.ABIVersion {
		return fmt.Errorf("unsupported module ABI %d", r.Manifest.ABIVersion)
	}
	imageReference, err := reference.ParseDockerRef(r.ImageRef)
	digested, hasDigest := imageReference.(reference.Digested)
	if err != nil || !hasDigest || digested.Digest().Algorithm().String() != "sha256" || len(digested.Digest().Encoded()) != sha256.Size*2 || digested.Digest().String() != strings.ToLower(digested.Digest().String()) {
		return fmt.Errorf("OCI image reference must contain @sha256:<64 lowercase hex>")
	}
	if len(r.DescriptorSet) == 0 || len(r.DescriptorSet) > MaxDescriptorBytes {
		return fmt.Errorf("descriptor set size must be between 1 and %d bytes", MaxDescriptorBytes)
	}
	if len(r.Vectors) == 0 {
		return fmt.Errorf("conformance vectors are required")
	}
	if r.Manifest.StateTypeURL == "" {
		return fmt.Errorf("state_type_url is required")
	}
	if isReservedTypeURL(r.Manifest.StateTypeURL) {
		return fmt.Errorf("module state type conflicts with reserved Ruleshift protobuf packages")
	}
	if len(r.Manifest.CommandTypeURLs) == 0 {
		return fmt.Errorf("at least one command type URL is required")
	}
	seen := map[string]struct{}{}
	for _, value := range r.Manifest.CommandTypeURLs {
		if value == "" || isReservedTypeURL(value) {
			return fmt.Errorf("invalid or reserved command type URL %q", value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate command type URL %q", value)
		}
		seen[value] = struct{}{}
	}
	if r.Manifest.TransitionDeadlineMS < 0 || r.Manifest.TransitionDeadlineMS > int(module.MaxTransitionDeadline/time.Millisecond) {
		return fmt.Errorf("transition deadline must not exceed 250ms")
	}
	if err := ValidateAdditiveMigrations(r.Manifest.DatabaseMigrations); err != nil {
		return err
	}
	if err := validateDescriptorSet(r.DescriptorSet, r.Manifest); err != nil {
		return err
	}
	return nil
}

func isReservedTypeURL(value string) bool {
	for _, prefix := range []string{"type.googleapis.com/ruleshift.v1.", "type.googleapis.com/ruleshift.v2.", "type.googleapis.com/ruleshift.module.v1."} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func validateDescriptorSet(raw []byte, manifest Manifest) error {
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &set); err != nil {
		return fmt.Errorf("decode protobuf descriptor set: %w", err)
	}
	if len(set.File) == 0 {
		return fmt.Errorf("protobuf descriptor set contains no files")
	}
	for _, file := range set.File {
		pkg := file.GetPackage()
		if pkg == "ruleshift.v1" || pkg == "ruleshift.v2" || pkg == "ruleshift.module.v1" {
			return fmt.Errorf("descriptor package %q conflicts with Ruleshift core", pkg)
		}
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return fmt.Errorf("validate protobuf descriptor set: %w", err)
	}
	required := append([]string{manifest.StateTypeURL}, manifest.CommandTypeURLs...)
	for _, value := range required {
		const typeURLPrefix = "type.googleapis.com/"
		if !strings.HasPrefix(value, typeURLPrefix) {
			return fmt.Errorf("manifest type URL %q is absent from descriptor set", value)
		}
		descriptor, findErr := files.FindDescriptorByName(protoreflect.FullName(strings.TrimPrefix(value, typeURLPrefix)))
		if findErr != nil {
			return fmt.Errorf("manifest type URL %q is absent from descriptor set", value)
		}
		if _, ok := descriptor.(protoreflect.MessageDescriptor); !ok {
			return fmt.Errorf("manifest type URL %q does not identify a protobuf message", value)
		}
	}
	return nil
}

func (r PublishRequest) Version() Version {
	digest := sha256.Sum256(r.DescriptorSet)
	imageDigest := r.ImageRef[strings.LastIndex(r.ImageRef, "@")+1:]
	now := time.Now().UTC()
	return Version{Ref: module.ModuleRef{DeveloperID: r.DeveloperID, ModuleID: r.ModuleID, Version: r.Manifest.Version, ImageDigest: imageDigest}, ImageRef: r.ImageRef, ABIVersion: r.Manifest.ABIVersion, DescriptorDigest: "sha256:" + hex.EncodeToString(digest[:]), DescriptorSet: slices.Clone(r.DescriptorSet), Manifest: r.Manifest, CredentialName: r.CredentialName, Status: StatusValidating, CreatedAt: now, UpdatedAt: now}
}

func ValidateAdditiveMigrations(migrations []DatabaseMigration) error {
	identifier := regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	var previous uint64
	for _, migration := range migrations {
		if migration.Version == 0 || migration.Version <= previous {
			return fmt.Errorf("database migration versions must be positive and increasing")
		}
		previous = migration.Version
		if migration.Name == "" || len(migration.Tables) == 0 {
			return fmt.Errorf("database migration requires name and tables")
		}
		for _, table := range migration.Tables {
			if !identifier.MatchString(table.Name) || len(table.Columns) == 0 {
				return fmt.Errorf("invalid additive table %q", table.Name)
			}
			for _, column := range table.Columns {
				if !identifier.MatchString(column.Name) {
					return fmt.Errorf("invalid column %q", column.Name)
				}
			}
		}
	}
	return nil
}

type Store interface {
	CreateModule(context.Context, Module) (Module, error)
	GetModule(context.Context, string, string) (Module, error)
	PutVersion(context.Context, Version) (Version, bool, error)
	GetVersion(context.Context, string, string, string) (Version, error)
	SetValidation(context.Context, ValidationRun) error
	GetValidation(context.Context, string, string, string) (ValidationRun, error)
	Activate(context.Context, string, string, string) error
	MarkStatus(context.Context, string, string, string, VersionStatus) error
	ResolveForNewRoom(context.Context, string, string, string) (Version, error)
}

type RuntimeDeployment struct {
	Endpoint string
	RPCToken string
}

type Scheduler interface {
	EnsureTenant(context.Context, string) error
	PutRegistryCredential(context.Context, string, string, string, string, string) error
	Deploy(context.Context, Version) (RuntimeDeployment, error)
	WaitReady(context.Context, Version, RuntimeDeployment) error
	Cleanup(context.Context, Version, int) error
}
