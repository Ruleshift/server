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
}

func TestLoadRejectsInvalidInt(t *testing.T) {
	t.Setenv("RULESHIFT_MAX_MESSAGE_BYTES", "not-an-int")

	if _, err := Load(); err == nil {
		t.Fatal("Load returned nil error for invalid integer")
	}
}
