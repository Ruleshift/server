package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/config"
)

func TestHealthHandler(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	healthHandler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); body != `{"status":"ok"}` {
		t.Fatalf("body = %q, want health JSON", body)
	}
}

func TestBuildAuthProviderMock(t *testing.T) {
	provider, err := buildAuthProvider(config.Config{AuthProvider: "mock"})
	if err != nil {
		t.Fatalf("buildAuthProvider returned error: %v", err)
	}
	if _, ok := provider.(*auth.MockProvider); !ok {
		t.Fatalf("provider = %T, want *auth.MockProvider", provider)
	}
}

func TestBuildAuthProviderSteamRequiresEnvKey(t *testing.T) {
	t.Setenv(auth.SteamWebAPIKeyEnv, "")

	_, err := buildAuthProvider(config.Config{AuthProvider: "steam", SteamAppID: "480"})
	if !errors.Is(err, auth.ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}
