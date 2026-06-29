package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Ruleshift/server/internal/observabilityapi"
)

func main() {
	cfg, addr, err := loadConfig()
	if err != nil {
		fatal("load config", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "ruleshift-observability-api")
	slog.SetDefault(logger)
	handler, err := observabilityapi.New(cfg)
	if err != nil {
		fatal("configure API", err)
	}
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Info("starting public observability API", "addr", addr)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("API stopped", err)
	}
}

func loadConfig() (observabilityapi.Config, string, error) {
	thresholds := observabilityapi.DefaultThresholds()
	var err error
	if thresholds.StaleAfter, err = envDuration("OBS_STALE_AFTER", thresholds.StaleAfter); err != nil {
		return observabilityapi.Config{}, "", err
	}
	if thresholds.UnavailableAfter, err = envDuration("OBS_UNAVAILABLE_AFTER", thresholds.UnavailableAfter); err != nil {
		return observabilityapi.Config{}, "", err
	}
	if thresholds.ErrorRatio, err = envFloat("OBS_ERROR_RATIO_THRESHOLD", thresholds.ErrorRatio); err != nil {
		return observabilityapi.Config{}, "", err
	}
	if thresholds.CommandP95, err = envDuration("OBS_COMMAND_P95_THRESHOLD", thresholds.CommandP95); err != nil {
		return observabilityapi.Config{}, "", err
	}
	if thresholds.QueueSaturation, err = envFloat("OBS_QUEUE_SATURATION_THRESHOLD", thresholds.QueueSaturation); err != nil {
		return observabilityapi.Config{}, "", err
	}
	if thresholds.SlowConsumerRate, err = envFloat("OBS_SLOW_CONSUMER_RATIO_THRESHOLD", thresholds.SlowConsumerRate); err != nil {
		return observabilityapi.Config{}, "", err
	}
	if thresholds.MinCommands, err = envFloat("OBS_MIN_COMMANDS", thresholds.MinCommands); err != nil {
		return observabilityapi.Config{}, "", err
	}
	if thresholds.StaleAfter >= thresholds.UnavailableAfter || thresholds.ErrorRatio < 0 || thresholds.QueueSaturation <= 0 || thresholds.QueueSaturation > 1 || thresholds.SlowConsumerRate < 0 || thresholds.MinCommands < 0 {
		return observabilityapi.Config{}, "", fmt.Errorf("invalid observability thresholds")
	}
	cfg := observabilityapi.Config{PrometheusURL: env("OBS_PROMETHEUS_URL", "http://prometheus:9090"), OperationsURL: env("OBS_OPERATIONS_URL", "http://ruleshift:9091"), AllowedOrigin: env("OBS_ALLOWED_ORIGIN", "https://ruleshift.github.io"), GrafanaSystem: os.Getenv("OBS_GRAFANA_SYSTEM_URL"), GrafanaRuntime: os.Getenv("OBS_GRAFANA_RUNTIME_URL"), Timeout: 3 * time.Second, CacheTTL: 5 * time.Second, Thresholds: thresholds}
	return cfg, env("OBS_ADDR", ":8081"), nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
func envFloat(key string, fallback float64) (float64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}
func fatal(message string, err error) { slog.Error(message, "error", err); os.Exit(1) }
