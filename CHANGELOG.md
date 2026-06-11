# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- Added `changelog [--since]`, derived from the embedded `CHANGELOG.md`, so Agents can refresh their knowledge after self-update.
- `reference` now reports tool/version, supported formats, exit codes, error codes, command params, output-schema summaries, permission tiers, and the T1 security boundary.
- `context` and `doctor` now report tool version, Skill minimum version, and security tier.
- `doctor` now checks whether the running binary meets the Skill's declared minimum version.
- Search hits now include `_untrusted` markers for returned log fields, including trace identifiers derived from log messages.
- Query commands now expose Agent-friendly pagination controls: `search --limit --offset`, `patterns list|fields --limit --offset`, and `agg --limit` for top-N buckets.
- Successful standalone binary updates now return `previous_version`, the new `current_version`, and a hint to run `changelog --since <previous_version>`.

### Changed

- Unified the golangci-lint v2 toolchain: Makefile installs from the `/v2` module path and CI uses `golangci-lint-action@v8` to match the v2 config format.
- **BREAKING:** Config and not-configured failures now use exit code `4`, aligning `E_CONFIG` with the Agent CLI spec.
- **BREAKING:** Error code names now match the Agent CLI spec: `E_RATE_LIMITED`, `E_CONFIRMATION_REQUIRED`, and `E_CONFLICT`.
- **BREAKING:** Write confirmation tokens now include an expiry timestamp and are bound to operation context.
- **BREAKING:** `auth login --plaintext` was removed; plaintext passwords in `config.json` are rejected.
- **BREAKING:** npm install no longer supports skipping checksum verification.
- Clarified the `.agent/CLI-SPEC.md` stdout/stderr contract: JSON-mode success and failure both emit one envelope on stdout; stderr is only a side channel for human-readable diagnostics.
- Write-command audit entries now include failed write attempts and UTC timestamps.
- `auth login` now falls back to `KIBANA_CLI_HOST`, `KIBANA_CLI_USER`, and `KIBANA_CLI_PASSWORD` when matching flags are omitted.
- Release publishing now waits for release artifacts before publishing the npm wrapper.
- Self-update now syncs the whole Agent Skill directory through `npx skills add fatecannotbealtered/kibana-cli -y -g` and reports `skill_sync_status`.
- Skill, README, `.agent/` specs, and test prompts now follow the unified Agent-first update and Skill sync contract.

### Security

- Confirm tokens are now signed with a machine-local HMAC key (`confirm.secret`, created on first use with 0600 permissions) so they cannot be fabricated without running `--dry-run` on the same machine.
- Release checksums are signed with Sigstore/Cosign, and install/update paths report signature verification status separately from checksum verification.


## [1.1.0] - 2026-06-07

### Added

- Unified Agent JSON envelope for all JSON output: `ok`, `schema_version`, `data` / `error`, and `meta.duration_ms`.
- Stable `E_*` error codes with `error.retryable` and command-specific failure data under `error.details`.
- Global `--confirm <token>` for write commands. Write commands now use `--dry-run` previews with `confirm_token` before mutation.
- Machine-readable `reference` output with command specs, flags, write markers, and raw-format support metadata.
- `doctor` checks now include actionable `checks[]` diagnostics.

### Changed

- **BREAKING:** JSON consumers must read success payloads from `data` and failures from `error` / `error.details`; the old flat AgentStatus-style JSON is no longer the primary contract.
- **BREAKING:** Exit codes now follow the Agent CLI table: `0` success, `1` general error, `2` usage error, `3` not found, `4` auth/permission, `5` confirmation required, `6` precondition conflict, `7` retryable transient error, `8` timeout.
- **BREAKING:** `auth login`, `auth logout`, `config init`, and standalone binary `update` require dry-run / confirm-token flow before writing.
- **BREAKING:** `auth login` is non-interactive; pass credentials with flags or environment variables.
- Updated README, README_zh, SECURITY, and the bundled Agent skill for the new contract.

## [1.0.3] - 2026-06-05

### Added

- `kibana-cli update` to check GitHub Releases and update standalone binaries with checksum verification. npm / Go managed installs return the package-manager command instead of mutating managed files.

### Changed

- **BREAKING:** CLI output now defaults to JSON. Use `--format text` for human-readable summaries/tables and `--format raw` for unwrapped raw content where supported.
- Added global `--format json|text|raw` and `--compact` for single-line JSON. `--json` remains as a compatibility alias for `--format json`.
- `--fields` is valid only with JSON output, `--quiet` only suppresses auxiliary text output, and unsupported command/format combinations now return explicit parameter errors.

## [1.0.2] - 2026-06-04

### Changed

- **`--query` defaults to broad all-fields search** (recall-first): free-text/Lucene queries now hit every field via `query_string`. Add `--precise` to narrow to `match_phrase` on the configured message field(s).
- **BREAKING:** removed the deprecated `--msg-only` and `--all-fields` flags on `search` / `agg`; use `--precise` (opt-in) instead.
- Documentation aligned (SECURITY, SKILL) with the new default query semantics.

### Added

- `--precise` matches across **all** configured message fields (not just the primary one).
- Trace lookup in `field` mode falls back to a free-text match of the id across all fields, so a present-but-differently-named trace field no longer silently returns zero hits.
- `search` zero-hit diagnostics (`zeroReason` / `hint` / `diagnostics`): distinguishes `no_data_in_window`, `matched_in_other_fields`, and `filters_excluded_all` via a `size:0` count probe.

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

First release: **Kibana-only log query CLI** for AI Agents.

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

[1.1.0]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.1.0
[1.0.3]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.0.3
[1.0.2]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.0.2
[1.0.1]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.0.1
[1.0.0]: https://github.com/fatecannotbealtered/kibana-cli/releases/tag/v1.0.0
