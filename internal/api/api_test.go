package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRoutesAreWired verifies every advertised route resolves to a handler
// rather than a 404 from an unregistered pattern.
func TestRoutesAreWired(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()
	paths := []string{
		"/healthz", "/api/institutions", "/api/institutions/GTEST",
		"/api/grants", "/api/grants/0", "/api/grants/0/terms",
		"/api/activity", "/api/stats",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		_, pattern := mux.Handler(req)
		if pattern == "" {
			t.Errorf("%s: no route registered", p)
		}
	}
}

// TestGrantIDValidation checks malformed ids are rejected before hitting the store.
func TestGrantIDValidation(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()
	for _, id := range []string{"abc", "-1", "1.5"} {
		req := httptest.NewRequest(http.MethodGet, "/api/grants/"+id, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: expected 400, got %d", id, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("id %q: body not json: %v", id, err)
		}
	}
}
