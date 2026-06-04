package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Compile-time check that TelegramProvider implements Provider.
var _ Provider = (*TelegramProvider)(nil)

// TelegramProvider sends messages via the Telegram Bot API.
type TelegramProvider struct {
	botToken string
	client   *http.Client
}

// NewTelegramProvider creates a provider for the Telegram Bot API.
// Returns nil if botToken is empty.
func NewTelegramProvider(botToken string) *TelegramProvider {
	if botToken == "" {
		return nil
	}
	return &TelegramProvider{
		botToken: botToken,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Send sends a text message to the given chat ID.
func (t *TelegramProvider) Send(chatID, text, parseMode string) (*SendResult, error) {
	if parseMode == "" {
		parseMode = "HTML"
	}

	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               parseMode,
		"disable_web_page_preview": true,
	}

	return t.call("sendMessage", payload)
}

// Edit updates an existing message by ID.
func (t *TelegramProvider) Edit(chatID string, messageID int, text, parseMode string) error {
	if parseMode == "" {
		parseMode = "HTML"
	}

	payload := map[string]any{
		"chat_id":                  chatID,
		"message_id":              messageID,
		"text":                     text,
		"parse_mode":               parseMode,
		"disable_web_page_preview": true,
	}

	_, err := t.call("editMessageText", payload)
	return err
}

// SendPhoto sends an image with an optional caption.
// photo can be a URL or Telegram file_id.
func (t *TelegramProvider) SendPhoto(chatID, photo, caption, parseMode string) (*SendResult, error) {
	if parseMode == "" {
		parseMode = "HTML"
	}

	payload := map[string]any{
		"chat_id":    chatID,
		"photo":      photo,
		"parse_mode": parseMode,
	}
	if caption != "" {
		payload["caption"] = caption
	}

	return t.call("sendPhoto", payload)
}

// call makes a POST to the Telegram Bot API and parses the response.
func (t *TelegramProvider) call(method string, payload map[string]any) (*SendResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling payload: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", t.botToken, method)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram %s returned HTTP %d: %s", method, resp.StatusCode, respBody)
	}

	var tgResp struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &SendResult{MessageID: tgResp.Result.MessageID}, nil
}
