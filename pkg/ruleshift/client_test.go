package ruleshift

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCreatesModuleWithBearerAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/developer/modules" {
			t.Fatalf("request = %s %s, want POST modules", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		var request CreateModuleRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Module{Key: request.Key, DisplayName: request.DisplayName})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret", server.Client())
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	module, err := client.CreateModule(context.Background(), CreateModuleRequest{Key: "inventory", DisplayName: "Inventory"})
	if err != nil {
		t.Fatalf("CreateModule returned error: %v", err)
	}
	if module.Key != "inventory" {
		t.Fatalf("module key = %q, want inventory", module.Key)
	}
}

func TestClientReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthorized","message":"bad key"}`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "wrong", server.Client())
	_, err := client.ListModules(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "unauthorized" || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %#v, want structured unauthorized APIError", err)
	}
}

func TestClientCreatesRow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/developer/modules/inventory/tables/items/rows" {
			t.Fatalf("path = %q, want rows endpoint", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"module":"inventory","table":"items","values":{"id":"item-1"}}`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", server.Client())
	row, err := client.CreateRow(context.Background(), "inventory", "items", map[string]any{"id": "item-1"})
	if err != nil {
		t.Fatalf("CreateRow returned error: %v", err)
	}
	if row.Values["id"] != "item-1" {
		t.Fatalf("row = %#v, want item-1", row)
	}
}
