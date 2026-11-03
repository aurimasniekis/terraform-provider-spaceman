resource "spaceman_user" "ada" {
  email        = "ada@example.com"
  display_name = "Ada Lovelace"
  # Require a second factor even without operator or admin grants (policy may force it anyway: see mfa_enforced).
  mfa_required = true
}
