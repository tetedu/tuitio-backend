package indexer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/tetedu/tuitio-backend/internal/api"
	"github.com/tetedu/tuitio-backend/internal/rpc"
	"github.com/tetedu/tuitio-backend/internal/store"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// TestLiveIngestion runs the whole pipeline against Stellar testnet: RPC
// polling, XDR decoding, Postgres apply, and the HTTP API. Guarded by
// TUITION_LIVE=1 because it needs network access and a deployed demo state.
func TestLiveIngestion(t *testing.T) {
	if os.Getenv("TUITION_LIVE") == "" {
		t.Skip("set TUITION_LIVE=1 to run the live testnet ingestion test")
	}

	rpcURL := os.Getenv("SOROBAN_RPC_URL")
	if rpcURL == "" {
		rpcURL = "https://soroban-testnet.stellar.org"
	}
	registry := os.Getenv("REGISTRY_CONTRACT")
	escrow := os.Getenv("ESCROW_CONTRACT")
	start := os.Getenv("START_LEDGER")
	if registry == "" || escrow == "" || start == "" {
		t.Fatal("REGISTRY_CONTRACT, ESCROW_CONTRACT and START_LEDGER must be set")
	}

	dir := t.TempDir()
	cfg := embeddedpostgres.DefaultConfig().
		RuntimePath(dir).DataPath(dir + "/data").
		Username("tuitio").Password("tuitio").Database("tuitio").
		Port(9877).Logger(nil)
	pg := embeddedpostgres.NewDatabase(cfg)
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.Connect(ctx, "postgres://tuitio:tuitio@localhost:9877/tuitio?sslmode=disable")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	var startLedger uint32
	for _, c := range start {
		startLedger = startLedger*10 + uint32(c-'0')
	}
	ix := New(rpc.New(rpcURL), st, []string{registry, escrow}, 2*time.Second, startLedger)
	go ix.Run(ctx)

	// Give the indexer a few polls to ingest the demo events.
	deadline := time.Now().Add(30 * time.Second)
	var grants []store.Grant
	for time.Now().Before(deadline) {
		grants, err = st.Grants(ctx, "", "", "")
		if err == nil && len(grants) >= 2 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if len(grants) < 2 {
		t.Fatalf("expected 2 grants from testnet, got %d (err=%v)", len(grants), err)
	}

	srv := httptest.NewServer(api.New(st).Routes())
	defer srv.Close()

	// The API must serve grant 1 with its released term and the verified institution.
	resp := getJSON(t, srv.URL+"/api/grants/1")
	var g store.Grant
	if err := json.Unmarshal(resp, &g); err != nil {
		t.Fatalf("grant json: %v", err)
	}
	if g.TermsTotal != 2 || g.NextTerm != 1 {
		t.Errorf("grant 1 = %+v", g)
	}
	if g.LockedAmount != 500000000 {
		t.Errorf("grant 1 locked = %d, want one remaining term", g.LockedAmount)
	}

	resp = getJSON(t, srv.URL+"/api/grants/1/terms")
	var terms []store.Term
	if err := json.Unmarshal(resp, &terms); err != nil {
		t.Fatalf("terms json: %v", err)
	}
	if len(terms) != 2 || terms[0].Status != "released" || terms[1].Status != "pending" {
		t.Errorf("grant 1 terms = %+v", terms)
	}

	resp = getJSON(t, srv.URL+"/api/institutions")
	var insts []store.Institution
	if err := json.Unmarshal(resp, &insts); err != nil {
		t.Fatalf("institutions json: %v", err)
	}
	if len(insts) == 0 || insts[0].Status != "verified" {
		t.Errorf("institutions = %+v", insts)
	}

	t.Logf("live pipeline ok: %d grants, %d institutions", len(grants), len(insts))
}

func getJSON(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read: %v", url, err)
	}
	return body
}
