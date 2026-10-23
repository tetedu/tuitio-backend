package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequestIDHeaderSet asserts the middleware assigns a fresh X-Request-Id when
// the client does not supply one.
func TestRequestIDHeaderSet(t *testing.T) {
	srv := &Server{store: nil}
	handler := srv.RoutesWithCORS(nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-Id")
	if id == "" {
		t.Fatal("X-Request-Id header not set")
	}
	if len(id) != 32 {
		t.Fatalf("expected 32-char request id, got %d: %q", len(id), id)
	}
}

// TestRequestIDForwarded asserts an incoming X-Request-Id is preserved and echoed
// back, so upstream proxies keep their correlation id.
func TestRequestIDForwarded(t *testing.T) {
	srv := &Server{store: nil}
	handler := srv.RoutesWithCORS(nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "upstream-42")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != "upstream-42" {
		t.Fatalf("expected forwarded id upstream-42, got %q", got)
	}
}
