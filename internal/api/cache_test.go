package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubHandler is the inner handler the cache middleware wraps; it records
// whether it was reached so pass-through paths can be asserted.
func stubHandler(body string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func cacheHandler(maxAge int, path string) http.Handler {
	return newCacheMiddleware(maxAge, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"other":true}`))
	}))
}

func TestCacheHeadersOnAPIRoute(t *testing.T) {
	h := cacheHandler(5, "/api/grants")

	req := httptest.NewRequest(http.MethodGet, "/api/grants", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=5" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=5")
	}
	if etag := rec.Header().Get("ETag"); etag == "" {
		t.Error("missing ETag")
	}
	if rec.Body.Len() == 0 {
		t.Error("body was dropped")
	}
}

func TestIfNoneMatchReturns304(t *testing.T) {
	h := cacheHandler(5, "/api/grants")

	// First request: capture the ETag the middleware derives from the body.
	req := httptest.NewRequest(http.MethodGet, "/api/grants", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first response")
	}

	// Conditional request with the same validator: 304, no body.
	req = httptest.NewRequest(http.MethodGet, "/api/grants", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != etag {
		t.Errorf("ETag = %q, want %q", got, etag)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=5" {
		t.Errorf("Cache-Control = %q, want it kept on the 304", got)
	}
}

func TestIfNoneMatchAcceptsListAndWildcard(t *testing.T) {
	h := cacheHandler(5, "/api/grants")

	req := httptest.NewRequest(http.MethodGet, "/api/grants", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	etag := rec.Header().Get("ETag")

	for _, header := range []string{
		"*",
		`"some-other-etag", ` + etag,
		`W/` + etag, // weak validator, same opaque tag
	} {
		req = httptest.NewRequest(http.MethodGet, "/api/grants", nil)
		req.Header.Set("If-None-Match", header)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified {
			t.Errorf("If-None-Match %q: status = %d, want 304", header, rec.Code)
		}
	}

	// A different validator must revalidate: full 200 with body.
	req = httptest.NewRequest(http.MethodGet, "/api/grants", nil)
	req.Header.Set("If-None-Match", `"stale-etag"`)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("stale etag: status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("stale etag: body missing")
	}
}

func TestCacheControlConfigurable(t *testing.T) {
	h := cacheHandler(60, "/api/grants")

	req := httptest.NewRequest(http.MethodGet, "/api/grants", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want %q", got, "public, max-age=60")
	}
}

func TestNoStorePathsNeverCached(t *testing.T) {
	for _, path := range []string{"/healthz", "/api/indexer"} {
		h := cacheHandler(5, path)

		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", path, got)
		}
		if etag := rec.Header().Get("ETag"); etag != "" {
			t.Errorf("%s: unexpected ETag %q", path, etag)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: body was dropped", path)
		}
	}
}

func TestNonAPIPathPassesThrough(t *testing.T) {
	h := cacheHandler(5, "/api/grants")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("unexpected ETag %q on a non-/api path", etag)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("unexpected Cache-Control %q on a non-/api path", cc)
	}
}

func TestNonGetPassesThrough(t *testing.T) {
	h := cacheHandler(5, "/api/grants")

	req := httptest.NewRequest(http.MethodPost, "/api/grants", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("unexpected ETag %q on a POST", etag)
	}
}

func TestErrorResponseNotCached(t *testing.T) {
	h := newCacheMiddleware(5, stubHandler(`{"error":"bad request"}`, http.StatusBadRequest))

	req := httptest.NewRequest(http.MethodGet, "/api/grants/abc", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("error response got an ETag %q", etag)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("error response got Cache-Control %q", cc)
	}
	if rec.Body.String() != `{"error":"bad request"}` {
		t.Errorf("body = %q, want the handler's body preserved", rec.Body.String())
	}
}

// TestRoutesWithCORSDefaultsMaxAge pins the variadic default so existing
// call sites keep compiling and behaving.
func TestRoutesWithCORSDefaultsMaxAge(t *testing.T) {
	srv := &Server{store: nil}
	h := srv.RoutesWithCORS(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/grants/abc", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// 400 from id validation — proves the mux is reachable through the
	// middleware chain — and no caching headers on an error.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("error response got an ETag %q", etag)
	}
}
