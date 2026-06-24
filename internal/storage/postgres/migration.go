package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type databaseMigration struct {
	Version uint64
	Name    string
	SQL     string
}

//go:embed migrations/control/*.sql migrations/module/*.sql
var migrationFiles embed.FS

var definitionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

const createMigrationTableSQL = `CREATE TABLE IF NOT EXISTS ruleshift_schema_migrations (
    component TEXT NOT NULL,
    version NUMERIC(20, 0) NOT NULL,
    name TEXT NOT NULL,
    checksum TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (component, version)
)`

func embeddedMigrations(directory string) ([]databaseMigration, error) {
	entries, err := fs.ReadDir(migrationFiles, directory)
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations %q: %w", directory, err)
	}

	migrations := make([]databaseMigration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(entry.Name(), ".sql"), "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}
		body, err := migrationFiles.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, databaseMigration{
			Version: version,
			Name:    parts[1],
			SQL:     string(body),
		})
	}
	return migrations, nil
}

func validateMigrations(migrations []databaseMigration) error {
	versions := make(map[uint64]struct{}, len(migrations))
	for _, migration := range migrations {
		if migration.Version == 0 {
			return fmt.Errorf("database migration version must be positive")
		}
		if strings.TrimSpace(migration.Name) == "" {
			return fmt.Errorf("database migration %d name must not be empty", migration.Version)
		}
		if strings.TrimSpace(migration.SQL) == "" {
			return fmt.Errorf("database migration %d SQL must not be empty", migration.Version)
		}
		if _, exists := versions[migration.Version]; exists {
			return fmt.Errorf("duplicate database migration version %d", migration.Version)
		}
		versions[migration.Version] = struct{}{}
	}
	return nil
}

func applyMigrations(ctx context.Context, db *sql.DB, component string, migrations []databaseMigration) error {
	if !definitionNamePattern.MatchString(component) {
		return fmt.Errorf("migration component %q must match %s", component, definitionNamePattern)
	}
	if err := validateMigrations(migrations); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, createMigrationTableSQL); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	ordered := append([]databaseMigration(nil), migrations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Version < ordered[j].Version })
	for _, migration := range ordered {
		if err := applyMigration(ctx, db, component, migration); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, component string, migration databaseMigration) error {
	sum := sha256.Sum256([]byte(migration.SQL))
	checksum := hex.EncodeToString(sum[:])

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s/%d: %w", component, migration.Version, err)
	}
	defer tx.Rollback()

	lockSum := sha256.Sum256([]byte(component + "/" + strconv.FormatUint(migration.Version, 10)))
	lockKey := int64(binary.BigEndian.Uint64(lockSum[:8]))
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("lock migration %s/%d: %w", component, migration.Version, err)
	}

	var storedName, storedChecksum string
	err = tx.QueryRowContext(ctx,
		`SELECT name, checksum FROM ruleshift_schema_migrations WHERE component = $1 AND version = $2`,
		component, strconv.FormatUint(migration.Version, 10),
	).Scan(&storedName, &storedChecksum)
	switch {
	case err == nil:
		if storedName != migration.Name || storedChecksum != checksum {
			return fmt.Errorf("migration %s/%d changed after it was applied", component, migration.Version)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration inspection %s/%d: %w", component, migration.Version, err)
		}
		return nil
	case err != sql.ErrNoRows:
		return fmt.Errorf("inspect migration %s/%d: %w", component, migration.Version, err)
	}

	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("apply migration %s/%d (%s): %w", component, migration.Version, migration.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ruleshift_schema_migrations(component, version, name, checksum) VALUES ($1, $2, $3, $4)`,
		component, strconv.FormatUint(migration.Version, 10), migration.Name, checksum,
	); err != nil {
		return fmt.Errorf("record migration %s/%d: %w", component, migration.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s/%d: %w", component, migration.Version, err)
	}
	return nil
}
