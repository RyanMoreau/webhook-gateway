package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// LogEntry is a structured log record stored in the ring buffer.
type LogEntry struct {
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"msg"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// RingBuffer stores the last N log entries in a fixed-size circular buffer.
type RingBuffer struct {
	mu        sync.RWMutex
	entries   []LogEntry
	pos       int
	size      int
	full      bool
	listeners []chan LogEntry
}

// NewRingBuffer creates a ring buffer that holds up to size entries.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		entries: make([]LogEntry, size),
		size:    size,
	}
}

// Push adds an entry to the ring buffer and notifies all subscribers.
func (rb *RingBuffer) Push(entry LogEntry) {
	rb.mu.Lock()
	rb.entries[rb.pos] = entry
	rb.pos = (rb.pos + 1) % rb.size
	if rb.pos == 0 {
		rb.full = true
	}

	// Notify listeners (non-blocking).
	for _, ch := range rb.listeners {
		select {
		case ch <- entry:
		default:
		}
	}
	rb.mu.Unlock()
}

// Recent returns the last n entries (or all if n > stored count), oldest first.
func (rb *RingBuffer) Recent(n int) []LogEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	count := rb.pos
	if rb.full {
		count = rb.size
	}
	if n > count {
		n = count
	}

	result := make([]LogEntry, n)
	start := rb.pos - n
	if start < 0 {
		start += rb.size
	}
	for i := range n {
		result[i] = rb.entries[(start+i)%rb.size]
	}
	return result
}

// Subscribe returns a channel that receives new log entries.
// Call Unsubscribe when done to avoid leaking the channel.
func (rb *RingBuffer) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 64)
	rb.mu.Lock()
	rb.listeners = append(rb.listeners, ch)
	rb.mu.Unlock()
	return ch
}

// Unsubscribe removes a previously subscribed channel.
func (rb *RingBuffer) Unsubscribe(ch chan LogEntry) {
	rb.mu.Lock()
	for i, l := range rb.listeners {
		if l == ch {
			rb.listeners = append(rb.listeners[:i], rb.listeners[i+1:]...)
			break
		}
	}
	rb.mu.Unlock()
	close(ch)
}

// MarshalEntry serializes a LogEntry to JSON.
func MarshalEntry(e LogEntry) ([]byte, error) {
	return json.Marshal(e)
}

// bufferHandler wraps an existing slog.Handler and copies records to a RingBuffer.
type bufferHandler struct {
	inner  slog.Handler
	buffer *RingBuffer
	attrs  []slog.Attr
	group  string
}

func newBufferHandler(inner slog.Handler, buf *RingBuffer) *bufferHandler {
	return &bufferHandler{inner: inner, buffer: buf}
}

func (h *bufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *bufferHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make(map[string]string)

	// Include pre-set attrs (from WithAttrs).
	for _, a := range h.attrs {
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		attrs[key] = a.Value.String()
	}

	// Include record attrs.
	r.Attrs(func(a slog.Attr) bool {
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		attrs[key] = a.Value.String()
		return true
	})

	entry := LogEntry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   attrs,
	}
	h.buffer.Push(entry)

	return h.inner.Handle(ctx, r)
}

func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &bufferHandler{
		inner:  h.inner.WithAttrs(attrs),
		buffer: h.buffer,
		attrs:  append(h.attrs, attrs...),
		group:  h.group,
	}
}

func (h *bufferHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &bufferHandler{
		inner:  h.inner.WithGroup(name),
		buffer: h.buffer,
		attrs:  h.attrs,
		group:  g,
	}
}
