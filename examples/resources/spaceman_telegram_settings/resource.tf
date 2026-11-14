# Every attribute is optional: only what you set is stored, and an attribute left out (or taken out later) follows
# SpaceMan's built-in default, which the values below are.
resource "spaceman_telegram_settings" "this" {
  # Who may link, and for how long.
  link_default_max_role = "operator" # or "viewer": the most a Telegram session may do in any space
  link_allow_admin_cap  = false
  link_sso_grants       = false
  link_reconfirm_days   = 90
  links_per_user        = 1
  link_code_ttl_seconds = 600

  # Mini App lifetimes.
  init_data_max_age_seconds        = 300
  miniapp_session_idle_seconds     = 900
  miniapp_session_absolute_seconds = 3600

  # Whether a new link starts with a topic per space.
  topics_mode_default = false

  # Outbound limits, at or below Telegram's published limits.
  send_limits = {
    global_per_second = 25
    chat_per_second   = 1
    chat_burst        = 3
    group_per_minute  = 20
    queue_total       = 5000
    queue_per_chat    = 50
  }

  # Inbound abuse limits.
  inbound_limits = {
    actions_per_minute   = 30
    actions_burst        = 10
    inbox_items_per_hour = 60
    downloads_per_hour   = 50
    updates_per_second   = 20
  }

  # What each link notifies about until its user changes it. Times are wall-clock times in each
  # user's own time zone.
  notification_defaults = {
    new_actions       = true
    min_priority      = "normal"
    assigned_to_me    = true
    escalations       = true
    reminders         = "push" # push, digest or off
    clarifications    = true
    operator_messages = true
    inbox_items       = "digest"
    tasks_in_review   = "digest"
    failed_runs       = "digest"
    digest            = true
    digest_time       = "09:00"
    quiet_hours       = true
    quiet_hours_start = "23:00"
    quiet_hours_end   = "08:00"
  }
}
