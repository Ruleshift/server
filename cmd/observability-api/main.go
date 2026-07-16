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
	"time"

	"github.com/Ruleshift/server/internal/observabilityapi"
	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
)

type environment struct {
	Addr             string        `env:"OBS_ADDR" envDefault:":8081" validate:"required"`
	PrometheusURL    string        `env:"OBS_PROMETHEUS_URL" envDefault:"http://prometheus:9090" validate:"required"`
	OperationsURL    string        `env:"OBS_OPERATIONS_URL" envDefault:"http://ruleshift:9091" validate:"required"`
	AllowedOrigin    string        `env:"OBS_ALLOWED_ORIGIN" envDefault:"https://ruleshift.github.io"`
	GrafanaSystem    string        `env:"OBS_GRAFANA_SYSTEM_URL"`
	GrafanaRuntime   string        `env:"OBS_GRAFANA_RUNTIME_URL"`
	StaleAfter       time.Duration `env:"OBS_STALE_AFTER" envDefault:"30s" validate:"ltfield=UnavailableAfter"`
	UnavailableAfter time.Duration `env:"OBS_UNAVAILABLE_AFTER" envDefault:"1m"`
	ErrorRatio       float64       `env:"OBS_ERROR_RATIO_THRESHOLD" envDefault:"0.01" validate:"gte=0"`
	CommandP95       time.Duration `env:"OBS_COMMAND_P95_THRESHOLD" envDefault:"250ms"`
	QueueSaturation  float64       `env:"OBS_QUEUE_SATURATION_THRESHOLD" envDefault:"0.8" validate:"gt=0,lte=1"`
	SlowConsumerRate float64       `env:"OBS_SLOW_CONSUMER_RATIO_THRESHOLD" envDefault:"0.005" validate:"gte=0"`
	MinCommands      float64       `env:"OBS_MIN_COMMANDS" envDefault:"100" validate:"gte=0"`
}

var environmentValidator = validator.New(validator.WithRequiredStructEnabled())

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
	values, err := env.ParseAs[environment]()
	if err != nil {
		return observabilityapi.Config{}, "", fmt.Errorf("parse observability environment: %w", err)
	}
	if err = environmentValidator.Struct(values); err != nil {
		return observabilityapi.Config{}, "", fmt.Errorf("validate observability configuration: %w", err)
	}
	thresholds := observabilityapi.Thresholds{
		StaleAfter:       values.StaleAfter,
		UnavailableAfter: values.UnavailableAfter,
		ErrorRatio:       values.ErrorRatio,
		CommandP95:       values.CommandP95,
		QueueSaturation:  values.QueueSaturation,
		SlowConsumerRate: values.SlowConsumerRate,
		MinCommands:      values.MinCommands,
	}
	return observabilityapi.Config{
		PrometheusURL:  values.PrometheusURL,
		OperationsURL:  values.OperationsURL,
		AllowedOrigin:  values.AllowedOrigin,
		GrafanaSystem:  values.GrafanaSystem,
		GrafanaRuntime: values.GrafanaRuntime,
		Timeout:        3 * time.Second,
		CacheTTL:       5 * time.Second,
		Thresholds:     thresholds,
	}, values.Addr, nil
}

func fatal(message string, err error) { slog.Error(message, "error", err); os.Exit(1) }
