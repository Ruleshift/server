package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("RULESHIFT_ADDR", "")
	t.Setenv("RULESHIFT_AUTH_PROVIDER", "")
	t.Setenv("RULESHIFT_STEAM_APP_ID", "")

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
	if cfg.AuthProvider != "mock" {
		t.Fatalf("AuthProvider = %q, want mock", cfg.AuthProvider)
	}
}

func TestLoadRejectsInvalidInt(t *testing.T) {
	t.Setenv("RULESHIFT_MAX_MESSAGE_BYTES", "not-an-int")

	if _, err := Load(); err == nil {
		t.Fatal("Load returned nil error for invalid integer")
	}
}

func TestLoadAcceptsSteamAuthProvider(t *testing.T) {
	t.Setenv("RULESHIFT_AUTH_PROVIDER", " steam ")
	t.Setenv("RULESHIFT_STEAM_APP_ID", "480")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.AuthProvider != "steam" {
		t.Fatalf("AuthProvider = %q, want steam", cfg.AuthProvider)
	}
	if cfg.SteamAppID != "480" {
		t.Fatalf("SteamAppID = %q, want 480", cfg.SteamAppID)
	}
}

func TestLoadRejectsSteamAuthWithoutAppID(t *testing.T) {
	t.Setenv("RULESHIFT_AUTH_PROVIDER", "steam")
	t.Setenv("RULESHIFT_STEAM_APP_ID", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load returned nil error for steam auth without app id")
	}
}
