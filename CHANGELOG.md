# Changelog

All notable changes to this provider are documented here. The format is based on
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- `spaceman_admin_mcp_server`: a SpaceMan MCP server (SpaceMan ADR-0063), SpaceMan's own API offered as MCP tools to a
  space's control and worker runs, granted with the existing `spaceman_mcp_grant`. Inputs: `slug` (replaces; `spaceman`
  is refused at plan time), `name`, `grants`, `preset` (default `admin`), `tools`, `approval` (default `none`; dangerous
  tools are always gated by SpaceMan), `expires_in_days` / `never_expires` (default never), `enabled` and the limits.
  Computed: `token_id`, `owner_user_id`, `expires_at`, `token_state`, `effective_tools` and `version`. It has no secret
  attributes: the server acts through a secretless internal token. A `grants` or expiry change re-issues the token in
  place (a new `token_id`, the old one revoked); every other change is an in-place edit, and destroy revokes the token.
  The provider must use an API token issued by a user (not the root token). `spaceman_mcp_server` now refuses a
  SpaceMan MCP server (on refresh and import) and points to this resource, which likewise refuses external (http)
  servers. Needs the SpaceMan release that adds SpaceMan MCP servers.
- Harbor Pilot gateway (SpaceMan ADR-0060). Needs the SpaceMan release that adds the Harbor Pilot gateway (after
  0.1.0-rc.9); configurations without these resources work with older servers.
  - `spaceman_harbor_pilot_connection`: central's connection to a Harbor Pilot gateway (0.1.3 or later), a singleton
    imported with the ID `harbor-pilot`. The parent token (`tokens:mint`) and the optional service-principal token are
    write-only (`parent_token_wo` / `service_token_wo` with `*_wo_version`): they never reach a plan or the state, and
    fit Harbor Pilot's ephemeral token resources. A changed `base_url`, `run_base_url` or newly allowed plain http sends
    the tokens again, as SpaceMan requires; the stored service token is removed only by a `service_token_wo_version`
    change, never by a move alone. Updates and deletes send `If-Match`.
  - `spaceman_space_harbor_pilot`: a space's opt-in, with `enabled`, `project_id`, `harnesses`, `ttl_seconds`,
    `on_budget_status_unavailable` and `allowed_model_aliases`, imported by space ID. Setting or changing `project_id`
    needs `account:write` in SpaceMan.
  - `examples/harbor-pilot`: both sides in one configuration, following Harbor Pilot's scenario 07 with its `harborpilot`
    provider (0.1.1 or later): a binding that pins only the fleet, a blocking budget and project per space, tokens from
    ephemeral resources, and the `harbor-pilot-budget` event subscriptions. It is acceptance-tested against a real
    `harbor-pilot serve --demo` (`make acc-harbor-pilot`): apply, an empty second plan, no token in the state, destroy.
- Event template filters can be optional (SpaceMan ADR-0061): `if_set` / `unless_set` name a variable, and the filter
  applies only when that variable has a value (`if_set`) or has none (`unless_set`). They work in
  `spaceman_event_template.definition` and `spaceman_event_subscription.custom`, together with the new `absent` op.
  The definition validator now refuses an unknown `op` and an `if_set` / `unless_set` naming an undefined variable at
  plan time. Needs SpaceMan 0.1.0-rc.9 or later; definitions without them work with older servers.
- `data.spaceman_event_template.definition`: the latest version's definition as canonical JSON, so the variables and
  filters of a built-in can be read with `jsondecode`. SpaceMan 0.1.0-rc.9 adds the built-ins `github-pr-comment`,
  `github-issue-comment`, `github-pr-review`, `github-pr-review-comment`, `github-workflow-run` and
  `github-check-suite`.

### Fixed
- A definition using `if_set` or `unless_set` was refused as "not a template definition: json: unknown field", so
  custom definitions with optional filters couldn't be managed.

## [0.1.0-rc.9] - 2026-11-07

Ninth release candidate, released with SpaceMan 0.1.0-rc.9: Claude Code settings on harness profiles.

### Added
- `spaceman_harness_profile.claude_settings`: Claude Code `settings.json` keys for the profile's runs, as a JSON object
  (`jsonencode({...})`), any keys such as `permissions`, `autoMode`, `env` or `hooks` (SpaceMan ADR-0059). The
  server's key order never causes a diff, and removing the attribute clears them. Needs SpaceMan 0.1.0-rc.9 or later; a
  configuration without it works with older servers.
- `spaceman_harness_profile.claude_settings` warns (without failing) when an `env` key looks like a credential or
  endpoint override: `ANTHROPIC_*`, `CLAUDE_CODE_*` or `*_PROXY`.

### Fixed
- `spaceman_harness_profile.claude_settings = jsonencode({})` failed on apply with "Provider produced inconsistent result
  after apply … `.claude_settings`: was `"{}"`, but now null". The state now keeps the configured `{}`, which clears the
  settings in SpaceMan, and a second plan is empty. SpaceMan stores `{}` as no settings, so an import reads it as unset.
- `spaceman_space_role` and `spaceman_role_template`: a skill listed without `pin` failed on apply with "Provider
  produced inconsistent result after apply … `.skills[0].pin`: was null, but now `""`". An omitted pin now stays null
  (it follows the latest version); `pin = ""` still works and existing state plans no change.

## [0.1.0-rc.8] - 2026-11-06

Eighth release candidate, released with SpaceMan 0.1.0-rc.8: shared Git SSH keys.

### Added
- `spaceman_git_ssh_key`: a shared Git SSH key SpaceMan generates and holds (SpaceMan ADR-0058), for the spaces it lists
  or every space, rotated by changing `key_version`; its `public_key` goes on a Git account, preferably a read-only
  machine user. `spaceman_space_repository.git_key_id` clones a repository with it. Needs SpaceMan 0.1.0-rc.8 or later.

## [0.1.0-rc.7] - 2026-11-06

Seventh release candidate, released with SpaceMan 0.1.0-rc.7: security updates only.

### Security
- Updated `golang.org/x/net` (0.62.0), `google.golang.org/grpc` (1.85.0) and `golang.org/x/text` (0.43.0). govulncheck
  reported nine vulnerabilities that the provider's code reaches through them (GO-2026-6617, -6612, -6611, -6603,
  -6443, -6348, -6061, -5970, -5026). CI now runs govulncheck.

## [0.1.0-rc.6] - 2026-11-05

Sixth release candidate, released with SpaceMan 0.1.0-rc.6.

### Added
- `spaceman_space_repository` manages repository SSH keys held by SpaceMan (SpaceMan ADR-0057):
  - `ssh_key = true` generates one, and `ssh_key = false` removes it; left unset, the key isn't managed;
  - changing `ssh_key_version` rotates it;
  - `ssh_public_key` and `ssh_key_fingerprint` feed a deploy-key resource. The private key never reaches Terraform;
  - `known_hosts` pins the Git host's SSH keys.

  It needs SpaceMan 0.1.0-rc.6 or later.

## [0.1.0-rc.5] - 2026-11-04

Fifth release candidate, released with SpaceMan 0.1.0-rc.5 (there is no provider 0.1.0-rc.4: the numbers follow
SpaceMan's). The new attributes need SpaceMan 0.1.0-rc.5 or later.

### Added
- `spaceman_node` registers a node and returns its one-time `enrollment_token` (sensitive) for the host's provisioning.
  It edits the node's name, description and labels in place (SpaceMan's new `PUT /nodes/{id}`). `enabled` disables and
  enables the node, and changing `enrollment_version` issues a new token. Destroying it revokes the node unless
  `revoke_on_destroy = false`. It needs SpaceMan 0.1.0-rc.5 or later.
- `spaceman_space_limits` manages `control_autostart` and `control_idle_stop_minutes`: whether SpaceMan starts the
  space's control for waiting work, and when it stops an idle control (SpaceMan ADR-0056). It also manages
  `event_depth`, which SpaceMan spaces could already override but the provider did not expose.

## [0.1.0-rc.3] - 2026-11-04

Third release candidate, the first for the Terraform Registry: a release workflow (GoReleaser) builds the zips, the
registry manifest and SHA256SUMS, and signs SHA256SUMS with the GPG key registered in the Registry. No provider changes
since 0.1.0-rc.2.

### Added
- `.github/workflows/release.yml`, `.goreleaser.yml` and `terraform-registry-manifest.json` (plugin protocol 6.0).

## [0.1.0-rc.2] - 2026-11-04

Second release candidate, released alongside SpaceMan 0.1.0-rc.2 (which fixes Codex runs with an OpenAI API key).
No provider changes since 0.1.0-rc.1, whose unpublished draft was withdrawn.

## [0.1.0-rc.1] - 2026-11-04

First release candidate (pre-release; not production-ready, see SpaceMan's docs/RELEASE_READINESS.md).

### Changed
- `spaceman_user`: `email` and `display_name` are updated in place through `PUT /users/{id}/profile` instead of being
  refused at plan time. Changing a password user's email ends that user's sessions in SpaceMan.
- The provider now lives in its own repository, `github.com/aurimasniekis/terraform-provider-spaceman` (previously the
  nested module `github.com/aurimasniekis/go-spaceman/terraform-provider-spaceman`). Its history came along. The
  acceptance tests build SpaceMan from `SPACEMAN_SRC` or `../spaceman`.

### Added
- Everything developed in the SpaceMan repository: 30 resources and 7 data sources for SpaceMan's durable configuration
  (spaces and relocation, repositories, roles, accounts, catalogs, MCP, events, human-action types and policies,
  approval policies, knowledge bases, dreams, limits and retention, schedules, cross-space access, OIDC, users), write-only
  secrets with version companions, `If-Match` from private state, import for every resource. Not published to a registry.
- `spaceman_human_action_type_permission`: who may see, create and respond to a human-action type in a space, and which
  agents may ask with it (SpaceMan SM-HUI-008, ADR-0050). Built-in types and custom types (by ID); an existing
  permission is refused on create (import it); destroy restores the defaults. `TestAccHumanTypePermissions`.
- `spaceman_user`: `mfa_required` (the administrator's setting) and computed `mfa_enforced` (the requirement in force);
  the user data source exposes both.
- `spaceman_space_queue_policy`: the incident policy (`incident_triggers`, `incident_untrusted`, `incident_workers`,
  `incident_capacity`, `incident_resume`; SpaceMan SM-QUEUE-009).
- `spaceman_account`: the kind `codex_chatgpt_login` (a Codex ChatGPT login: `secret_wo` is the `auth.json` that
  `codex login` wrote; SpaceMan SM-ACCT-002, ADR-0053). `TestAccCodexChatGPTAccount`.
- `spaceman_event_subscription`: `while_paused` (`defer`, the default, `wake` or `stop`), what the subscription does while
  its space is paused (SpaceMan SM-EVT-014, ADR-0049).
- `spaceman_human_action_policy`: `response_inbox` (`off`, `routed` or `always`), whether a response also becomes an
  inbox item (SpaceMan SM-INBOX-001, ADR-0049). Without the attribute, the provider's writes reset the policy to `off`.

[Unreleased]: https://github.com/aurimasniekis/terraform-provider-spaceman/compare/v0.1.0-rc.9...HEAD
[0.1.0-rc.9]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.9
[0.1.0-rc.8]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.8
[0.1.0-rc.7]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.7
[0.1.0-rc.6]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.6
[0.1.0-rc.5]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.5
[0.1.0-rc.3]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.3
[0.1.0-rc.2]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.2
