package delivery

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxAttempts     int
	InitialInterval time.Duration
	MaxInterval     time.Duration
}

// WithRetry calls fn up to cfg.MaxAttempts times with exponential backoff
// and jitter. It stops immediately on non-retryable errors or context
// cancellation. Returns the last error on exhaustion.
func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsRetryable(lastErr) {
			return lastErr
		}

		// Don't sleep after the last attempt.
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		backoff := backoffDuration(attempt, cfg.InitialInterval, cfg.MaxInterval)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	return lastErr
}

// backoffDuration computes exponential backoff with full jitter.
// interval = min(maxInterval, initialInterval * 2^attempt)
// jitter = random value in [0, interval)
func backoffDuration(attempt int, initial, max time.Duration) time.Duration {
	exp := math.Pow(2, float64(attempt))
	interval := time.Duration(float64(initial) * exp)
	if interval > max {
		interval = max
	}
	// Full jitter: uniform random in [0, interval).
	if interval <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(interval)))
}
