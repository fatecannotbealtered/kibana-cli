# Open-Source Checklist

[English](OPEN_SOURCE_CHECKLIST.md) | [中文](OPEN_SOURCE_CHECKLIST_zh.md)

Run through this gate **before the first public push** of `kibana-cli`. It is a security and quality checkpoint, not documentation: every box must be ticked, or consciously waived with a note, before the repo goes public. Once public, secrets in history cannot be un-leaked. Rule IDs refer to the specs in `.agent/` and to REPO-SPEC in ai-native-cli-spec.

## Secrets

- [ ] No credentials, tokens, API keys, or passwords anywhere in the working tree.
- [ ] No secrets in **git history**, checked with a scanner (e.g. `gitleaks detect`, `git log -p | grep`); if found, the history is rewritten or the repo re-created, not just deleted from `HEAD`.
- [ ] No internal hostnames, private IPs, internal URLs, or employer-internal identifiers in code, config, or comments.
- [ ] Test fixtures, recorded responses and evidence records (`docs/E2E.md` and the files it cites) contain only synthetic or redacted data: argv with secrets redacted, no real account data, no real tokens (VER-005).
- [ ] `.env`, `*.local`, credential files and everything from the state directory (`~/.kibana-cli/`: `confirm.key`, `operations/`, `scope.json`, `update-check.json`) are untracked (`git status --ignored`); runtime state never lives in the repo (REPO-050).
- [ ] Tests use a temporary state directory, never the developer's confirm key, journal or scope file (REPO-073).
- [ ] Stored credentials follow the keyring pattern: OS keyring first, an encrypted file only as the reported fallback (`context.credentials.source`), no secrets in config files (SEC-040).

## Docs

- [ ] `README.md` follows the REPO-010 skeleton: title, language switch and badges → one line → Agent Install → What It Does → Authorization → Capabilities → Agent Workflow → Machine Contract → Configuration → Uninstall → Project Structure → Development → Links.
- [ ] The Agent Install block runs as pasted in a clean shell, installs the Skills from the installed package with a pinned installer version (`npx skills@<version> add`) rather than from a branch, and runs no `context`, `doctor` or `reference` first (REPO-010, SKL-009, CLI-034).
- [ ] The Authorization section gives the scope file path, a minimal grant and the protection statement, and never tells an agent to write the scope file (REPO-011, SEC-013).
- [ ] The Capabilities map in the README and the command map in the Skill are both the `CommandMap()` output, and the map test checks both (SKL-002).
- [ ] The Configuration section lists every setting with its variable and shows the settings file's profiles; the Uninstall section removes the package or binary, the state directory, the keyring entries, a settings file kept elsewhere and the Skill directories (REPO-010, REPO-011, CLI-100).
- [ ] `README.md` and `README_zh.md` are **content-synced**: same sections, same commands, same filled-in values.
- [ ] `CHANGELOG.md` uses Keep a Changelog, has `## [Unreleased]` on top, and the first release section carries its real date.
- [ ] `LICENSE` is present, the license is chosen deliberately (default MIT), and the copyright year and holder are filled in.
- [ ] No template placeholder is left: `grep -rnE '\{\{[A-Z_0-9]+\}\}' . --exclude-dir=node_modules --exclude-dir=.git` finds nothing.

## Governance

- [ ] `SECURITY.md` has a working disclosure channel and a supported-versions table, and states the risk tier with its worst case in one sentence (SEC-001).
- [ ] For a tool with write commands, `SECURITY.md` also gives the scope file path, how to grant and withdraw a scope, and the honest protection statement (SEC-011, SEC-013).
- [ ] The tool sends no telemetry and connects only to its configured upstream and, in `update` and `doctor`, to its release source; `SECURITY.md` lists any other destination (SEC-022).
- [ ] `CONTRIBUTING.md` is present (setup, branch and commit convention, tests and evidence, PR flow).
- [ ] `CODE_OF_CONDUCT.md` is present (Contributor Covenant) if the project accepts external contributions.
- [ ] If `kibana-cli` wraps a third-party product (Kibana), `NOTICE.md` carries the trademark and non-affiliation notice and `docs/COMPATIBILITY.md` lists the verified upstream versions; otherwise both NOTICE files are deleted.

## Build / CI

- [ ] CI (`.github/workflows/ci.yml`) is **green** on the commit being pushed, and a red step blocks the merge (not advisory-only).
- [ ] CI runs format and lint, the tests with the conformance suite on every OS, a dependency audit for every shipped ecosystem, `check-version.js` and `check-spec.js` (REPO-070, SEC-050).
- [ ] The conformance test runs with a `Sample` for every write command and coverage recording on (R06), and filters nothing (VER-001, VER-003).
- [ ] Every envelope the tests produce is validated, and each command at `experimental` or above has its behaviour-matrix tests (VER-002, VER-004).
- [ ] Each command's maturity matches its evidence: `verified` cites a `live:` record, `stable` also an `eval:` report, and every referenced file exists (VER-005).
- [ ] `release.yml` runs every merge check on the tagged commit before anything is signed or published (VER-008).
- [ ] No retired 1.x file or field remains: no FCC guard, no `release_readiness`, no per-tool `contract_gen`, no confirm-token, key or consumed-store code of the tool's own (REPO-001).
- [ ] The formatter config is committed (ruff / golangci-lint / prettier, by language) and CI runs the format check.
- [ ] No build artifacts, caches, venvs, or IDE config are committed (covered by `.gitignore`).
- [ ] The launcher-budget job ran; if the npm launcher adds more than 100 ms per call, the README's Agent Install puts the native binary on PATH (REPO-041).

## Distribution

- [ ] `package.json` `version` matches the git tag being released (`vX.Y.Z` ↔ `X.Y.Z`); `release.yml` guards this and fails on mismatch.
- [ ] `package.json` `version` is the only hand-edited version: `npm version` syncs the lockfile, every Skill's `metadata.version` and `metadata.min_version`, the Python `__version__` and the CHANGELOG, and `check-version.js` is green (REPO-031).
- [ ] The binary itself (`bin/`, `*.exe`, `dist/`) is **not committed**; CI builds it and `.gitignore` covers it.
- [ ] Release archives ship a `checksums.txt` signed with Sigstore keyless signing in the tagged release workflow, with the bundle published beside it; the binary update path verifies signature and checksum in-process and fails closed (REPO-042, SEC-050).
- [ ] npm publishes the main wrapper package plus every supported OS/CPU platform package from CI-built artifacts, with `npm publish --provenance`; installing downloads nothing else (REPO-040).
- [ ] Every channel ships the Skills of its own version: `npm pack --dry-run` lists `skills/` (REPO-043).
- [ ] The runtime `changelog` command and the GitHub Release body both come from `CHANGELOG.md`, not hand-copied text (REPO-032).
- [ ] `update` is aicli's built-in, set up with `App.Update` and the repository and package names this tool is published under; the tool has no update code of its own, and release archives carry the binary and `skills/` at their root, so a bare `update` (one command, no confirm token) can verify the release, install it and copy the Skills from it (CLI-070, REPO-043).

## AI-native

- [ ] Root `AGENTS.md` is present and points to `.agent/AGENT.md`.
- [ ] `.agent/` holds the five specs (`AGENT`, `CLI-SPEC`, `SEC-SPEC`, `SKILL-SPEC`, `VERIFY-SPEC`) and `SPEC_VERSION`; `contract/` holds `contract.json` and `schemas/`; the aicli library exists only at its vendored path (`internal/aicli/` or `<pkg>/_aicli/`) with its `MANIFEST.json`, and `check-spec.js` is green (REPO-001, REPO-062).
- [ ] `skills/kibana-cli/SKILL.md` follows SKILL-SPEC 2.0: Agent Skills frontmatter only (`name` equal to the directory, a `description` that says what, when and not for, `license`, `compatibility`, and `metadata` with `version`, `min_version` and `requires.bins`), at most 150 lines, a generated command map, one write recipe, risk-scaled `STOP CHECKPOINT` lines, the scope, untrusted-content, ambiguity and lost-reply lines, and the end-of-task report (SKL-001 to SKL-008).
- [ ] `skills/kibana-cli/test-prompts.json` is valid JSON, its entries have only `id`, `scenario`, `prompt`, `expected` and an optional `grader_notes`, and it covers every VER-006 scenario (SKL-011).
- [ ] `kibana-cli reference --index`, `kibana-cli context` and `kibana-cli doctor` run offline from a clean install and emit valid envelopes; with a scope granted, `doctor`'s `authorization` and `version` checks pass (CLI-031, CLI-035, CLI-036).
- [ ] Without a scope file, every write command is refused with `E_FORBIDDEN` and `details.scope`, and nothing is sent (SEC-010).
- [ ] Flags use the shared vocabulary and verbs their standard meaning; settings come from `App.Settings`; no `--verbose` or `--debug` flag; upstream requests have timeouts and verified TLS and never re-send a write (CLI-028, CLI-029, CLI-100, CLI-102, CLI-110).
- [ ] The risk tier in `SECURITY.md` equals `reference.risk_tier` and is at least what the commands' effects require (SEC-001).
- [ ] No command is `stable` without a passed agent eval (VER-005, VER-006).
