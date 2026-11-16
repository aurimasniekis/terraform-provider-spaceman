# SpaceMan's own API as MCP tools for an infra space's control runs. The provider must use an API token issued by a
# user (not the root token): the server's internal token is owned by that user, with grants covered by theirs.
resource "spaceman_admin_mcp_server" "admin" {
  slug   = "sm-admin"
  name   = "SpaceMan admin"
  preset = "operate" # read and write tools; dangerous ones only by name (and always approved by a person)
  tools  = ["delete_schedule"]

  # The internal token's grants. Changing them (or expires_in_days) re-issues the token: a new token_id.
  grants = [
    { role = "operator", domain = spaceman_space.payments.id },
  ]
  approval        = "writes" # every write tool needs a person's approval too
  expires_in_days = 90
}

# Only granted roles' runs see the tools.
resource "spaceman_mcp_grant" "admin_control" {
  server_id = spaceman_admin_mcp_server.admin.id
  space_id  = spaceman_space.payments.id
  role      = "control"
}

output "admin_mcp_tools" {
  value = spaceman_admin_mcp_server.admin.effective_tools
}
