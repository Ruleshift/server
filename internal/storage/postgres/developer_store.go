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
	"room_players":                {},
	"room_events":                 {},
	"ruleshift_schema_migrations": {},
}

func (p *Platform) ListModules(ctx context.Context, developerID string) ([]ruleshift.Module, error) {
	rows, err := p.control.QueryContext(ctx, `
SELECT module_key, display_name, game_type, created_at
FROM modules
WHERE developer_id = $1
ORDER BY module_key`, developerID)
	if err != nil {
		return nil, fmt.Errorf("list developer modules: %w", err)
	}
	defer rows.Close()

	modules := make([]ruleshift.Module, 0)
	for rows.Next() {
		var module ruleshift.Module
		var gameType int16
		if err := rows.Scan(&module.Key, &module.DisplayName, &gameType, &module.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan developer module: %w", err)
		}
		if gameType < 0 || gameType > 255 {
			return nil, fmt.Errorf("module %q has unsupported game type %d", module.Key, gameType)
		}
		module.GameType = uint8(gameType)
		modules = append(modules, module)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate developer modules: %w", err)
	}
	return modules, nil
}

func (p *Platform) GetModule(ctx context.Context, developerID, moduleKey string) (ruleshift.Module, error) {
	var module ruleshift.Module
	var gameType int16
	err := p.control.QueryRowContext(ctx, `
SELECT module_key, display_name, game_type, created_at
FROM modules
WHERE developer_id = $1 AND module_key = $2`, developerID, moduleKey).
		Scan(&module.Key, &module.DisplayName, &gameType, &module.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ruleshift.Module{}, ErrModuleNotFound
	}
	if err != nil {
		return ruleshift.Module{}, fmt.Errorf("get developer module: %w", err)
	}
	if gameType < 0 || gameType > 255 {
		return ruleshift.Module{}, fmt.Errorf("module %q has unsupported game type %d", module.Key, gameType)
	}
	module.GameType = uint8(gameType)
	return module, nil
}

func (p *Platform) DescribeModule(ctx context.Context, developerID, moduleKey string) (ruleshift.Schema, error) {
	module, db, err := p.moduleDatabase(ctx, developerID, moduleKey)
	if err != nil {
		return ruleshift.Schema{}, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT c.table_name, c.column_name, c.data_type, c.is_nullable,
       EXISTS (
           SELECT 1
           FROM information_schema.table_constraints tc
           JOIN information_schema.key_column_usage kcu
             ON tc.constraint_name = kcu.constraint_name
            AND tc.table_schema = kcu.table_schema
          WHERE tc.constraint_type = 'PRIMARY KEY'
            AND tc.table_schema = c.table_schema
            AND tc.table_name = c.table_name
            AND kcu.column_name = c.column_name
       ) AS primary_key
FROM information_schema.columns c
WHERE c.table_schema = 'public'
ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		return ruleshift.Schema{}, fmt.Errorf("describe module schema: %w", err)
	}
	defer rows.Close()

	schema := ruleshift.Schema{Module: module.Key, Tables: make([]ruleshift.TableSchema, 0)}
	var current *ruleshift.TableSchema
	for rows.Next() {
		var tableName string
		var column ruleshift.ColumnSchema
		var nullable string
		if err := rows.Scan(&tableName, &column.Name, &column.SQLType, &nullable, &column.PrimaryKey); err != nil {
			return ruleshift.Schema{}, fmt.Errorf("scan module schema: %w", err)
		}
		column.Nullable = nullable == "YES"
		if current == nil || current.Name != tableName {
			schema.Tables = append(schema.Tables, ruleshift.TableSchema{Name: tableName, Columns: make([]ruleshift.ColumnSchema, 0)})
			current = &schema.Tables[len(schema.Tables)-1]
		}
		current.Columns = append(current.Columns, column)
	}
	if err := rows.Err(); err != nil {
		return ruleshift.Schema{}, fmt.Errorf("iterate module schema: %w", err)
	}
	return schema, nil
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
		Module:  module.Key,
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
	result := ruleshift.Row{Module: module.Key, Table: tableName, Values: make(map[string]any, len(columns))}
	for i, column := range columns {
		result.Values[column] = normalizeDatabaseValue(resultValues[i])
	}
	return result, nil
}

func (p *Platform) moduleDatabase(ctx context.Context, developerID, moduleKey string) (ruleshift.Module, *sql.DB, error) {
	module, err := p.GetModule(ctx, developerID, moduleKey)
	if err != nil {
		return ruleshift.Module{}, nil, err
	}
	var databaseName string
	if err := p.control.QueryRowContext(ctx, `
SELECT database_name FROM modules
WHERE developer_id = $1 AND module_key = $2`, developerID, moduleKey).Scan(&databaseName); err != nil {
		return ruleshift.Module{}, nil, fmt.Errorf("get module database location: %w", err)
	}
	moduleURL, err := databaseURL(p.controlURL, databaseName)
	if err != nil {
		return ruleshift.Module{}, nil, err
	}
	db, err := p.openModuleDatabase(ctx, databaseName, moduleURL)
	if err != nil {
		return ruleshift.Module{}, nil, fmt.Errorf("open module database: %w", err)
	}
	return module, db, nil
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
