package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

// defaultCacheMaxAge is the Cache-Control lifetime applied to /api/* GET
// responses when no explicit max-age is configured.
const defaultCacheMaxAge = 5

// cacheMiddleware adds short-lived caching headers with ETag validation to
// GET responses under /api/. The frontend polls these endpoints after every
// transaction; without validators every poll recomputes and re-sends the
// full payload even when nothing changed.
//
// /healthz and /api/indexer are deliberately excluded and pinned to
// no-store: a stale "ok" hides an outage, and a stale lag number hides a
// stalled indexer.
type cacheMiddleware struct {
	maxAge int
	next   http.Handler
}

func newCacheMiddleware(maxAge int, next http.Handler) *cacheMiddleware {
	return &cacheMiddleware{maxAge: maxAge, next: next}
}

// bodyRecorder captures the status and body so the body can be hashed for
// the ETag before anything is committed to the client. Headers set by the
// wrapped handler land on the real response through the embedded writer.
type bodyRecorder struct {
	http.ResponseWriter
	body   []byte
	status int
}

func (r *bodyRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *bodyRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body = append(r.body, b...)
	return len(b), nil
}

func (m *cacheMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz" || r.URL.Path == "/api/indexer":
		w.Header().Set("Cache-Control", "no-store")
		m.next.ServeHTTP(w, r)
		return
	case r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/"):
		m.next.ServeHTTP(w, r)
		return
	}

	rec := &bodyRecorder{ResponseWriter: w}
	m.next.ServeHTTP(rec, r)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}

	if status != http.StatusOK {
		// Errors and redirects are per-request facts; caching them would
		// pin a failure (or a validation message) into every browser.
		w.WriteHeader(status)
		_, _ = w.Write(rec.body)
		return
	}

	sum := sha256.Sum256(rec.body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(m.maxAge))
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(rec.body)
}

// etagMatches reports whether an If-None-Match header covers etag. The
// header may be "*", a single validator, or a comma-separated list; weak
// validators (W/"...") compare by their opaque tag per RFC 7232.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == etag {
			return true
		}
	}
	return false
}
