# One operations space may watch every space's run dialogs. Only an installation administrator can give this grant, so the
# provider needs an operator-level token (an API token with system:write). Without the grant, ingest_own_scope = "fleet"
# is refused with 403; revoking it later makes the subscription filter other spaces' events.
resource "spaceman_event_fleet_scope_grant" "ops" {
  space_id = spaceman_space.ops.id
  reason   = "Operations space that watches every run for stuck dialogs"
}

resource "spaceman_event_subscription" "all_dialogs" {
  space_id         = spaceman_space.ops.id
  name             = "all run dialogs"
  connection_id    = spaceman_event_connection.pulse.id
  ingest_own       = true
  ingest_own_scope = "fleet"
  custom = jsonencode({
    subject = "evt.spaceman.fleet.run.dialog_detected"
    title   = "A run in any space is stopped at a dialog"
  })

  depends_on = [spaceman_event_fleet_scope_grant.ops]
}
