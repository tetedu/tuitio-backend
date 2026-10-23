package log

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/tetedu/tuitio-backend/internal/config"
)

// TestConfigLoggingRedactsSecrets asserts that no secret (the DATABASE_URL value,
// or anything looking like a credential) ever reaches a log line. The database
// URL is reported only as a boolean marker.
func TestConfigLoggingRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	reset := captureDefault(t, &buf)

	c := config.Config{
		DatabaseURL:      "postgres://user:s3cr3t@db.example:5432/tuitio?sslmode=disable",
		RpcURL:           "https://soroban-testnet.stellar.org",
		RegistryContract: "CREG",
		EscrowContract:   "CESC",
		StartLedger:      10,
		PollSeconds:      15,
		Port:             "8080",
		Host:             "0.0.0.0",
		AllowedOrigins:   []string{"https://app.example"},
	}
	LogConfig(context.Background(), c)
	reset()

	out := buf.String()
	for _, leak := range []string{"s3cr3t", "postgres://", "user:s3cr3t", "db.example"} {
		if strings.Contains(out, leak) {
			t.Fatalf("secret leaked into config log (%q): %s", leak, out)
		}
	}
	if !strings.Contains(out, "database_url_set") {
		t.Fatalf("expected redaction marker database_url_set in log: %s", out)
	}
	assertJSON(t, out)
}

// TestRequestIDInjectedIntoLogLine asserts the request-id handler copies the id
// from context onto every record.
func TestRequestIDInjectedIntoLogLine(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(&requestIDHandler{Handler: slog.NewJSONHandler(&buf, nil)})
	ctx := WithRequestID(context.Background(), "req-abc-123")
	l.InfoContext(ctx, "handled")
	if !strings.Contains(buf.String(), "req-abc-123") {
		t.Fatalf("request_id missing from log line: %s", buf.String())
	}
	assertJSON(t, buf.String())
}

// TestRequestIDAbsentWhenNotSet asserts lines logged without a request id do
// not gain a spurious field.
func TestRequestIDAbsentWhenNotSet(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(&requestIDHandler{Handler: slog.NewJSONHandler(&buf, nil)})
	l.InfoContext(context.Background(), "handled")
	if strings.Contains(buf.String(), "request_id") {
		t.Fatalf("unexpected request_id field: %s", buf.String())
	}
}

// TestNewSelectsJSON asserts LOG_FORMAT=json yields JSON output, while a
// non-json value yields text.
func TestNewSelectsJSON(t *testing.T) {
	// JSON handler
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = w
	l := New("json")
	l.Info("hello", "k", "v")
	w.Close()
	os.Stderr = oldStderr
	var b bytes.Buffer
	io.Copy(&b, r)
	assertJSON(t, b.String())
	if !strings.Contains(b.String(), `"msg":"hello"`) {
		t.Fatalf("expected json msg field: %s", b.String())
	}

	// text handler
	r2, w2, _ := os.Pipe()
	os.Stderr = w2
	l2 := New("text")
	l2.Info("hello", "k", "v")
	w2.Close()
	os.Stderr = oldStderr
	var b2 bytes.Buffer
	io.Copy(&b2, r2)
	if strings.Contains(b2.String(), `"msg":`) {
		t.Fatalf("text handler should not emit JSON: %s", b2.String())
	}
}

// TestNewRequestIDFormat asserts generated ids are 32 hex chars.
func TestNewRequestIDFormat(t *testing.T) {
	id := NewRequestID()
	if len(id) != 32 {
		t.Fatalf("expected 32-char id, got %d: %q", len(id), id)
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("non-hex char in request id: %q", id)
		}
	}
}

// captureDefault swaps the package default logger for one writing to buf and
// returns a restore func.
func captureDefault(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	old := Default()
	SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return func() { SetDefault(old) }
}

func assertJSON(t *testing.T, line string) {
	t.Helper()
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("log line not valid JSON: %v\n%s", err, line)
	}
}
