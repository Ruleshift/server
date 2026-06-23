package developerapi

import (
	"strings"
	"testing"

	"github.com/Ruleshift/server/pkg/ruleshift"
)

func TestCompileDefinitionBuildsSafeDeclarativeSQL(t *testing.T) {
	definition, err := compileDefinition(ruleshift.CreateModuleRequest{
		Key: "inventory",
		Schema: ruleshift.ModuleSchema{Tables: []ruleshift.TableDefinition{{
			Name: "items",
			Columns: []ruleshift.ColumnDefinition{
				{Name: "id", Type: ruleshift.ColumnTypeString, PrimaryKey: true},
				{Name: "quantity", Type: ruleshift.ColumnTypeInt64},
				{Name: "metadata", Type: ruleshift.ColumnTypeJSON, Nullable: true},
			},
		}}},
	})
	if err != nil {
		t.Fatalf("compileDefinition returned error: %v", err)
	}
	if definition.Name != "inventory" || len(definition.Migrations) != 1 {
		t.Fatalf("definition = %#v, want inventory migration", definition)
	}
	sql := definition.Migrations[0].SQL
	for _, expected := range []string{`CREATE TABLE "items"`, `"id" TEXT NOT NULL`, `"quantity" BIGINT NOT NULL`, `"metadata" JSONB`, `PRIMARY KEY ("id")`} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("migration SQL = %q, want %q", sql, expected)
		}
	}
}

func TestCompileDefinitionRejectsReservedTableAndUnsupportedType(t *testing.T) {
	tests := []ruleshift.CreateModuleRequest{
		{Key: "bad", Schema: ruleshift.ModuleSchema{Tables: []ruleshift.TableDefinition{{
			Name: "room_events", Columns: []ruleshift.ColumnDefinition{{Name: "id", Type: ruleshift.ColumnTypeString}},
		}}}},
		{Key: "bad", Schema: ruleshift.ModuleSchema{Tables: []ruleshift.TableDefinition{{
			Name: "custom", Columns: []ruleshift.ColumnDefinition{{Name: "id", Type: "sql"}},
		}}}},
	}
	for _, request := range tests {
		if _, err := compileDefinition(request); err == nil {
			t.Fatalf("compileDefinition(%#v) returned nil error", request)
		}
	}
}
