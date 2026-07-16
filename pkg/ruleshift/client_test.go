package ruleshift

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request headers: %v", r.Header)
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

func TestClientPublishesMultipartModuleVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/developer/modules/hiddennumber/versions" {
			t.Errorf("path = %q, want module versions endpoint", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
			t.Errorf("unexpected request headers: %v", r.Header)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart request: %v", err)
		}
		for field, want := range map[string]string{
			"descriptor_set":      "descriptor",
			"conformance_vectors": "vectors",
			"oci_reference":       "registry.example/hidden@sha256:digest",
			"registry_credential": "credential",
		} {
			if got := r.FormValue(field); got != want {
				t.Errorf("field %q = %q, want %q", field, got, want)
			}
		}
		if manifest := r.FormValue("manifest"); !strings.Contains(manifest, `"module_id":"hiddennumber"`) {
			t.Errorf("manifest = %q, want hiddennumber", manifest)
		}
		_, _ = w.Write([]byte(`{"status":"validating"}`))
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "secret", server.Client())
	version, err := client.PublishModuleVersion(context.Background(), PublishModuleVersionRequest{
		ModuleID:           "hiddennumber",
		OCIReference:       "registry.example/hidden@sha256:digest",
		RegistryCredential: "credential",
		Manifest:           RuntimeManifest{ModuleID: "hiddennumber"},
		DescriptorSet:      []byte("descriptor"),
		ConformanceVectors: []byte("vectors"),
	})
	if err != nil {
		t.Fatalf("PublishModuleVersion returned error: %v", err)
	}
	if version.Status != "validating" {
		t.Fatalf("Status = %q, want validating", version.Status)
	}
}

func TestClientLimitsResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.Copy(w, bytes.NewReader(bytes.Repeat([]byte{'x'}, maxResponseBytes+1)))
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "secret", server.Client())
	_, err := client.GetRoom(context.Background(), "room")
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v, want response size error", err)
	}
}

func TestClientTreatsRedirectAsErrorWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	httpClient := server.Client()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	client, _ := NewClient(server.URL, "secret", httpClient)
	_, err := client.GetRoom(context.Background(), "room")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusFound {
		t.Fatalf("error = %#v, want redirect APIError", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want one request", calls.Load())
	}
}

func TestClientAcceptsEmptySuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, "secret", server.Client())
	if _, err := client.GetRoom(context.Background(), "room"); err != nil {
		t.Fatalf("GetRoom returned error for empty success response: %v", err)
	}
}
