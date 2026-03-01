package idempotency

import (
	"sync"
	"time"
)

// Store tracks event IDs for deduplication.
type Store interface {
	// Seen returns true if the key has been marked within its TTL window.
	Seen(key string) (bool, error)

	// Mark records the key. Subsequent Seen calls return true until the TTL expires.
	Mark(key string, ttl time.Duration) error
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
