resource "spaceman_space_accounts" "payments" {
  space_id = spaceman_space.payments.id
  binding = [
    { account_id = spaceman_account.claude.id, default = true, priority = 10 },
    { account_id = spaceman_account.openai.id, priority = 20 },
  ]
}
