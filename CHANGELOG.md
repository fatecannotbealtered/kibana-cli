# Changelog

All notable changes to this project will be documented in this file.

## [1.0.1] - 2026-05-26

### Fixed

- `KIBANA_CLI_TIMEOUT` env var now applies when `--timeout` is not passed on the CLI
- `auth status --json`: `ok` requires valid Kibana host (not only non-empty credentials)
- Partial `KIBANA_CLI_*` auth env vars are rejected; host, user, and password must be set together
- `context` / `doctor` emit full Agent JSON envelope on config validation errors
- Search probe errors (`context canceled`, deadline, EOF) map to network/unknown instead of forbidden
- Local config/keyring I/O failures use exit code `2` instead of `7`
- `--dry-run` with `--data-view` makes no Kibana API calls (placeholder index in preview)
- `search --json` includes `totalRelation`; queries use `track_total_hits` for accurate totals
- npm `postinstall` requires checksum verification by default (`KIBANA_CLI_SKIP_CHECKSUM=1` to opt out)

### Changed

- CLI help: `--query` documents default `match_phrase` vs Lucene with `--all-fields`; `--force` documents `config init` overwrite
- Documentation aligned (README, SKILL, SECURITY): dry-run behavior, PowerShell env examples, `context` status values

### Added

- 100% statement coverage on all Go packages; `make coverage` / `scripts/coverage.ps1` gate
- Extensive unit tests across `cmd`, `internal/*`, and `cmd/kibana-cli`

## [1.0.0] - 2026-05-24

First release: **Kibana-only log query CLI** for humans and AI Agents.

### Features

- Log search and terms aggregation via **Kibana Console Proxy** (HTTP Basic only)
- OS credential store by default; env vars for CI/agents (`KIBANA_CLI_*`)
- Commands: `auth`, `context`, `doctor`, `config`, `patterns list|fields`, `search`, `agg`, `reference`
- Unified **`--json` Agent contract** — same `AgentStatus` envelope for bootstrap, validation, and API errors (stdout)
- `context` / `doctor` bootstrap with semantic exit codes (`0`/`2`/`3`/`4`/`5`/`6`/`7`)
- `field-map.yaml` for cross-index logical service names (`--profile`, `--service`)
- `search` / `agg`: `--data-view`, `--level`, `--trace-id`, `--msg-only`, `--dry-run`
- Optional index allowlist: `KIBANA_CLI_ALLOWED_INDEX_PREFIXES`
- Global flags: `--json`, `--quiet`, `--timeout`, `--insecure`, `--dry-run`, `--force`
- Agent skill: `skills/kibana-cli/SKILL.md` (`npx skills add fatecannotbealtered/kibana-cli`)

### Environment

- `KIBANA_CLI_HOST` — Kibana base URL (no path)
- `KIBANA_CLI_USER` / `KIBANA_CLI_PASSWORD`
- Optional: `KIBANA_CLI_KIBANA_VERSION`, `KIBANA_CLI_TIMEOUT`, `KIBANA_CLI_INSECURE`, `KIBANA_CLI_ALLOWED_INDEX_PREFIXES`

[1.0.1]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.0.1
[1.0.0]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.0.0
