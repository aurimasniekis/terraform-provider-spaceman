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

// ---- Telegram settings (SpaceMan ADR-0083, plan §5.2) ----

// TelegramSendLimits bound what the bot sends.
type TelegramSendLimits struct {
	GlobalPerSecond float64 `json:"global_per_second"`
	ChatPerSecond   float64 `json:"chat_per_second"`
	ChatBurst       int64   `json:"chat_burst"`
	GroupPerMinute  int64   `json:"group_per_minute"`
	QueueTotal      int64   `json:"queue_total"`
	QueuePerChat    int64   `json:"queue_per_chat"`
}

// TelegramInboundLimits bound what linked users can make central do.
type TelegramInboundLimits struct {
	ActionsPerMinute  int64 `json:"actions_per_minute"`
	ActionsBurst      int64 `json:"actions_burst"`
	InboxItemsPerHour int64 `json:"inbox_items_per_hour"`
	DownloadsPerHour  int64 `json:"downloads_per_hour"`
	UpdatesPerSecond  int64 `json:"updates_per_second"`
}

// TelegramNotificationDefaults are what a link notifies about until its user overrides a field.
type TelegramNotificationDefaults struct {
	NewActions       bool   `json:"new_actions"`
	MinPriority      string `json:"min_priority"`
	AssignedToMe     bool   `json:"assigned_to_me"`
	Escalations      bool   `json:"escalations"`
	Reminders        string `json:"reminders"`
	Clarifications   bool   `json:"clarifications"`
	OperatorMessages bool   `json:"operator_messages"`
	InboxItems       string `json:"inbox_items"`
	TasksInReview    string `json:"tasks_in_review"`
	FailedRuns       string `json:"failed_runs"`
	Digest           bool   `json:"digest"`
	DigestTime       string `json:"digest_time"`
	QuietHours       bool   `json:"quiet_hours"`
	QuietHoursStart  string `json:"quiet_hours_start"`
	QuietHoursEnd    string `json:"quiet_hours_end"`
	Threads          string `json:"threads"`
}

// TelegramSettings are the effective global Telegram settings. Stored is false (version 0) while nothing is stored:
// every value is then SpaceMan's built-in default.
type TelegramSettings struct {
	LinkDefaultMaxRole            string                       `json:"link_default_max_role"`
	LinkAllowAdminCap             bool                         `json:"link_allow_admin_cap"`
	LinkSSOGrants                 bool                         `json:"link_sso_grants"`
	LinkReconfirmDays             int64                        `json:"link_reconfirm_days"`
	LinksPerUser                  int64                        `json:"links_per_user"`
	LinkCodeTTLSeconds            int64                        `json:"link_code_ttl_seconds"`
	InitDataMaxAgeSeconds         int64                        `json:"init_data_max_age_seconds"`
	MiniAppSessionIdleSeconds     int64                        `json:"miniapp_session_idle_seconds"`
	MiniAppSessionAbsoluteSeconds int64                        `json:"miniapp_session_absolute_seconds"`
	SendLimits                    TelegramSendLimits           `json:"send_limits"`
	InboundLimits                 TelegramInboundLimits        `json:"inbound_limits"`
	NotificationDefaults          TelegramNotificationDefaults `json:"notification_defaults"`
	TopicsModeDefault             bool                         `json:"topics_mode_default"`
	Stored                        bool                         `json:"stored"`
	Version                       int64                        `json:"version"`
}

// TelegramSendLimitsInput sets send_limits; a nil field takes SpaceMan's default.
type TelegramSendLimitsInput struct {
	GlobalPerSecond *float64 `json:"global_per_second,omitempty"`
	ChatPerSecond   *float64 `json:"chat_per_second,omitempty"`
	ChatBurst       *int64   `json:"chat_burst,omitempty"`
	GroupPerMinute  *int64   `json:"group_per_minute,omitempty"`
	QueueTotal      *int64   `json:"queue_total,omitempty"`
	QueuePerChat    *int64   `json:"queue_per_chat,omitempty"`
}

// TelegramInboundLimitsInput sets inbound_limits; a nil field takes SpaceMan's default.
type TelegramInboundLimitsInput struct {
	ActionsPerMinute  *int64 `json:"actions_per_minute,omitempty"`
	ActionsBurst      *int64 `json:"actions_burst,omitempty"`
	InboxItemsPerHour *int64 `json:"inbox_items_per_hour,omitempty"`
	DownloadsPerHour  *int64 `json:"downloads_per_hour,omitempty"`
	UpdatesPerSecond  *int64 `json:"updates_per_second,omitempty"`
}

// TelegramNotificationDefaultsInput sets notification_defaults; a nil field takes SpaceMan's default.
type TelegramNotificationDefaultsInput struct {
	NewActions       *bool   `json:"new_actions,omitempty"`
	MinPriority      *string `json:"min_priority,omitempty"`
	AssignedToMe     *bool   `json:"assigned_to_me,omitempty"`
	Escalations      *bool   `json:"escalations,omitempty"`
	Reminders        *string `json:"reminders,omitempty"`
	Clarifications   *bool   `json:"clarifications,omitempty"`
	OperatorMessages *bool   `json:"operator_messages,omitempty"`
	InboxItems       *string `json:"inbox_items,omitempty"`
	TasksInReview    *string `json:"tasks_in_review,omitempty"`
	FailedRuns       *string `json:"failed_runs,omitempty"`
	Digest           *bool   `json:"digest,omitempty"`
	DigestTime       *string `json:"digest_time,omitempty"`
	QuietHours       *bool   `json:"quiet_hours,omitempty"`
	QuietHoursStart  *string `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd    *string `json:"quiet_hours_end,omitempty"`
	Threads          *string `json:"threads,omitempty"`
}

// TelegramSettingsInput is PUT /telegram/settings. It replaces everything stored: a nil field takes SpaceMan's
// built-in default, and only the fields sent are stored.
type TelegramSettingsInput struct {
	LinkDefaultMaxRole            *string                            `json:"link_default_max_role,omitempty"`
	LinkAllowAdminCap             *bool                              `json:"link_allow_admin_cap,omitempty"`
	LinkSSOGrants                 *bool                              `json:"link_sso_grants,omitempty"`
	LinkReconfirmDays             *int64                             `json:"link_reconfirm_days,omitempty"`
	LinksPerUser                  *int64                             `json:"links_per_user,omitempty"`
	LinkCodeTTLSeconds            *int64                             `json:"link_code_ttl_seconds,omitempty"`
	InitDataMaxAgeSeconds         *int64                             `json:"init_data_max_age_seconds,omitempty"`
	MiniAppSessionIdleSeconds     *int64                             `json:"miniapp_session_idle_seconds,omitempty"`
	MiniAppSessionAbsoluteSeconds *int64                             `json:"miniapp_session_absolute_seconds,omitempty"`
	SendLimits                    *TelegramSendLimitsInput           `json:"send_limits,omitempty"`
	InboundLimits                 *TelegramInboundLimitsInput        `json:"inbound_limits,omitempty"`
	NotificationDefaults          *TelegramNotificationDefaultsInput `json:"notification_defaults,omitempty"`
	TopicsModeDefault             *bool                              `json:"topics_mode_default,omitempty"`
}

const telegramSettingsPath = "/telegram/settings"

// GetTelegramSettings is GET /telegram/settings (the defaults, Stored false, when nothing is stored).
func (c *Client) GetTelegramSettings(ctx context.Context) (*TelegramSettings, error) {
	var out TelegramSettings
	return &out, c.get(ctx, telegramSettingsPath, &out)
}

// PutTelegramSettings is PUT /telegram/settings: version 0 stores them for the first time (no If-Match), otherwise it
// replaces them at that version.
func (c *Client) PutTelegramSettings(ctx context.Context, version int64, in TelegramSettingsInput) (*TelegramSettings, error) {
	var out TelegramSettings
	return &out, c.put(ctx, telegramSettingsPath, version, in, &out)
}

// DeleteTelegramSettings is DELETE /telegram/settings with If-Match: every value goes back to its default. A 404
// counts as success.
func (c *Client) DeleteTelegramSettings(ctx context.Context, version int64) error {
	err := c.do2(ctx, http.MethodDelete, telegramSettingsPath, version, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// SpaceTelegram is a space's Telegram settings. Stored is false while the space has none: Telegram is off for it.
type SpaceTelegram struct {
	SpaceID           string `json:"space_id"`
	Stored            bool   `json:"stored"`
	Enabled           bool   `json:"enabled"`
	ContentLevel      string `json:"content_level"`
	EnforcedApprovals string `json:"enforced_approvals"`
	Topics            bool   `json:"topics"`
	TerminalWatch     bool   `json:"terminal_watch"`
	GroupChatID       int64  `json:"group_chat_id"` // the bound forum supergroup; 0 (omitted by central) when none
	Version           int64  `json:"version"`
}

// SpaceTelegramInput is PUT /spaces/{id}/telegram. It replaces the settings: a nil field takes its default.
type SpaceTelegramInput struct {
	Enabled           *bool   `json:"enabled,omitempty"`
	ContentLevel      *string `json:"content_level,omitempty"`
	EnforcedApprovals *string `json:"enforced_approvals,omitempty"`
	Topics            *bool   `json:"topics,omitempty"`
	TerminalWatch     *bool   `json:"terminal_watch,omitempty"`
	// GroupChatID binds the forum supergroup (negative chat ID) to the space and 0 unbinds it; nil leaves the binding
	// as it is. Changing it needs telegram_group:write.
	GroupChatID *int64 `json:"group_chat_id,omitempty"`
}

func spaceTelegramPath(spaceID string) string { return "/spaces/" + esc(spaceID) + "/telegram" }

// GetSpaceTelegram is GET /spaces/{id}/telegram (the defaults, Stored false, when never saved).
func (c *Client) GetSpaceTelegram(ctx context.Context, spaceID string) (*SpaceTelegram, error) {
	var out SpaceTelegram
	return &out, c.get(ctx, spaceTelegramPath(spaceID), &out)
}

// PutSpaceTelegram is PUT /spaces/{id}/telegram: version 0 creates the settings, otherwise it replaces them.
func (c *Client) PutSpaceTelegram(ctx context.Context, spaceID string, version int64, in SpaceTelegramInput) (*SpaceTelegram, error) {
	var out SpaceTelegram
	return &out, c.put(ctx, spaceTelegramPath(spaceID), version, in, &out)
}

// DeleteSpaceTelegram is DELETE /spaces/{id}/telegram with If-Match; a 404 counts as success.
func (c *Client) DeleteSpaceTelegram(ctx context.Context, spaceID string, version int64) error {
	err := c.do2(ctx, http.MethodDelete, spaceTelegramPath(spaceID), version, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
