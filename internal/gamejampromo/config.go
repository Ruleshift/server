package gamejampromo

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	PublicAddr         string
	AdminAddr          string
	DatabaseURL        string
	CodeMasterKey      string
	AdminUsername      string
	AdminPasswordHash  string
	AllowedOrigin      string
	UserAgent          string
	SyncInterval       time.Duration
	ShutdownTimeout    time.Duration
	AfishaURL          string
	JammerURL          string
	ItchURL            string
	MaxConcurrentCheck int
}

func LoadConfig() (Config, error) {
	cfg := Config{
		PublicAddr:         env("GAMEJAM_PUBLIC_ADDR", ":8082"),
		AdminAddr:          env("GAMEJAM_ADMIN_ADDR", ":9092"),
		DatabaseURL:        os.Getenv("GAMEJAM_DATABASE_URL"),
		CodeMasterKey:      os.Getenv("GAMEJAM_CODE_MASTER_KEY"),
		AdminUsername:      os.Getenv("GAMEJAM_ADMIN_USERNAME"),
		AdminPasswordHash:  os.Getenv("GAMEJAM_ADMIN_PASSWORD_BCRYPT"),
		AllowedOrigin:      env("GAMEJAM_ALLOWED_ORIGIN", "https://ruleshift.ru"),
		UserAgent:          env("GAMEJAM_SOURCE_USER_AGENT", "RuleshiftGameJamBot/1.0 (+https://ruleshift.ru/)"),
		SyncInterval:       6 * time.Hour,
		ShutdownTimeout:    10 * time.Second,
		AfishaURL:          env("GAMEJAM_AFISHA_URL", "https://gamedev-afisha.ru/"),
		JammerURL:          env("GAMEJAM_JAMMER_URL", "https://jammer.website/ru/jams"),
		ItchURL:            env("GAMEJAM_ITCH_URL", "https://itch.io/jams/upcoming"),
		MaxConcurrentCheck: 32,
	}
	var err error
	if value := os.Getenv("GAMEJAM_SYNC_INTERVAL"); value != "" {
		cfg.SyncInterval, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse GAMEJAM_SYNC_INTERVAL: %w", err)
		}
	}
	if value := os.Getenv("GAMEJAM_SHUTDOWN_TIMEOUT"); value != "" {
		cfg.ShutdownTimeout, err = time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse GAMEJAM_SHUTDOWN_TIMEOUT: %w", err)
		}
	}
	if value := os.Getenv("GAMEJAM_MAX_CONCURRENT_CHECKS"); value != "" {
		cfg.MaxConcurrentCheck, err = strconv.Atoi(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse GAMEJAM_MAX_CONCURRENT_CHECKS: %w", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.PublicAddr == "" || c.AdminAddr == "" || c.PublicAddr == c.AdminAddr {
		return fmt.Errorf("GAMEJAM_PUBLIC_ADDR and GAMEJAM_ADMIN_ADDR must be different and nonempty")
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("GAMEJAM_DATABASE_URL is required")
	}
	key, err := base64.StdEncoding.DecodeString(c.CodeMasterKey)
	if err != nil || len(key) != 32 {
		return fmt.Errorf("GAMEJAM_CODE_MASTER_KEY must be base64 for exactly 32 bytes")
	}
	if strings.TrimSpace(c.AdminUsername) == "" || len(c.AdminUsername) > 128 {
		return fmt.Errorf("GAMEJAM_ADMIN_USERNAME is required and must not exceed 128 bytes")
	}
	if _, err := bcrypt.Cost([]byte(c.AdminPasswordHash)); err != nil {
		return fmt.Errorf("GAMEJAM_ADMIN_PASSWORD_BCRYPT must contain a bcrypt hash")
	}
	if c.AllowedOrigin == "" || c.UserAgent == "" || c.SyncInterval <= 0 || c.ShutdownTimeout <= 0 || c.MaxConcurrentCheck < 1 || c.MaxConcurrentCheck > 1024 {
		return fmt.Errorf("invalid game jam service limits")
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
