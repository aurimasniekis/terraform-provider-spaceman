# Telegram is opt-in per space: without this resource the space is invisible to the bot.
resource "spaceman_space_telegram" "payments" {
  space_id = spaceman_space.payments.id
  enabled  = true

  content_level      = "full"     # or "summary" (bodies only in the Mini App) or "off"
  enforced_approvals = "web_only" # or "confirm": decide enforced approvals in chat with a second confirmation
  topics             = true       # a topic of its own for users in topics mode
  terminal_watch     = false      # read-only terminal watching from the Mini App

  # Group mode (optional; needs the admin permission telegram_group:write): bind a forum supergroup the bot is in.
  # Left out, the binding is not managed; 0 unbinds.
  group_chat_id = -1001234567890
}
