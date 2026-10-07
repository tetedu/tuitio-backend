package reconcile

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/tetedu/tuitio-backend/internal/chain"
	"github.com/tetedu/tuitio-backend/internal/store"
)

// TestLiveReconcileRebuildsFromChain points the reconciler at the deployed
// testnet contracts with a completely empty database and asserts it rebuilds
// the read model from contract state alone — the recovery path for an indexer
// that had to skip history. Guarded because it needs network access.
func TestLiveReconcileRebuildsFromChain(t *testing.T) {
	if os.Getenv("TUITION_LIVE") == "" {
		t.Skip("set TUITION_LIVE=1 to reconcile against Stellar testnet")
	}
	registry := envOr("REGISTRY_CONTRACT", "CD2INHSYNIQTVWM222CNSS7MN4MJSOBZOXVEWY3SOAK6MHICCZIEVQWV")
	escrow := envOr("ESCROW_CONTRACT", "CDV7FA3QJPBR7LORHCZCZG6UCRL7DMXVMZYRDLMW7EXUNBMN7SEXINXW")
	rpcURL := envOr("SOROBAN_RPC_URL", "https://soroban-testnet.stellar.org")
	source := envOr("RECONCILE_SOURCE", "GCPYAUYJNA3FQDJBEKCVHAD5TQYLNIZVNQHVVB4ZLK76B4K3EQPLBLEH")

	dir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			RuntimePath(dir).DataPath(dir + "/data").
			Username("tuitio").Password("tuitio").Database("tuitio").
			Port(9881).Logger(io.Discard),
	)
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	st, err := store.Connect(ctx, "postgres://tuitio:tuitio@localhost:9881/tuitio?sslmode=disable")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	reader := chain.NewReader(rpcURL, source)
	esc := chain.NewEscrow(reader, escrow)
	reg := chain.NewRegistry(reader, registry)

	// Pass one: empty database, everything must be rebuilt.
	rep, err := Run(ctx, esc, reg, st)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	t.Logf("pass one: %s", rep)
	if rep.GrantsChecked == 0 {
		t.Fatal("no grants read from the contract")
	}
	if rep.GrantsRepaired == 0 {
		t.Error("empty read model was not rebuilt")
	}

	grants, err := st.Grants(ctx, "", "", "")
	if err != nil {
		t.Fatalf("read grants: %v", err)
	}
	if len(grants) != rep.GrantsChecked {
		t.Errorf("rebuilt %d grants, chain has %d", len(grants), rep.GrantsChecked)
	}
	insts, err := st.Institutions(ctx)
	if err != nil {
		t.Fatalf("read institutions: %v", err)
	}
	if len(insts) == 0 {
		t.Error("no institutions rebuilt")
	}

	// Pass two must be a no-op: the model now matches the chain.
	rep2, err := Run(ctx, esc, reg, st)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	t.Logf("pass two: %s", rep2)
	if !rep2.Clean() {
		t.Errorf("reconciliation is not idempotent: %s / %v", rep2, rep2.Drift)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
