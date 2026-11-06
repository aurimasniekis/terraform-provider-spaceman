# One key for many private repositories: SpaceMan generates it and keeps the private half; agents never receive it and
# Terraform only sees the public half. Add it to a read-only machine user (a key on a person's account can also push to
# everything that person can), here with the integrations/github provider while authenticated as that user.
resource "spaceman_git_ssh_key" "github_bot" {
  name        = "github-bot"
  description = "Read-only machine user for the payments repositories"
  spaces      = [spaceman_space.payments.id]
}

resource "github_user_ssh_key" "spaceman" {
  title = "SpaceMan"
  key   = spaceman_git_ssh_key.github_bot.public_key
}

resource "spaceman_space_repository" "settlements" {
  space_id   = spaceman_space.payments.id
  name       = "settlements"
  url        = "git@github.com:example-org/settlements.git"
  git_key_id = spaceman_git_ssh_key.github_bot.id
}
