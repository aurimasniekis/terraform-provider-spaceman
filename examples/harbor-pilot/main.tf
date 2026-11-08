# SpaceMan runs through a Harbor Pilot gateway, both sides in one configuration.
#
# The Harbor Pilot half follows Harbor Pilot's scenario 07 (SpaceMan integration) from its Terraform provider: SpaceMan
# is one client among others, with a client binding, a parent gateway token holding tokens:mint and a read-only service
# principal. The SpaceMan half connects central to the gateway and opts each space in with its own project, so each run
# gets a short-lived child token bound to its space, run and project in place of a harness account.
#
# No token is kept in a plan or in either provider's state: both Harbor Pilot tokens come from ephemeral resources and
# reach SpaceMan through write-only arguments. Terraform opens ephemeral resources on every plan and apply, so each run
# issues new tokens, but SpaceMan only receives them when a *_wo_version changes (bump it to rotate, before ttl_seconds
# runs out). With revoke_on_close = false the unused ones stay valid until they expire.
#
# Needs SpaceMan with the Harbor Pilot gateway (ADR-0060), Harbor Pilot 0.1.3 or later and its provider 0.1.1 or later.
#
#   export SPACEMAN_ENDPOINT=https://spaceman.example.com SPACEMAN_TOKEN=…  # account:write (the connection, projects)
#   export HP_SERVER=https://harbor-pilot.example.com HP_API_TOKEN=…        # a Harbor Pilot admin API token
#   terraform apply -var node_name=build-01 -var harbor_pilot_url=$HP_SERVER

terraform {
  required_version = ">= 1.11"
  required_providers {
    spaceman = {
      source = "aurimasniekis/spaceman"
    }
    harborpilot = {
      source  = "aurimasniekis/harbor-pilot"
      version = ">= 0.1.1"
    }
  }
}

provider "spaceman" {}    # SPACEMAN_ENDPOINT and SPACEMAN_TOKEN
provider "harborpilot" {} # HP_SERVER and HP_API_TOKEN (or HP_API_TOKEN_FILE)

variable "prefix" {
  type    = string
  default = "spaceman"
}

variable "spaces" {
  description = "Spaces whose runs go through Harbor Pilot, each charged to its own project."
  type        = set(string)
  default     = ["payments", "platform"]
}

variable "node_name" {
  description = "The SpaceMan node the spaces run on."
  type        = string
}

variable "harbor_pilot_url" {
  description = "The gateway origin as SpaceMan central reaches it."
  type        = string
}

variable "harbor_pilot_run_url" {
  description = "The gateway origin as runs reach it, when it differs (null: harbor_pilot_url)."
  type        = string
  default     = null
}

variable "allow_insecure_http" {
  description = "Plain http to the gateway (tests and trusted local networks only)."
  type        = bool
  default     = false
}

variable "harbor_pilot_tokens_version" {
  description = "Bump to hand SpaceMan freshly issued Harbor Pilot tokens (rotation)."
  type        = number
  default     = 1
}

variable "pulse_url" {
  description = "A Pulse server Harbor Pilot publishes its events to; null skips the budget event subscriptions."
  type        = string
  default     = null
}

variable "pulse_creds" {
  description = "SpaceMan's Pulse credentials (a .creds file's content), with pulse_url."
  type        = string
  default     = null
  sensitive   = true
  ephemeral   = true
}

# ---- Harbor Pilot (scenario 07) ----

resource "harborpilot_workspace" "fleet" {
  name = "${var.prefix}-fleet"
  spec = jsonencode({ display_name = "Agent fleet" })
}

resource "harborpilot_project" "space" {
  for_each = var.spaces
  name     = "${var.prefix}-${each.key}"
  spec = jsonencode({
    workspace_id = harborpilot_workspace.fleet.id
    display_name = "Space ${each.key}"
  })
}

resource "harborpilot_routing_plan" "fleet" {
  name = "${var.prefix}-plan"
  spec = jsonencode({ display_name = "Fleet runs" })
}

resource "harborpilot_provider" "api" {
  name = "${var.prefix}-anthropic"
  spec = jsonencode({
    type   = "anthropic"
    models = [{ id = "claude-sonnet-4-5", display_name = "Claude Sonnet 4.5", context_window = 200000, max_output_tokens = 64000, capabilities = ["tools", "streaming"] }]
  })
}

resource "harborpilot_provider_account" "api" {
  name = "${var.prefix}-anthropic-key"
  spec = jsonencode({
    provider_id    = harborpilot_provider.api.id
    billing        = "metered_api"
    credential_ref = "env:HP_SECRET_ANTHROPIC_API_KEY" # read by Harbor Pilot, never by Terraform
    enabled        = true
  })
}

resource "harborpilot_pool" "fleet" {
  name = "${var.prefix}-fleet"
  spec = jsonencode({ members = [{ account_id = harborpilot_provider_account.api.id, model = "claude-sonnet-4-5", priority = 1, weight = 1 }] })
}

resource "harborpilot_model_alias" "fleet" {
  name = "${var.prefix}/default"
  spec = jsonencode({ pool_id = harborpilot_pool.fleet.id })
}

resource "harborpilot_routing_plan_version" "fleet" {
  plan_id = harborpilot_routing_plan.fleet.id
  document = jsonencode({
    model_mappings   = [{ match = "*", alias_id = harborpilot_model_alias.fleet.id }]
    default_alias_id = harborpilot_model_alias.fleet.id
    fallback         = { steps = ["retry", "stop"], max_attempts = 2 }
  })
}

# The orchestrator's read-only control-plane identity: budget status (usage.read) and project checks (config.read).
resource "harborpilot_service_principal" "spaceman" {
  name        = "${var.prefix}-orchestrator"
  description = "SpaceMan fleet orchestrator"
  permissions = ["config.read", "usage.read", "activity.read"]
}

resource "harborpilot_client_binding" "spaceman" {
  name = "${var.prefix}-runner"
  spec = jsonencode({
    display_name = "SpaceMan runs"
    owner_kind   = "service"
    owner_id     = harborpilot_service_principal.spaceman.id
    plan_id      = harborpilot_routing_plan.fleet.id
    # Runs may select any space's project; the binding default covers runs without one.
    project_id          = harborpilot_project.space[sort(tolist(var.spaces))[0]].id
    allowed_project_ids = [for p in harborpilot_project.space : p.id]
    limits              = { max_concurrency = 16, requests_per_minute = 600 }
    trust               = { session_hints = true, project_hints = true }
    # Pin only the fleet. A spaceman.space here would bind every run to one space; SpaceMan binds each run's own
    # space (and run, instance, session and project) into its child token instead.
    external_refs = { "spaceman.fleet" = var.prefix }
    enabled       = true
  })
  depends_on = [harborpilot_routing_plan_version.fleet]
}

# A blocking daily budget per space keeps one space from exhausting the fleet's spend. SpaceMan refuses a launch early
# when a blocking budget covering the space is used up.
resource "harborpilot_budget" "space" {
  for_each = harborpilot_project.space
  name     = "${var.prefix}-${each.key}-daily"
  spec = jsonencode({
    scope       = "project"
    scope_id    = each.value.id
    period      = "day"
    limit       = "25"
    currency    = "USD"
    enforcement = "block"
    enabled     = true
  })
}

# The parent token SpaceMan mints run tokens from. It pins only the fleet; each child binds its own space and one of
# the allowed projects.
ephemeral "harborpilot_gateway_token" "spaceman_parent" {
  binding_id      = harborpilot_client_binding.spaceman.id
  name            = "spaceman-parent"
  scopes          = ["gateway:invoke", "gateway:models", "tokens:mint"]
  bound_refs      = { "spaceman.fleet" = var.prefix }
  ttl_seconds     = 30 * 24 * 3600
  revoke_on_close = false
}

# The orchestrator's API token (hpa_…).
ephemeral "harborpilot_service_principal_token" "spaceman" {
  service_principal_id = harborpilot_service_principal.spaceman.id
  name                 = "spaceman-orchestrator"
  ttl_seconds          = 30 * 24 * 3600
  revoke_on_close      = false
}

# ---- SpaceMan ----

data "spaceman_node" "fleet" {
  name = var.node_name
}

resource "spaceman_harbor_pilot_connection" "this" {
  base_url            = var.harbor_pilot_url
  run_base_url        = var.harbor_pilot_run_url
  allow_insecure_http = var.allow_insecure_http

  parent_token_wo          = ephemeral.harborpilot_gateway_token.spaceman_parent.token
  parent_token_wo_version  = var.harbor_pilot_tokens_version
  service_token_wo         = ephemeral.harborpilot_service_principal_token.spaceman.token
  service_token_wo_version = var.harbor_pilot_tokens_version
}

resource "spaceman_space" "space" {
  for_each = var.spaces
  slug     = "${var.prefix}-${each.key}"
  name     = "Space ${each.key}"
  node_id  = data.spaceman_node.fleet.id
}

resource "spaceman_space_harbor_pilot" "space" {
  for_each   = var.spaces
  space_id   = spaceman_space.space[each.key].id
  enabled    = true
  project_id = harborpilot_project.space[each.key].id

  harnesses                    = ["claude-code", "codex"]
  ttl_seconds                  = 12 * 3600
  on_budget_status_unavailable = "proceed"
  allowed_model_aliases        = [harborpilot_model_alias.fleet.name]

  # Enabling needs the connection; destroying removes these first, so the connection can go.
  depends_on = [spaceman_harbor_pilot_connection.this]
}

# Harbor Pilot's budget events for each space's project, as inbox items (needs Harbor Pilot's Pulse event destination).
resource "spaceman_event_connection" "pulse" {
  count            = var.pulse_url == null ? 0 : 1
  name             = "${var.prefix}-harbor-pilot-events"
  url              = var.pulse_url
  creds_wo         = var.pulse_creds
  creds_wo_version = 1
}

data "spaceman_event_template" "budget" {
  slug = "harbor-pilot-budget"
}

resource "spaceman_event_subscription" "budget" {
  for_each            = var.pulse_url == null ? toset([]) : var.spaces
  space_id            = spaceman_space.space[each.key].id
  name                = "Harbor Pilot budget"
  connection_id       = spaceman_event_connection.pulse[0].id
  template_version_id = data.spaceman_event_template.budget.latest_version_id
  follow              = "latest"
  vars                = { project_id = harborpilot_project.space[each.key].id }
}

output "project_ids" {
  description = "The Harbor Pilot project of each space."
  value       = { for k, p in harborpilot_project.space : k => p.id }
}

output "binding_id" {
  value = harborpilot_client_binding.spaceman.id
}
