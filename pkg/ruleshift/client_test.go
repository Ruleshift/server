package ruleshift

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthorized","message":"bad key"}`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "wrong", server.Client())
	_, err := client.GetRoom(context.Background(), "missing")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "unauthorized" || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %#v, want structured unauthorized APIError", err)
	}
}

func TestClientCreatesRow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/developer/modules/inventory/tables/items/rows" {
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
