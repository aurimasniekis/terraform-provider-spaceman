resource "spaceman_harness_profile" "careful" {
  slug     = "careful"
  name     = "Careful Claude Code"
  harness  = "claude-code"
  model    = "claude-sonnet-4-5"
  settings = { permission_mode = "default" }
}

# Claude Code settings.json keys for the profile's runs: any keys. "$defaults" keeps
# Claude Code's built-in rules in an auto mode list.
resource "spaceman_harness_profile" "auto" {
  slug     = "auto"
  name     = "Claude Code in auto mode"
  harness  = "claude-code"
  settings = { permission_mode = "auto" }

  claude_settings = jsonencode({
    autoMode = {
      environment = ["$defaults", "Source control: GitHub repositories of example-org, reached through the gateway host."]
      allow       = ["$defaults", "Pushing feature branches to the space's repositories is allowed; main only through pull requests."]
    }
    permissions = {
      ask  = ["Bash(git push --force:*)"]
      deny = ["Read(./secrets/**)"]
    }
    env = { GIT_TERMINAL_PROMPT = "0" }
  })
}
