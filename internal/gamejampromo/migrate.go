package gamejampromo

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ruleshift_schema_migrations (
component TEXT NOT NULL, version NUMERIC(20,0) NOT NULL, name TEXT NOT NULL,
checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
PRIMARY KEY(component, version))`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	body, err := migrationFiles.ReadFile("migrations/001_initial.sql")
	if err != nil {
		return fmt.Errorf("read game jam migration: %w", err)
	}
	return applyMigration(ctx, db, 1, "initial", string(body))
}

func applyMigration(ctx context.Context, db *sql.DB, version int64, name, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin game jam migration: %w", err)
	}
	defer tx.Rollback()
	lockDigest := sha256.Sum256([]byte("ruleshift_gamejam_promotions/1"))
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(lockDigest[:8]))); err != nil {
		return fmt.Errorf("lock game jam migration: %w", err)
	}
	sum := sha256.Sum256([]byte(body))
	checksum := hex.EncodeToString(sum[:])
	var storedName, storedChecksum string
	err = tx.QueryRowContext(ctx, `SELECT name,checksum FROM ruleshift_schema_migrations WHERE component='gamejam_promotions' AND version=$1`, version).Scan(&storedName, &storedChecksum)
	switch {
	case err == nil:
		if storedName != name || storedChecksum != checksum {
			return fmt.Errorf("game jam migration %d changed after application", version)
		}
	case err == sql.ErrNoRows:
		if _, err := tx.ExecContext(ctx, body); err != nil {
			return fmt.Errorf("apply game jam migration: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ruleshift_schema_migrations(component,version,name,checksum) VALUES('gamejam_promotions',$1,$2,$3)`, version, name, checksum); err != nil {
			return fmt.Errorf("record game jam migration: %w", err)
		}
	default:
		return fmt.Errorf("inspect game jam migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit game jam migration: %w", err)
	}
	return nil
}
