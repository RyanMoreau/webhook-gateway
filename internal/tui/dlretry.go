package tui

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/deadletter"
)

var retryClient = &http.Client{Timeout: 30 * time.Second}

// RetryEntry re-delivers a dead letter entry to its original destination.
// On success the dead letter file is deleted. On failure the file remains.
func RetryEntry(entry deadletter.Entry, filePath string) error {
	req, err := http.NewRequest(http.MethodPost, entry.DestinationURL, bytes.NewReader(entry.RequestBody))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	for k, v := range entry.Headers {
		req.Header.Set(k, v)
	}

	resp, err := retryClient.Do(req)
	if err != nil {
		return fmt.Errorf("delivering to %s: %w", entry.DestinationURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("destination returned %d", resp.StatusCode)
	}

	if err := DeleteEntry(filePath); err != nil {
		return fmt.Errorf("delivered successfully but failed to remove dead letter: %w", err)
	}

	return nil
}
