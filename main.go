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
	"github.com/ryanmoreau/webhook-gateway/internal/tui"
)

func main() {
	// Handle "tui" subcommand before default flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "tui" {
		runTUI()
		return
	}

	configPath := flag.String("config", "./config.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logBuf := logging.Setup(cfg.Logging.Level, cfg.Logging.Format)

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
	}, r, r, r.Stats, notifyHandler, notifyStatsFn, logBuf)

	if err := srv.ListenAndServe(30 * time.Second); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// findConfig checks common locations for a config file.
func findConfig() string {
	candidates := []string{
		"./config.yaml",
		"./config.yml",
		"/etc/webhook-gateway/config.yaml",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func runTUI() {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	configPath := fs.String("config", "", "path to configuration file")
	gatewayURL := fs.String("gateway", "http://localhost:8080", "base URL of the running gateway")
	dlDir := fs.String("dead-letters", "", "dead letter directory (defaults to config value)")
	demo := fs.Bool("demo", false, "run with mock data (no real gateway needed)")
	fs.Parse(os.Args[2:])

	if *demo {
		runDemo()
		return
	}

	// Auto-discover config if not specified.
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = findConfig()
	}

	// Config is optional — TUI works without it (routes tab will be empty).
	var cfg *config.Config
	if cfgPath != "" {
		c, err := config.LoadReadOnly(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load config: %v\n", err)
		} else {
			cfg = c
		}
	}

	dir := *dlDir
	if dir == "" && cfg != nil {
		dir = cfg.DeadLetter.Path
	}
	if dir == "" {
		dir = "./dead_letters"
	}

	if err := tui.Run(tui.Options{
		GatewayURL: *gatewayURL,
		Config:     cfg,
		DLDir:      dir,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runDemo() {
	ds, url, cfg, dlDir, err := tui.StartDemo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error starting demo: %v\n", err)
		os.Exit(1)
	}
	defer ds.Cleanup()

	if err := tui.Run(tui.Options{
		GatewayURL: url,
		Config:     cfg,
		DLDir:      dlDir,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
