package idempotency

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_NewKey(t *testing.T) {
	s := NewMemoryStore(1 * time.Minute)
	defer s.Close()

	seen, err := s.Seen("new-key")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("expected new key to not be seen")
	}
}

func TestMemoryStore_MarkThenSeen(t *testing.T) {
	s := NewMemoryStore(1 * time.Minute)
	defer s.Close()

	if err := s.Mark("evt_1", 1*time.Hour); err != nil {
		t.Fatal(err)
	}

	seen, err := s.Seen("evt_1")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("expected marked key to be seen")
	}
}

func TestMemoryStore_TTLExpiry(t *testing.T) {
	s := NewMemoryStore(10 * time.Millisecond)
	defer s.Close()

	if err := s.Mark("evt_exp", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	// Should be seen immediately.
	seen, err := s.Seen("evt_exp")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("expected key to be seen before TTL")
	}

	// Wait for TTL to expire.
	time.Sleep(100 * time.Millisecond)

	seen, err = s.Seen("evt_exp")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("expected key to not be seen after TTL")
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	s := NewMemoryStore(1 * time.Minute)
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		key := "evt_concurrent"
		go func() {
			defer wg.Done()
			s.Mark(key, 1*time.Hour)
		}()
		go func() {
			defer wg.Done()
			s.Seen(key)
		}()
	}
	wg.Wait()
}

func TestMemoryStore_SeenOrMark_AtomicDedup(t *testing.T) {
	s := NewMemoryStore(1 * time.Minute)
	defer s.Close()

	const goroutines = 100
	var wg sync.WaitGroup
	var firstCount int64
	var mu sync.Mutex

	// All goroutines race on the same key. Exactly one should win.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			seen, err := s.SeenOrMark("race-key", 1*time.Hour)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if !seen {
				mu.Lock()
				firstCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if firstCount != 1 {
		t.Errorf("expected exactly 1 goroutine to see key as new, got %d", firstCount)
	}
}

func TestMemoryStore_SeenOrMark_ExpiredEntry(t *testing.T) {
	s := NewMemoryStore(10 * time.Millisecond)
	defer s.Close()

	seen, _ := s.SeenOrMark("exp-key", 50*time.Millisecond)
	if seen {
		t.Fatal("first call should return false")
	}

	seen, _ = s.SeenOrMark("exp-key", 50*time.Millisecond)
	if !seen {
		t.Fatal("second call should return true (duplicate)")
	}

	time.Sleep(100 * time.Millisecond)

	seen, _ = s.SeenOrMark("exp-key", 50*time.Millisecond)
	if seen {
		t.Fatal("should return false after TTL expires")
	}
}
