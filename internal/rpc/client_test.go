package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Testnet contract addresses from .env.example, used in the recorded
// getEvents fixtures.
const (
	registry = "CCTZVSPMIYEA4X2Z6UP5IPIO2HBIZWHLV37OUDBV32YUCP64JNIPBCPO"
	escrow   = "CDPGTHO2O7LTURTJF7L7ZZZTZCXPNABAPGVRDMBYIJ737R6O3DOPWJ34"
)

// fixtureServer serves one recorded response from testdata/ for the lifetime
// of the test and records every request it receives, so request-shape
// regressions fail loudly.
type fixtureServer struct {
	body     []byte
	hits     int
	lastReq  []byte
	lastType string
}

func serveFixture(t *testing.T, name string) (*Client, *fixtureServer) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	f := &fixtureServer{body: raw}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		f.lastReq, _ = io.ReadAll(r.Body)
		f.lastType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), f
}

func decodeRequest(t *testing.T, body []byte) rpcRequest {
	t.Helper()
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode recorded request: %v", err)
	}
	return req
}

func TestEventsRequestShape(t *testing.T) {
	c, f := serveFixture(t, "getEvents.json")

	if _, _, err := c.Events(context.Background(), 4571234, []string{registry, escrow}); err != nil {
		t.Fatalf("Events: %v", err)
	}
	if f.hits != 1 {
		t.Fatalf("hits = %d, want 1", f.hits)
	}
	if f.lastType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", f.lastType)
	}

	req := decodeRequest(t, f.lastReq)
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", req.JSONRPC)
	}
	if req.Method != "getEvents" {
		t.Errorf("method = %q, want getEvents", req.Method)
	}
	if req.Params.StartLedger != 4571234 {
		t.Errorf("startLedger = %d, want 4571234", req.Params.StartLedger)
	}
	if len(req.Params.Filters) != 1 {
		t.Fatalf("filters = %d, want 1", len(req.Params.Filters))
	}
	flt := req.Params.Filters[0]
	if flt.Type != "contract" {
		t.Errorf("filter type = %q, want contract", flt.Type)
	}
	if len(flt.ContractIDs) != 2 || flt.ContractIDs[0] != registry || flt.ContractIDs[1] != escrow {
		t.Errorf("contractIds = %v, want [%s %s]", flt.ContractIDs, registry, escrow)
	}
	if req.Params.Limit != 100 {
		t.Errorf("limit = %d, want 100", req.Params.Limit)
	}
}

func TestEventsDecodesRecordedResponse(t *testing.T) {
	c, _ := serveFixture(t, "getEvents.json")

	events, latest, err := c.Events(context.Background(), 4571234, []string{registry, escrow})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if latest != 4571402 {
		t.Errorf("latestLedger = %d, want 4571402", latest)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}

	inst := events[0]
	if inst.Type != "contract" {
		t.Errorf("event[0].type = %q, want contract", inst.Type)
	}
	if inst.ID != "0019633300532371456-0000000001" {
		t.Errorf("event[0].id = %q", inst.ID)
	}
	if inst.Ledger != 4571234 || inst.LedgerClosedAt != "2026-09-08T17:23:07Z" {
		t.Errorf("event[0] ledger = %d closedAt = %q", inst.Ledger, inst.LedgerClosedAt)
	}
	if inst.ContractID != registry {
		t.Errorf("event[0].contractId = %q, want %s", inst.ContractID, registry)
	}
	if len(inst.Topic) != 2 {
		t.Fatalf("event[0].topic = %d entries, want 2", len(inst.Topic))
	}
	// The XDR below is the real institution_verified capture recorded in
	// internal/indexer/decode_test.go — a schema change in the contract
	// event shape must fail here, not silently mis-index.
	if inst.Topic[0] != "AAAADwAAABRpbnN0aXR1dGlvbl92ZXJpZmllZA==" {
		t.Errorf("event[0].topic[0] = %q", inst.Topic[0])
	}
	if inst.Value != "AAAAEQAAAAEAAAABAAAADwAAAAt2ZXJpZmllZF9hdAAAAAAFAAAAAGqgHvs=" {
		t.Errorf("event[0].value = %q", inst.Value)
	}
	if inst.TxHash == "" {
		t.Error("event[0].txHash empty")
	}

	grant := events[1]
	if grant.ContractID != escrow {
		t.Errorf("event[1].contractId = %q, want %s", grant.ContractID, escrow)
	}
	if len(grant.Topic) != 4 || grant.Topic[0] != "AAAADwAAAA1ncmFudF9jcmVhdGVkAAAA" {
		t.Errorf("event[1].topic = %v", grant.Topic)
	}
}

func TestEventsEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"events":[],"latestLedger":4571402,"cursor":"0019633541050531840-0000000000"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL)

	events, latest, err := c.Events(context.Background(), 4571234, []string{registry, escrow})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want 0", len(events))
	}
	if latest != 4571402 {
		t.Errorf("latestLedger = %d, want 4571402", latest)
	}
}

func TestLatestLedgerReadsLatestLedgerField(t *testing.T) {
	c, f := serveFixture(t, "getLatestLedger.json")

	got, err := c.LatestLedger(context.Background())
	if err != nil {
		t.Fatalf("LatestLedger: %v", err)
	}
	if got != 4571402 {
		t.Errorf("latest = %d, want 4571402", got)
	}
	if req := decodeRequest(t, f.lastReq); req.Method != "getLatestLedger" {
		t.Errorf("method = %q, want getLatestLedger", req.Method)
	}
}

func TestLatestLedgerFallsBackToSequence(t *testing.T) {
	c, _ := serveFixture(t, "getLatestLedger_sequence.json")

	got, err := c.LatestLedger(context.Background())
	if err != nil {
		t.Fatalf("LatestLedger: %v", err)
	}
	if got != 4571402 {
		t.Errorf("latest = %d, want 4571402 (from sequence)", got)
	}
}

func TestEventsPropagatesRangeError(t *testing.T) {
	c, _ := serveFixture(t, "getEvents_range_error.json")

	_, _, err := c.Events(context.Background(), 4451041, []string{registry, escrow})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var re *RangeError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v (%T), want *RangeError", err, err)
	}
	if re.Oldest != 4451042 {
		t.Errorf("oldest = %d, want 4451042", re.Oldest)
	}
	if re.Newest != 4571402 {
		t.Errorf("newest = %d, want 4571402", re.Newest)
	}
}

func TestEventsPropagatesPlainRPCError(t *testing.T) {
	c, _ := serveFixture(t, "getEvents_plain_error.json")

	_, _, err := c.Events(context.Background(), 4571234, nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var re *RangeError
	if errors.As(err, &re) {
		t.Fatalf("error was wrongly converted to a RangeError: %v", err)
	}
	if want := "invalid filter"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

func TestEventsPropagatesMalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("<html>gateway timeout</html>"))
	}))
	defer srv.Close()
	c := New(srv.URL)

	if _, _, err := c.Events(context.Background(), 4571234, []string{registry}); err == nil {
		t.Fatal("expected a decode error for a non-JSON body, got nil")
	}
}

func TestEventsPropagatesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening anymore
	c := New(url)

	if _, _, err := c.Events(context.Background(), 4571234, []string{registry}); err == nil {
		t.Fatal("expected a transport error, got nil")
	}
}
