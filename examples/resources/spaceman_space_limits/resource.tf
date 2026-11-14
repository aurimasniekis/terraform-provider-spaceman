resource "spaceman_space_limits" "payments" {
  space_id = spaceman_space.payments.id

  # Only these limits are Terraform's; overrides of other limits set in the UI are kept.
  max_concurrent_runs    = 4
  max_tool_calls_per_run = 2000
  task_deadline_minutes  = 240
  budget_tokens_24h      = 20000000
  budget_warn_percent    = 75

  # New runs' terminals are held at 200x50 (both must be set; 0 is the node's default). Needs terminal:takeover.
  terminal_cols = 200
  terminal_rows = 50

  on_lower = "drain"
}
