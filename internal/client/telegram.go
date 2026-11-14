package client

import (
	"context"
	"net/http"
	"time"
)

// ---- Telegram bot (SpaceMan ADR-0083) ----

// TelegramBot is central's Telegram bot connection. The token is never returned: only whether one is stored and a
// counter SpaceMan increments each time one is written.
type TelegramBot struct {
	Enabled            bool      `json:"enabled"`
	Mode               string    `json:"mode"`
	MiniAppHost        string    `json:"miniapp_host"`
	WebhookIPAllowlist []string  `json:"webhook_ip_allowlist"`
	HasToken           bool      `json:"has_token"`
	TokenVersion       int64     `json:"token_version"`
	BotID              int64     `json:"bot_id"`
	Username           string    `json:"username"`
	WebhookStatus      string    `json:"webhook_status"`
	Warnings           []string  `json:"warnings"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Version            int64     `json:"version"`
}

// TelegramBotInput is PUT /telegram/bot. It replaces every field; an empty BotToken keeps the stored token (required on
// create).
type TelegramBotInput struct {
	BotToken           string   `json:"bot_token,omitempty"`
	Enabled            bool     `json:"enabled"`
	Mode               string   `json:"mode,omitempty"`
	MiniAppHost        string   `json:"miniapp_host,omitempty"`
	WebhookIPAllowlist []string `json:"webhook_ip_allowlist"`
}

const telegramBotPath = "/telegram/bot"

// GetTelegramBot is GET /telegram/bot (404 when none is configured).
func (c *Client) GetTelegramBot(ctx context.Context) (*TelegramBot, error) {
	var out TelegramBot
	return &out, c.get(ctx, telegramBotPath, &out)
}

// PutTelegramBot is PUT /telegram/bot: version 0 creates it (no If-Match), otherwise it replaces the configuration at
// that version. SpaceMan checks a new token with Telegram first.
func (c *Client) PutTelegramBot(ctx context.Context, version int64, in TelegramBotInput) (*TelegramBot, error) {
	var out TelegramBot
	return &out, c.put(ctx, telegramBotPath, version, in, &out)
}

// DeleteTelegramBot is DELETE /telegram/bot with If-Match; a 404 counts as success.
func (c *Client) DeleteTelegramBot(ctx context.Context, version int64) error {
	err := c.do2(ctx, http.MethodDelete, telegramBotPath, version, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
