package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/tetedu/tuitio-backend/internal/store"
)

// TestRoutesAreWired verifies every advertised route resolves to a handler
// rather than a 404 from an unregistered pattern.
func TestRoutesAreWired(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()
	paths := []string{
		"/healthz", "/api/institutions", "/api/institutions/GTEST",
		"/api/institutions/GTEST/stats",
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

// newTestServer boots a throwaway Postgres, seeds it with an institution and
// a couple of grants/terms, and returns an API server backed by it.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := embeddedpostgres.DefaultConfig().
		RuntimePath(dir).
		DataPath(dir + "/data").
		Username("tuitio").Password("tuitio").Database("tuitio").
		Port(9877).
		Logger(io.Discard)
	pg := embeddedpostgres.NewDatabase(cfg)
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	st, err := store.Connect(context.Background(), "postgres://tuitio:tuitio@localhost:9877/tuitio?sslmode=disable")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	events := []store.Applied{
		{Event: "institution_registered", Ledger: 100, TxHash: "tx1", Contract: "registry", Decoded: store.UpsertInstitution{
			Address: "GSCHOOL", Payout: "GSCHOOL", Name: "Kigali Technical College", Country: "RW", Status: "verified", RegisteredAt: 1000,
		}},
		{Event: "grant_created", Ledger: 102, TxHash: "tx3", Contract: "escrow", Decoded: store.UpsertGrant{
			GrantID: 0, Sponsor: "GSPONSOR", Beneficiary: "GSTUDENT", Institution: "GSCHOOL",
			Token: "CTOKEN", TermAmount: 500000000, TermsTotal: 3, CreatedAt: 2000,
		}},
		{Event: "grant_created", Ledger: 103, TxHash: "tx4", Contract: "escrow", Decoded: store.UpsertGrant{
			GrantID: 1, Sponsor: "GSPONSOR", Beneficiary: "GSTUDENT", Institution: "GSCHOOL",
			Token: "CTOKEN", TermAmount: 500000000, TermsTotal: 2, CreatedAt: 2100,
		}},
		{Event: "term_attested", Ledger: 104, TxHash: "tx5", Contract: "escrow", Decoded: store.UpsertTerm{
			GrantID: 0, TermIndex: 0, Status: "released", AttestedAt: 3000, ReleaseAfter: 3600,
		}},
		{Event: "term_attested", Ledger: 105, TxHash: "tx6", Contract: "escrow", Decoded: store.UpsertTerm{
			GrantID: 1, TermIndex: 0, Status: "disputed", AttestedAt: 3100, ReleaseAfter: 3700,
		}},
	}
	if err := st.Apply(context.Background(), events, 105); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return New(st)
}

// TestInstitutionStatsHandler verifies the per-school stats endpoint aggregates
// grants and terms in one query and reports the expected totals.
func TestInstitutionStatsHandler(t *testing.T) {
	srv := newTestServer(t)
	mux := srv.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/institutions/GSCHOOL/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var st store.InstitutionStats
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Address != "GSCHOOL" {
		t.Errorf("address = %q, want GSCHOOL", st.Address)
	}
	if st.GrantsTotal != 2 {
		t.Errorf("grants_total = %d, want 2", st.GrantsTotal)
	}
	if st.TermsReleased != 1 {
		t.Errorf("terms_released = %d, want 1", st.TermsReleased)
	}
	if st.TermsRefunded != 0 {
		t.Errorf("terms_refunded = %d, want 0", st.TermsRefunded)
	}
	if st.TotalReleased != 500000000 {
		t.Errorf("total_released = %d, want 500000000", st.TotalReleased)
	}
	// grant 1 term 0 is disputed; grant 0 term 0 is released -> 1 of 2 terms disputed.
	if st.DisputeRate != 0.5 {
		t.Errorf("dispute_rate = %v, want 0.5", st.DisputeRate)
	}
}

// TestInstitutionStatsNotFound confirms an unknown institution 404s rather than
// returning a fabricated zero record.
func TestInstitutionStatsNotFound(t *testing.T) {
	srv := newTestServer(t)
	mux := srv.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/institutions/UNKNOWN/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
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
