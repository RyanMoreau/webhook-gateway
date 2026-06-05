package logging

import (
	"sync"
	"testing"
	"time"
)

func entry(msg string) LogEntry {
	return LogEntry{Time: time.Now(), Level: "INFO", Message: msg}
}

func TestRingBuffer_RecentBeforeFull(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(entry("a"))
	rb.Push(entry("b"))
	rb.Push(entry("c"))

	got := rb.Recent(10)
	if len(got) != 3 {
		t.Fatalf("Recent(10) = %d entries, want 3", len(got))
	}
	if got[0].Message != "a" || got[2].Message != "c" {
		t.Errorf("order wrong: got %q, %q, %q", got[0].Message, got[1].Message, got[2].Message)
	}
}

func TestRingBuffer_RecentExactSize(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Push(entry("a"))
	rb.Push(entry("b"))
	rb.Push(entry("c"))

	got := rb.Recent(3)
	if len(got) != 3 {
		t.Fatalf("Recent(3) = %d entries, want 3", len(got))
	}
	if got[0].Message != "a" || got[2].Message != "c" {
		t.Errorf("order wrong: got %q, %q, %q", got[0].Message, got[1].Message, got[2].Message)
	}
}

func TestRingBuffer_WrapAround(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Push(entry("a"))
	rb.Push(entry("b"))
	rb.Push(entry("c"))
	rb.Push(entry("d")) // overwrites "a"
	rb.Push(entry("e")) // overwrites "b"

	got := rb.Recent(3)
	if len(got) != 3 {
		t.Fatalf("Recent(3) = %d entries, want 3", len(got))
	}
	if got[0].Message != "c" || got[1].Message != "d" || got[2].Message != "e" {
		t.Errorf("wrap-around order wrong: got %q, %q, %q", got[0].Message, got[1].Message, got[2].Message)
	}
}

func TestRingBuffer_RecentZero(t *testing.T) {
	rb := NewRingBuffer(5)
	rb.Push(entry("a"))

	got := rb.Recent(0)
	if len(got) != 0 {
		t.Fatalf("Recent(0) = %d entries, want 0", len(got))
	}
}

func TestRingBuffer_RecentMoreThanSize(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Push(entry("a"))
	rb.Push(entry("b"))

	got := rb.Recent(100)
	if len(got) != 2 {
		t.Fatalf("Recent(100) = %d entries, want 2", len(got))
	}
}

func TestRingBuffer_Subscribe(t *testing.T) {
	rb := NewRingBuffer(10)
	ch := rb.Subscribe()

	rb.Push(entry("hello"))

	select {
	case e := <-ch:
		if e.Message != "hello" {
			t.Errorf("got %q, want hello", e.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for entry")
	}

	rb.Unsubscribe(ch)
}

func TestRingBuffer_UnsubscribeStopsDelivery(t *testing.T) {
	rb := NewRingBuffer(10)
	ch := rb.Subscribe()
	rb.Unsubscribe(ch)

	rb.Push(entry("after-unsub"))

	// Channel should be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(100 * time.Millisecond):
		// Also acceptable — channel is closed so receive should be immediate.
		t.Error("timed out, channel should be closed")
	}
}

func TestRingBuffer_ConcurrentPushRecent(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(100)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := range 1000 {
			rb.Push(entry("msg-" + string(rune('A'+i%26))))
		}
	}()

	go func() {
		defer wg.Done()
		for range 1000 {
			entries := rb.Recent(50)
			if len(entries) > 100 {
				t.Errorf("Recent returned %d entries, max should be 100", len(entries))
			}
		}
	}()

	wg.Wait()
}
