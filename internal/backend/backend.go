package backend

import (
	"context"
	"net/http"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/config"
)

// Event is the normalized webhook payload handed to a delivery backend.
type Event struct {
	RequestID      string
	RoutePath      string
	Body           []byte
	Headers        http.Header
	ForwardHeaders []string
	Destinations   []config.DestConfig
	Retry          config.RetryConfig
	ReceivedAt     time.Time
}

// Acceptance is the backend's durable acknowledgement for a webhook event.
type Acceptance struct {
	ReceiptID  string    `json:"receipt_id"`
	AcceptedAt time.Time `json:"accepted_at"`
	Status     string    `json:"status"`
}

// Adapter accepts a normalized event and returns an acknowledgement once the
// backend has taken responsibility for it.
type Adapter interface {
	Accept(ctx context.Context, event Event) (Acceptance, error)
	WaitInFlight(ctx context.Context)
}
