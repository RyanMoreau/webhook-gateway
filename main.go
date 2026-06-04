package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/config"
	"github.com/ryanmoreau/webhook-gateway/internal/deadletter"
	"github.com/ryanmoreau/webhook-gateway/internal/delivery"
	"github.com/ryanmoreau/webhook-gateway/internal/idempotency"
	"github.com/ryanmoreau/webhook-gateway/internal/logging"
	"github.com/ryanmoreau/webhook-gateway/internal/notify"
	"github.com/ryanmoreau/webhook-gateway/internal/router"
	"github.com/ryanmoreau/webhook-gateway/internal/server"
)

func main() {
	configPath := flag.String("config", "./config.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logging.Setup(cfg.Logging.Level, cfg.Logging.Format)

	delivery.SetClient(delivery.NewClient(cfg.Server.AllowInsecure))

	// Initialize idempotency store.
	idemStore := idempotency.NewMemoryStore(1 * time.Minute)
	defer idemStore.Close()

	// Initialize dead letter store.
	storeBody := true
	if cfg.DeadLetter.StoreBody != nil {
		storeBody = *cfg.DeadLetter.StoreBody
	}
	dlPath := cfg.DeadLetter.Path
	if dlPath == "" {
		dlPath = "./dead_letters"
	}
	dlStore, err := deadletter.NewFileStore(dlPath, storeBody, cfg.DeadLetter.MaxBodyBytes)
	if err != nil {
		slog.Error("initializing dead letter store", "error", err)
		os.Exit(1)
	}

	// Build router.
	r := router.New(cfg, dlStore, idemStore)

	// Build notify handler (optional — works without providers configured).
	var notifyHandler server.NotifyHandler
	var notifyStatsFn func() map[string]int64

	nh, err := notify.NewHandler(cfg.Notify)
	if err != nil {
		slog.Error("initializing notify handler", "error", err)
		os.Exit(1)
	}
	if nh != nil {
		notifyHandler = nh
		notifyStatsFn = func() map[string]int64 { return nh.Stats().Snapshot() }
		slog.Info("notify endpoint enabled", "channels", len(cfg.Notify.Channels))
	} else {
		slog.Info("notify endpoint disabled (no providers or channels configured)")
	}

	// Build and start server.
	srv := server.New(server.Config{
		Port:         cfg.Server.Port,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		MaxBodySize:  cfg.Server.MaxBodySize,
	}, r, r, r.Stats, notifyHandler, notifyStatsFn)

	if err := srv.ListenAndServe(30 * time.Second); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
