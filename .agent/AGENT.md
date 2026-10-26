# AGENT.md: playbook for coding agents

**中文版 → [AGENT_zh.md](AGENT_zh.md)**

Read this before you build or change this tool. It maps the specs, lists the constraints you must not break, and gives the workflow for each kind of task. `.agent/`, `contract/` and the aicli library are vendored from [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec) at the tag in `.agent/SPEC_VERSION`, and they override your defaults. Read only the spec your task needs, and cite rules by ID (`CLI-054`) in commits and reviews.

## How the specs divide the work

| Document | Covers | Rules |
|---|---|---|
| `AGENT.md` (this file) | workflows, hard constraints, self-check | – |
| `CLI-SPEC.md` | the machine contract: output, errors, command model, discovery, reads, the write gate, self-update, versions and deprecation, settings and profiles, network | CLI-xxx |
| `SEC-SPEC.md` | risk tiers, scope files, write-gate security, secrets, outbound contact, state store, supply chain | SEC-xxx |
| `SKILL-SPEC.md` | the Skill: frontmatter, content, size, checkpoints, packaging, evaluation | SKL-xxx |
| `VERIFY-SPEC.md` | conformance suite, contract on real output, coverage, behaviour matrix, maturity, agent eval, release parity | VER-xxx |
| [`REPO-SPEC.md`](https://github.com/fatecannotbealtered/ai-native-cli-spec/blob/main/REPO-SPEC.md) (in the spec repo, at your pinned tag) | repo layout, vendoring, CI, release, distribution | – |
| `contract/contract.json`, `contract/schemas/` | the machine source of truth for codes, exit codes, enums and key sets; the prose defers to it | – |

The aicli library (Go: `internal/aicli/`; Python: `<pkg>/_aicli/`) implements most of CLI-SPEC and SEC-SPEC: the envelope and errors, the registry, help and `reference`, usage suggestions, the write gate (binding, tokens, journal, scope, receipts), the built-in commands (`update` included, turned on with `App.Update`), settings and profiles, validators and the conformance suite. Each spec says what the library does. You write the commands and their `Meta`, the upstream client, `Preview` and `Apply` for each write, the `context` and `doctor` hooks, the `UpdateConfig` and the declared settings, the tests, the Skill and the evidence. The spec repo's `template/go/example/` and `template/python/example/` show every pattern in a small notes CLI.

## Hard constraints

1. Never hand-edit vendored files (`.agent/`, `contract/`, the aicli directory). Change them in the spec repo, then run `node scripts/sync-spec.js` (SEC-050).
2. All output goes through aicli: one JSON envelope on stdout and nothing else. Never write to stdout yourself (CLI-001, CLI-002).
3. Every command with an effect other than `observe` calls `App.Write` or `App.WriteBatch`. Never add your own tokens, keys, confirm stores or gate flags (CLI-050). Declare `Effects` and `Dangerous` truthfully; a read never writes (CLI-025).
4. Neither the CLI nor you create or edit the scope file, unless the user explicitly asks you to (SEC-011, SKL-006).
5. Map upstream failures by status with `FromHTTP`, never by message text (CLI-016). In `Apply`, wrap an error in `NotDispatched` only if nothing was sent and in `Rejected` only if the upstream definitively refused, and wrap the `*aicli.Error` from `FromHTTP` (status 0 for a connection that never opened), never a raw error; return any other error as is, so aicli reports `E_OUTCOME_UNKNOWN` (CLI-015, CLI-050).
6. Declare secret flags in `Meta.Secrets` and never log or echo them, nor any secret the upstream returns (SEC-021). Declare every output path that carries external text as untrusted, a ref that holds people's text included (CLI-046, SEC-002).
7. Lists state their completeness and scope (CLI-041); counting, filtering and finding the latest happen in the tool (CLI-044).
8. `update` is aicli's built-in: set `App.Update` to an `UpdateConfig` with the repository and the package names, and never write an `update` command or update code of your own (CLI-070).
9. Every write command has a conformance `Sample`, and every registry command runs in a test (VER-001, VER-003).
10. Read settings only through `app.Setting`, and never put a secret in the settings file (CLI-100, SEC-040). Diagnostics go through `app.Debugf` under `<PREFIX>_DEBUG`, never through a `--verbose` or `--debug` flag (CLI-102).
11. Every upstream request has a timeout and verified TLS; reads retry at most twice and writes are never re-sent; the tool connects only to its configured upstream and sends no telemetry (CLI-110, SEC-022).
12. Use the shared flag names and verb meanings; deprecate a command or flag before removing it, and remove it only in a MAJOR release (CLI-028, CLI-029, CLI-081).

## Workflow A: new tool from the template

1. Instantiate: follow `template/INSTANTIATE.md` in the spec repo: copy `common/` and only the overlay's CI and build files (never its `aicli/` or `example/`; sync-spec vendors the library), fill the placeholders, and write the spec tag, for example `v2.0.0`, to `.agent/SPEC_VERSION`.
2. Vendor: `node scripts/sync-spec.js` copies the specs, the contract and schemas, and aicli; `node scripts/check-spec.js` must pass.
3. Wire the app (Go shown; Python mirrors it in snake_case on click). Read every environment variable through `app.Env`, so tests can inject it; `main` sets it to `os.Getenv`, because the field has no default. `Update` turns on the built-in `update` (CLI-070), and `Settings` declares the settings that `app.Setting` resolves (CLI-100).
   ```go
   app := &aicli.App{Name: "notes-cli", Version: version, RiskTier: "T1", EnvPrefix: "NOTES_CLI",
       Root: root, Changelog: changelogMD, Identity: identity, Env: os.Getenv,
       Update:   &aicli.UpdateConfig{Repo: "acme/notes-cli", NPMPackage: "@acme/notes-cli"}, // the built-in update
       Settings: []aicli.Setting{{Name: "host", Description: "Acme Notes URL"}}}            // NOTES_CLI_HOST, profiles
   os.Exit(app.Execute(os.Args[1:]))
   ```
4. Shape the tree: flat `<noun> <verb>`, with `list`, `get`, `search`, `create`, `update`, `delete` before domain verbs (CLI-021), each verb with its standard meaning and each flag from the shared vocabulary where one fits (CLI-028, CLI-029). Accept the fleet's measured guesses through `AddShortcut` (one root shortcut per noun nested under exactly one parent), `ShortFlags`, `FlagAliases` and `Meta.Aliases`, and suggest the rest through `FlagSynonyms` and `VerbSynonyms` (CLI-022).
5. Register every leaf with `app.Register(cmd, aicli.Meta{...})`: `Summary`, `Effects`, `Dangerous`, `Maturity`, `Output` (a schema defined with `app.Schema`), `ErrorCodes`, `Examples` (writes show the dry-run and confirm pair), plus `Positional`, `FileInputs`, `Secrets`, `DefaultFields` and `BatchFlag` where they apply.
6. Reads return through `app.OK` or `app.List(cmd, aicli.Page{...})`; errors use the constructors (`Usage`, `Validation`, `NotFound`, `Ambiguous`, `FromHTTP`).
7. Writes return through `app.Write`; batches use `app.WriteBatch` with `Meta.BatchFlag` (CLI-063). A target that takes an upstream lookup comes from `WriteSpec.ResolveTarget`, which the gate calls after the token checks, never from a read before `app.Write` (CLI-057).
   ```go
   return app.Write(cmd, aicli.WriteSpec{
       Target:  aicli.Target{Key: "team/notes#7", Kind: "note", Label: "#7 Groceries"},
       Preview: preview, // reads only, deterministic; returns Changes and BaseVersion
       Apply:   apply,   // sends the change; returns Result{Outcome, Observed} from a read-back
   })
   ```
8. Tests: one conformance test, `aicli.RunConformance(t, h)` with a `Harness` that sets `NewApp`, `Writes`, `Reset`, `Samples` and `Scope` (VER-001); behaviour-matrix tests per command, run through a helper that validates every envelope (VER-002, VER-004); and coverage: `<PREFIX>_COVERAGE_FILE` set while the tool's own tests run, then `aicli.CheckCoverage(t, h)` with `CoverageFile` in `TestMain` after `m.Run()` (R06, VER-003).
9. Security: derive the risk tier from the effects and fill in `SECURITY.md` (SEC-001, SEC-060).
10. Skill: write it per SKILL-SPEC, with the command map from `app.CommandMap()` between the `aicli:command-map` markers (CLI-020, SKL-002); `test-prompts.json` covers the VER-006 scenarios.
11. Evidence: a command stays `draft` until its behaviour matrix passes (`experimental`); a recorded run against the real upstream makes it `verified`; a passed agent eval makes it `stable` (VER-005, VER-006).
12. CI and release follow REPO-SPEC; the release runs every merge check (VER-008).

## Workflow B: extend this tool

1. Read the rules for what you touch: output and errors (CLI-001…CLI-018), commands and discovery (CLI-020…CLI-037), reads (CLI-040…CLI-049), writes (CLI-050…CLI-065, SEC-010…SEC-021), settings and network (CLI-100…CLI-110, SEC-022), the Skill (SKILL-SPEC).
2. New command: register it with `Meta`; VER-003 requires a test that runs it. If it writes, implement it with `app.Write` and add its `Sample`, or G00 fails. Set its maturity from its evidence (VER-005).
3. Changed parameters or output: update the output schema. A command, flag or field you rename or remove is deprecated first (`Meta.Deprecated`, `Meta.DeprecatedFlags`), keeps working through at least one MINOR release and 90 days, and goes only in a MAJOR release (CLI-081). The change voids the command's `live` and `eval` evidence: re-run them or lower its maturity (VER-005).
4. Agents keep guessing a form you don't accept (eval traces, bug reports)? Accept it (`Meta.Aliases`, `FlagAliases`, `AddShortcut`), or make the error carry the exact `did_you_mean` (`FlagSynonyms`, `VerbSynonyms`) (CLI-022).
5. Update `CHANGELOG.md` and the Skill (playbooks by hand, the command map from the registry). Never edit `metadata.version` or `metadata.min_version`: `npm version` sets both to the tool version on every release (`scripts/sync-version.js`), and `check-version.js` fails on any other value (SKL-001, REPO-031).
6. Before merge: the tests including the conformance suite, `node scripts/check-spec.js`, lint. Before a release that changes commands, outputs or the Skill: the agent eval (VER-006).
7. New spec version: write its tag to `.agent/SPEC_VERSION`, run `node scripts/sync-spec.js` and the full test suite, and fix the tool, never the vendored files.

## Workflow C: migrate a 1.6.x tool

`MIGRATION-2.0.md` in the spec repo is the authority for this path; follow it step by step. In outline:

1. Pin `v2.0.1`, sync, and route output and errors through aicli: `schema_version` becomes `"2.0"` and JSON is compact by default.
2. Register every command with `Meta` (effects, maturity, evidence) and add root shortcuts and the measured guesses (CLI-021, CLI-022).
3. Move every write onto `app.Write` or `app.WriteBatch`. Delete the tool's own token, key and consumed-store code and any switch that weakens binding for old tests, and migrate tests that mint tokens by hand.
4. Delete `release_readiness` and its `doctor` check, the FCC guard and the per-tool `contract_gen`; VER-001…VER-005 replace them.
5. Delete the tool's own `update` and set `App.Update`; move configuration into `App.Settings` with profiles; rename flags to the shared vocabulary and keep the old names as deprecated (CLI-070, CLI-100, CLI-028, CLI-081).
6. Tell users what changes for them: writes are refused until they grant a scope file (SEC-010), partial batches fail with `E_PARTIAL` (CLI-063), and an unusable key or token store blocks writes (CLI-055).
7. Rewrite the Skill per SKILL-SPEC (no bootstrap step, no copy of the protocol, a command map), then run the agent eval with the last 1.6.x release as the baseline (VER-006).

## Self-check before you finish

- [ ] `node scripts/check-spec.js` passes; nothing vendored was edited by hand.
- [ ] Every leaf has `Meta`; the conformance suite passes, including R01 (reference budgets) and R02 (tree rules), and so does `CheckCoverage` (R06).
- [ ] Every non-`observe` command goes through `app.Write` or `app.WriteBatch` and has a `Sample`.
- [ ] Read commands send no state-changing request; `Effects` and `Dangerous` match what the code does.
- [ ] Each command's behaviour-matrix rows are tested or skipped as `not applicable: <reason>`; every envelope in tests is validated.
- [ ] `NotDispatched` and `Rejected` are used only when they are true; upstream errors go through `FromHTTP`.
- [ ] Secret flags and untrusted paths are declared; lists carry completeness and scope.
- [ ] `update` comes from `App.Update`, with no update code of the tool's own; settings are read through `app.Setting`, and the settings file holds no secret.
- [ ] Upstream requests have timeouts and verified TLS, writes are never re-sent, and nothing goes anywhere but the configured upstream.
- [ ] Each command's maturity matches its evidence.
- [ ] `CHANGELOG.md`, the Skill and `test-prompts.json` are updated.
- [ ] If commands, outputs or the Skill changed and a command is `stable`: the agent eval ran and passed.

Part of [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec). Copyright (c) 2026 Sean Guo. MIT License; see `LICENSE` in the spec repository.
