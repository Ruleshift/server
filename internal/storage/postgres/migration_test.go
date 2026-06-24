package postgres

import (
	"strings"
	"testing"
)

func TestEmbeddedSchemasAreValid(t *testing.T) {
	for _, directory := range []string{"migrations/control", "migrations/module"} {
		migrations, err := embeddedMigrations(directory)
		if err != nil {
			t.Fatalf("embeddedMigrations(%q) returned error: %v", directory, err)
		}
		if len(migrations) == 0 {
			t.Fatalf("embeddedMigrations(%q) returned no migrations", directory)
		}
		if err := validateMigrations(migrations); err != nil {
			t.Fatalf("validateMigrations(%q) returned error: %v", directory, err)
		}
	}
}

func TestValidateMigrationsRejectsDuplicateVersions(t *testing.T) {
	migrations := []databaseMigration{{Version: 1, Name: "first", SQL: "SELECT 1"}, {Version: 1, Name: "duplicate", SQL: "SELECT 2"}}
	if err := validateMigrations(migrations); err == nil {
		t.Fatal("duplicate migration version accepted")
	}
}

func TestGeneratedTenantModuleDatabaseNameIsSafe(t *testing.T) {
	name, err := moduleDatabaseName("ruleshift_module_", "default", "xiangqi")
	if err != nil {
		t.Fatalf("moduleDatabaseName returned error: %v", err)
	}
	if !validPostgresIdentifier(name) {
		t.Fatalf("database name %q should be safe", name)
	}
	longName, err := moduleDatabaseName("ruleshift_module_", strings.Repeat("d", 47), strings.Repeat("m", 47))
	if err != nil {
		t.Fatalf("long moduleDatabaseName returned error: %v", err)
	}
	if len(longName) != 63 || !validPostgresIdentifier(longName) {
		t.Fatalf("long database name = %q (%d), want safe 63-character name", longName, len(longName))
	}
	if validPostgresIdentifier(strings.Repeat("x", 64)) {
		t.Fatal("64-character PostgreSQL identifier should be rejected")
	}
}
