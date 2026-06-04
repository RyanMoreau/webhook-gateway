package notify

// Provider delivers notifications to an external service (Telegram, Slack, etc.).
// Implementations must be safe for concurrent use.
type Provider interface {
	// Send delivers a text message and returns the external message ID.
	Send(target, text, parseMode string) (*SendResult, error)

	// Edit updates a previously sent message. Not all providers support this —
	// return ErrEditNotSupported if the provider cannot edit messages.
	Edit(target string, messageID int, text, parseMode string) error

	// SendPhoto delivers an image with an optional caption.
	// Not all providers support this — return ErrPhotoNotSupported.
	SendPhoto(target, photo, caption, parseMode string) (*SendResult, error)
}

// SendResult holds the response from a provider after sending a message.
type SendResult struct {
	MessageID int `json:"message_id"`
}
