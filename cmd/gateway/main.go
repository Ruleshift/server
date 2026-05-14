package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/config"
	"github.com/Ruleshift/server/internal/gateway"
	"github.com/Ruleshift/server/internal/room"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	authProvider, err := buildAuthProvider(cfg)
	if err != nil {
		logger.Error("create auth provider", "error", err)
		os.Exit(1)
	}
	registry := room.NewRegistry(room.RuntimeConfig{InputQueueSize: cfg.RoomInputQueueSize})
	gatewayHandler, err := gateway.New(gateway.Config{
		MaxMessageBytes:      cfg.MaxMessageBytes,
		SessionSendQueueSize: cfg.SessionSendQueueSize,
		AuthTimeout:          cfg.AuthTimeout,
	}, authProvider, registry, logger)
	if err != nil {
		logger.Error("create gateway", "error", err)
		os.Exit(1)
	}
	defer gatewayHandler.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc(gateway.WebSocketPath, gatewayHandler.HandleWebSocket)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	logger.Info(
		"starting ruleshift gateway",
		"addr", cfg.Addr,
		"env", cfg.Env,
		"auth_provider", fmt.Sprintf("%T", authProvider),
		"room_count", registry.RoomCount(),
	)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func buildAuthProvider(cfg config.Config) (auth.Provider, error) {
	switch cfg.AuthProvider {
	case "mock":
		return auth.NewMockProvider(), nil
	case "steam":
		return auth.NewSteamWebAPIProviderFromEnv(cfg.SteamAppID, nil)
	default:
		return nil, fmt.Errorf("unsupported auth provider %q", cfg.AuthProvider)
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
