# Changelog

All notable changes to this provider are documented here. The format is based on
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

[Unreleased]: https://github.com/aurimasniekis/terraform-provider-spaceman/compare/v0.1.0-rc.3...HEAD
[0.1.0-rc.3]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.3
[0.1.0-rc.2]: https://github.com/aurimasniekis/terraform-provider-spaceman/releases/tag/v0.1.0-rc.2
