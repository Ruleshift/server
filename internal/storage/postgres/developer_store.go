package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Ruleshift/server/pkg/ruleshift"
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
	if err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = 'public' AND table_type = 'BASE TABLE' AND table_name = $1
)`, tableName).Scan(&exists); err != nil {
		return ruleshift.RowsPage{}, fmt.Errorf("inspect module table: %w", err)
	}
	if !exists {
		return ruleshift.RowsPage{}, ErrTableNotFound
	}

	rows, err := db.QueryContext(ctx, `SELECT * FROM `+quoteIdentifier(tableName)+` LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return ruleshift.RowsPage{}, fmt.Errorf("list module table rows: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return ruleshift.RowsPage{}, fmt.Errorf("read module table columns: %w", err)
	}
	page := ruleshift.RowsPage{
		Module:  module,
		Table:   tableName,
		Columns: columns,
		Rows:    make([]map[string]any, 0, limit),
		Limit:   limit,
		Offset:  offset,
	}
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return ruleshift.RowsPage{}, fmt.Errorf("scan module table row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = normalizeDatabaseValue(values[i])
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return ruleshift.RowsPage{}, fmt.Errorf("iterate module table rows: %w", err)
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
	if err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = 'public' AND table_type = 'BASE TABLE' AND table_name = $1
)`, tableName).Scan(&exists); err != nil {
		return ruleshift.Row{}, fmt.Errorf("inspect module table: %w", err)
	}
	if !exists {
		return ruleshift.Row{}, ErrTableNotFound
	}
	columnRows, err := db.QueryContext(ctx, `
SELECT column_name FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1`, tableName)
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("list module table columns: %w", err)
	}
	allowedColumns := make(map[string]struct{})
	for columnRows.Next() {
		var column string
		if err := columnRows.Scan(&column); err != nil {
			columnRows.Close()
			return ruleshift.Row{}, fmt.Errorf("scan module table column: %w", err)
		}
		allowedColumns[column] = struct{}{}
	}
	if err := columnRows.Close(); err != nil {
		return ruleshift.Row{}, fmt.Errorf("close module table columns: %w", err)
	}
	if err := columnRows.Err(); err != nil {
		return ruleshift.Row{}, fmt.Errorf("iterate module table columns: %w", err)
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
	rows, err := db.QueryContext(ctx,
		`INSERT INTO `+quoteIdentifier(tableName)+` SELECT * FROM json_populate_record(NULL::`+quoteIdentifier(tableName)+`, $1::json) RETURNING *`,
		string(payload))
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("insert module table row: %w", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return ruleshift.Row{}, fmt.Errorf("read inserted row columns: %w", err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return ruleshift.Row{}, fmt.Errorf("read inserted row: %w", err)
		}
		return ruleshift.Row{}, fmt.Errorf("inserted row was not returned")
	}
	resultValues := make([]any, len(columns))
	destinations := make([]any, len(columns))
	for i := range resultValues {
		destinations[i] = &resultValues[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		return ruleshift.Row{}, fmt.Errorf("scan inserted row: %w", err)
	}
	result := ruleshift.Row{Module: module, Table: tableName, Values: make(map[string]any, len(columns))}
	for i, column := range columns {
		result.Values[column] = normalizeDatabaseValue(resultValues[i])
	}
	return result, nil
}

func (p *Platform) moduleDatabase(ctx context.Context, developerID, moduleKey string) (string, *sql.DB, error) {
	var databaseName string
	err := p.control.QueryRowContext(ctx, `
SELECT database_name FROM modules
WHERE developer_id = $1 AND module_key = $2`, developerID, moduleKey).Scan(&databaseName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrModuleNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("get module database location: %w", err)
	}
	moduleURL, err := databaseURL(p.controlURL, databaseName)
	if err != nil {
		return "", nil, err
	}
	db, err := p.openModuleDatabase(ctx, databaseName, moduleURL)
	if err != nil {
		return "", nil, fmt.Errorf("open module database: %w", err)
	}
	return moduleKey, db, nil
}

func normalizeDatabaseValue(value any) any {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
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
