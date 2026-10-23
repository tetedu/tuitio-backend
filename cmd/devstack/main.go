// Command devstack runs the entire Tuitio backend against Stellar testnet
// with a throwaway embedded Postgres. Intended for local development and
// demos where provisioning a database is unwanted friction.
//
// The Postgres binaries download to a cache dir on first run and are reused.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/tetedu/tuitio-backend/internal/api"
	"github.com/tetedu/tuitio-backend/internal/indexer"
	"github.com/tetedu/tuitio-backend/internal/log"
	"github.com/tetedu/tuitio-backend/internal/rpc"
	"github.com/tetedu/tuitio-backend/internal/store"
)

const (
	defaultPort = 54329
	dataDir     = ".devstack"
)

func main() {
	log.SetDefault(log.New(os.Getenv("LOG_FORMAT")))

	rpcURL := getenv("SOROBAN_RPC_URL", "https://soroban-testnet.stellar.org")
	registry := getenv("REGISTRY_CONTRACT", "CCTZVSPMIYEA4X2Z6UP5IPIO2HBIZWHLV37OUDBV32YUCP64JNIPBCPO")
	escrow := getenv("ESCROW_CONTRACT", "CDPGTHO2O7LTURTJF7L7ZZZTZCXPNABAPGVRDMBYIJ737R6O3DOPWJ34")
	apiPort := getenv("PORT", "8080")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Default().Error("mkdir failed", "dir", dataDir, "error", err)
		os.Exit(1)
	}
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			RuntimePath(dataDir).
			DataPath(dataDir + "/data").
			Username("tuitio").Password("tuitio").Database("tuitio").
			Port(defaultPort),
	)
	if err := pg.Start(); err != nil {
		slog.Default().Error("start postgres failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := pg.Stop(); err != nil {
			slog.Default().Error("stop postgres", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbURL := "postgres://tuitio:tuitio@localhost:54329/tuitio?sslmode=disable"
	st, err := store.Connect(ctx, dbURL)
	if err != nil {
		slog.Default().Error("store connect failed", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	// Backfill from a recent ledger unless told otherwise.
	startLedger := parseLedger(getenv("START_LEDGER", "0"))
	ix := indexer.New(rpc.New(rpcURL), st, []string{registry, escrow}, 10*time.Second, startLedger)
	go ix.Run(ctx)

	origins := strings.Split(getenv("ALLOWED_ORIGINS", ""), ",")
	host := getenv("HOST", "0.0.0.0")
	srv := &http.Server{
		Addr:              net.JoinHostPort(host, apiPort),
		Handler:           api.New(st).RoutesWithCORS(origins),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Default().Info("devstack api up", "port", apiPort, "postgres_port", defaultPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Default().Error("listen failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLedger(raw string) uint32 {
	var n uint32
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + uint32(c-'0')
	}
	return n
}
