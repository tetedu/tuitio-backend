package api

import (
	"net/http"
	"strings"
)

// corsMiddleware handles cross-origin requests from configured origins.
// With no ALLOWED_ORIGINS set, no CORS headers are emitted and browsers
// treat the API as same-origin only.
type corsMiddleware struct {
	allowed map[string]bool
	next    http.Handler
}

func newCORSMiddleware(origins []string, next http.Handler) *corsMiddleware {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = true
	}
	return &corsMiddleware{allowed: allowed, next: next}
}

func (m *corsMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" && m.allowed[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "86400")
	}
	if r.Method == http.MethodOptions {
		// Preflight: answer only for allowed origins; everything else gets
		// the browser's default rejection.
		if origin != "" && m.allowed[origin] {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		return
	}
	m.next.ServeHTTP(w, r)
}

// Routes wraps the mux with request-id assignment, structured request logging,
// and CORS handling for the given allowed origins. The request-id middleware is
// outermost so the id is on the context for everything inside it.
func (s *Server) RoutesWithCORS(origins []string) http.Handler {
	var handler http.Handler = newCORSMiddleware(origins, s.Routes())
	handler = s.LoggingMiddleware(handler)
	handler = s.RequestIDMiddleware(handler)
	return handler
}
