// Command api runs the Tuitio indexer and REST API in one process.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tetedu/tuitio-backend/internal/api"
	"github.com/tetedu/tuitio-backend/internal/chain"
	"github.com/tetedu/tuitio-backend/internal/config"
	"github.com/tetedu/tuitio-backend/internal/indexer"
	"github.com/tetedu/tuitio-backend/internal/reconcile"
	"github.com/tetedu/tuitio-backend/internal/rpc"
	"github.com/tetedu/tuitio-backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	client := rpc.New(cfg.RpcURL)
	ix := indexer.New(client, st,
		[]string{cfg.RegistryContract, cfg.EscrowContract},
		time.Duration(cfg.PollSeconds)*time.Second,
		cfg.StartLedger,
	)

	// Simulation needs an existing account to name as the transaction source;
	// it neither signs nor submits, so any funded address works.
	reconcileSource := os.Getenv("RECONCILE_SOURCE")
	if reconcileSource == "" {
		reconcileSource = "GCPYAUYJNA3FQDJBEKCVHAD5TQYLNIZVNQHVVB4ZLK76B4K3EQPLBLEH"
	}
	reader := chain.NewReader(cfg.RpcURL, reconcileSource)
	esc := chain.NewEscrow(reader, cfg.EscrowContract)
	reg := chain.NewRegistry(reader, cfg.RegistryContract)

	// Run a reconciliation pass automatically when the indexer detects a skipped
	// history gap, and (optionally) on a periodic safety-net cadence. Passes
	// never overlap: concurrent triggers are coalesced.
	var repo reconcile.Repo = st
	super := reconcile.NewSupervisor(
		func(ctx context.Context) (reconcile.Report, error) {
			return reconcile.Run(ctx, esc, reg, repo)
		},
		func(ctx context.Context, repaired int) error {
			return st.RecordReconcile(ctx, repaired)
		},
	)
	ix.SetGapHandler(func(ctx context.Context) { super.Trigger(ctx) })
	super.StartPeriodic(ctx, time.Duration(cfg.ReconcileIntervalSeconds)*time.Second)

	go ix.Run(ctx)

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, cfg.Port),
		Handler:           api.New(st).RoutesWithCORS(cfg.AllowedOrigins),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("api: listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	os.Exit(0)
}
