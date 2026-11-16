resource "spaceman_space_role_pool" "worker" {
  space_id = spaceman_space.payments.id
  role     = "worker"
  account = [
    { account_id = spaceman_account.claude_a.id, priority = 10 },
    { account_id = spaceman_account.claude_b.id, priority = 20 },
  ]
}
