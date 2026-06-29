package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ruleshift/server/internal/metrics"
	"github.com/Ruleshift/server/internal/module"
	"github.com/Ruleshift/server/internal/roomcore"
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

func TestReadyHandler(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	readyHandler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); body != `{"status":"ready"}` {
		t.Fatalf("body = %q, want readiness JSON", body)
	}
}

func TestMetricsHandler(t *testing.T) {
	registry, err := roomcore.NewRegistry(roomcore.NewMemoryStore(), module.ResolverFunc(func(context.Context, module.ModuleRef) (module.Runtime, error) { return nil, nil }), 4)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	metricsHandler(metrics.New(), registry).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if !strings.Contains(body, "ruleshift_up 1") {
		t.Fatalf("metrics body = %q, want ruleshift_up metric", body)
	}
	if !strings.Contains(body, "ruleshift_room_runtimes 0") {
		t.Fatalf("metrics body = %q, want room runtime metric", body)
	}
}
