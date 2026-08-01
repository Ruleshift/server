// Package ruleshift is the developer-facing client SDK for the Ruleshift service.
package ruleshift

import "time"

type ColumnType string

const (
	ColumnTypeString    ColumnType = "string"
	ColumnTypeInt64     ColumnType = "int64"
	ColumnTypeFloat64   ColumnType = "float64"
	ColumnTypeBool      ColumnType = "bool"
	ColumnTypeTimestamp ColumnType = "timestamp"
	ColumnTypeJSON      ColumnType = "json"
)

type ColumnDefinition struct {
	Name       string     `json:"name"`
	Type       ColumnType `json:"type"`
	Nullable   bool       `json:"nullable,omitempty"`
	PrimaryKey bool       `json:"primary_key,omitempty"`
}

type TableDefinition struct {
	Name    string             `json:"name"`
	Columns []ColumnDefinition `json:"columns"`
}

type RowsPage struct {
	Module  string           `json:"module"`
	Table   string           `json:"table"`
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

type CreateRowRequest struct {
	Values map[string]any `json:"values"`
}

type Row struct {
	Module string         `json:"module"`
	Table  string         `json:"table"`
	Values map[string]any `json:"values"`
}

type RuntimeManifest struct {
	ModuleID             string              `json:"module_id"`
	Version              string              `json:"version"`
	ABIVersion           uint32              `json:"abi_version"`
	MinPlayers           uint32              `json:"min_players"`
	MaxPlayers           uint32              `json:"max_players"`
	StateTypeURL         string              `json:"state_type_url"`
	CommandTypeURLs      []string            `json:"command_type_urls"`
	TransitionDeadlineMS int                 `json:"transition_deadline_ms,omitempty"`
	Capabilities         []string            `json:"capabilities,omitempty"`
	DatabaseMigrations   []DatabaseMigration `json:"database_migrations,omitempty"`
}

type DatabaseMigration struct {
	Version uint64            `json:"version"`
	Name    string            `json:"name"`
	Tables  []TableDefinition `json:"tables"`
}

type PublishModuleVersionRequest struct {
	ModuleID           string
	OCIReference       string
	RegistryCredential string
	Manifest           RuntimeManifest
	DescriptorSet      []byte
	ConformanceVectors []byte
}

type ModuleReference struct {
	DeveloperID string `json:"developer_id"`
	ModuleID    string `json:"module_id"`
	Version     string `json:"version"`
	ImageDigest string `json:"image_digest"`
}

type ModuleVersion struct {
	Ref              ModuleReference `json:"ref"`
	ImageRef         string          `json:"image_ref"`
	ABIVersion       uint32          `json:"abi_version"`
	DescriptorDigest string          `json:"descriptor_digest"`
	Manifest         RuntimeManifest `json:"manifest"`
	Status           string          `json:"status"`
	Endpoint         string          `json:"endpoint,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type RuntimeModule struct {
	DeveloperID   string    `json:"developer_id"`
	Key           string    `json:"key"`
	DisplayName   string    `json:"display_name"`
	ActiveVersion string    `json:"active_version,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ValidationStatus struct {
	DeveloperID string     `json:"developer_id"`
	ModuleID    string     `json:"module_id"`
	Version     string     `json:"version"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Result      string     `json:"result"`
	Logs        string     `json:"logs"`
}

type CreateRoomRequest struct {
	ModuleID    string `json:"module_id"`
	Version     string `json:"version,omitempty"`
	PlayerCount uint32 `json:"player_count,omitempty"`
}

type Room struct {
	RoomID         string          `json:"room_id"`
	Module         ModuleReference `json:"module"`
	ModuleDatabase string          `json:"module_database"`
	PlayerCount    uint32          `json:"player_count"`
	Seed           uint64          `json:"seed"`
	CreatedAt      time.Time       `json:"created_at"`
	InviteCode     string          `json:"invite_code"`
	InviteDeadline time.Time       `json:"invite_deadline"`
}
