// Command api runs the Tuitio indexer and REST API in one process.
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tetedu/tuitio-backend/internal/api"
	"github.com/tetedu/tuitio-backend/internal/config"
	"github.com/tetedu/tuitio-backend/internal/indexer"
	"github.com/tetedu/tuitio-backend/internal/log"
	"github.com/tetedu/tuitio-backend/internal/rpc"
	"github.com/tetedu/tuitio-backend/internal/store"
)

func main() {
	log.SetDefault(log.New(os.Getenv("LOG_FORMAT")))

	cfg, err := config.Load()
	if err != nil {
		slog.Default().Error("config load failed", "error", err)
		os.Exit(1)
	}
	log.LogConfig(context.Background(), cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Default().Error("store connect failed", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	client := rpc.New(cfg.RpcURL)
	ix := indexer.New(client, st,
		[]string{cfg.RegistryContract, cfg.EscrowContract},
		time.Duration(cfg.PollSeconds)*time.Second,
		cfg.StartLedger,
	)
	go ix.Run(ctx)

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, cfg.Port),
		Handler:           api.New(st).RoutesWithCORS(cfg.AllowedOrigins),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Default().Info("api listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Default().Error("api listen failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Default().Error("api shutdown", "error", err)
	}
	os.Exit(0)
}
