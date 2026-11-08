# terraform-provider-spaceman

The Terraform provider for [SpaceMan](https://github.com/aurimasniekis/go-spaceman): it manages a central's durable
configuration through the administrative API (`/api/v1`). Go module `github.com/aurimasniekis/terraform-provider-spaceman`,
built on the HashiCorp [terraform-plugin-framework](https://developer.hashicorp.com/terraform/plugin/framework). Design
decisions are SpaceMan's ADR-0035 (phase 1), ADR-0041 (phase 2), ADR-0047 (this separate repository) and ADR-0050 (type
permissions, user profiles, the incident policy), in SpaceMan's
`docs/DECISIONS.md`. Until the move into this repository the provider was developed in SpaceMan's
`terraform-provider-spaceman/` directory; its history came along.

Status: **phases 1 and 2** of work package M5-01. Not published to a registry.

## What it manages

| Resource | API |
|---|---|
| `spaceman_node` | `/nodes` (returns the one-time enrollment token; revoke on destroy, opt-out) |
| `spaceman_space` | `/spaces` (archive on destroy, purge opt-in; a `node_id` change relocates through `/spaces/{id}/relocation`) |
| `spaceman_space_role` | `/spaces/{id}/roles/{name}` |
| `spaceman_space_accounts` | `/spaces/{id}/accounts` (the whole binding set) |
| `spaceman_space_repository` | `/spaces/{id}/repositories/{name}` (a configured repository; path changes replace it) |
| `spaceman_git_ssh_key` | `/git-ssh-keys/{id}` (a shared Git SSH key SpaceMan holds; `key_version` rotates it) |
| `spaceman_account` | `/accounts` (write-only `secret_wo`) |
| `spaceman_harness_profile` | `/harness-profiles` |
| `spaceman_role_template` | `/role-templates` |
| `spaceman_prompt` | `/prompts` (publishes immutable versions) |
| `spaceman_skill` | `/skills` (publishes immutable versions) |
| `spaceman_mcp_server` | `/mcp-servers` (write-only `headers_wo`) |
| `spaceman_mcp_grant` | `/mcp-servers/{id}/grants` |
| `spaceman_event_connection` | `/event-connections` (write-only `creds_wo`) |
| `spaceman_event_template` | `/event-templates`, `/event-template-versions/{id}/publish` |
| `spaceman_event_subscription` | `/spaces/{id}/subscriptions` |
| `spaceman_human_action_type` | `/human-types` (publishes immutable versions) |
| `spaceman_space_human_action_type` | `/spaces/{id}/human-types/{type}` |
| `spaceman_approval_policy` | `/approval-policies` |
| `spaceman_knowledge_base` | `/spaces/{id}/kbs` (disabled on destroy) |
| `spaceman_dream` | `/spaces/{id}/dreams` (disabled on destroy) |
| `spaceman_oidc_provider` | `/oidc/providers` (write-only `client_secret_wo`) |
| `spaceman_oidc_group_mapping` | `/oidc/providers/{id}/mappings` |
| `spaceman_space_limits` | `/spaces/{id}/limits` (only the configured overrides; removing one clears it; destroy clears the owned ones) |
| `spaceman_space_queue_policy` | `PUT /spaces/{id}/queue/policy` (the top-level task limit and the incident policy; kept on destroy) |
| `spaceman_schedule` | `/spaces/{id}/schedules` |
| `spaceman_space_xspace_settings` | `/spaces/{id}/xspace/settings` (reset to defaults on destroy) |
| `spaceman_xspace_peer` | `/spaces/{id}/xspace/peers/{peer_id}` (one side of a grant, owned whole) |
| `spaceman_human_action_policy` | `/human-policies/{type}`, `/spaces/{id}/human-policies/{type}` |
| `spaceman_human_action_type_permission` | `/spaces/{id}/human-type-permissions/{type}` (who may see, create and respond to a type; deleted on destroy) |
| `spaceman_user` | `/users`, `PUT /users/{id}/profile` (email, display name and `mfa_required` in place; disabled on destroy) |
| `spaceman_user_grants` | `PUT /users/{id}/grants` (the whole manual grant set) |
| `spaceman_user_identity` | `/users/{id}/identities` (expected OIDC identity) |
| `spaceman_harbor_pilot_connection` | `/harbor-pilot/connection` (singleton, import ID `harbor-pilot`; write-only `parent_token_wo` and `service_token_wo`) |
| `spaceman_space_harbor_pilot` | `/spaces/{id}/harbor-pilot` (a space's Harbor Pilot opt-in; removed on destroy) |

Data sources: `spaceman_node`, `spaceman_nodes`, `spaceman_space`, `spaceman_spaces`, `spaceman_event_template`,
`spaceman_space_limits` (effective values and sources), `spaceman_user`.

Never managed: runtime state (runs, tasks, inbox items, messages, schedule occurrences, cross-space requests,
human-action requests and responses, approvals, recordings), a node's connection and capacity, API tokens, password
reset tokens and SSO link invitations (shown once), passwords and MFA enrollment.

Not available: the backup target and schedule (configuration file only).

## Behaviour in brief

- **Ownership and drift (SM-API-005).** UI edits use the same API and show as drift for fields the configuration sets.
  Optional fields left out are not owned: their current value is kept and never shows as drift. `spaceman_space_limits`
  owns exactly the configured limits (removing one clears that override); authorization grants
  (`spaceman_xspace_peer`, `spaceman_user_grants`, `spaceman_space_accounts`) are owned whole.
- **Optimistic concurrency.** Object versions are kept in private state and sent as `If-Match`; a change made after the
  plan makes the apply fail with a conflict instead of being overwritten. Where the API has no usable `If-Match` (queue
  policy, user grants) the provider re-reads the object and refuses when it changed since the refresh.
- **Idempotent creates.** Creates send an `Idempotency-Key` where the API accepts one and retry safely on 502/503/504.
- **Write-only secrets.** Credentials are write-only arguments (Terraform 1.11+) with a `<name>_version` companion: bump it
  to rotate. They never reach a plan or the state.
- **Immutable versions.** Prompts, skills, event templates and human-action types publish one new version per content
  change and never re-publish an unchanged configuration.
- **No silent replacement.** Fields the API cannot change in place on objects it cannot delete (slugs, kinds, a prompt's
  name) are refused at plan time. Creating a cross-space peer configuration, a human-action policy or a human-action
  type permission that already exists fails instead of overwriting it (import it).
- **Relocation.** A change of a space's `node_id` is an in-place update that runs SpaceMan's safe relocation and waits for
  it (`relocation_dirty_state`, `relocation_stop_runs`, `relocation_recovery`, `relocation_retain_days`,
  `relocation_timeout`). Unpushed or uncommitted state is refused by default; the space stays paused on the new node.

The [registry documentation](docs/index.md) has the details of every resource.

## Using it locally

```sh
go build -o ~/.terraform.d/spaceman-dev/terraform-provider-spaceman .
cat > ~/.terraformrc-spaceman <<'EOF'
provider_installation {
  dev_overrides {
    "aurimasniekis/spaceman" = "/home/you/.terraform.d/spaceman-dev"
  }
  direct {}
}
EOF
export TF_CLI_CONFIG_FILE=~/.terraformrc-spaceman
export SPACEMAN_ENDPOINT=https://spaceman.example.com
export SPACEMAN_TOKEN=...   # an API token of an automation user
terraform -chdir=examples/complete plan
```

## Development

| Target | What it does |
|---|---|
| `make test` | `go vet` and unit tests (client, schema validity, conversions, plan modifiers, docs drift). No SpaceMan needed. |
| `make lint` | golangci-lint with this repository's configuration. |
| `make check` | `test` and `lint`. |
| `make acc` | Acceptance tests (`TF_ACC=1`) against a real local central (see below). Needs Go, Terraform ≥ 1.11 and a SpaceMan checkout. |
| `make acc-harbor-pilot` | The Harbor Pilot acceptance tests, including the combined example, against a real `harbor-pilot serve --demo` (see below). |
| `make docs` | Regenerates `docs/` from the schemas and `examples/`. |

The acceptance harness (`internal/provider/harness_test.go`) builds `spaceman` and `devpulse` from a SpaceMan checkout:
`SPACEMAN_SRC` when set, otherwise `../spaceman` next to this repository. A provider change that needs a new SpaceMan API
therefore needs the matching SpaceMan checkout. It initializes a KEK and a central in a fresh temporary directory, bootstraps it (root token generated at run time, never
printed), starts `spaceman serve` on a random loopback port, registers two nodes and runs every `TestAcc*` test: create,
update, import, drift from direct API edits, write-only secret rotation, version publication without perpetual diffs, and
a `node_id` change planned in place and sent to SpaceMan as a relocation (refused there: the harness runs no node agents), and
for phase 2 ownership of only the configured limit overrides, schedules, both sides of a cross-space grant, human-action
policies, users with grants and linked identities. It stops only its own processes and removes only its own directory (`SM_TF_KEEP=1` keeps
it for debugging). Nothing in the tests talks to a real model, Pulse deployment or identity provider.

The Harbor Pilot tests (`TestAccHarborPilot*`) are opt-in: with `SPACEMAN_HARBOR_PILOT=1` they also start a real
`harbor-pilot serve --demo` (v0.1.3) on a loopback port, from `HARBOR_PILOT_BIN` or else installed with `go install`
into the user cache directory, and provision a client binding, a service principal and parent tokens through its demo
administrator. `TestAccHarborPilotExample` builds the `harborpilot` provider (v0.1.1, fetched with `go mod download`, or
`HARBORPILOT_PROVIDER_SRC`), points both providers at local builds through `dev_overrides`, and applies
`examples/harbor-pilot` (Harbor Pilot's scenario 07 plus the SpaceMan side), plans again (must be empty), checks that no
Harbor Pilot token is in the state and destroys it. The demo's model upstreams are synthetic: no provider credentials
are involved. Run them with `make acc-harbor-pilot`.
