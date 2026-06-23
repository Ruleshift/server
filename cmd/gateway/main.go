package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/config"
	"github.com/Ruleshift/server/internal/developerapi"
	"github.com/Ruleshift/server/internal/game/xiangqi"
	"github.com/Ruleshift/server/internal/gateway"
	"github.com/Ruleshift/server/internal/room"
	storagepostgres "github.com/Ruleshift/server/internal/storage/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	gameModule := xiangqi.NewModule()
	var authProvider auth.Provider = auth.NewMockProvider()
	var eventStore room.EventStore = room.NewInMemoryEventStore()
	var platform *storagepostgres.Platform
	var developerAPI http.Handler
	if cfg.DatabaseURL != "" {
		platform, err = storagepostgres.Open(ctx, storagepostgres.Config{
			ControlURL:           cfg.DatabaseURL,
			AdminURL:             cfg.DatabaseAdminURL,
			ModuleDatabasePrefix: cfg.ModuleDatabasePrefix,
			DeveloperID:          cfg.DeveloperID,
			DeveloperName:        cfg.DeveloperName,
		})
		if err != nil {
			logger.Error("open platform database", "error", err)
			os.Exit(1)
		}
		defer platform.Close()

		moduleStore, provisionErr := platform.ProvisionModule(ctx, gameModule)
		if provisionErr != nil {
			logger.Error("provision module database", "module", gameModule.DatabaseDefinition().Name, "error", provisionErr)
			os.Exit(1)
		}
		eventStore = moduleStore
		authProvider = auth.NewPersistingProvider(authProvider, platform)
		if cfg.DeveloperAPIKey != "" {
			if err := platform.EnsureDeveloperAPIKey(ctx, cfg.DeveloperID, "bootstrap", cfg.DeveloperAPIKey); err != nil {
				logger.Error("bootstrap developer API key", "error", err)
				os.Exit(1)
			}
			developerAPI, err = developerapi.New(platform)
			if err != nil {
				logger.Error("create developer API", "error", err)
				os.Exit(1)
			}
		}
	}
	registry := room.NewRegistry(room.RuntimeConfig{
		InputQueueSize: cfg.RoomInputQueueSize,
		EventStore:     eventStore,
		GameModule:     gameModule,
	})
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
	mux.HandleFunc("/readyz", readyHandler)
	if cfg.EnableMetrics {
		mux.HandleFunc("/metrics", metricsHandler(registry))
	}
	if developerAPI != nil {
		mux.Handle("/v1/developer/", developerAPI)
	}
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
		"database_enabled", cfg.DatabaseURL != "",
		"developer_api_enabled", developerAPI != nil,
	)

	go func() {
		<-ctx.Done()
		logger.Info("shutting down ruleshift gateway")
		gatewayHandler.Close()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown gateway", "error", err)
		}
	}()

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

func readyHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

func metricsHandler(registry *room.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "ruleshift_up 1\nruleshift_rooms %d\n", registry.RoomCount())
	}
}
