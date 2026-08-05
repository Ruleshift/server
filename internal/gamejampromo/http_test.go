package gamejampromo

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPublicHandlerRejectsMalformedCodeAndUnknownFields(t *testing.T) {
	manager, err := NewCodeManager(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(&Store{}, manager, NewMetrics())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewPublicHandler(service, &Store{}, "https://ruleshift.ru", 1)
	for _, body := range []string{`{"code":"123"}`, `{"code":"0123456789","extra":true}`, strings.Repeat("x", maxPublicBody+1)} {
		request := httptest.NewRequest(http.MethodPost, "/v1/gamejam-discounts/verify", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d", body, recorder.Code)
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("body %s: Cache-Control = %q", body, recorder.Header().Get("Cache-Control"))
		}
	}
}

func TestAdminHandlerRequiresBasicAuthAndSetsSecurityHeaders(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAdminHandler(nil, nil, nil, NewMetrics(), "moderator", string(hash), slog.Default())
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized response = %d, %v", recorder.Code, recorder.Header())
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/", nil)
	request.SetBasicAuth("moderator", "test-password")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authorized response = %d", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("security headers = %v", recorder.Header())
	}
}

func TestPublicHandlerCORS(t *testing.T) {
	handler := NewPublicHandler(nil, nil, "https://ruleshift.ru", 1)
	allowed := httptest.NewRequest(http.MethodOptions, "/v1/gamejam-discounts/verify", nil)
	allowed.Header.Set("Origin", "https://ruleshift.ru")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, allowed)
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("allowed preflight = %d, %v", recorder.Code, recorder.Header())
	}
	denied := httptest.NewRequest(http.MethodOptions, "/v1/gamejam-discounts/verify", nil)
	denied.Header.Set("Origin", "https://evil.example")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, denied)
	if recorder.Code != http.StatusForbidden || recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("denied preflight = %d, %v", recorder.Code, recorder.Header())
	}
}
