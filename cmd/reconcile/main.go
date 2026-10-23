// Command reconcile checks the indexed read model against authoritative
// contract state and repairs any drift.
//
// Run it after the indexer reports a skipped history gap (GET /api/indexer),
// or on a schedule as a safety net:
//
//	DATABASE_URL=... SOROBAN_RPC_URL=... REGISTRY_CONTRACT=... ESCROW_CONTRACT=... \
//	  go run ./cmd/reconcile
//
// Exits non-zero when drift was found, so a cron wrapper can alert on it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/tetedu/tuitio-backend/internal/chain"
	"github.com/tetedu/tuitio-backend/internal/config"
	"github.com/tetedu/tuitio-backend/internal/log"
	"github.com/tetedu/tuitio-backend/internal/reconcile"
	"github.com/tetedu/tuitio-backend/internal/store"
)

func main() {
	log.SetDefault(log.New(os.Getenv("LOG_FORMAT")))

	dryRun := flag.Bool("dry-run", false, "report drift without writing repairs")
	asJSON := flag.Bool("json", false, "emit the report as JSON")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Default().Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	st, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Default().Error("store connect failed", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	// Simulation needs an existing account to name as the transaction source;
	// it neither signs nor submits, so any funded address works.
	source := os.Getenv("RECONCILE_SOURCE")
	if source == "" {
		source = "GCPYAUYJNA3FQDJBEKCVHAD5TQYLNIZVNQHVVB4ZLK76B4K3EQPLBLEH"
	}

	reader := chain.NewReader(cfg.RpcURL, source)
	esc := chain.NewEscrow(reader, cfg.EscrowContract)
	reg := chain.NewRegistry(reader, cfg.RegistryContract)

	var repo reconcile.Repo = st
	if *dryRun {
		repo = readOnly{st}
	}

	report, err := reconcile.Run(ctx, esc, reg, repo)
	if err != nil {
		slog.Default().Error("reconcile failed", "error", err)
		os.Exit(1)
	}

	if *asJSON {
		out, _ := json.MarshalIndent(report, "", "  ")
		os.Stdout.Write(append(out, '\n'))
	} else {
		slog.Default().Info("reconcile complete", "report", report.String())
		for _, d := range report.Drift {
			slog.Default().Info("drift", "detail", d)
		}
	}

	if !*dryRun {
		if err := st.RecordReconcile(ctx, report.GrantsRepaired+report.TermsRepaired+report.InstitutionsRepaired); err != nil {
			slog.Default().Error("record reconcile", "error", err)
		}
	}
	if !report.Clean() {
		os.Exit(1)
	}
}

// readOnly turns the repairs into no-ops for --dry-run, so the same pass can
// report drift without touching the database.
type readOnly struct{ *store.Store }

func (readOnly) ReconcileGrant(context.Context, store.Grant) error             { return nil }
func (readOnly) ReconcileTerm(context.Context, store.Term) error               { return nil }
func (readOnly) ReconcileInstitution(context.Context, store.Institution) error { return nil }
