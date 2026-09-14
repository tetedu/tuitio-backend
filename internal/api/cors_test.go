package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsServer(origins []string) http.Handler {
	srv := &Server{store: nil}
	return srv.RoutesWithCORS(origins)
}

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	h := corsServer([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("allow-origin = %q", got)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("missing Vary: Origin")
	}
}

func TestCORSPreflightAllowedOrigin(t *testing.T) {
	h := corsServer([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodOptions, "/api/grants", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("allow-origin = %q", got)
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	h := corsServer([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example.net")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin leaked for unknown origin: %q", got)
	}
}

func TestCORSPreflightUnknownOriginIsForbidden(t *testing.T) {
	h := corsServer([]string{"https://app.example.com"})
	req := httptest.NewRequest(http.MethodOptions, "/api/grants", nil)
	req.Header.Set("Origin", "https://evil.example.net")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("preflight status = %d, want 403", rec.Code)
	}
}

func TestCORSNoOriginsConfiguredEmitsNothing(t *testing.T) {
	h := corsServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin emitted with no configuration: %q", got)
	}
}
