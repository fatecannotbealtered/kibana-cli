# Contributing to kibana-cli

*English | [中文](CONTRIBUTING_zh.md)*

Thanks for improving **kibana-cli**: A Kibana CLI that searches, reads and counts application logs in Elasticsearch through Kibana's Console Proxy, and lists Kibana's data views, fields and saved objects. This document covers building, testing and submitting changes.

> This is a side project shared for AI-tooling experimentation; maintainers do not provide commercial support or production guarantees.

## The build playbook lives in the specs

This repo is an **AI-native CLI tool**: its main caller is an AI agent. Before implementing or changing a feature, read:

- **[AGENTS.md](AGENTS.md)**: the entry point and the rules every change keeps.
- **[`.agent/AGENT.md`](.agent/AGENT.md)**: the playbook: workflows, hard constraints and a self-check.
- **[`.agent/CLI-SPEC.md`](.agent/CLI-SPEC.md)**: the machine contract: output, errors, the command model, discovery, reads, the write gate and self-update (`CLI-xxx`).
- **[`.agent/SEC-SPEC.md`](.agent/SEC-SPEC.md)**: risk tiers, untrusted content, scope files, secrets, the state store and the supply chain (`SEC-xxx`).
- **[`.agent/SKILL-SPEC.md`](.agent/SKILL-SPEC.md)**: the bundled Agent Skill (`SKL-xxx`).
- **[`.agent/VERIFY-SPEC.md`](.agent/VERIFY-SPEC.md)**: the conformance suite, tests, maturity and evidence, and the agent eval (`VER-xxx`).

The specs, `contract/` and the aicli library (`internal/aicli/` in Go, `<pkg>/_aicli/` in Python) are vendored from [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec) at the tag in `.agent/SPEC_VERSION`. They take priority over default habits, and they are read-only here: `node scripts/check-spec.js` fails CI when they differ from the pinned spec. A change they need goes to the spec repository as an issue or pull request; once it is released, bump `.agent/SPEC_VERSION` and run `node scripts/sync-spec.js` (REPO-063). Code that writes its own output, errors or write gate instead of using the library will not be merged (CLI-001, CLI-050).

## Development setup

<!--
Language toolchain: keep ONE block below and delete the other.
  - Go (the version in go.mod)  : compiled binary + npm wrapper
  - Python 3.10+                : PyInstaller binary + npm wrapper
  - Node 16+                    : the npm wrapper and the spec and version scripts, in every variant
The shape is always: install deps -> build -> test -> run a `--help` smoke test.
-->

```bash
# Clone
git clone https://github.com/fatecannotbealtered/kibana-cli.git
cd kibana-cli

# --- Go variant ---
go mod download
make build                      # or: go build -o bin/kibana-cli ./cmd/kibana-cli
make test                       # go vet, then go test -race ./... (conformance suite included)
./bin/kibana-cli --help

# --- Python variant ---
# pip install -e ".[dev]"
# python build.py
# pytest tests/ -v              # conformance suite included
# kibana-cli --help

# Both: vendored files and version locations
node scripts/check-spec.js && node scripts/check-version.js
```

If dependency download is slow, use a regional proxy (e.g. Go: `GOPROXY=https://goproxy.cn,direct`; pip: a mirror index).

## Commands

| Goal | ▶ Command |
|------|-----------|
| Build | `make build` (Go) / `python build.py` (Python) |
| Test, conformance suite included | `make test` → `go test -race ./...` / `pytest tests/ -v` |
| Conformance suite only | `make conformance` / `pytest tests/test_conformance.py` |
| Lint | `make lint` → `golangci-lint run ./...` / `ruff check .` |
| Format | `make fmt` → `gofmt -w .` / `ruff format .` |
| Vendored files match the pin | `node scripts/check-spec.js` (Go: `make spec-check`) |
| Re-vendor after a pin change | `node scripts/sync-spec.js` (Go: `make spec-sync`) |
| Version locations match | `node scripts/check-version.js` |

`make` targets are variable-driven; on Windows fall back to the underlying tool command. Run **lint + test** locally before pushing.

## Branch & commit convention

- Branch from the default branch: `git checkout -b feat/your-feature`.
- Keep one logical change per branch; rebase the default branch before requesting review.
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/):

```text
<type>: <description>

<optional body>
```

| Type | Use for |
|------|---------|
| `feat` | New features |
| `fix` | Bug fixes |
| `refactor` | Code restructuring without behavior change |
| `docs` | Documentation changes |
| `test` | Adding or updating tests |
| `chore` | Build, CI, dependency, or tooling changes |
| `perf` | Performance improvements |
| `ci` | CI/CD pipeline changes |

Examples: `feat: add export command`, `fix: handle nil pointer in status check`, `docs: sync README_zh`. Cite spec rules by ID where they apply (`fix: bind the body file by content (CLI-054)`).

## CI checks

`.github/workflows/ci.yml` runs on every push and pull request: format and lint; the tests, conformance suite included, on Linux, macOS and Windows; the dependency audits (`govulncheck` or `pip-audit`, and `npm audit`); `check-version.js` and `check-spec.js`. A red step blocks the merge. `release.yml` runs the same checks on the tagged commit before anything is signed or published (REPO-070, REPO-071, VER-008).

## Tests and evidence

What "tested" means is defined in [`.agent/VERIFY-SPEC.md`](.agent/VERIFY-SPEC.md):

- **Conformance suite (VER-001).** One test runs `aicli.RunConformance` (Python: `run_conformance`) with a fake upstream that counts writes, a `Sample` for every write command and a scope. It checks the write gate of every write command (G00 to G10) and `reference`, the command tree, usage errors, envelopes, `context`, `doctor` and `operation` for the whole tool (R01 to R05). A write command without a sample fails it. Never skip, filter or weaken a check.
- **Real output (VER-002).** Command-level tests run the CLI through one helper that executes the App, validates the envelope with the library's validator and checks the exit code against contract.json.
- **Coverage from runs (VER-003).** Every command in the registry, built-ins included, runs in at least one command-level test. While the tests run, `KIBANA_CLI_COVERAGE_FILE` names a temporary file the library records every executed command into; after a full run, `aicli.CheckCoverage` (Python: `check_coverage`) enumerates the registry and names every command that never ran (R06). In Go that is `TestMain` in `cmd/main_test.go`. There is no source-scan guard.
- **Behaviour matrix (VER-004).** Each command at `experimental` or above has tests for the rows that apply to it: success, invalid input, auth and permission, upstream failure, empty, pagination, exit code, no side effect for reads, and for writes the gate and the apply failures. A row that does not apply is a skipped subtest whose reason starts with `not applicable:`. Every bug fix that changes observable behavior adds a regression test.
- **Maturity (VER-005).** Each command declares `Maturity` and `Evidence` in its `Meta`, and the level never exceeds the evidence: `draft` (declared), `experimental` (behaviour matrix against a fake upstream, `simulated:` evidence), `verified` (also a recorded run against a real Kibana, `live:` evidence kept in `docs/E2E.md`), `stable` (also a passed agent eval and no open P1 defect, `eval:` evidence). A change to a command's request, parsing or output voids its `live` and `eval` evidence: re-run them or lower the maturity in the same change.
- **Agent eval (VER-006).** Before a release that changes commands, outputs or the Skill, run the agent eval with `skills/kibana-cli/test-prompts.json`, with and without the Skill. It is required before any command is marked, or stays, `stable`.
- **No user state (REPO-073).** Tests point the state directory at a temporary directory (`App.StateDir` or `KIBANA_CLI_STATE_DIR`), never at the user's confirm key, journal or scope file.

## Adding or changing a command

1. **Client**: add the upstream call next to the domain's other calls. Reuse the shared HTTP and auth helpers, build URLs from parameters (never by concatenating user input), map failures by status with `aicli.FromHTTP` (CLI-016), give every request a timeout and verified TLS, and retry reads only, at most twice, never writes (CLI-110).
2. **Command**: add the cobra (Go) or click (Python) command and register it with `app.Register(cmd, aicli.Meta{...})`: summary, effects, `Dangerous`, maturity and evidence, output schema (`app.Schema`), error codes and examples (a write shows the `--dry-run` and `--confirm` pair, a dangerous command `--dangerous`), plus `Positional`, `FileInputs`, `Secrets`, `DefaultFields` or `BatchFlag` where they apply (CLI-020 to CLI-027). Name flags from the shared vocabulary (`--limit`, `--cursor`, `--query`, `--since`) and keep the standard verb meanings (CLI-028, CLI-029). `--help` and `reference` are generated from the registry, so the command appears in both without extra wiring.
3. **Reads** return through `app.OK` or `app.List`, with a `scope` that says what the list really covers (CLI-041). **Writes** return through `app.Write` (or `app.WriteBatch`) with a read-only `Preview` and an `Apply` that reads back what it changed; wrap an `Apply` error in `NotDispatched` or `Rejected` only when that is true (CLI-050, CLI-059).
4. **Tests**: the behaviour-matrix rows above, and a conformance `Sample` for every new write command.
5. **Skill**: regenerate the command map, which sits between the `aicli:command-map` markers in `skills/kibana-cli/SKILL.md` and in the Capabilities section of `README.md` and `README_zh.md`. It is rendered from the registry and checked by the map test; in Go, `UPDATE_SKILL_MAP=1 go test ./cmd -run TestSkillCommandMap` rewrites every block. Never edit them by hand. Then update the playbooks and the domain-knowledge lines in `SKILL.md`, and add or adjust `test-prompts.json` entries (`id`, `scenario`, `prompt`, `expected`, optional `grader_notes`). Depth that only some tasks need goes to `skills/kibana-cli/references/<topic>.md`, linked from `SKILL.md`, which stays within 150 lines (SKL-002, SKL-003, SKL-011). `metadata.version` and `metadata.min_version` follow `package.json` through `npm version`; never edit them by hand.
6. **Docs**: add a line under `## [Unreleased]` in `CHANGELOG.md`. Agents read it after an update through `kibana-cli changelog`, so name the commands, flags and fields that changed. A command or flag that is going away gets `Meta.Deprecated` or `Meta.DeprecatedFlags` and a line under `Deprecated`; it is removed only in a MAJOR release (CLI-081).

## Pull request guidelines

1. **One logical change per PR** when possible.
2. **Tests**: add or update tests for behavior changes.
3. **Docs**: update user-facing docs and the Skill when commands, flags or outputs change.
4. **Commits**: Conventional Commits; no secrets or real tokens anywhere.

### PR checklist

- [ ] One logical change, focused diff
- [ ] Tests added/updated and passing (`make test` / `pytest`), conformance suite included
- [ ] Every new write command has a conformance `Sample`, and every new command runs in a test (VER-001, VER-003)
- [ ] Each changed command's maturity still matches its evidence (VER-005)
- [ ] Lint and format passing
- [ ] `node scripts/check-spec.js` passes: nothing vendored was edited by hand
- [ ] `node scripts/check-version.js` passes
- [ ] Command maps regenerated (Skill and README Capabilities); playbooks and `test-prompts.json` adjusted
- [ ] Agent eval run if the change touches commands, outputs or the Skill and any command is `stable` (VER-006)
- [ ] `CHANGELOG.md` (`## [Unreleased]`) updated
- [ ] **Bilingual docs synced**: every `*.md` change mirrored in its `*_zh.md` counterpart (and vice versa)
- [ ] No secrets, tokens, or real credentials in code, tests, fixtures, evidence records, or commit history
- [ ] Commit messages follow Conventional Commits

## Security

Do not open public issues for undisclosed security vulnerabilities. See [SECURITY.md](SECURITY.md).
