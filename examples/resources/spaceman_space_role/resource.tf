resource "spaceman_space_role" "implementor" {
  space_id     = spaceman_space.payments.id
  name         = "implementor-backend"
  template_id  = spaceman_role_template.worker.id
  model        = "claude-sonnet-4-5"
  instructions = "Work in small, reviewed steps."
  skills = [
    { skill_id = spaceman_skill.release_notes.id },
  ]
}

# Renaming a role is an in-place update (SpaceMan 0.1.11 or later): change `name` and apply. Schedules, knowledge bases,
# dreams and human permissions that reference `spaceman_space_role.implementor.name` follow in the same apply.
# To also rename the resource address, add a `moved` block; nothing is destroyed:
#
# moved {
#   from = spaceman_space_role.implementor
#   to   = spaceman_space_role.implementor_backend
# }
