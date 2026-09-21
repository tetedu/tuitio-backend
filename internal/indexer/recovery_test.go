package indexer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/tetedu/tuitio-backend/internal/rpc"
	"github.com/tetedu/tuitio-backend/internal/store"
)

// fakeRPC rejects the first getEvents call with the retention-window error the
// Soroban RPC really returns, then succeeds. It records every startLedger it
// was asked for so the test can assert the cursor actually moved.
type fakeRPC struct {
	calls     atomic.Int32
	requested chan uint32
}

func (f *fakeRPC) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Method string `json:"method"`
		Params struct {
			StartLedger uint32 `json:"startLedger"`
		} `json:"params"`
	}
	_ = json.Unmarshal(body, &req)

	w.Header().Set("Content-Type", "application/json")

	if req.Method == "getLatestLedger" {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"sequence":4795282}}`))
		return
	}

	select {
	case f.requested <- req.Params.StartLedger:
	default:
	}

	if f.calls.Add(1) == 1 {
		// Cursor is behind the retention window.
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,` +
			`"message":"startLedger must be within the ledger range: 4674323 - 4795282"}}`))
		return
	}
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"events":[],"latestLedger":4795282}}`))
}

// TestIndexerRecoversFromRetentionGap is the regression test for an indexer
// that used to wedge permanently: when its cursor aged out of the RPC's
// retention window it retried the same rejected range forever and silently
// stopped updating the read model.
func TestIndexerRecoversFromRetentionGap(t *testing.T) {
	dir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			RuntimePath(dir).DataPath(dir + "/data").
			Username("tuitio").Password("tuitio").Database("tuitio").
			Port(9878).Logger(io.Discard),
	)
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.Connect(ctx, "postgres://tuitio:tuitio@localhost:9878/tuitio?sslmode=disable")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	// A cursor far behind the window the fake RPC advertises.
	const staleCursor = 4_000_000
	if err := st.SetCursor(ctx, staleCursor); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}

	f := &fakeRPC{requested: make(chan uint32, 8)}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	ix := New(rpc.New(srv.URL), st, []string{"CREGISTRY", "CESCROW"}, time.Second, 0)
	ix.pollOnce(ctx)

	cursor, err := st.Cursor(ctx)
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor <= staleCursor {
		t.Fatalf("cursor did not advance past the retention gap: %d", cursor)
	}
	// It should land at the tip reported after the successful retry.
	if cursor != 4795282 {
		t.Errorf("cursor = %d, want the reported tip 4795282", cursor)
	}
	if got := f.calls.Load(); got < 2 {
		t.Errorf("expected a retry after the range error, got %d call(s)", got)
	}
}

// TestIndexerDoesNotSpinOnRepeatedRangeErrors guards the loop bound: a range
// error that persists must end the poll rather than loop forever.
func TestIndexerDoesNotSpinOnRepeatedRangeErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,` +
			`"message":"startLedger must be within the ledger range: 4674323 - 4795282"}}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			RuntimePath(dir).DataPath(dir + "/data").
			Username("tuitio").Password("tuitio").Database("tuitio").
			Port(9879).Logger(io.Discard),
	)
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := store.Connect(ctx, "postgres://tuitio:tuitio@localhost:9879/tuitio?sslmode=disable")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	ix := New(rpc.New(srv.URL), st, []string{"C1"}, time.Second, 0)
	done := make(chan struct{})
	go func() { ix.pollOnce(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("pollOnce did not return: it is spinning on the range error")
	}
	if calls > 2 {
		t.Errorf("expected at most one jump attempt, got %d calls", calls)
	}
}
