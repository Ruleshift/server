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

type ModuleSchema struct {
	Tables []TableDefinition `json:"tables"`
}

type CreateModuleRequest struct {
	Key         string       `json:"key"`
	DisplayName string       `json:"display_name"`
	Schema      ModuleSchema `json:"schema"`
}

type Module struct {
	Key         string    `json:"key"`
	DisplayName string    `json:"display_name"`
	GameType    uint8     `json:"game_type"`
	CreatedAt   time.Time `json:"created_at"`
}

type TableSchema struct {
	Name    string         `json:"name"`
	Columns []ColumnSchema `json:"columns"`
}

type ColumnSchema struct {
	Name       string `json:"name"`
	SQLType    string `json:"sql_type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primary_key"`
}

type Schema struct {
	Module string        `json:"module"`
	Tables []TableSchema `json:"tables"`
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
