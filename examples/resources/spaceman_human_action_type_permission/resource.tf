# Only operators see approvals in the payments space, only admins decide them, and only deployer agents may ask.
resource "spaceman_human_action_type_permission" "approvals" {
  space_id     = spaceman_space.payments.id
  action_type  = "approval"
  view_role    = "operator"
  respond_role = "admin"
  agents       = "roles"
  agent_roles  = ["deployer"]
}

# A custom type: name it by its ID. Only admins may ask people with it; agents may not.
resource "spaceman_human_action_type_permission" "deploy_window" {
  space_id    = spaceman_space.payments.id
  action_type = spaceman_human_action_type.deploy_window.id
  create_role = "admin"
  agents      = "none"
}
