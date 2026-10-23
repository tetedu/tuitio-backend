// Package log configures structured logging (log/slog) for the Tuitio service.
//
// It builds a slog.Logger whose output format is driven by the LOG_FORMAT
// environment variable ("json" → JSON handler, anything else → text), and
// attaches a request id from the context to every record so a single request's
// lines can be correlated across packages and with the X-Request-Id header.
package log

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/tetedu/tuitio-backend/internal/config"
)

type ctxKey int

const requestIDKey ctxKey = iota

// New builds a *slog.Logger from a LOG_FORMAT value. "json" selects the JSON
// handler; anything else (including empty) selects the human-readable text
// handler. Every record is run through a handler that injects the request id
// from the context.
func New(format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var h slog.Handler
	if strings.EqualFold(format, "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(&requestIDHandler{Handler: h})
}

// requestIDHandler copies the request id from the context onto every record so
// correlated lines share a stable identifier.
type requestIDHandler struct {
	slog.Handler
}

func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		r = r.Clone()
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithRequestID returns a context carrying the given request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID extracts the request id from ctx, or "" if absent.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// NewRequestID returns a fresh, collision-resistant request id. Callers may
// instead forward an incoming X-Request-Id header; this is the fallback.
func NewRequestID() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		// Extremely unlikely; a fixed marker is better than panicking.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

var (
	defaultMu sync.RWMutex
	defaultL  = New(os.Getenv("LOG_FORMAT"))
)

// Default returns the process-wide logger. It is safe for concurrent use.
func Default() *slog.Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultL
}

// SetDefault replaces the process-wide logger. Used by tests to capture output.
func SetDefault(l *slog.Logger) {
	defaultMu.Lock()
	defaultL = l
	defaultMu.Unlock()
}

// LogConfig emits the startup configuration with secrets redacted. The database
// URL is never logged in cleartext — only whether it was provided — so a dump
// of logs can never leak credentials.
func LogConfig(ctx context.Context, c config.Config) {
	Default().InfoContext(ctx, "config loaded",
		"rpc_url", c.RpcURL,
		"registry_contract", c.RegistryContract,
		"escrow_contract", c.EscrowContract,
		"start_ledger", c.StartLedger,
		"poll_seconds", c.PollSeconds,
		"port", c.Port,
		"host", c.Host,
		"allowed_origins", len(c.AllowedOrigins),
		"database_url_set", c.DatabaseURL != "",
	)
}
