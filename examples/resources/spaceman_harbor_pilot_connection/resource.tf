# Tokens issued by Harbor Pilot. Pass them as ephemeral values (or from Harbor Pilot's own ephemeral
# harborpilot_gateway_token and harborpilot_service_principal_token resources): Terraform never stores them.
variable "harbor_pilot_parent_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

variable "harbor_pilot_service_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "spaceman_harbor_pilot_connection" "this" {
  base_url = "https://harbor-pilot.example.com"

  # A gateway credential with gateway:invoke, gateway:models and tokens:mint.
  parent_token_wo         = var.harbor_pilot_parent_token
  parent_token_wo_version = 1

  # Optional: a service-principal token with usage.read and config.read (budget status, project checks).
  service_token_wo         = var.harbor_pilot_service_token
  service_token_wo_version = 1
}
