package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type contextKey struct{}

// WithRequestID returns a context carrying the given request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// RequestID extracts the request ID from the context, or returns "".
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(contextKey{}).(string)
	return v
}

// Setup initializes the default slog logger based on the given level and format.
// It returns a RingBuffer that captures all log entries for streaming via the TUI.
func Setup(level, format string) *RingBuffer {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	buf := NewRingBuffer(1000)
	wrapped := newBufferHandler(handler, buf)
	slog.SetDefault(slog.New(wrapped))

	return buf
}
