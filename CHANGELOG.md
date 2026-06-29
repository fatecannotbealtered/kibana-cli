# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Fixed

- **`npm ci` lockfile drift fixed.** The per-platform `optionalDependencies` subentries in `package-lock.json` (`node_modules/@fateforge/kibana-cli-*`) were missing their `version`, so once those platform packages were published, `npm ci` failed its consistency check (`lock file's <pkg>@ does not satisfy <pkg>@<version>`). The version bump (`scripts/version-files.js`) now syncs the lockfile platform subentries too, and `check-version.js` guards them, so the drift can't silently recur.

## [1.1.13] - 2026-06-29

### Fixed

- `update` Sigstore signature verification now bounds the TUF trust-root refresh with an explicit wall-clock timeout in addition to the existing SIGINT cancellation and `WithForceCache` (cache-first) behavior. A hung Sigstore CDN on a cold or expired cache can no longer stall the `verify_signature` stage indefinitely, matching the bound the rest of the fleet enforces (CLI-SPEC §14).

## [1.1.12] - 2026-06-25

### Added

- Canonical JSON contract is now single-sourced from the `ai-native-cli-spec` template (pinned via `.agent/SPEC_VERSION`). `contract/contract.json` is vendored and generates `internal/contract/contract_gen.go`; the error-code → exit → retryable mapping and `schema_version` now derive from it, so they cannot drift from the fleet contract. A new conformance test (`internal/output/contract_conformance_test.go`) asserts every emitted error code, the exit/retryable mapping, the envelope key sets, and `meta` keys match the contract, and a `check-spec` CI guard fails closed on drift of the vendored specs/contract or the generated module.

### Changed

- The bundled `.agent/` specs are now synced (not hand-maintained) from `ai-native-cli-spec@v1.4`, which adds the machine-readable contract governance (CLI-SPEC §3.1) and the install-method dispatch note for `update`.

## [1.1.11] - 2026-06-25

### Changed

- `update` now upgrades package-manager installs in one call instead of only printing the command. When kibana-cli is managed by npm or Go, a bare `update` DRIVES the package manager — it runs `npm install -g @fateforge/kibana-cli@<version>` (or `go install …@<version>`) on your behalf, then syncs the Skill, and reports `status: "updated"`. The binary is never mutated in place under a package manager (that would desync its metadata); integrity stays the package manager's own, so `signature_status` remains `not_checked` on this path. Standalone-binary installs are unchanged (in-process Sigstore verify + atomic swap). `--check`/`--dry-run` stay read-only and now preview the package-manager command without running it. A package-manager failure reports `E_IO` (exit 1) with `binary_replaced: false` and the exact command to run manually.

## [1.1.10] - 2026-06-25

### Changed

- `update` now replaces the running binary in place on Windows using the same cross-platform rename trick as on Unix (write `.<name>.new` → rename the in-use binary to `.<name>.old` → move `.new` into place → roll back on failure). Windows no longer returns `manual_update_required`; a successful self-update reports `status: "updated"` with `binary_replaced: true` on every platform.
- `update` standardizes the target-version flag on `--target-version` (CLI-SPEC §2/§14). The previous `--version` flag is retained as a hidden, deprecated alias so existing callers keep working.

### Fixed

- The cached update-available notice is now re-validated against the running version when read, not the version cached at write time. Within the 24h cache TTL after an upgrade, an already-current CLI no longer keeps advertising an update to a version it already runs; while still behind the latest, `current_version` and the notice message are refreshed to the running binary.
- `update` no longer misclassifies a transient verify-stage failure as a non-retryable integrity failure. The `verify_signature` stage now splits failure modes: a signature/identity/transparency-log mismatch stays `E_INTEGRITY` (exit 1, non-retryable), while a network step inside verification — downloading the signature bundle or refreshing the Sigstore TUF trusted root — maps to the retryable network/timeout taxonomy (`E_NETWORK`/`E_TIMEOUT`, exit 7/8), and a SIGINT/SIGTERM during verify emits a terminal `E_INTERRUPTED` envelope (exit 130). Request timeouts during discover/download are now distinguished as `E_TIMEOUT` (exit 8) rather than collapsed into `E_NETWORK`.
- The Sigstore trusted-root refresh during `update` is now cache-first and context-bound (`WithForceCache` + the command context), so a still-valid cached trusted root is reused without a network call and a refresh is cancelled on interrupt and bounded by the command timeout. The self-update result reports the trust-root source via `trust_root_source`; the code comments now describe the trust model truthfully (embedded TUF anchor + online `trusted_root.json` refresh), since sigstore-go v1.2.1 exposes no fully offline embedded trusted root.

## [1.1.9] - 2026-06-22

### Added

- The update-available notice now also rides along on **every** command's `meta.notices[]`, read **only from the local cache** (no network — cost is one local file read). It is omitted when the cache has nothing to report, and present on any command (not just `context`/`doctor`/`update --check`) while the cache holds an available-update notice. The fresh/active `data.notices` on `context`/`doctor`/`update --check` is unchanged.
- Update notices are now **severity-graded** from the embedded CHANGELOG delta between the running version and the latest: `warning` when the delta contains a `security` entry or the latest crosses a major version, otherwise `info` (`critical` is reserved). Severity is computed at check time and stored in the cache, so the cached `meta.notices` carries the right level.

## [1.1.8] - 2026-06-21

### Changed

- `update` is now a SINGLE command with no confirm token. A bare `kibana-cli update` performs the whole self-update in one call (resolve latest or `--version` → verify Sigstore signature in-process → verify checksum → replace binary → sync Skill). The previous dry-run → `--confirm <token>` write gate has been removed from `update` only; `--check` and `--dry-run` remain optional read-only flags and no longer issue a `confirm_token` or `expires_at`. Other (data-write) commands keep their dry-run/confirm flow unchanged.
- Every `update` failure and interruption envelope now carries `stage` (`discover`|`download`|`verify_signature`|`verify_checksum`|`replace`|`skill_sync`), `current_version` (the version running now), `binary_replaced`, and `skill_sync_status`, so an agent can determine its post-failure state from the envelope alone.

### Fixed

- `replace`-stage failures during update are now classified by next action instead of being lumped into a retryable network error: local IO/disk faults return non-retryable `E_IO` (exit 1) and permission faults return `E_FORBIDDEN` (exit 4), with `binary_replaced: false`.
- A Skill-sync failure after a successful binary replace is now a PARTIAL SUCCESS (`ok: false`, `binary_replaced: true`, retryable) carrying `skill_sync_command`, instead of a hard network error that lost the fact the binary already updated.

### Added

- New error codes `E_IO` (→ exit 1) and `E_INTERRUPTED` (→ exit 130), wired into the code→exit mapping. SIGINT/SIGTERM during `update` is now trapped: the temp dir is cleaned and a terminal JSON envelope (`E_INTERRUPTED`) is still emitted on stdout stating the truthful post-state, instead of dying as a bare killed process.

### Security

- Release-integrity verification is unchanged and still fails closed: the signature-then-checksum order and the non-retryable `E_INTEGRITY` (exit 1) behavior are preserved. Removing the confirm-token gate from `update` does not weaken integrity — the safety guarantee is the mandatory in-process Sigstore verification.

## [1.1.7] - 2026-06-16

### Fixed

- npm `optionalDependencies` platform-package pins now match the package version. The previous release bumped the top-level version but left the pins at the prior version, so `npm install` resolved a stale platform binary (the new wrapper with the old binary). The publish workflow now rewrites `optionalDependencies` from the package version before `npm publish`, so the pins can no longer drift from the single source of truth.

## [1.1.6] - 2026-06-16

### Changed

- `update` now verifies the release Sigstore signature on `checksums.txt` **in-process** (embedded `sigstore-go`, bootstrapped from the embedded TUF trust root) instead of shelling out to an external `cosign`. Verification is mandatory and fail-closed: a missing signature bundle, a signature that does not verify against this repo's tagged release-workflow identity, or a checksum mismatch all refuse the update — there is no skip path. Releases are now signed with `cosign sign-blob --new-bundle-format`.

### Security

- Release-integrity failures (missing/invalid signature or checksum mismatch) now return the non-retryable `E_INTEGRITY` error code (exit 1) instead of a retryable network code, so an agent treats a possible supply-chain issue as a hard stop rather than retrying.

## [1.1.5] - 2026-06-16

### Added

- **Multi-system contexts** (kubectl-style). `config.json` is now a context store (`schemaVersion: 2`) holding named systems, each with its own host, credentials (keyring), `defaultIndex`, and optional per-context `fieldMapFile`. New commands: `context list` / `context current` / `context use <name>` / `context add <name>` / `context remove <name>`. Selection precedence: `KIBANA_CLI_HOST/USER/PASSWORD` triad (anonymous) → `--context <name>` → `KIBANA_CLI_CONTEXT` → current context. `auth login --context <name>` stores and switches in one step; `context` bootstrap reports `activeContext`.
- **Normalized search output**. Hits gain canonical aliases — `_service`, `_message`, `_level` — plus a unified `traceId`/`spanId` derived from the resolved field-map, so results from indices with different field names (msg vs message, log_app vs service_name) share one schema. Original fields are preserved (non-destructive).
- **`patterns infer`**. Probes an index's fields, maps them onto logical groups via a built-in alias dictionary, samples recent messages to detect a traceId embedded in the log line (`trace_mode: msg`), and emits a paste-ready field-map profile. `--write` appends it to the active field-map under dry-run/confirm.
- **Multi-pattern trace extraction**. `traceId` is now pulled from the message body across several formats (MDC `[trace, span]`, `traceId=` / `trace_id:`, bare 32-hex). field-map gains an optional `trace_msg_patterns` list (defaults/profile/index-rule) for custom shapes; bad patterns are skipped, not fatal.

### Changed

- `config.json` schema is context-based (`schemaVersion: 2`); the active `Config` is the resolved view. Secrets remain keyring-only and never touch the file.
- Env auth is anchored on `KIBANA_CLI_HOST`: only the full host+user+password triad overrides the active context. A lone `KIBANA_CLI_USER` / `KIBANA_CLI_PASSWORD` (commonly set to feed `context add` / `auth login`) is ignored, not an error, and no longer suppresses a keyring-backed context's credentials or `defaultIndex`.
- Search output always preserves `_index` / `_id` (hit provenance) even under an explicit `--fields`, so results spanning multiple indices keep their source.

## [1.1.4] - 2026-06-15

### Changed

- npm scope 迁移 `@fatecannotbealtered-` → `@fateforge`（无横线 org 在 npm 被占，迁移到 `@fateforge`）。Affects the root package, the six platform `optionalDependencies`, and all `npm install -g` references in docs/SKILL.
- Synced `.agent/` CLI-SPEC and bumped CI actions to the latest template versions.

## [1.1.3] - 2026-06-14

### Added

- `search --dsl <json>` raw Elasticsearch query passthrough; `search --search-after` stable cursor (`next_search_after`) for large/time-ordered result sets.
- `agg --agg-type date_histogram` with `--interval`, plus metric sub-aggregations (`--metric avg|sum|min|max|count --metric-field`).
- `objects list` / `objects get` — saved-objects (dashboards, visualizations, searches, index-patterns) read.
- `reference` now exposes a real per-command `output_schema` + `examples[]`, guarded against regression.

### Changed

- Confirm tokens are now single-use (E_CONFLICT on replay) for auth/config/update writes.

## [1.1.2] - 2026-06-14

### Changed

- Unified the HTTP-status→exit-code mapping into a single source of truth (`output.ExitCodeForHTTP`); the `cmd` layer now delegates to it instead of maintaining a parallel mapper, so the status→exit contract can never drift between the two layers.

## [1.1.1] - 2026-06-13

### Added

- Recorded live smoke against a real Elasticsearch+Kibana 7.10.2 instance with X-Pack security enabled (`docs/LIVE-SMOKE-EVIDENCE.md`, 2026-06-13: auth/keyring, patterns, search, agg, error taxonomy all PASS); `release_readiness` is now `stable` with `live_smoke_status: verified`.
- FCC enumeration guard (`TestFCC_EveryLeafCommandHasTest`): enumerates every leaf command from live `reference` output and asserts each has a command-level test; skips while `fcc_status` is honestly declared non-verified, so the claim cannot be flipped without coverage.
- Added `changelog [--since]`, derived from the embedded `CHANGELOG.md`, so Agents can refresh their knowledge after self-update.
- `reference` now reports tool/version, supported formats, exit codes, error codes, command params, output-schema summaries, permission tiers, and the T1 security boundary.
- `context` and `doctor` now report tool version, Skill minimum version, and security tier.
- `doctor` now checks whether the running binary meets the Skill's declared minimum version.
- Search hits now include `_untrusted` markers for returned log fields, including trace identifiers derived from log messages.
- Query commands now expose Agent-friendly pagination controls: `search --limit --offset`, `patterns list|fields --limit --offset`, and `agg --limit` for top-N buckets.
- Successful standalone binary updates now return `previous_version`, the new `current_version`, and a hint to run `changelog --since <previous_version>`.

### Changed

- Synced `.agent/` SEC-SPEC from the template: credential-at-rest is now the keyring three-part pattern (password discarded after login / secrets in the OS keyring / zero-secret config), file encryption demoted to a visible fallback, env vars as the recommended secret channel, and an honest note on Windows `0600` semantics.
- Synced the `.agent/` spec copies from the ai-native-cli-spec template: stdout failure envelope (§4), HMAC confirm-token requirement (§7), signature_status/signature_verified fields (§14), Skill frontmatter `version` rule.
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
