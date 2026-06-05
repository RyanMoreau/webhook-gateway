package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ryanmoreau/webhook-gateway/internal/deadletter"
)

// EntrySummary holds metadata parsed from a dead letter filename for fast listing.
type EntrySummary struct {
	Path      string
	Timestamp time.Time
	RequestID string
}

// ListEntries reads the dead letter directory and returns summaries sorted newest-first.
// Only parses filenames — file contents are loaded lazily via ReadEntry.
func ListEntries(dir string) ([]EntrySummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading dead letter directory: %w", err)
	}

	var summaries []EntrySummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Skip temp files from atomic writes.
		if strings.HasPrefix(e.Name(), ".dl-") {
			continue
		}

		s := parseFilename(e.Name(), dir)
		if s.Path != "" {
			summaries = append(summaries, s)
		}
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Timestamp.After(summaries[j].Timestamp)
	})

	return summaries, nil
}

// parseFilename extracts timestamp and request ID from the dead letter filename
// format: {YYYYMMDDTHHMMSSZ}_{request_id}.json
func parseFilename(name, dir string) EntrySummary {
	base := strings.TrimSuffix(name, ".json")
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 {
		return EntrySummary{}
	}

	ts, err := time.Parse("20060102T150405Z", parts[0])
	if err != nil {
		return EntrySummary{}
	}

	return EntrySummary{
		Path:      filepath.Join(dir, name),
		Timestamp: ts,
		RequestID: parts[1],
	}
}

// ReadEntry loads and parses a single dead letter JSON file.
func ReadEntry(path string) (deadletter.Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return deadletter.Entry{}, fmt.Errorf("reading dead letter: %w", err)
	}

	var entry deadletter.Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return deadletter.Entry{}, fmt.Errorf("parsing dead letter: %w", err)
	}
	return entry, nil
}

// DeleteEntry removes a dead letter file from disk.
func DeleteEntry(path string) error {
	return os.Remove(path)
}
