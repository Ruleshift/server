package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/config"
	netx "github.com/Ruleshift/server/internal/net"
	"github.com/Ruleshift/server/internal/room"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	authProvider := auth.NewMockProvider()
	registry := room.NewRegistry(room.RuntimeConfig{InputQueueSize: cfg.RoomInputQueueSize})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc(netx.WebSocketPath, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "websocket gateway will be implemented in phase 5", http.StatusNotImplemented)
	})

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

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
