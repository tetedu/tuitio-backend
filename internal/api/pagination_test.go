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

// newTestStoreForAPI boots an isolated Postgres so the pagination handlers can
// be exercised against a real database, mirroring the store package's own tests.
func newTestStoreForAPI(t *testing.T) *store.Store {
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
	return st
}

func seedPaginationData(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		r := rune('A' + i)
		if err := st.Apply(ctx, []store.Applied{{
			Event: "institution_registered", Ledger: int64(100 + i), TxHash: "txI" + string(r), Contract: "registry",
			Decoded: store.UpsertInstitution{
				Address: "INST" + string(r), Payout: "P" + string(r),
				Name: "Institution " + string(r), Country: "RW", Status: "verified", RegisteredAt: int64(1000 + i),
			},
		}}, int64(100+i)); err != nil {
			t.Fatalf("seed institution %d: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		r := rune('a' + i)
		if err := st.Apply(ctx, []store.Applied{{
			Event: "grant_created", Ledger: int64(200 + i), TxHash: "txG" + string(r), Contract: "escrow",
			Decoded: store.UpsertGrant{
				GrantID: int64(i), Sponsor: "SPONSOR", Beneficiary: "STUDENT", Institution: "INSTA",
				Token: "CTOKEN", TermAmount: 500000000, TermsTotal: 3, CreatedAt: int64(2000 + i),
			},
		}}, int64(200+i)); err != nil {
			t.Fatalf("seed grant %d: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		r := rune('a' + i)
		if err := st.RecordActivity(ctx, int64(300+i), "txA"+string(r), "registry", "institution_registered", map[string]any{"n": i}); err != nil {
			t.Fatalf("seed activity %d: %v", i, err)
		}
	}
}

func cursorParam(cursor string) string {
	if cursor == "" {
		return ""
	}
	return "&cursor=" + cursor
}

// walkPages drives an endpoint with ?limit=2 and follows next_cursor until it
// is empty, asserting that every row appears exactly once.
func walkPages(t *testing.T, mux *http.ServeMux, path string, total int, key func([]byte) (map[string]bool, error)) {
	t.Helper()
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		req := httptest.NewRequest(http.MethodGet, path+"?limit=2"+cursorParam(cursor), nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		keys, err := key(rec.Body.Bytes())
		if err != nil {
			t.Fatalf("decode page: %v", err)
		}
		if len(keys) == 0 {
			t.Fatal("empty page before exhaustion")
		}
		for k := range keys {
			if seen[k] {
				t.Fatalf("duplicate row %s", k)
			}
			seen[k] = true
		}
		pages++
		// extract next_cursor
		var resp struct {
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode cursor: %v", err)
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
		if pages > 20 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != total {
		t.Errorf("collected %d rows, want %d", len(seen), total)
	}
}

func TestInstitutionsPagination(t *testing.T) {
	st := newTestStoreForAPI(t)
	seedPaginationData(t, st)
	srv := &Server{store: st}
	mux := srv.Routes()

	key := func(body []byte) (map[string]bool, error) {
		var resp struct {
			Items []store.Institution `json:"items"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		m := map[string]bool{}
		for _, it := range resp.Items {
			m[it.Address] = true
		}
		return m, nil
	}
	walkPages(t, mux, "/api/institutions", 5, key)
}

func TestGrantsPagination(t *testing.T) {
	st := newTestStoreForAPI(t)
	seedPaginationData(t, st)
	srv := &Server{store: st}
	mux := srv.Routes()

	key := func(body []byte) (map[string]bool, error) {
		var resp struct {
			Items []store.Grant `json:"items"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		m := map[string]bool{}
		for _, g := range resp.Items {
			m[string(rune('0'+g.GrantID))] = true
		}
		return m, nil
	}
	walkPages(t, mux, "/api/grants", 5, key)
}

func TestActivityPagination(t *testing.T) {
	st := newTestStoreForAPI(t)
	seedPaginationData(t, st)
	srv := &Server{store: st}
	mux := srv.Routes()

	key := func(body []byte) (map[string]bool, error) {
		var resp struct {
			Items []store.Activity `json:"items"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, err
		}
		m := map[string]bool{}
		for _, a := range resp.Items {
			m[a.TxHash] = true
		}
		return m, nil
	}
	walkPages(t, mux, "/api/activity", 5, key)
}

// A malformed limit is rejected before the store is touched, so these run with
// a nil store and still exercise the handler's validation path.
func TestInvalidLimitReturns400(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()
	req := httptest.NewRequest(http.MethodGet, "/api/institutions?limit=abc", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d, want 400", rec.Code)
	}
}

// A malformed cursor is validated (and rejected) before the database call, so a
// nil store is safe here too.
func TestInvalidCursorReturns400(t *testing.T) {
	srv := &Server{store: nil}
	mux := srv.Routes()
	for _, path := range []string{
		"/api/institutions?cursor=not-a-cursor",
		"/api/grants?cursor=not-a-cursor",
		"/api/activity?cursor=not-a-cursor",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rec.Code)
		}
	}
}

// The documented response shape must be items + next_cursor, not the legacy
// bare array.
func TestInstitutionsResponseShape(t *testing.T) {
	st := newTestStoreForAPI(t)
	seedPaginationData(t, st)
	srv := &Server{store: st}
	mux := srv.Routes()
	req := httptest.NewRequest(http.MethodGet, "/api/institutions?limit=2", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Items      []store.Institution `json:"items"`
		NextCursor string              `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Items == nil {
		t.Error("missing items field")
	}
	// 5 seeded, limit 2 -> first page has 2 and a cursor.
	if len(resp.Items) != 2 {
		t.Errorf("page size = %d, want 2", len(resp.Items))
	}
	if resp.NextCursor == "" {
		t.Error("expected a next_cursor on a non-final page")
	}
}
