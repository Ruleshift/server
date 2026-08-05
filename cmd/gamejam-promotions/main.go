package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ruleshift/server/internal/gamejampromo"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		slog.Error("game jam promotions service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "ruleshift-gamejam-promotions")
	slog.SetDefault(logger)
	cfg, err := gamejampromo.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return errors.New("open game jam database: invalid configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	startupCtx, cancelStartup := context.WithTimeout(rootCtx, 30*time.Second)
	defer cancelStartup()
	if err := db.PingContext(startupCtx); err != nil {
		return errors.New("game jam database is unavailable")
	}
	if err := gamejampromo.ApplyMigrations(startupCtx, db); err != nil {
		return err
	}

	store := gamejampromo.NewStore(db)
	codes, err := gamejampromo.NewCodeManager(cfg.CodeMasterKey)
	if err != nil {
		return err
	}
	metrics := gamejampromo.NewMetrics()
	service, err := gamejampromo.NewService(store, codes, metrics)
	if err != nil {
		return err
	}
	sources, err := gamejampromo.NewSources(gamejampromo.SourceConfig{AfishaURL: cfg.AfishaURL, JammerURL: cfg.JammerURL, ItchURL: cfg.ItchURL, UserAgent: cfg.UserAgent})
	if err != nil {
		return err
	}
	discovery := gamejampromo.NewDiscovery(store, sources, metrics, logger)

	publicServer := &http.Server{
		Addr: cfg.PublicAddr, Handler: gamejampromo.NewPublicHandler(service, store, cfg.AllowedOrigin, cfg.MaxConcurrentCheck),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	adminServer := &http.Server{
		Addr: cfg.AdminAddr, Handler: gamejampromo.NewAdminHandler(service, discovery, store, metrics, cfg.AdminUsername, cfg.AdminPasswordHash, logger),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}

	serverErrors := make(chan error, 2)
	go serve(logger, "public", publicServer, serverErrors)
	go serve(logger, "admin", adminServer, serverErrors)
	go scheduleDiscovery(rootCtx, cfg.SyncInterval, discovery, logger)

	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
			return err
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	publicErr := publicServer.Shutdown(shutdownCtx)
	adminErr := adminServer.Shutdown(shutdownCtx)
	if publicErr != nil {
		return fmt.Errorf("shutdown public server: %w", publicErr)
	}
	if adminErr != nil {
		return fmt.Errorf("shutdown admin server: %w", adminErr)
	}
	return nil
}

func serve(logger *slog.Logger, name string, server *http.Server, errors chan<- error) {
	logger.Info("starting HTTP listener", "listener", name, "addr", server.Addr)
	errors <- server.ListenAndServe()
}

func scheduleDiscovery(ctx context.Context, interval time.Duration, discovery *gamejampromo.Discovery, logger *slog.Logger) {
	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		if err := discovery.Run(runCtx); err != nil && !errors.Is(err, gamejampromo.ErrDiscoveryBusy) && !errors.Is(err, context.Canceled) {
			logger.ErrorContext(ctx, "scheduled game jam discovery failed")
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
