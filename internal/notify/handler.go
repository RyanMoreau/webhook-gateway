package notify

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/ryanmoreau/webhook-gateway/internal/config"
)

// channel is a resolved channel: a provider instance + target (chat ID, etc.).
type channel struct {
	provider Provider
	target   string
}

// Handler serves the POST /notify and POST /notify/edit endpoints.
type Handler struct {
	channels  map[string]channel
	authToken string // if non-empty, requires X-Notify-Token header
	stats     *Stats
}

// Stats tracks notification metrics.
type Stats struct {
	Sent   atomic.Int64
	Failed atomic.Int64
	Edits  atomic.Int64
}

// Snapshot returns a point-in-time copy of notification stats.
func (s *Stats) Snapshot() map[string]int64 {
	return map[string]int64{
		"notifications_sent":   s.Sent.Load(),
		"notifications_failed": s.Failed.Load(),
		"notifications_edits":  s.Edits.Load(),
	}
}

// NewHandler creates a notification handler from config. It instantiates
// providers and wires each channel to its provider. Returns nil if no
// channels are configured.
func NewHandler(cfg config.NotifyConfig) (*Handler, error) {
	if len(cfg.Channels) == 0 {
		return nil, nil
	}

	// Build provider instances.
	providers := make(map[string]Provider, len(cfg.Providers))
	disabledProviders := make(map[string]bool)
	for name, pc := range cfg.Providers {
		p, err := buildProvider(pc)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", name, err)
		}
		if p != nil {
			providers[name] = p
		} else {
			disabledProviders[name] = true
			slog.Info("provider disabled (no credentials)", "provider", name)
		}
	}

	// Wire channels to providers.
	channels := make(map[string]channel, len(cfg.Channels))
	for name, cc := range cfg.Channels {
		if disabledProviders[cc.Provider] {
			slog.Info("channel skipped (provider disabled)", "channel", name, "provider", cc.Provider)
			continue
		}
		p, ok := providers[cc.Provider]
		if !ok {
			return nil, fmt.Errorf("channel %q: unknown provider %q (not defined in providers)", name, cc.Provider)
		}
		if cc.Target == "" {
			return nil, fmt.Errorf("channel %q: no target configured", name)
		}
		channels[name] = channel{provider: p, target: cc.Target}
	}

	if len(channels) == 0 {
		return nil, nil
	}

	return &Handler{
		channels:  channels,
		authToken: cfg.AuthToken,
		stats:     &Stats{},
	}, nil
}

// buildProvider creates a Provider from config. Returns nil if credentials
// are missing (provider is disabled).
func buildProvider(pc config.ProviderConfig) (Provider, error) {
	switch pc.Type {
	case "telegram", "":
		return NewTelegramProvider(pc.BotToken), nil
	default:
		return nil, fmt.Errorf("unknown provider type %q", pc.Type)
	}
}

// Stats returns the handler's stats for inclusion in /health.
func (h *Handler) Stats() *Stats {
	return h.stats
}

// sendRequest is the JSON body for POST /notify.
type sendRequest struct {
	Channel   string `json:"channel"`              // logical channel name
	Text      string `json:"text"`                 // message text (required for send)
	ParseMode string `json:"parse_mode,omitempty"` // HTML (default) or Markdown
	Photo     string `json:"photo,omitempty"`      // photo URL or file_id
	Caption   string `json:"caption,omitempty"`    // photo caption
}

// editRequest is the JSON body for POST /notify/edit.
type editRequest struct {
	Channel   string `json:"channel"`
	MessageID int    `json:"message_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// ServeHTTP handles POST /notify and POST /notify/edit.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.authToken != "" {
		token := r.Header.Get("X-Notify-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(h.authToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	switch r.URL.Path {
	case "/notify":
		h.handleSend(w, r)
	case "/notify/edit":
		h.handleEdit(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Channel == "" {
		http.Error(w, `"channel" is required`, http.StatusBadRequest)
		return
	}
	if req.Text == "" && req.Photo == "" {
		http.Error(w, `"text" or "photo" is required`, http.StatusBadRequest)
		return
	}

	ch, ok := h.channels[req.Channel]
	if !ok {
		http.Error(w, "unknown channel: "+req.Channel, http.StatusBadRequest)
		return
	}

	logger := slog.With("channel", req.Channel)

	var result *SendResult
	var err error

	if req.Photo != "" {
		result, err = ch.provider.SendPhoto(ch.target, req.Photo, req.Caption, req.ParseMode)
	} else {
		result, err = ch.provider.Send(ch.target, req.Text, req.ParseMode)
	}

	if err != nil {
		h.stats.Failed.Add(1)
		logger.Error("notification failed", "error", err)
		http.Error(w, "delivery failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	h.stats.Sent.Add(1)
	logger.Info("notification sent", "message_id", result.MessageID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"message_id": result.MessageID,
	})
}

func (h *Handler) handleEdit(w http.ResponseWriter, r *http.Request) {
	var req editRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Channel == "" || req.MessageID == 0 || req.Text == "" {
		http.Error(w, `"channel", "message_id", and "text" are required`, http.StatusBadRequest)
		return
	}

	ch, ok := h.channels[req.Channel]
	if !ok {
		http.Error(w, "unknown channel: "+req.Channel, http.StatusBadRequest)
		return
	}

	if err := ch.provider.Edit(ch.target, req.MessageID, req.Text, req.ParseMode); err != nil {
		h.stats.Failed.Add(1)
		slog.Error("edit failed", "channel", req.Channel, "message_id", req.MessageID, "error", err)
		http.Error(w, "edit failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	h.stats.Edits.Add(1)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
