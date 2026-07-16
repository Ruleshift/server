package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Ruleshift/server/pkg/ruleshift"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrModuleNotFound = errors.New("developer module not found")
	ErrTableNotFound  = errors.New("module table not found")
)

var platformManagedModuleTables = map[string]struct{}{
	"rooms":                       {},
	"room_events":                 {},
	"room_snapshots":              {},
	"ruleshift_schema_migrations": {},
}

func (p *Platform) ListTableRows(ctx context.Context, developerID, moduleKey, tableName string, limit, offset int) (ruleshift.RowsPage, error) {
	if !validPostgresIdentifier(tableName) {
		return ruleshift.RowsPage{}, ErrTableNotFound
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		return ruleshift.RowsPage{}, fmt.Errorf("row limit must not exceed 200")
	}
	if offset < 0 || offset > 1_000_000 {
		return ruleshift.RowsPage{}, fmt.Errorf("row offset must be between 0 and 1000000")
	}

	module, db, err := p.moduleDatabase(ctx, developerID, moduleKey)
	if err != nil {
		return ruleshift.RowsPage{}, err
	}
	var exists bool
	if err := db.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = 'public' AND table_type = 'BASE TABLE' AND table_name = $1
)`, tableName).Scan(&exists); err != nil {
		return ruleshift.RowsPage{}, fmt.Errorf("inspect module table: %w", err)
	}
	if !exists {
		return ruleshift.RowsPage{}, ErrTableNotFound
	}

	rows, err := db.Query(ctx, `SELECT * FROM `+pgx.Identifier{tableName}.Sanitize()+` LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return ruleshift.RowsPage{}, fmt.Errorf("list module table rows: %w", err)
	}
	fields := rows.FieldDescriptions()
	columns := make([]string, len(fields))
	for i := range fields {
		columns[i] = fields[i].Name
	}
	resultRows, err := pgx.AppendRows(make([]map[string]any, 0, limit), rows, pgx.RowToMap)
	if err != nil {
		return ruleshift.RowsPage{}, fmt.Errorf("read module table rows: %w", err)
	}
	page := ruleshift.RowsPage{
		Module:  module,
		Table:   tableName,
		Columns: columns,
		Rows:    resultRows,
		Limit:   limit,
		Offset:  offset,
	}
	for _, row := range page.Rows {
		for column, value := range row {
			row[column] = normalizeDatabaseValue(value)
		}
	}
	return page, nil
}

func (p *Platform) CreateTableRow(ctx context.Context, developerID, moduleKey, tableName string, values map[string]any) (ruleshift.Row, error) {
	if !validPostgresIdentifier(tableName) {
		return ruleshift.Row{}, ErrTableNotFound
	}
	if _, managed := platformManagedModuleTables[tableName]; managed {
		return ruleshift.Row{}, ErrTableNotFound
	}
	if len(values) == 0 {
		return ruleshift.Row{}, fmt.Errorf("row values must not be empty")
	}
	if len(values) > 128 {
		return ruleshift.Row{}, fmt.Errorf("row must not exceed 128 values")
	}
	for column := range values {
		if !validPostgresIdentifier(column) {
			return ruleshift.Row{}, fmt.Errorf("unsafe column name %q", column)
		}
	}

	module, db, err := p.moduleDatabase(ctx, developerID, moduleKey)
	if err != nil {
		return ruleshift.Row{}, err
	}
	var exists bool
	if err := db.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = 'public' AND table_type = 'BASE TABLE' AND table_name = $1
)`, tableName).Scan(&exists); err != nil {
		return ruleshift.Row{}, fmt.Errorf("inspect module table: %w", err)
	}
	if !exists {
		return ruleshift.Row{}, ErrTableNotFound
	}
	columnRows, err := db.Query(ctx, `
SELECT column_name FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1`, tableName)
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("list module table columns: %w", err)
	}
	columns, err := pgx.CollectRows(columnRows, pgx.RowTo[string])
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("read module table columns: %w", err)
	}
	allowedColumns := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		allowedColumns[column] = struct{}{}
	}
	for column := range values {
		if _, exists := allowedColumns[column]; !exists {
			return ruleshift.Row{}, fmt.Errorf("column %q does not exist in table %q", column, tableName)
		}
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("encode row values: %w", err)
	}
	rows, err := db.Query(ctx,
		`INSERT INTO `+pgx.Identifier{tableName}.Sanitize()+` SELECT * FROM json_populate_record(NULL::`+pgx.Identifier{tableName}.Sanitize()+`, $1::json) RETURNING *`,
		string(payload))
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("insert module table row: %w", err)
	}
	resultValues, err := pgx.CollectExactlyOneRow(rows, pgx.RowToMap)
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("read inserted row: %w", err)
	}
	for column, value := range resultValues {
		resultValues[column] = normalizeDatabaseValue(value)
	}
	return ruleshift.Row{Module: module, Table: tableName, Values: resultValues}, nil
}

func (p *Platform) moduleDatabase(ctx context.Context, developerID, moduleKey string) (string, *pgxpool.Pool, error) {
	var databaseName string
	err := p.control.QueryRow(ctx, `
SELECT database_name FROM modules
WHERE developer_id = $1 AND module_key = $2`, developerID, moduleKey).Scan(&databaseName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrModuleNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("get module database location: %w", err)
	}
	db, err := p.openModuleDatabase(ctx, databaseName)
	if err != nil {
		return "", nil, fmt.Errorf("open module database: %w", err)
	}
	return moduleKey, db, nil
}

func normalizeDatabaseValue(value any) any {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case pgtype.Numeric:
		decoded, _ := typed.Value()
		return decoded
	case []byte:
		if json.Valid(typed) {
			var decoded any
			if json.Unmarshal(typed, &decoded) == nil {
				return decoded
			}
		}
		return string(typed)
	default:
		return value
	}
}
