# Changelog

All notable changes to this provider are documented here. The format is based on
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed
- The provider now lives in its own repository, `github.com/aurimasniekis/terraform-provider-spaceman` (previously the
  nested module `github.com/aurimasniekis/go-spaceman/terraform-provider-spaceman`). Its history came along. The
  acceptance tests build SpaceMan from `SPACEMAN_SRC` or `../spaceman`.

### Added
- Everything developed in the SpaceMan repository: 30 resources and 7 data sources for SpaceMan's durable configuration
  (spaces and relocation, repositories, roles, accounts, catalogs, MCP, events, human-action types and policies,
  approval policies, knowledge bases, dreams, limits and retention, schedules, cross-space access, OIDC, users), write-only
  secrets with version companions, `If-Match` from private state, import for every resource. Not published to a registry.
