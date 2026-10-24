package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tetedu/tuitio-backend/internal/activity"
)

// TestRoutesAreWired verifies every advertised route resolves to a handler
// rather than a 404 from an unregistered pattern.
func TestRoutesAreWired(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()
	paths := []string{
		"/healthz", "/api/institutions", "/api/institutions/GTEST",
		"/api/grants", "/api/grants/0", "/api/grants/0/terms",
		"/api/activity", "/api/activity/stream", "/api/stats",
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

// TestIndexDescribesService checks the root path returns a service descriptor
// instead of a bare 404, and that unknown paths still 404.
func TestIndexDescribesService(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("root status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("root body not json: %v", err)
	}
	if body["service"] != "tuitio-backend" {
		t.Errorf("service = %v", body["service"])
	}
	if _, ok := body["endpoints"]; !ok {
		t.Error("no endpoints listed")
	}
}

func TestUnknownPathStillNotFound(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", rec.Code)
	}
}

// TestActivityStreamDisabledReturns503 checks the endpoint degrades cleanly
// when no broker is attached.
func TestActivityStreamDisabledReturns503(t *testing.T) {
	srv := &Server{store: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/activity/stream", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestActivityStreamDeliversEvent exercises the full SSE path with a real HTTP
// server and a broker: an event published after connect must arrive as a
// "data:" frame and the contract must match store.Activity's shape.
func TestActivityStreamDeliversEvent(t *testing.T) {
	broker := activity.NewBroker(16)
	srv := &Server{store: nil, broker: broker}
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/activity/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	broker.Publish(activity.Event{
		Ledger:   7,
		TxHash:   "0xabc",
		Contract: "C",
		Event:    "GrantCreated",
		Payload:  json.RawMessage(`{"grant_id":1}`),
		SeenAt:   time.Now(),
	})

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	got := ""
	deadline := time.After(3 * time.Second)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			got = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for SSE event")
		default:
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got == "" {
		t.Fatal("no data frame received")
	}
	if !strings.Contains(got, "GrantCreated") {
		t.Errorf("event missing GrantCreated: %s", got)
	}
	if !strings.Contains(got, `"ledger":7`) {
		t.Errorf("event missing ledger field: %s", got)
	}
}
