package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/game"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type Config struct {
	ControlURL           string
	AdminURL             string
	ModuleDatabasePrefix string
	DeveloperID          string
	DeveloperName        string
}

type Platform struct {
	controlURL string
	admin      *sql.DB
	control    *sql.DB
	cfg        Config

	mu        sync.Mutex
	moduleDBs map[string]*sql.DB
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

	controlConfig, err := pgx.ParseConfig(cfg.ControlURL)
	if err != nil {
		return nil, fmt.Errorf("parse control database URL: %w", err)
	}
	if controlConfig.Database == "" {
		return nil, fmt.Errorf("control database URL must select a database")
	}

	adminURL := cfg.AdminURL
	if adminURL == "" {
		adminConfig := controlConfig.Copy()
		adminConfig.Database = "postgres"
		adminURL = adminConfig.ConnString()
	}
	admin, err := openDatabase(ctx, adminURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL admin connection: %w", err)
	}
	if err := ensureDatabase(ctx, admin, controlConfig.Database); err != nil {
		admin.Close()
		return nil, fmt.Errorf("ensure control database: %w", err)
	}

	control, err := openDatabase(ctx, cfg.ControlURL)
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
		controlURL: cfg.ControlURL,
		admin:      admin,
		control:    control,
		cfg:        cfg,
		moduleDBs:  make(map[string]*sql.DB),
	}
	if err := platform.ensureDeveloper(ctx); err != nil {
		platform.Close()
		return nil, err
	}
	return platform, nil
}

func (p *Platform) ProvisionModule(ctx context.Context, module game.DatabaseModule) (*EventStore, error) {
	definition := module.DatabaseDefinition()
	db, err := p.provisionDefinition(ctx, p.cfg.DeveloperID, module.Type(), definition.Name, definition)
	if err != nil {
		return nil, err
	}
	return NewEventStore(db, module), nil
}

// ProvisionDefinition creates an isolated database for a data-only developer
// module. Game runtime modules use ProvisionModule so their payload codec is also
// attached to the authoritative room event store.
func (p *Platform) ProvisionDefinition(ctx context.Context, developerID, displayName string, definition game.DatabaseDefinition) error {
	_, err := p.provisionDefinition(ctx, developerID, game.TypeUnspecified, displayName, definition)
	return err
}

func (p *Platform) provisionDefinition(ctx context.Context, developerID string, gameType game.Type, displayName string, definition game.DatabaseDefinition) (*sql.DB, error) {
	if !definitionNamePattern.MatchString(developerID) {
		return nil, fmt.Errorf("developer id %q must match %s", developerID, definitionNamePattern)
	}
	if err := validateDefinition(definition); err != nil {
		return nil, err
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = definition.Name
	}
	databaseName, err := moduleDatabaseName(p.cfg.ModuleDatabasePrefix, developerID, definition.Name)
	if err != nil {
		return nil, err
	}

	if err := ensureDatabase(ctx, p.admin, databaseName); err != nil {
		return nil, fmt.Errorf("ensure module database %q: %w", databaseName, err)
	}
	moduleURL, err := databaseURL(p.controlURL, databaseName)
	if err != nil {
		return nil, err
	}
	db, err := p.openModuleDatabase(ctx, databaseName, moduleURL)
	if err != nil {
		return nil, fmt.Errorf("open module database %q: %w", databaseName, err)
	}

	baseMigrations, err := embeddedMigrations("migrations/module")
	if err == nil {
		err = applyMigrations(ctx, db, "ruleshift_module", baseMigrations)
	}
	if err == nil {
		err = applyMigrations(ctx, db, definition.Name, definition.Migrations)
	}
	if err != nil {
		return nil, fmt.Errorf("migrate module database %q: %w", databaseName, err)
	}
	if err := p.registerModule(ctx, developerID, gameType, displayName, definition, databaseName); err != nil {
		return nil, err
	}
	return db, nil
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

	tx, err := p.control.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin identity transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO users(id, display_name) VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    last_authenticated_at = NOW()`, identity.PlayerID, identity.DisplayName); err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit identity transaction: %w", err)
	}
	return nil
}

func (p *Platform) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var firstErr error
	for _, db := range p.moduleDBs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	p.moduleDBs = nil
	if p.control != nil {
		if err := p.control.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if p.admin != nil {
		if err := p.admin.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *Platform) ensureDeveloper(ctx context.Context) error {
	_, err := p.control.ExecContext(ctx, `
INSERT INTO developers(id, display_name) VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET display_name = EXCLUDED.display_name, updated_at = NOW()`,
		p.cfg.DeveloperID, p.cfg.DeveloperName)
	if err != nil {
		return fmt.Errorf("upsert default developer: %w", err)
	}
	return nil
}

func (p *Platform) registerModule(ctx context.Context, developerID string, gameType game.Type, displayName string, definition game.DatabaseDefinition, databaseName string) error {
	moduleID := developerID + ":" + definition.Name
	_, err := p.control.ExecContext(ctx, `
INSERT INTO modules(id, developer_id, module_key, display_name, game_type, database_name)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (developer_id, module_key) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    game_type = EXCLUDED.game_type,
    database_name = EXCLUDED.database_name,
    updated_at = NOW()`,
		moduleID, developerID, definition.Name, displayName, int16(gameType), databaseName)
	if err != nil {
		return fmt.Errorf("register module %q: %w", definition.Name, err)
	}
	return nil
}

func (p *Platform) openModuleDatabase(ctx context.Context, databaseName, moduleURL string) (*sql.DB, error) {
	p.mu.Lock()
	if db := p.moduleDBs[databaseName]; db != nil {
		p.mu.Unlock()
		return db, nil
	}
	p.mu.Unlock()

	db, err := openDatabase(ctx, moduleURL)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if existing := p.moduleDBs[databaseName]; existing != nil {
		p.mu.Unlock()
		db.Close()
		return existing, nil
	}
	p.moduleDBs[databaseName] = db
	p.mu.Unlock()
	return db, nil
}

func ensureDatabase(ctx context.Context, admin *sql.DB, name string) error {
	if !validPostgresIdentifier(name) {
		return fmt.Errorf("unsafe PostgreSQL database name %q", name)
	}
	var exists bool
	if err := admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return fmt.Errorf("inspect PostgreSQL database %q: %w", name, err)
	}
	if exists {
		return nil
	}
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+quoteIdentifier(name)); err != nil {
		// A concurrent provisioner may have won the race; verify before failing.
		if checkErr := admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); checkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("create PostgreSQL database %q: %w", name, err)
	}
	return nil
}

func openDatabase(ctx context.Context, databaseURL string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*config)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func databaseURL(baseURL, databaseName string) (string, error) {
	config, err := pgx.ParseConfig(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse database URL: %w", err)
	}
	config.Database = databaseName
	return config.ConnString(), nil
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

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
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
