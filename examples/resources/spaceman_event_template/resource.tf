resource "spaceman_event_template" "deploy_failed" {
  slug = "deploy-failed"
  name = "Failed deploys"
  definition = jsonencode({
    subject = "evt.deploy.{{vars.env}}.>"
    variables = [
      { name = "env", required = true, example = "prod" },
      { name = "service", description = "Only this service; empty for every service" },
      { name = "include_rollbacks", pattern = "true", description = "Set to true to include rollbacks" },
    ]
    filters = [
      { path = "data.status", op = "eq", value = "failed" },
      # Optional filters: applied only when the subscription sets `service` / leaves `include_rollbacks` empty.
      { path = "data.service", op = "eq", value = "{{vars.service}}", if_set = "service" },
      { path = "data.rollback_of", op = "absent", unless_set = "include_rollbacks" },
    ]
    title   = "Deploy to {{vars.env}} failed"
    body    = "{{data.message}}"
    urgency = "high"
  })
}
