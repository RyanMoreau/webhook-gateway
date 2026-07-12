package router

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/backend"
	"github.com/ryanmoreau/webhook-gateway/internal/config"
	"github.com/ryanmoreau/webhook-gateway/internal/deadletter"
	"github.com/ryanmoreau/webhook-gateway/internal/idempotency"
	"github.com/ryanmoreau/webhook-gateway/internal/logging"
	"github.com/ryanmoreau/webhook-gateway/internal/signature"
	"github.com/ryanmoreau/webhook-gateway/internal/stats"
)

// Router matches incoming webhook requests to configured routes, verifies
// signatures, checks idempotency, and hands accepted events to a backend.
type Router struct {
	routes  []routeEntry
	idem    idempotency.Store
	backend backend.Adapter
	Stats   *stats.Counters
}

type routeEntry struct {
	cfg      config.RouteConfig
	verifier signature.Verifier
	secret   string
}

type Option func(*Router)

// WithBackend overrides the default delivery backend.
func WithBackend(b backend.Adapter) Option {
	return func(r *Router) {
		if b != nil {
			r.backend = b
		}
	}
}

// New creates a Router from the loaded config.
func New(cfg *config.Config, dlq deadletter.Store, idem idempotency.Store, opts ...Option) *Router {
	r := &Router{
		idem:  idem,
		Stats: stats.New(),
	}

	for _, opt := range opts {
		opt(r)
	}

	for _, rc := range cfg.Routes {
		var v signature.Verifier
		switch rc.Signature.Type {
		case "none":
			v = nil // no signature verification
		case "stripe":
			v = &signature.StripeVerifier{Tolerance: rc.Signature.Tolerance}
		default: // hmac-sha256
			v = &signature.HMACVerifier{
				Prefix:   rc.Signature.Prefix,
				Encoding: rc.Signature.Encoding,
			}
		}

		r.routes = append(r.routes, routeEntry{
			cfg:      rc,
			verifier: v,
			secret:   rc.Signature.SecretEnv, // already resolved to the actual secret
		})
	}

	if r.backend == nil {
		r.backend = backend.NewFanout(dlq, r.Stats, cfg.Server.ConcurrencyLimit)
	}

	return r
}

// WaitInFlight blocks until all in-flight deliveries complete or ctx expires.
func (r *Router) WaitInFlight(ctx context.Context) {
	if r.backend == nil {
		return
	}
	r.backend.WaitInFlight(ctx)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var matched *routeEntry
	for i := range r.routes {
		if r.routes[i].cfg.Path == req.URL.Path {
			matched = &r.routes[i]
			break
		}
	}
	if matched == nil {
		http.NotFound(w, req)
		return
	}

	// Assign request ID and detach background delivery from the request
	// cancellation so the backend can finish after we return 202.
	reqID := newRequestID()
	ctx := logging.WithRequestID(context.WithoutCancel(req.Context()), reqID)
	logger := slog.With("request_id", reqID, "route", matched.cfg.Path)

	// Read body once.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		logger.Error("reading request body", "error", err)
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	r.Stats.RequestsReceived.Add(1)

	// Verify signature (skip for routes with type: none).
	if matched.verifier != nil {
		sigHeader := req.Header.Get(matched.cfg.Signature.Header)
		if err := matched.verifier.Verify(sigHeader, matched.secret, body); err != nil {
			r.Stats.SignatureFailures.Add(1)
			logger.Warn("signature verification failed", "error", err)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	// Check idempotency (atomic check-and-set to avoid TOCTOU races).
	if matched.cfg.Idempotency.Enabled && r.idem != nil {
		eventID := extractKeyPath(body, matched.cfg.Idempotency.KeyPath)
		if eventID != "" {
			seen, err := r.idem.SeenOrMark(eventID, matched.cfg.Idempotency.TTL)
			if err != nil {
				logger.Warn("idempotency check failed", "error", err)
				// Continue delivery on error — don't block on dedup failures.
			} else if seen {
				r.Stats.DuplicatesSkipped.Add(1)
				logger.Info("duplicate event, skipping delivery", "event_id", eventID)
				w.WriteHeader(http.StatusOK)
				return
			}
		} else {
			logger.Warn("could not extract event ID from body, skipping deduplication",
				"key_path", matched.cfg.Idempotency.KeyPath)
		}
	}

	accepted, err := r.backend.Accept(ctx, backend.Event{
		RequestID:      reqID,
		RoutePath:      matched.cfg.Path,
		Body:           body,
		Headers:        req.Header.Clone(),
		ForwardHeaders: matched.cfg.ForwardHeaders,
		Destinations:   matched.cfg.Destinations,
		Retry:          matched.cfg.Retry,
		ReceivedAt:     time.Now().UTC(),
	})
	if err != nil {
		logger.Error("accepting event failed", "error", err)
		http.Error(w, "failed to accept event", http.StatusServiceUnavailable)
		return
	}

	logger.Info("event accepted",
		"receipt_id", accepted.ReceiptID,
		"accepted_at", accepted.AcceptedAt)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(accepted); err != nil {
		logger.Error("encoding acceptance response failed", "error", err)
	}
}

// extractKeyPath extracts a value from JSON using a dot-separated path.
// Returns "" if the body isn't valid JSON, the path doesn't exist, or the
// value is not a string. Arrays are not supported.
func extractKeyPath(body []byte, path string) string {
	if path == "" {
		return ""
	}

	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return ""
	}

	parts := strings.Split(path, ".")
	current := any(obj)

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = m[part]
		if !ok {
			return ""
		}
	}

	switch v := current.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func newRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
