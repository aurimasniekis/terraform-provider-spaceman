# Changelog

All notable changes to this provider are documented here. The format is based on
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

[Unreleased]: https://github.com/aurimasniekis/terraform-provider-spaceman/compare/v0.1.0-rc.7...HEAD
[0.1.0-rc.7]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.7
[0.1.0-rc.6]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.6
[0.1.0-rc.5]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.5
[0.1.0-rc.3]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.3
[0.1.0-rc.2]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.2
