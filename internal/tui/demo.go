package tui

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/config"
	"github.com/ryanmoreau/webhook-gateway/internal/deadletter"
	"github.com/ryanmoreau/webhook-gateway/internal/logging"
	"github.com/ryanmoreau/webhook-gateway/internal/stats"
)

// DemoServer runs a fake gateway that serves mock stats, logs, and dead letters
// so the TUI can be previewed without a real gateway.
type DemoServer struct {
	listener net.Listener
	logBuf   *logging.RingBuffer
	counters *stats.Counters
	dlDir    string
	cfg      *config.Config
	stop     chan struct{}
}

// StartDemo creates mock data and starts a local HTTP server.
// Returns the server, gateway URL, config, and dead letter directory.
func StartDemo() (*DemoServer, string, *config.Config, string, error) {
	// Create temp dir for dead letters.
	dlDir, err := os.MkdirTemp("", "gateway-demo-dl-*")
	if err != nil {
		return nil, "", nil, "", fmt.Errorf("creating temp dir: %w", err)
	}

	// Write sample dead letter files.
	writeSampleDeadLetters(dlDir)

	// Build a demo config.
	cfg := demoConfig()

	// Set up counters with realistic numbers.
	counters := stats.New()
	counters.RequestsReceived.Store(12847)
	counters.SignatureFailures.Store(12)
	counters.DuplicatesSkipped.Store(341)
	counters.DeliveriesAttempted.Store(25694)
	counters.DeliveriesSucceeded.Store(25601)
	counters.DeliveriesFailed.Store(93)
	counters.DeadLettersWritten.Store(8)
	counters.StartedAt = time.Now().Add(-4*time.Hour - 23*time.Minute)

	// Set up log ring buffer with sample entries.
	logBuf := logging.NewRingBuffer(1000)
	seedLogs(logBuf)

	// Start HTTP server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.RemoveAll(dlDir)
		return nil, "", nil, "", fmt.Errorf("starting demo server: %w", err)
	}

	ds := &DemoServer{
		listener: ln,
		logBuf:   logBuf,
		counters: counters,
		dlDir:    dlDir,
		cfg:      cfg,
		stop:     make(chan struct{}),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"stats":  counters.Snapshot(),
		})
	})

	mux.HandleFunc("GET /logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(logBuf.Recent(200))
	})

	mux.HandleFunc("GET /logs/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher.Flush()

		ch := logBuf.Subscribe()
		defer logBuf.Unsubscribe(ch)

		for {
			select {
			case <-r.Context().Done():
				return
			case entry := <-ch:
				data, _ := logging.MarshalEntry(entry)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	})

	go http.Serve(ln, mux)

	// Background goroutine: simulate live activity.
	go ds.simulateActivity()

	url := fmt.Sprintf("http://%s", ln.Addr().String())
	return ds, url, cfg, dlDir, nil
}

// Cleanup removes temp files and stops the server.
func (ds *DemoServer) Cleanup() {
	close(ds.stop)
	ds.listener.Close()
	os.RemoveAll(ds.dlDir)
}

func (ds *DemoServer) simulateActivity() {
	routes := []string{"/hooks/github", "/hooks/stripe", "/hooks/gitlab", "/hooks/orders"}
	messages := []struct {
		level string
		msg   string
	}{
		{"INFO", "delivery succeeded"},
		{"INFO", "delivery succeeded"},
		{"INFO", "delivery succeeded"},
		{"INFO", "request received"},
		{"INFO", "request received"},
		{"WARN", "signature verification failed"},
		{"DEBUG", "idempotency check passed"},
		{"INFO", "delivery succeeded"},
		{"ERROR", "delivery failed"},
		{"INFO", "duplicate event, skipping delivery"},
	}
	destinations := []string{
		"https://deploy.flowcheck.co/webhook",
		"https://api.flowcheck.co/hooks/billing",
		"https://slack-bridge.flowcheck.co/github",
		"https://inventory.flowcheck.co/webhook",
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ds.stop:
			return
		case <-ticker.C:
			// Bump counters.
			ds.counters.RequestsReceived.Add(int64(rand.IntN(3) + 1))
			ds.counters.DeliveriesAttempted.Add(int64(rand.IntN(5) + 1))
			ds.counters.DeliveriesSucceeded.Add(int64(rand.IntN(5) + 1))

			// Push a log entry.
			m := messages[rand.IntN(len(messages))]
			attrs := map[string]string{
				"request_id": fmt.Sprintf("%08x", rand.Uint32()),
				"route":      routes[rand.IntN(len(routes))],
			}
			if m.msg == "delivery succeeded" || m.msg == "delivery failed" {
				attrs["destination"] = destinations[rand.IntN(len(destinations))]
			}
			ds.logBuf.Push(logging.LogEntry{
				Time:    time.Now(),
				Level:   m.level,
				Message: m.msg,
				Attrs:   attrs,
			})
		}
	}
}

func writeSampleDeadLetters(dir string) {
	samples := []deadletter.Entry{
		{
			RequestID:      "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			Timestamp:      time.Now().Add(-2 * time.Hour),
			RoutePath:      "/hooks/github",
			DestinationURL: "https://deploy.flowcheck.co/webhook",
			RequestBody:    []byte(`{"action":"push","ref":"refs/heads/main","repository":{"full_name":"flowcheck-qr/web"}}`),
			Headers:        map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": "a1b2c3d4"},
			ErrorMessage:   "delivering to https://deploy.flowcheck.co/webhook: connection refused",
			AttemptCount:   3,
		},
		{
			RequestID:      "b2c3d4e5-f6a7-8901-bcde-f12345678901",
			Timestamp:      time.Now().Add(-1*time.Hour - 30*time.Minute),
			RoutePath:      "/hooks/stripe",
			DestinationURL: "https://api.flowcheck.co/hooks/billing",
			RequestBody:    []byte(`{"id":"evt_1234567890","type":"invoice.paid","data":{"object":{"amount_paid":4900,"customer":"cus_abc123"}}}`),
			Headers:        map[string]string{"Stripe-Signature": "t=1234567890,v1=abc123"},
			ErrorMessage:   "destination returned 503",
			AttemptCount:   5,
		},
		{
			RequestID:      "c3d4e5f6-a7b8-9012-cdef-123456789012",
			Timestamp:      time.Now().Add(-45 * time.Minute),
			RoutePath:      "/hooks/github",
			DestinationURL: "https://slack-bridge.flowcheck.co/github",
			RequestBody:    []byte(`{"action":"opened","pull_request":{"title":"feat: add export endpoint","number":42}}`),
			Headers:        map[string]string{"X-GitHub-Event": "pull_request", "X-GitHub-Delivery": "c3d4e5f6"},
			ErrorMessage:   "delivering to https://slack-bridge.flowcheck.co/github: i/o timeout",
			AttemptCount:   3,
		},
		{
			RequestID:      "d4e5f6a7-b8c9-0123-defa-234567890123",
			Timestamp:      time.Now().Add(-20 * time.Minute),
			RoutePath:      "/hooks/orders",
			DestinationURL: "https://inventory.flowcheck.co/webhook",
			RequestBody:    []byte(`{"data":{"order":{"id":"ord_789","items":[{"sku":"FC-QR-100","qty":2}],"total":9800}}}`),
			Headers:        map[string]string{"X-Event-Type": "order.created", "X-Signature": "sha256=deadbeef"},
			ErrorMessage:   "destination returned 500",
			AttemptCount:   4,
		},
		{
			RequestID:      "e5f6a7b8-c9d0-1234-efab-345678901234",
			Timestamp:      time.Now().Add(-8 * time.Minute),
			RoutePath:      "/hooks/stripe",
			DestinationURL: "https://api.flowcheck.co/hooks/billing",
			RequestBody:    []byte(`{"id":"evt_0987654321","type":"customer.subscription.updated","data":{"object":{"id":"sub_xyz","status":"active"}}}`),
			Headers:        map[string]string{"Stripe-Signature": "t=9876543210,v1=def456"},
			ErrorMessage:   "delivering to https://api.flowcheck.co/hooks/billing: connection reset by peer",
			AttemptCount:   5,
		},
	}

	store, _ := deadletter.NewFileStore(dir, true, 0)
	for _, e := range samples {
		store.Save(e)
	}
}

func seedLogs(buf *logging.RingBuffer) {
	base := time.Now().Add(-10 * time.Minute)
	logs := []struct {
		offset time.Duration
		level  string
		msg    string
		attrs  map[string]string
	}{
		{0, "INFO", "server starting", map[string]string{"addr": ":8080"}},
		{1 * time.Second, "INFO", "notify endpoint enabled", map[string]string{"channels": "4"}},
		{5 * time.Second, "INFO", "request received", map[string]string{"request_id": "f7a8b9c0", "route": "/hooks/github"}},
		{5 * time.Second, "INFO", "delivery succeeded", map[string]string{"request_id": "f7a8b9c0", "route": "/hooks/github", "destination": "https://deploy.flowcheck.co/webhook"}},
		{5 * time.Second, "INFO", "delivery succeeded", map[string]string{"request_id": "f7a8b9c0", "route": "/hooks/github", "destination": "https://slack-bridge.flowcheck.co/github"}},
		{12 * time.Second, "INFO", "request received", map[string]string{"request_id": "a1b2c3d4", "route": "/hooks/stripe"}},
		{12 * time.Second, "INFO", "duplicate event, skipping delivery", map[string]string{"request_id": "a1b2c3d4", "route": "/hooks/stripe", "event_id": "evt_1234567890"}},
		{30 * time.Second, "INFO", "request received", map[string]string{"request_id": "b2c3d4e5", "route": "/hooks/github"}},
		{31 * time.Second, "WARN", "signature verification failed", map[string]string{"request_id": "b2c3d4e5", "route": "/hooks/github"}},
		{45 * time.Second, "INFO", "request received", map[string]string{"request_id": "c3d4e5f6", "route": "/hooks/orders"}},
		{45 * time.Second, "INFO", "delivery succeeded", map[string]string{"request_id": "c3d4e5f6", "route": "/hooks/orders", "destination": "https://inventory.flowcheck.co/webhook"}},
		{45 * time.Second, "INFO", "delivery succeeded", map[string]string{"request_id": "c3d4e5f6", "route": "/hooks/orders", "destination": "https://shipping.flowcheck.co/webhook"}},
		{46 * time.Second, "ERROR", "delivery failed", map[string]string{"request_id": "c3d4e5f6", "route": "/hooks/orders", "destination": "https://analytics.flowcheck.co/events", "error": "connection refused", "attempts": "4"}},
		{46 * time.Second, "ERROR", "saving dead letter", map[string]string{"request_id": "c3d4e5f6"}},
		{60 * time.Second, "INFO", "request received", map[string]string{"request_id": "d4e5f6a7", "route": "/hooks/stripe"}},
		{60 * time.Second, "INFO", "delivery succeeded", map[string]string{"request_id": "d4e5f6a7", "route": "/hooks/stripe", "destination": "https://api.flowcheck.co/hooks/billing"}},
		{90 * time.Second, "DEBUG", "idempotency check passed", map[string]string{"request_id": "e5f6a7b8", "route": "/hooks/stripe", "event_id": "evt_new_event"}},
		{90 * time.Second, "INFO", "delivery succeeded", map[string]string{"request_id": "e5f6a7b8", "route": "/hooks/stripe", "destination": "https://api.flowcheck.co/hooks/billing"}},
	}

	for _, l := range logs {
		buf.Push(logging.LogEntry{
			Time:    base.Add(l.offset),
			Level:   l.level,
			Message: l.msg,
			Attrs:   l.attrs,
		})
	}
}

func demoConfig() *config.Config {
	t := true
	return &config.Config{
		Server: config.ServerConfig{
			Port:             8080,
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     30 * time.Second,
			MaxBodySize:      1 << 20,
			ConcurrencyLimit: 100,
		},
		Routes: []config.RouteConfig{
			{
				Path: "/hooks/github",
				Signature: config.SignatureConfig{
					Type:      "hmac-sha256",
					Header:    "X-Hub-Signature-256",
					SecretEnv: "GITHUB_WEBHOOK_SECRET",
					Prefix:    "sha256=",
					Encoding:  "hex",
				},
				Destinations: []config.DestConfig{
					{URL: "https://deploy.flowcheck.co/webhook", Timeout: 10 * time.Second},
					{URL: "https://slack-bridge.flowcheck.co/github", Timeout: 5 * time.Second},
				},
				ForwardHeaders: []string{"X-GitHub-Event", "X-GitHub-Delivery"},
				Retry: config.RetryConfig{
					MaxAttempts:     3,
					Backoff:         "exponential",
					InitialInterval: 1 * time.Second,
					MaxInterval:     30 * time.Second,
				},
			},
			{
				Path: "/hooks/stripe",
				Signature: config.SignatureConfig{
					Type:      "stripe",
					Header:    "Stripe-Signature",
					SecretEnv: "STRIPE_WEBHOOK_SECRET",
					Tolerance: 300 * time.Second,
				},
				Idempotency: config.IdempotencyConfig{
					Enabled: true,
					TTL:     24 * time.Hour,
					KeyPath: "id",
				},
				Destinations: []config.DestConfig{
					{URL: "https://api.flowcheck.co/hooks/billing", Timeout: 10 * time.Second},
				},
				ForwardHeaders: []string{"Stripe-Signature"},
				Retry: config.RetryConfig{
					MaxAttempts:     5,
					Backoff:         "exponential",
					InitialInterval: 2 * time.Second,
					MaxInterval:     60 * time.Second,
				},
			},
			{
				Path: "/hooks/orders",
				Signature: config.SignatureConfig{
					Type:      "hmac-sha256",
					Header:    "X-Signature",
					SecretEnv: "ORDERS_WEBHOOK_SECRET",
					Encoding:  "hex",
				},
				Idempotency: config.IdempotencyConfig{
					Enabled: true,
					TTL:     12 * time.Hour,
					KeyPath: "data.order.id",
				},
				Destinations: []config.DestConfig{
					{URL: "https://inventory.flowcheck.co/webhook", Timeout: 10 * time.Second},
					{URL: "https://shipping.flowcheck.co/webhook", Timeout: 10 * time.Second},
					{URL: "https://analytics.flowcheck.co/events", Timeout: 5 * time.Second},
				},
				ForwardHeaders: []string{"X-Event-Type"},
				Retry: config.RetryConfig{
					MaxAttempts:     4,
					Backoff:         "exponential",
					InitialInterval: 500 * time.Millisecond,
					MaxInterval:     15 * time.Second,
				},
			},
		},
		DeadLetter: config.DeadLetterConfig{
			Type:      "file",
			StoreBody: &t,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}
