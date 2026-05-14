package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSteamWebAPIProviderAuthenticatesTicket(t *testing.T) {
	var gotKey string
	var gotAppID string
	var gotTicket string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		gotAppID = r.URL.Query().Get("appid")
		gotTicket = r.URL.Query().Get("ticket")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"params":{"result":"OK","steamid":"76561198000000001","ownersteamid":"76561198000000001"}}}`))
	}))
	defer server.Close()

	provider := newTestSteamProvider(t, server, "test-key", "480")

	identity, err := provider.AuthenticateTicket(context.Background(), "ticket-bytes")
	if err != nil {
		t.Fatalf("AuthenticateTicket returned error: %v", err)
	}

	if gotKey != "test-key" {
		t.Fatalf("key query = %q, want test-key", gotKey)
	}
	if gotAppID != "480" {
		t.Fatalf("appid query = %q, want 480", gotAppID)
	}
	if gotTicket != "ticket-bytes" {
		t.Fatalf("ticket query = %q, want ticket-bytes", gotTicket)
	}
	if identity.PlayerID != "steam:76561198000000001" {
		t.Fatalf("PlayerID = %q, want steam-prefixed SteamID", identity.PlayerID)
	}
	if identity.SteamID != "76561198000000001" {
		t.Fatalf("SteamID = %q, want raw SteamID", identity.SteamID)
	}
	if identity.AppID != "480" {
		t.Fatalf("AppID = %q, want 480", identity.AppID)
	}
	if !identity.OwnershipVerified {
		t.Fatal("OwnershipVerified = false, want true")
	}
}

func TestSteamWebAPIProviderRejectsSteamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"error":{"errorcode":101,"errordesc":"Invalid ticket"}}}`))
	}))
	defer server.Close()

	provider := newTestSteamProvider(t, server, "test-key", "480")

	_, err := provider.AuthenticateTicket(context.Background(), "bad-ticket")
	if !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("error = %v, want ErrInvalidTicket", err)
	}
}

func TestSteamWebAPIProviderReportsUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	provider := newTestSteamProvider(t, server, "test-key", "480")

	_, err := provider.AuthenticateTicket(context.Background(), "ticket-bytes")
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("error = %v, want ErrProviderUnavailable", err)
	}
}

func TestSteamWebAPIProviderFromEnvReadsAPIKey(t *testing.T) {
	t.Setenv(SteamWebAPIKeyEnv, "env-key")

	provider, err := NewSteamWebAPIProviderFromEnv("480", http.DefaultClient)
	if err != nil {
		t.Fatalf("NewSteamWebAPIProviderFromEnv returned error: %v", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
	if provider.appID != "480" {
		t.Fatalf("appID = %q, want 480", provider.appID)
	}
}

func TestSteamWebAPIProviderRejectsEmptyTicket(t *testing.T) {
	provider, err := NewSteamWebAPIProvider(SteamWebAPIConfig{
		APIKey:   "test-key",
		AppID:    "480",
		Endpoint: defaultSteamAuthenticateUserTicketURL,
		Client:   http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("NewSteamWebAPIProvider returned error: %v", err)
	}

	_, err = provider.AuthenticateTicket(context.Background(), "")
	if !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("error = %v, want ErrInvalidTicket", err)
	}
}

func newTestSteamProvider(t *testing.T, server *httptest.Server, apiKey string, appID string) *SteamWebAPIProvider {
	t.Helper()

	provider, err := NewSteamWebAPIProvider(SteamWebAPIConfig{
		APIKey:   apiKey,
		AppID:    appID,
		Endpoint: server.URL,
		Client:   server.Client(),
	})
	if err != nil {
		t.Fatalf("NewSteamWebAPIProvider returned error: %v", err)
	}
	return provider
}
