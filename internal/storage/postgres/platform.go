package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	ControlURL           string
	AdminURL             string
	ModuleDatabasePrefix string
	DeveloperID          string
	DeveloperName        string
}

type Platform struct {
	controlConfig *pgxpool.Config
	admin         *pgxpool.Pool
	control       *pgxpool.Pool
	cfg           Config

	mu        sync.Mutex
	moduleDBs map[string]*pgxpool.Pool
	closed    bool
}

func Open(ctx context.Context, cfg Config) (*Platform, error) {
	if strings.TrimSpace(cfg.ControlURL) == "" {
		return nil, fmt.Errorf("control database URL must not be empty")
	}
	if cfg.ModuleDatabasePrefix == "" {
		cfg.ModuleDatabasePrefix = "ruleshift_module_"
	}
	if cfg.DeveloperID == "" {
		cfg.DeveloperID = "default"
	}
	if cfg.DeveloperName == "" {
		cfg.DeveloperName = "Default developer"
	}
	if !definitionNamePattern.MatchString(cfg.DeveloperID) {
		return nil, fmt.Errorf("developer id %q must match %s", cfg.DeveloperID, definitionNamePattern)
	}

	controlConfig, err := pgxpool.ParseConfig(cfg.ControlURL)
	if err != nil {
		return nil, fmt.Errorf("parse control database URL: %w", err)
	}
	if controlConfig.ConnConfig.Database == "" {
		return nil, fmt.Errorf("control database URL must select a database")
	}

	var adminConfig *pgxpool.Config
	if cfg.AdminURL == "" {
		adminConfig = controlConfig.Copy()
		adminConfig.ConnConfig.Database = "postgres"
	} else {
		adminConfig, err = pgxpool.ParseConfig(cfg.AdminURL)
		if err != nil {
			return nil, fmt.Errorf("parse PostgreSQL admin URL: %w", err)
		}
	}
	admin, err := openDatabase(ctx, adminConfig)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL admin connection: %w", err)
	}
	if err := ensureDatabase(ctx, admin, controlConfig.ConnConfig.Database); err != nil {
		admin.Close()
		return nil, fmt.Errorf("ensure control database: %w", err)
	}

	control, err := openDatabase(ctx, controlConfig)
	if err != nil {
		admin.Close()
		return nil, fmt.Errorf("open control database: %w", err)
	}
	controlMigrations, err := embeddedMigrations("migrations/control")
	if err != nil {
		admin.Close()
		control.Close()
		return nil, err
	}
	if err := applyMigrations(ctx, control, "ruleshift_control", controlMigrations); err != nil {
		admin.Close()
		control.Close()
		return nil, fmt.Errorf("migrate control database: %w", err)
	}

	platform := &Platform{
		controlConfig: controlConfig,
		admin:         admin,
		control:       control,
		cfg:           cfg,
		moduleDBs:     make(map[string]*pgxpool.Pool),
	}
	if err := platform.ensureDeveloper(ctx); err != nil {
		platform.Close()
		return nil, err
	}
	return platform, nil
}

func (p *Platform) SaveIdentity(ctx context.Context, identity auth.Identity) error {
	if identity.PlayerID == "" {
		return fmt.Errorf("identity player id must not be empty")
	}
	provider := identity.Provider
	if provider == "" {
		provider = "unknown"
	}
	providerUserID := identity.PlayerID
	if identity.SteamID != "" {
		providerUserID = identity.SteamID
	}

	return pgx.BeginFunc(ctx, p.control, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO users(id, display_name) VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    last_authenticated_at = NOW()`, identity.PlayerID, identity.DisplayName); err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO user_identities(provider, provider_user_id, user_id, app_id, steam_id, ownership_verified)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (provider, provider_user_id) DO UPDATE SET
    user_id = EXCLUDED.user_id,
    app_id = EXCLUDED.app_id,
    steam_id = EXCLUDED.steam_id,
    ownership_verified = EXCLUDED.ownership_verified,
    last_authenticated_at = NOW()`, provider, providerUserID, identity.PlayerID, identity.AppID, identity.SteamID, identity.OwnershipVerified); err != nil {
			return fmt.Errorf("upsert user identity: %w", err)
		}
		return nil
	})
}

func (p *Platform) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for _, db := range p.moduleDBs {
		db.Close()
	}
	p.moduleDBs = nil
	if p.control != nil {
		p.control.Close()
	}
	if p.admin != nil {
		p.admin.Close()
	}
	return nil
}

func (p *Platform) ensureDeveloper(ctx context.Context) error {
	_, err := p.control.Exec(ctx, `
INSERT INTO developers(id, display_name) VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET display_name = EXCLUDED.display_name, updated_at = NOW()`,
		p.cfg.DeveloperID, p.cfg.DeveloperName)
	if err != nil {
		return fmt.Errorf("upsert default developer: %w", err)
	}
	return nil
}

func (p *Platform) openModuleDatabase(ctx context.Context, databaseName string) (*pgxpool.Pool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("PostgreSQL platform is closed")
	}
	if db := p.moduleDBs[databaseName]; db != nil {
		return db, nil
	}

	config := p.controlConfig.Copy()
	config.ConnConfig.Database = databaseName
	db, err := openDatabase(ctx, config)
	if err != nil {
		return nil, err
	}
	p.moduleDBs[databaseName] = db
	return db, nil
}

func ensureDatabase(ctx context.Context, admin *pgxpool.Pool, name string) error {
	if !validPostgresIdentifier(name) {
		return fmt.Errorf("unsafe PostgreSQL database name %q", name)
	}
	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return fmt.Errorf("inspect PostgreSQL database %q: %w", name, err)
	}
	if exists {
		return nil
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		// A concurrent provisioner may have won the race; verify before failing.
		if checkErr := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); checkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("create PostgreSQL database %q: %w", name, err)
	}
	return nil
}

func openDatabase(ctx context.Context, config *pgxpool.Config) (*pgxpool.Pool, error) {
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func validPostgresIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func moduleDatabaseName(prefix, developerID, moduleName string) (string, error) {
	raw := prefix + developerID + "_" + moduleName
	if len(raw) <= 63 {
		if !validPostgresIdentifier(raw) {
			return "", fmt.Errorf("generated module database name %q is not a safe PostgreSQL identifier", raw)
		}
		return raw, nil
	}
	if !validPostgresIdentifier(prefix + "x") {
		return "", fmt.Errorf("module database prefix %q is not a safe PostgreSQL identifier prefix", prefix)
	}
	sum := sha256.Sum256([]byte(raw))
	suffix := "_" + hex.EncodeToString(sum[:6])
	shortened := raw[:63-len(suffix)] + suffix
	if !validPostgresIdentifier(shortened) {
		return "", fmt.Errorf("generated module database name %q is not a safe PostgreSQL identifier", shortened)
	}
	return shortened, nil
}
