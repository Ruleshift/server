package main

import (
	"testing"
	"time"

	"github.com/Ruleshift/server/internal/observabilityapi"
)

var observabilityEnvironmentKeys = []string{
	"OBS_ADDR",
	"OBS_PROMETHEUS_URL",
	"OBS_OPERATIONS_URL",
	"OBS_ALLOWED_ORIGIN",
	"OBS_GRAFANA_SYSTEM_URL",
	"OBS_GRAFANA_RUNTIME_URL",
	"OBS_STALE_AFTER",
	"OBS_UNAVAILABLE_AFTER",
	"OBS_ERROR_RATIO_THRESHOLD",
	"OBS_COMMAND_P95_THRESHOLD",
	"OBS_QUEUE_SATURATION_THRESHOLD",
	"OBS_SLOW_CONSUMER_RATIO_THRESHOLD",
	"OBS_MIN_COMMANDS",
}

func TestLoadConfigDefaults(t *testing.T) {
	clearObservabilityEnvironment(t)

	cfg, addr, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if addr != ":8081" || cfg.PrometheusURL != "http://prometheus:9090" || cfg.OperationsURL != "http://ruleshift:9091" {
		t.Fatalf("unexpected defaults: addr=%q prometheus=%q operations=%q", addr, cfg.PrometheusURL, cfg.OperationsURL)
	}
	if cfg.Thresholds != observabilityapi.DefaultThresholds() {
		t.Fatalf("Thresholds = %+v, want %+v", cfg.Thresholds, observabilityapi.DefaultThresholds())
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	clearObservabilityEnvironment(t)
	t.Setenv("OBS_ADDR", "127.0.0.1:8082")
	t.Setenv("OBS_STALE_AFTER", "45s")
	t.Setenv("OBS_UNAVAILABLE_AFTER", "2m")
	t.Setenv("OBS_ERROR_RATIO_THRESHOLD", "0.02")
	t.Setenv("OBS_QUEUE_SATURATION_THRESHOLD", "0.9")

	cfg, addr, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if addr != "127.0.0.1:8082" || cfg.Thresholds.StaleAfter != 45*time.Second || cfg.Thresholds.UnavailableAfter != 2*time.Minute || cfg.Thresholds.ErrorRatio != .02 || cfg.Thresholds.QueueSaturation != .9 {
		t.Fatalf("overrides were not decoded: addr=%q thresholds=%+v", addr, cfg.Thresholds)
	}
}

func TestLoadConfigRejectsInvalidEnvironment(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "duration", key: "OBS_STALE_AFTER", value: "later"},
		{name: "float", key: "OBS_ERROR_RATIO_THRESHOLD", value: "many"},
		{name: "threshold order", key: "OBS_STALE_AFTER", value: "2m"},
		{name: "queue ratio", key: "OBS_QUEUE_SATURATION_THRESHOLD", value: "1.1"},
		{name: "negative minimum", key: "OBS_MIN_COMMANDS", value: "-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearObservabilityEnvironment(t)
			t.Setenv(test.key, test.value)
			if _, _, err := loadConfig(); err == nil {
				t.Fatal("loadConfig returned nil error")
			}
		})
	}
}

func clearObservabilityEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range observabilityEnvironmentKeys {
		t.Setenv(key, "")
	}
}
