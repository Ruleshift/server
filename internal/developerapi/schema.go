package developerapi

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Ruleshift/server/internal/game"
	"github.com/Ruleshift/server/pkg/ruleshift"
)

const (
	maxTablesPerModule = 32
	maxColumnsPerTable = 128
)

var schemaIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

var reservedTables = map[string]struct{}{
	"rooms":                       {},
	"room_players":                {},
	"room_events":                 {},
	"ruleshift_schema_migrations": {},
}

func compileDefinition(request ruleshift.CreateModuleRequest) (game.DatabaseDefinition, error) {
	if !schemaIdentifierPattern.MatchString(request.Key) {
		return game.DatabaseDefinition{}, fmt.Errorf("module key %q must match %s", request.Key, schemaIdentifierPattern)
	}
	if len(request.Schema.Tables) > maxTablesPerModule {
		return game.DatabaseDefinition{}, fmt.Errorf("module schema must not exceed %d tables", maxTablesPerModule)
	}
	seenTables := make(map[string]struct{}, len(request.Schema.Tables))
	statements := make([]string, 0, len(request.Schema.Tables))
	for _, table := range request.Schema.Tables {
		statement, err := compileTable(table, seenTables)
		if err != nil {
			return game.DatabaseDefinition{}, err
		}
		statements = append(statements, statement)
	}

	definition := game.DatabaseDefinition{Name: request.Key}
	if len(statements) > 0 {
		definition.Migrations = []game.DatabaseMigration{{
			Version: 1,
			Name:    "create_declarative_schema",
			SQL:     strings.Join(statements, "\n\n"),
		}}
	}
	return definition, nil
}

func compileTable(table ruleshift.TableDefinition, seenTables map[string]struct{}) (string, error) {
	if !schemaIdentifierPattern.MatchString(table.Name) {
		return "", fmt.Errorf("table name %q must match %s", table.Name, schemaIdentifierPattern)
	}
	if _, reserved := reservedTables[table.Name]; reserved {
		return "", fmt.Errorf("table name %q is reserved by Ruleshift", table.Name)
	}
	if _, duplicate := seenTables[table.Name]; duplicate {
		return "", fmt.Errorf("duplicate table %q", table.Name)
	}
	seenTables[table.Name] = struct{}{}
	if len(table.Columns) == 0 {
		return "", fmt.Errorf("table %q must contain at least one column", table.Name)
	}
	if len(table.Columns) > maxColumnsPerTable {
		return "", fmt.Errorf("table %q must not exceed %d columns", table.Name, maxColumnsPerTable)
	}

	seenColumns := make(map[string]struct{}, len(table.Columns))
	columns := make([]string, 0, len(table.Columns)+1)
	primaryKeys := make([]string, 0)
	for _, column := range table.Columns {
		if !schemaIdentifierPattern.MatchString(column.Name) {
			return "", fmt.Errorf("column name %q in table %q must match %s", column.Name, table.Name, schemaIdentifierPattern)
		}
		if _, duplicate := seenColumns[column.Name]; duplicate {
			return "", fmt.Errorf("duplicate column %q in table %q", column.Name, table.Name)
		}
		seenColumns[column.Name] = struct{}{}
		sqlType, err := columnSQLType(column.Type)
		if err != nil {
			return "", fmt.Errorf("column %q in table %q: %w", column.Name, table.Name, err)
		}
		line := "    " + quoteSchemaIdentifier(column.Name) + " " + sqlType
		if !column.Nullable || column.PrimaryKey {
			line += " NOT NULL"
		}
		columns = append(columns, line)
		if column.PrimaryKey {
			primaryKeys = append(primaryKeys, quoteSchemaIdentifier(column.Name))
		}
	}
	if len(primaryKeys) > 0 {
		columns = append(columns, "    PRIMARY KEY ("+strings.Join(primaryKeys, ", ")+")")
	}
	return "CREATE TABLE " + quoteSchemaIdentifier(table.Name) + " (\n" + strings.Join(columns, ",\n") + "\n);", nil
}

func columnSQLType(columnType ruleshift.ColumnType) (string, error) {
	switch columnType {
	case ruleshift.ColumnTypeString:
		return "TEXT", nil
	case ruleshift.ColumnTypeInt64:
		return "BIGINT", nil
	case ruleshift.ColumnTypeFloat64:
		return "DOUBLE PRECISION", nil
	case ruleshift.ColumnTypeBool:
		return "BOOLEAN", nil
	case ruleshift.ColumnTypeTimestamp:
		return "TIMESTAMPTZ", nil
	case ruleshift.ColumnTypeJSON:
		return "JSONB", nil
	default:
		return "", fmt.Errorf("unsupported type %q", columnType)
	}
}

func quoteSchemaIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
