# The API service, cloned by the space's node as soon as it holds the lease. The node's own Git authentication (SSH key,
# credential helper, Hallpass Git role) is used; the URL carries no credentials.
resource "spaceman_space_repository" "api" {
  space_id       = spaceman_space.payments.id
  name           = "api"
  path           = "repos/api"
  url            = "git@github.com:example-org/payments-api.git"
  default_branch = "main"
  description    = "Settlement and refunds API"
}

# A knowledge base's repository, cloned the first time a KB session needs it.
resource "spaceman_space_repository" "kb" {
  space_id     = spaceman_space.payments.id
  name         = "payments-kb"
  path         = "repos/payments-kb"
  url          = "https://github.com/example-org/payments-kb.git"
  role         = "kb"
  clone_policy = "on_demand"
}

# A private repository cloned with an SSH key SpaceMan holds: agents never receive it, and Terraform only sees the
# public half, which becomes a read-only deploy key on GitHub (integrations/github provider). github.com's host keys are
# built in; other hosts need known_hosts.
resource "spaceman_space_repository" "ledger" {
  space_id = spaceman_space.payments.id
  name     = "ledger"
  url      = "git@github.com:example-org/ledger.git"
  ssh_key  = true
}

resource "github_repository_deploy_key" "ledger" {
  repository = "ledger"
  title      = "SpaceMan (payments)"
  key        = spaceman_space_repository.ledger.ssh_public_key
  read_only  = true
}
