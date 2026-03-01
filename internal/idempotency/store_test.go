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
