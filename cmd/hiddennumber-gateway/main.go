package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Ruleshift/server/internal/auth"
	"github.com/Ruleshift/server/internal/config"
	"github.com/Ruleshift/server/internal/game/hiddennumber"
	"github.com/Ruleshift/server/internal/gateway"
	"github.com/Ruleshift/server/internal/room"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	registry := room.NewRegistry(room.RuntimeConfig{
		InputQueueSize: cfg.RoomInputQueueSize, EventStore: room.NewInMemoryEventStore(),
		GameModule: hiddennumber.NewModule(),
	})
	handler, err := gateway.New(gateway.Config{
		MaxMessageBytes: cfg.MaxMessageBytes, SessionSendQueueSize: cfg.SessionSendQueueSize,
		AuthTimeout: cfg.AuthTimeout,
	}, auth.NewMockProvider(), registry, slog.Default())
	if err != nil {
		slog.Error("create gateway", "error", err)
		os.Exit(1)
	}
	defer handler.Close()
	server := &http.Server{Addr: cfg.Addr, Handler: http.HandlerFunc(handler.HandleWebSocket), ReadHeaderTimeout: cfg.ReadTimeout}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("starting hidden-number demo gateway", "addr", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "error", err)
		os.Exit(1)
	}
}
