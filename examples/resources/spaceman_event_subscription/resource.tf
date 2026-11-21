data "spaceman_event_template" "github_issues" {
  slug = "github-issue"
}

resource "spaceman_event_subscription" "issues" {
  space_id            = spaceman_space.payments.id
  name                = "payments issues"
  connection_id       = spaceman_event_connection.pulse.id
  template_version_id = data.spaceman_event_template.github_issues.latest_version_id
  follow              = "latest"
  vars                = { account_key = "acme", repository = "acme/payments" }
}

# Ingest this SpaceMan's own run dialog events (needs SpaceMan 0.1.16 or later). ingest_own is accepted only when the
# template's subject is exactly evt.spaceman.fleet.run.dialog_detected or evt.spaceman.fleet.run.dialog_cleared.
resource "spaceman_event_subscription" "run_dialogs" {
  space_id      = spaceman_space.payments.id
  name          = "payments run dialogs"
  connection_id = spaceman_event_connection.pulse.id
  ingest_own    = true
  custom = jsonencode({
    subject = "evt.spaceman.fleet.run.dialog_detected"
    title   = "A run is stopped at a dialog"
    body    = "{{data.space_id}}"
  })
}

# Ingest every space's run dialog events instead (ingest_own_scope = "fleet", needs SpaceMan 0.1.17 or later). SpaceMan
# refuses it unless an installation administrator gave the space a grant: see spaceman_event_fleet_scope_grant.
resource "spaceman_event_subscription" "fleet_dialogs" {
  space_id         = spaceman_space.ops.id
  name             = "fleet run dialogs"
  connection_id    = spaceman_event_connection.pulse.id
  ingest_own       = true
  ingest_own_scope = "fleet"
  custom = jsonencode({
    subject = "evt.spaceman.fleet.run.dialog_detected"
    title   = "A run in any space is stopped at a dialog"
  })
}
