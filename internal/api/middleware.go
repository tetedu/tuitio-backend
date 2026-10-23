package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/tetedu/tuitio-backend/internal/log"
)

// statusRecorder captures the response status so the access log can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// RequestIDMiddleware assigns (or forwards) a request id, stores it in the
// request context, and echoes it back as the X-Request-Id header so clients can
// correlate their request with server-side logs.
func (s *Server) RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = log.NewRequestID()
		}
		r = r.WithContext(log.WithRequestID(r.Context(), id))
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware emits one structured line per request carrying the request
// id, method, path, status, and duration.
func (s *Server) LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Default().InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
