# The token BotFather gave. Pass it as an ephemeral value: Terraform never stores it.
variable "telegram_bot_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "spaceman_telegram_bot" "this" {
  bot_token_wo         = var.telegram_bot_token
  bot_token_wo_version = 1 # change it to send the token again (rotation)

  enabled = true
  mode    = "webhook" # or "polling"

  # Optional: the Mini App's own host (not central's public host, no port or path). It takes effect only
  # when central's telegram.miniapp_host is set to the same host.
  miniapp_host = "tg.central.example.com"

  # Optional: only accept webhook calls from Telegram's ranges. Safe only when the
  # client address behind your proxy is trustworthy.
  webhook_ip_allowlist = ["149.154.160.0/20", "91.108.4.0/22"]
}
