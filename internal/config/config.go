package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr                 string
	Env                  string
	DatabaseURL          string
	DatabaseAdminURL     string
	ModuleDatabasePrefix string
	DeveloperID          string
	DeveloperName        string
	DeveloperAPIKey      string
	MaxMessageBytes      int
	RoomInputQueueSize   int
	SessionSendQueueSize int
	AuthTimeout          time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	ShutdownTimeout      time.Duration
	EnableMetrics        bool
	EnablePprof          bool
}

func Load() (Config, error) {
	cfg := Config{
		Addr:                 envString("RULESHIFT_ADDR", ":8080"),
		Env:                  envString("RULESHIFT_ENV", "dev"),
		DatabaseURL:          envString("RULESHIFT_DATABASE_URL", ""),
		DatabaseAdminURL:     envString("RULESHIFT_DATABASE_ADMIN_URL", ""),
		ModuleDatabasePrefix: envString("RULESHIFT_MODULE_DATABASE_PREFIX", "ruleshift_module_"),
		DeveloperID:          envString("RULESHIFT_DEVELOPER_ID", "default"),
		DeveloperName:        envString("RULESHIFT_DEVELOPER_NAME", "Default developer"),
		DeveloperAPIKey:      envString("RULESHIFT_DEVELOPER_API_KEY", ""),
		MaxMessageBytes:      64 * 1024,
		RoomInputQueueSize:   1024,
		SessionSendQueueSize: 256,
		AuthTimeout:          5 * time.Second,
		ReadTimeout:          30 * time.Second,
		WriteTimeout:         30 * time.Second,
		ShutdownTimeout:      10 * time.Second,
		EnableMetrics:        true,
		EnablePprof:          false,
	}

	var err error
	if cfg.MaxMessageBytes, err = envInt("RULESHIFT_MAX_MESSAGE_BYTES", cfg.MaxMessageBytes); err != nil {
		return Config{}, err
	}
	if cfg.RoomInputQueueSize, err = envInt("RULESHIFT_ROOM_INPUT_QUEUE_SIZE", cfg.RoomInputQueueSize); err != nil {
		return Config{}, err
	}
	if cfg.SessionSendQueueSize, err = envInt("RULESHIFT_SESSION_SEND_QUEUE_SIZE", cfg.SessionSendQueueSize); err != nil {
		return Config{}, err
	}
	if cfg.AuthTimeout, err = envDuration("RULESHIFT_AUTH_TIMEOUT", cfg.AuthTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ReadTimeout, err = envDuration("RULESHIFT_READ_TIMEOUT", cfg.ReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.WriteTimeout, err = envDuration("RULESHIFT_WRITE_TIMEOUT", cfg.WriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = envDuration("RULESHIFT_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.EnableMetrics, err = envBool("RULESHIFT_ENABLE_METRICS", cfg.EnableMetrics); err != nil {
		return Config{}, err
	}
	if cfg.EnablePprof, err = envBool("RULESHIFT_ENABLE_PPROF", cfg.EnablePprof); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("RULESHIFT_ADDR must not be empty")
	}
	if c.MaxMessageBytes <= 0 {
		return fmt.Errorf("RULESHIFT_MAX_MESSAGE_BYTES must be positive")
	}
	if c.ModuleDatabasePrefix == "" {
		return fmt.Errorf("RULESHIFT_MODULE_DATABASE_PREFIX must not be empty")
	}
	if c.DeveloperID == "" {
		return fmt.Errorf("RULESHIFT_DEVELOPER_ID must not be empty")
	}
	if c.DeveloperName == "" {
		return fmt.Errorf("RULESHIFT_DEVELOPER_NAME must not be empty")
	}
	if c.DeveloperAPIKey != "" && c.DatabaseURL == "" {
		return fmt.Errorf("RULESHIFT_DEVELOPER_API_KEY requires RULESHIFT_DATABASE_URL")
	}
	if c.RoomInputQueueSize <= 0 {
		return fmt.Errorf("RULESHIFT_ROOM_INPUT_QUEUE_SIZE must be positive")
	}
	if c.SessionSendQueueSize <= 0 {
		return fmt.Errorf("RULESHIFT_SESSION_SEND_QUEUE_SIZE must be positive")
	}
	if c.AuthTimeout <= 0 {
		return fmt.Errorf("RULESHIFT_AUTH_TIMEOUT must be positive")
	}
	if c.ReadTimeout <= 0 {
		return fmt.Errorf("RULESHIFT_READ_TIMEOUT must be positive")
	}
	if c.WriteTimeout <= 0 {
		return fmt.Errorf("RULESHIFT_WRITE_TIMEOUT must be positive")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("RULESHIFT_SHUTDOWN_TIMEOUT must be positive")
	}
	return nil
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
