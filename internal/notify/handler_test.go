package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ryanmoreau/webhook-gateway/internal/config"
)

// fakeTelegram spins up a test server that records requests and returns
// a valid Telegram-shaped response.
func fakeTelegram(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(r.Body)
		calls = append(calls, r.URL.Path+": "+string(body))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"message_id": 42},
		})
	}))
	return srv, &calls
}

// readAll is a small helper to avoid importing io just for tests.
func readAll(r interface{ Read([]byte) (int, error) }) []byte {
	var buf []byte
	tmp := make([]byte, 1024)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

// testHandler creates a handler backed by a fake Telegram API.
func testHandler(t *testing.T, tgURL string) *Handler {
	t.Helper()

	// Create a TelegramProvider that routes to our test server.
	provider := &TelegramProvider{
		botToken: "test-token",
		client: &http.Client{
			Transport: &rewriteTransport{baseURL: tgURL},
		},
	}

	cfg := config.NotifyConfig{
		Providers: map[string]config.ProviderConfig{
			"telegram": {Type: "telegram", BotToken: "test-token"},
		},
		Channels: map[string]config.ChannelConfig{
			"deploys": {Provider: "telegram", Target: "-100123"},
			"alerts":  {Provider: "telegram", Target: "-100456"},
		},
	}

	// Build handler, but replace the provider with our test one.
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Override the resolved providers with our test provider.
	for name, ch := range h.channels {
		ch.provider = provider
		h.channels[name] = ch
	}

	return h
}

// rewriteTransport rewrites requests to a local test server.
type rewriteTransport struct {
	baseURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.baseURL, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

func TestSendText(t *testing.T) {
	srv, calls := fakeTelegram(t)
	defer srv.Close()

	h := testHandler(t, srv.URL)

	body := `{"channel":"deploys","text":"<b>Deployed</b> v1.2.3"}`
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}
	if resp["message_id"] != float64(42) {
		t.Fatalf("expected message_id=42, got %v", resp["message_id"])
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 telegram call, got %d", len(*calls))
	}
	if !strings.Contains((*calls)[0], "sendMessage") {
		t.Errorf("expected sendMessage call, got: %s", (*calls)[0])
	}
	if !strings.Contains((*calls)[0], "-100123") {
		t.Errorf("expected chat_id -100123, got: %s", (*calls)[0])
	}
}

func TestSendPhoto(t *testing.T) {
	srv, calls := fakeTelegram(t)
	defer srv.Close()

	h := testHandler(t, srv.URL)

	body := `{"channel":"alerts","photo":"https://example.com/img.jpg","caption":"screenshot"}`
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains((*calls)[0], "sendPhoto") {
		t.Errorf("expected sendPhoto call, got: %s", (*calls)[0])
	}
}

func TestEditMessage(t *testing.T) {
	srv, calls := fakeTelegram(t)
	defer srv.Close()

	h := testHandler(t, srv.URL)

	body := `{"channel":"deploys","message_id":42,"text":"updated"}`
	req := httptest.NewRequest(http.MethodPost, "/notify/edit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains((*calls)[0], "editMessageText") {
		t.Errorf("expected editMessageText call, got: %s", (*calls)[0])
	}
}

func TestUnknownChannel(t *testing.T) {
	srv, _ := fakeTelegram(t)
	defer srv.Close()

	h := testHandler(t, srv.URL)

	body := `{"channel":"nonexistent","text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestMissingFields(t *testing.T) {
	srv, _ := fakeTelegram(t)
	defer srv.Close()

	h := testHandler(t, srv.URL)

	cases := []struct {
		name string
		path string
		body string
	}{
		{"no channel", "/notify", `{"text":"hello"}`},
		{"no text or photo", "/notify", `{"channel":"deploys"}`},
		{"edit no message_id", "/notify/edit", `{"channel":"deploys","text":"hello"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := fakeTelegram(t)
	defer srv.Close()

	h := testHandler(t, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/notify", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestStats(t *testing.T) {
	srv, _ := fakeTelegram(t)
	defer srv.Close()

	h := testHandler(t, srv.URL)

	body := `{"channel":"deploys","text":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	stats := h.Stats().Snapshot()
	if stats["notifications_sent"] != 1 {
		t.Errorf("expected sent=1, got %d", stats["notifications_sent"])
	}
}

func TestNewHandler_UnknownProvider(t *testing.T) {
	cfg := config.NotifyConfig{
		Providers: map[string]config.ProviderConfig{
			"telegram": {Type: "telegram", BotToken: "test"},
		},
		Channels: map[string]config.ChannelConfig{
			"test": {Provider: "nonexistent", Target: "123"},
		},
	}
	_, err := NewHandler(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider reference")
	}
}

func TestNewHandler_UnknownProviderType(t *testing.T) {
	cfg := config.NotifyConfig{
		Providers: map[string]config.ProviderConfig{
			"custom": {Type: "carrier-pigeon", BotToken: "test"},
		},
		Channels: map[string]config.ChannelConfig{
			"test": {Provider: "custom", Target: "123"},
		},
	}
	_, err := NewHandler(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider type")
	}
}

func TestNewHandler_NoChannels(t *testing.T) {
	cfg := config.NotifyConfig{}
	h, err := NewHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if h != nil {
		t.Fatal("expected nil handler when no channels configured")
	}
}
