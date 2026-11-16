resource "spaceman_space_harbor_pilot" "payments" {
  space_id   = spaceman_space.payments.id
  enabled    = true
  project_id = "prj_payments" # a project the connection's client binding allows

  harnesses                    = ["claude-code", "codex"]
  ttl_seconds                  = 43200 # 12 h run tokens
  on_budget_status_unavailable = "proceed"
  allowed_model_aliases        = ["fleet/default"]

  # Enabling needs the connection.
  depends_on = [spaceman_harbor_pilot_connection.this]
}
