package observabilityapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOverviewEvaluatesDegradedThresholds(t *testing.T) {
	prometheus := fakePrometheus(t, map[string]float64{
		`max(ruleshift_up)`:                                   1,
		`max(timestamp(ruleshift_up))`:                        float64(time.Now().Unix()),
		`sum(increase(ruleshift_gateway_commands_total[5m]))`: 200,
		`sum(rate(ruleshift_gateway_commands_total{result=~"error|module_unavailable|timeout"}[5m])) / clamp_min(sum(rate(ruleshift_gateway_commands_total[5m])), 1)`: .02,
	})
	defer prometheus.Close()
	operations := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer operations.Close()
	h, err := New(Config{PrometheusURL: prometheus.URL, OperationsURL: operations.URL, AllowedOrigin: "https://ruleshift.github.io", Thresholds: DefaultThresholds()})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/overview", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var value Overview
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.Status != "degraded" || !contains(value.Reasons, "server_error_ratio_high") {
		t.Fatalf("overview = %+v", value)
	}
}

func TestOverviewUsesLastScrapeTimestamp(t *testing.T) {
	prometheus := fakePrometheus(t, map[string]float64{
		`max(ruleshift_up)`:            1,
		`max(timestamp(ruleshift_up))`: float64(time.Now().Add(-45 * time.Second).Unix()),
	})
	defer prometheus.Close()
	operations := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer operations.Close()
	h, err := New(Config{PrometheusURL: prometheus.URL, OperationsURL: operations.URL, Thresholds: DefaultThresholds()})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/overview", nil))
	var value Overview
	if err = json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.Status != "degraded" || !contains(value.Reasons, "metrics_stale") {
		t.Fatalf("overview = %+v", value)
	}
}

func TestRoomsProxyForwardsOnlyAllowedQuery(t *testing.T) {
	var received url.Values
	operations := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.URL.Query()
		_, _ = w.Write([]byte(`{"items":[{"public_room_ref":"rm_aaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
	}))
	defer operations.Close()
	prometheus := fakePrometheus(t, nil)
	defer prometheus.Close()
	h, _ := New(Config{PrometheusURL: prometheus.URL, OperationsURL: operations.URL, Thresholds: DefaultThresholds()})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/rooms?limit=10&module=x&query=up&token=secret", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if received.Get("limit") != "10" || received.Get("module") != "x" || received.Has("query") || received.Has("token") {
		t.Fatalf("forwarded query = %v", received)
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("unsafe response: %s", recorder.Body.String())
	}
}

func TestCORSAllowsOnlyConfiguredOrigin(t *testing.T) {
	prometheus := fakePrometheus(t, nil)
	defer prometheus.Close()
	operations := httptest.NewServer(http.NotFoundHandler())
	defer operations.Close()
	h, _ := New(Config{PrometheusURL: prometheus.URL, OperationsURL: operations.URL, AllowedOrigin: "https://ruleshift.github.io", Thresholds: DefaultThresholds()})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unexpected CORS permission")
	}
}

func fakePrometheus(t *testing.T, values map[string]float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		value := values[query]
		stamp := float64(time.Now().UnixMilli()) / 1000
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[%f,%q]}]}}`, stamp, fmt.Sprintf("%g", value))
	}))
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
