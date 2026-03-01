package delivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithRetry_SuccessFirstAttempt(t *testing.T) {
	calls := 0
	err := WithRetry(context.Background(), RetryConfig{
		MaxAttempts:     3,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
	}, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestWithRetry_FailThenSucceed(t *testing.T) {
	calls := 0
	err := WithRetry(context.Background(), RetryConfig{
		MaxAttempts:     3,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
	}, func() error {
		calls++
		if calls < 3 {
			return &retryableError{err: errors.New("temporary")}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestWithRetry_ExhaustsAttempts(t *testing.T) {
	calls := 0
	err := WithRetry(context.Background(), RetryConfig{
		MaxAttempts:     3,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
	}, func() error {
		calls++
		return &retryableError{err: errors.New("always fails")}
	})
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestWithRetry_NonRetryableStopsImmediately(t *testing.T) {
	calls := 0
	err := WithRetry(context.Background(), RetryConfig{
		MaxAttempts:     5,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
	}, func() error {
		calls++
		return &nonRetryableError{err: errors.New("bad request")}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (should stop on non-retryable)", calls)
	}
}

func TestWithRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := WithRetry(ctx, RetryConfig{
		MaxAttempts:     100,
		InitialInterval: 1 * time.Second,
		MaxInterval:     5 * time.Second,
	}, func() error {
		calls++
		return &retryableError{err: errors.New("keep going")}
	})
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	// Should have made at least 1 attempt but far fewer than 100.
	if calls < 1 || calls > 10 {
		t.Errorf("calls = %d, expected between 1 and 10", calls)
	}
}
