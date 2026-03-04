package delivery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Destination describes a downstream target for webhook delivery.
type Destination struct {
	URL     string
	Timeout time.Duration
}

// retryableError is an error that the retry layer should retry.
type retryableError struct {
	err error
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// nonRetryableError is an error that should not be retried (e.g. 4xx).
type nonRetryableError struct {
	err error
}

func (e *nonRetryableError) Error() string { return e.err.Error() }
func (e *nonRetryableError) Unwrap() error { return e.err }

// IsRetryable returns true if the error should be retried.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Non-retryable errors explicitly opt out.
	if _, ok := err.(*nonRetryableError); ok {
		return false
	}
	return true
}

// Deliver forwards the webhook payload to a single destination.
// The provided headers should already be filtered to the allowlist.
func Deliver(ctx context.Context, dest Destination, headers http.Header, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, dest.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest.URL, nil)
	if err != nil {
		return &nonRetryableError{err: fmt.Errorf("creating request: %w", err)}
	}

	// Set body separately so we can use a fresh reader.
	req.Body = io.NopCloser(&bytesReader{b: body})
	req.ContentLength = int64(len(body))

	// Copy filtered headers.
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return &retryableError{err: fmt.Errorf("sending request: %w", err)}
	}
	defer resp.Body.Close()
	// Drain up to 64KB so the connection can be reused, but no more.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	msg := fmt.Errorf("destination returned HTTP %d", resp.StatusCode)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return &nonRetryableError{err: msg}
	}
	// 5xx and anything else: retryable.
	return &retryableError{err: msg}
}

// bytesReader is a minimal io.Reader over a byte slice. We use our own
// instead of bytes.NewReader so the request body is not seekable (avoids
// accidental re-reads by the HTTP client).
type bytesReader struct {
	b   []byte
	off int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}
