// Command devstack runs the entire Tuitio backend against Stellar testnet
// with a throwaway embedded Postgres. Intended for local development and
// demos where provisioning a database is unwanted friction.
//
// The Postgres binaries download to a cache dir on first run and are reused.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/adelekevictor12/tuitio-backend/internal/api"
	"github.com/adelekevictor12/tuitio-backend/internal/indexer"
	"github.com/adelekevictor12/tuitio-backend/internal/rpc"
	"github.com/adelekevictor12/tuitio-backend/internal/store"
)

const (
	defaultPort = 54329
	dataDir     = ".devstack"
)

func main() {
	rpcURL := getenv("SOROBAN_RPC_URL", "https://soroban-testnet.stellar.org")
	registry := getenv("REGISTRY_CONTRACT", "CD2INHSYNIQTVWM222CNSS7MN4MJSOBZOXVEWY3SOAK6MHICCZIEVQWV")
	escrow := getenv("ESCROW_CONTRACT", "CDV7FA3QJPBR7LORHCZCZG6UCRL7DMXVMZYRDLMW7EXUNBMN7SEXINXW")
	apiPort := getenv("PORT", "8080")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", dataDir, err)
	}
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			RuntimePath(dataDir).
			DataPath(dataDir + "/data").
			Username("tuitio").Password("tuitio").Database("tuitio").
			Port(defaultPort),
	)
	if err := pg.Start(); err != nil {
		log.Fatalf("start postgres: %v", err)
	}
	defer func() {
		if err := pg.Stop(); err != nil {
			log.Printf("stop postgres: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbURL := "postgres://tuitio:tuitio@localhost:54329/tuitio?sslmode=disable"
	st, err := store.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// Backfill from a recent ledger unless told otherwise.
	startLedger := parseLedger(getenv("START_LEDGER", "0"))
	ix := indexer.New(rpc.New(rpcURL), st, []string{registry, escrow}, 10*time.Second, startLedger)
	go ix.Run(ctx)

	srv := &http.Server{
		Addr:              ":" + apiPort,
		Handler:           api.New(st).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("devstack: api on :%s, postgres on :%d", apiPort, defaultPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
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
