package idempotency

import (
	"sync"
	"time"
)

// Store tracks event IDs for deduplication.
type Store interface {
	// SeenOrMark atomically checks whether the key exists and, if not, marks it
	// with the given TTL. Returns true if the key was already present (duplicate).
	SeenOrMark(key string, ttl time.Duration) (bool, error)
}

// MemoryStore is an in-memory Store backed by sync.Map with TTL-based expiry.
type MemoryStore struct {
	entries sync.Map // map[string]time.Time (expiry time)
	done    chan struct{}
}

// NewMemoryStore creates a MemoryStore and starts a background goroutine
// that purges expired entries at the given sweep interval.
func NewMemoryStore(sweepInterval time.Duration) *MemoryStore {
	s := &MemoryStore{
		done: make(chan struct{}),
	}
	go s.sweep(sweepInterval)
	return s
}

// SeenOrMark atomically checks and marks the key. Uses sync.Map.LoadOrStore
// to eliminate the TOCTOU race between separate Seen()+Mark() calls.
func (s *MemoryStore) SeenOrMark(key string, ttl time.Duration) (bool, error) {
	exp := time.Now().Add(ttl)
	actual, loaded := s.entries.LoadOrStore(key, exp)
	if !loaded {
		// First time seeing this key.
		return false, nil
	}
	// Key existed — check if it has expired.
	if time.Now().After(actual.(time.Time)) {
		// Expired; overwrite with fresh TTL and treat as new.
		s.entries.Store(key, exp)
		return false, nil
	}
	return true, nil
}

// Seen returns true if the key has been marked within its TTL window.
func (s *MemoryStore) Seen(key string) (bool, error) {
	v, ok := s.entries.Load(key)
	if !ok {
		return false, nil
	}
	exp := v.(time.Time)
	if time.Now().After(exp) {
		s.entries.Delete(key)
		return false, nil
	}
	return true, nil
}

// Mark records the key with the given TTL.
func (s *MemoryStore) Mark(key string, ttl time.Duration) error {
	s.entries.Store(key, time.Now().Add(ttl))
	return nil
}

// Close stops the background sweep goroutine.
func (s *MemoryStore) Close() {
	close(s.done)
}

func (s *MemoryStore) sweep(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			now := time.Now()
			s.entries.Range(func(key, value any) bool {
				if now.After(value.(time.Time)) {
					s.entries.Delete(key)
				}
				return true
			})
		}
	}
}
