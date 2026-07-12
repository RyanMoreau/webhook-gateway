package backend

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/deadletter"
	"github.com/ryanmoreau/webhook-gateway/internal/delivery"
	"github.com/ryanmoreau/webhook-gateway/internal/stats"
)

// FanoutBackend keeps the current in-process delivery behavior behind the
// adapter interface so consumers can swap in durable backends later.
type FanoutBackend struct {
	dlq   deadletter.Store
	stats *stats.Counters
	sem   chan struct{}

	mu       sync.Mutex
	inFlight sync.WaitGroup
}

// NewFanout creates the default adapter used by the OSS gateway.
func NewFanout(dlq deadletter.Store, counters *stats.Counters, concurrencyLimit int) *FanoutBackend {
	var sem chan struct{}
	if concurrencyLimit > 0 {
		sem = make(chan struct{}, concurrencyLimit)
	}
	return &FanoutBackend{dlq: dlq, stats: counters, sem: sem}
}

func (b *FanoutBackend) Accept(ctx context.Context, event Event) (Acceptance, error) {
	acceptedAt := time.Now().UTC()
	receiptID := event.RequestID

	b.mu.Lock()
	b.inFlight.Add(1)
	if b.stats != nil {
		b.stats.InFlight.Add(1)
	}
	b.mu.Unlock()

	go func() {
		defer b.inFlight.Done()
		if b.stats != nil {
			defer b.stats.InFlight.Add(-1)
		}
		b.deliver(ctx, event)
	}()

	return Acceptance{
		ReceiptID:  receiptID,
		AcceptedAt: acceptedAt,
		Status:     "accepted",
	}, nil
}

func (b *FanoutBackend) WaitInFlight(ctx context.Context) {
	b.mu.Lock()
	done := make(chan struct{})
	go func() {
		b.inFlight.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
	b.mu.Unlock()
}

func (b *FanoutBackend) deliver(ctx context.Context, event Event) {
	retryCfg := delivery.RetryConfig{
		MaxAttempts:     event.Retry.MaxAttempts,
		InitialInterval: event.Retry.InitialInterval,
		MaxInterval:     event.Retry.MaxInterval,
	}

	headers := buildForwardHeaders(event.Headers, event.ForwardHeaders, event.RequestID)
	var wg sync.WaitGroup

	for _, d := range event.Destinations {
		dest := delivery.Destination{
			URL:     d.URL,
			Timeout: d.Timeout,
			Headers: d.Headers,
		}

		if b.sem != nil {
			b.sem <- struct{}{}
		}

		wg.Add(1)
		go func(dest delivery.Destination) {
			defer wg.Done()
			if b.sem != nil {
				defer func() { <-b.sem }()
			}

			if b.stats != nil {
				b.stats.DeliveriesAttempted.Add(1)
			}

			err := delivery.WithRetry(ctx, retryCfg, func() error {
				return delivery.Deliver(ctx, dest, headers, event.Body)
			})
			if err != nil {
				if b.stats != nil {
					b.stats.DeliveriesFailed.Add(1)
				}

				slog.Error("delivery failed",
					"request_id", event.RequestID,
					"route", event.RoutePath,
					"destination", dest.URL,
					"error", err,
					"attempts", retryCfg.MaxAttempts)

				if b.dlq != nil {
					dlEntry := deadletter.Entry{
						RequestID:      event.RequestID,
						Timestamp:      time.Now().UTC(),
						RoutePath:      event.RoutePath,
						DestinationURL: dest.URL,
						RequestBody:    event.Body,
						Headers:        flattenHeaders(headers),
						ErrorMessage:   err.Error(),
						AttemptCount:   retryCfg.MaxAttempts,
					}
					if dlErr := b.dlq.Save(dlEntry); dlErr != nil {
						slog.Error("saving dead letter", "request_id", event.RequestID, "error", dlErr)
					} else if b.stats != nil {
						b.stats.DeadLettersWritten.Add(1)
					}
				}
				return
			}

			if b.stats != nil {
				b.stats.DeliveriesSucceeded.Add(1)
			}
			slog.Info("delivery succeeded",
				"request_id", event.RequestID,
				"route", event.RoutePath,
				"destination", dest.URL)
		}(dest)
	}

	wg.Wait()
}

// buildForwardHeaders constructs the header set to forward to destinations.
// Always includes Content-Type and X-Webhook-Gateway-Request-Id.
// Includes any headers from the allowlist. Strips hop-by-hop and Host.
func buildForwardHeaders(src http.Header, allowlist []string, reqID string) http.Header {
	h := make(http.Header)

	if ct := src.Get("Content-Type"); ct != "" {
		h.Set("Content-Type", ct)
	}

	for _, name := range allowlist {
		if hopByHopHeaders[http.CanonicalHeaderKey(name)] {
			continue
		}
		if val := src.Get(name); val != "" {
			h.Set(name, val)
		}
	}

	h.Set("X-Webhook-Gateway-Request-Id", reqID)
	return h
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) == 0 {
			continue
		}
		out[k] = vals[0]
	}
	return out
}

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

var _ Adapter = (*FanoutBackend)(nil)
