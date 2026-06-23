package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("RULESHIFT_ADDR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.RoomInputQueueSize <= 0 {
		t.Fatalf("RoomInputQueueSize = %d, want positive", cfg.RoomInputQueueSize)
	}
	if cfg.ShutdownTimeout <= 0 {
		t.Fatalf("ShutdownTimeout = %s, want positive", cfg.ShutdownTimeout)
	}
}

func TestLoadRejectsInvalidInt(t *testing.T) {
	t.Setenv("RULESHIFT_MAX_MESSAGE_BYTES", "not-an-int")

	if _, err := Load(); err == nil {
		t.Fatal("Load returned nil error for invalid integer")
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("RULESHIFT_SHUTDOWN_TIMEOUT", "not-a-duration")

	if _, err := Load(); err == nil {
		t.Fatal("Load returned nil error for invalid duration")
	}
}

func TestLoadRejectsDeveloperAPIWithoutDatabase(t *testing.T) {
	t.Setenv("RULESHIFT_DATABASE_URL", "")
	t.Setenv("RULESHIFT_DEVELOPER_API_KEY", "secret")

	if _, err := Load(); err == nil {
		t.Fatal("Load returned nil error for developer API without database")
	}
}
