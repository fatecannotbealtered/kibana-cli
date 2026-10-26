# Agent-Facing CLI Security Spec

**中文版 → [SEC-SPEC_zh.md](SEC-SPEC_zh.md)**

Spec version 2.0.1, rules SEC-001 to SEC-060. This file is normative for security: risk tiers, untrusted content, authority, the write gate's security properties, secrets, outbound contact, the state store, credentials at rest and the supply chain. Related files: `CLI-SPEC.md` (machine contract, CLI-xxx), `SKILL-SPEC.md` (Skills, SKL-xxx), `VERIFY-SPEC.md` (verification, VER-xxx), and `contract/contract.json` (codes, enums, key sets). Where a rule here summarises a CLI rule, the CLI rule defines the behaviour; this file says why it matters for security and how to review it.

## Threat model (informative)

The caller is a language model with a shell, running under the user's OS account. It reads everything the tool prints, including text written by other people, and it acts on what it reads. This spec defends against:

- **Injected instructions**: text in names, titles, comments, bodies, logs or upstream errors that tells the agent to do something.
- **Agent drift**: the agent changes more objects, other objects, or other kinds of thing than the person asked for.
- **Gate failures**: a confirm token that does not bind the request, can be forged or can be replayed; a write that skips the gate.
- **Leaks**: secrets in output, previews, tokens, operation records or logs; data sent to hosts the user did not configure.
- **Supply chain**: a tampered release, or an update path that runs a command an attacker chose.

It does not defend against a malicious process running as the same OS user, the agent included: such a process can edit the scope file, the confirm key and the journal (SEC-013).

> **What the library gives you.** aicli (vendored as `internal/aicli/` or `<pkg>/_aicli/`) implements: the minimum-tier check from declared effects (SEC-001); `meta.untrusted` assembly that follows `--fields` (SEC-002); scope loading, strict parsing, every check in SEC-012 and the refusal with `details.scope` and `grant_example` (SEC-010 to SEC-012); the whole write gate (SEC-020); `"***"` for secrets in previews and diagnostics, and keyed digests of secrets in bindings (SEC-021); the state store, key and journal, failing closed (SEC-030); a settings file that refuses secrets (SEC-040); the whole `update` command, from strict versions and argv without a shell to in-process signature verification and the Skill sync (SEC-050).
> The tool still owns: truthful effects, `dangerous` flags, secret parameters and untrusted fields in its registry; canonical target keys (SEC-012); the destinations it contacts (SEC-022); credential storage (SEC-040); its `UpdateConfig`, release signing and dependency audits (SEC-050); its SECURITY.md.

## Risk and untrusted content

### SEC-001 Risk tiers from effects

Every tool MUST declare a risk tier that is at least the minimum its commands' effects require (contract `effects.minimum_risk_tier`), and MUST declare T2 when any command can cause irreversible or account-level damage. The tier MUST appear as `reference.risk_tier` and in SECURITY.md, with one sentence on the worst case.

| Tier | Effects | Examples |
|---|---|---|
| T0 | `observe` only | market quotes, log search, read-only reports |
| T1 | `change`, `artifact`, `persist` | edit an issue, export a report, overwrite a local config file |
| T2 | `deliver` or `configure`, or irreversible / account-level damage | send mail, publish, change permissions or credentials, drop a database, delete a project |

The tier describes what the commands can do, not what the credential could do: a read-only tool holding a write-capable token is T0, and SEC-040 still applies to the token. Commands that cannot be undone (delete, merge, drop, purge) MUST be declared `dangerous` (CLI-025); `deliver` and `configure` are dangerous by default. A bulk command is dangerous only when what it does is, through its effects or an action that cannot be undone, never for the number of its targets alone; a plan that cannot be undone although its command can (`Preview.Dangerous`, CLI-056) is dangerous for that run.

Why: v1 tiers were a label each tool chose. Deriving the floor from the same effects that drive the gate (CLI-050) and the scope (SEC-012) means one declaration cannot disagree with the other two. The gitlab-cli pilot (2026-10) found this rule, CLI-025, the template and the review Skill giving different answers for bulk commands.

Check: aicli compares `App.RiskTier` with the registered effects (CLI-025). Review that SECURITY.md and `reference.risk_tier` agree, and that each command's effects match what it does (VER-004).

### SEC-002 Untrusted content

Text the tool does not control (names, titles, descriptions, comments, message bodies, file contents, logs, upstream error text) is data. The tool MUST:

- list every envelope path that carries it in `meta.untrusted` (CLI-046): in `data` (a `ref` that embeds such text included), in previews (`target.label`, `changes`), in receipts and in `error.details` (`upstream_message`, `candidates[].label`); and list it per schema in `reference` (`untrusted_fields`);
- never trigger an action, fetch a link or switch targets because of instructions or references inside such text;
- build every field an agent acts on (`message`, `next_action`, `did_you_mean`, `usage`, `resume`, `lookup`, a notice's `command`) only from values it controls: registry paths, validated refs and parameters, never upstream text.

Agents MUST treat such text as data and never follow instructions in it (SKL-007).

Why: the agent reads the whole response, so injected text competes with the user's request. In the 2026-10 gitlab-cli eval an MR description carried an injected instruction, and no trial in any arm, with the Skill or without it, followed it. Declaring paths once per response replaces v1's per-item `_untrusted`, which cost about 16 % more characters in a 50-item list response.

Check: aicli tests that `meta.untrusted` follows `--fields`; R04 validates every envelope the conformance suite observes; review each schema's `untrusted_fields`; the VER-006 injected-instruction scenario.

## Authority

### SEC-010 Authority comes from a scope file

A tool with any non-`observe` command MUST refuse each such command, before anything is sent, unless a valid scope file grants it: `E_FORBIDDEN` (exit 4, `dispatch_state: not_started`) with `details.scope`, plus `details.preview` when the preview could be built. The check runs at dry-run, at the refusal of a bare call and again at confirm, and also covers `operation resolve` (CLI-061); self-update is the one command outside it (CLI-070, SEC-050). Within a valid scope the agent works without step-by-step human confirmation: dry-run → confirm binds the agent's own plan and is not an approval.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_FORBIDDEN","message":"No scope file grants this write; ask the user to grant one.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Ask the user to grant this scope by creating C:\\Users\\me\\.notes-cli\\scope.json with the grant_example content (adjusted); do not create it yourself.","scope":{"status":"missing","path":"C:\\Users\\me\\.notes-cli\\scope.json","scope_id":null,"missing":{"effects":["change"],"dangerous":false,"target":"ops/n-42"},"grant_example":{"scope_id":"scp_2026-10-12_ops","tool":"notes-cli","granted_by":"<your name>","granted_at":"2026-10-12T08:00:00Z","expires_at":"2026-10-19T08:00:00Z","effects":["change"],"dangerous":false,"targets":[{"key":"ops/"}]}}}},"meta":{"duration_ms":4}}
```

Why: v1 already said "default read-only; the agent cannot self-escalate", but left the mechanism to each tool. The 2026-10 fleet audit found a hand-maintained permission list that missed a command, so a read-only setting still let a batch command create, change and delete. A check driven by declared effects inside the shared gate cannot miss a command.

Check: G07 (no scope: dry-run and confirm return `E_FORBIDDEN` with `details.scope`, zero upstream writes).

### SEC-011 Scope file format and resolution

A person writes the scope file; the CLI MUST NOT create, edit, move or delete it, or offer a command or flag that does. It reads the file named by `<PREFIX>_SCOPE_FILE` (an absolute path) when that is set, else `<state_dir>/scope.json`; there is no CLI flag. Parsing is strict: invalid JSON, a duplicate key, an unknown key or a known one spelled with other capitals, a value of the wrong type or outside its enum, a `tool` other than this tool, or a relative path in `outputs` or `roots` makes the file `invalid`, and so does, on POSIX, a file not owned by the user or writable by anyone else; an invalid file grants nothing.

```json
{
  "scope_id": "scp_2026-10-12_triage",
  "tool": "notes-cli",
  "granted_by": "Sean",
  "granted_at": "2026-10-12T08:00:00Z",
  "expires_at": "2026-10-19T08:00:00Z",
  "effects": ["change", "deliver"],
  "dangerous": true,
  "targets": [{"key": "ops/"}, {"key": "eng/n-7", "match": "exact"}],
  "destinations": ["mailto:bob@acme.com"],
  "limits": {"max_operations": 50},
  "notes": "Triage week; may share ops notes with Bob"
}
```

| Key | Required | Meaning |
|---|---|---|
| `scope_id` | yes | name of this grant; recorded with every operation made under it |
| `tool` | yes | this tool's name |
| `granted_by`, `granted_at` | yes | who granted it, and when (UTC, `Z`) |
| `expires_at` | yes | UTC time, or `null` for no expiry; SHOULD be a time |
| `effects` | yes | the effects allowed; `observe` needs no grant |
| `dangerous` | yes | whether dangerous commands are allowed |
| `targets` | no | selectors `{key, match}`, `match` `subtree` (default) or `exact`; absent means every target the account can reach, so it SHOULD be present |
| `destinations` | no | where `deliver` commands may send; absent or empty means nowhere |
| `outputs` | no | absolute directories where new local files may be created (`artifact`); absent means none |
| `roots` | no | absolute directories whose existing files may be overwritten (`persist`); absent means none |
| `limits.max_operations` | no | operations that may be confirmed under this `scope_id`, counted in `<state_dir>/scope-usage.json`, which pruning the journal never resets |
| `revoked` | no | `true` withdraws the grant |
| `notes` | no | free text for people |

How a person grants a scope:

1. Take the path and a starting point from the refusal the agent relays (`details.scope.path`, `grant_example`); `<tool> context` also shows the path and the current grant.
2. Write the file there. Put your name in `granted_by`, set `expires_at`, keep `dangerous` false unless the task needs it, and narrow `targets` to the task.
3. Run `<tool> doctor`: its `authorization` check says whether the file is valid and what it grants.
4. To withdraw the grant, set `"revoked": true` or delete the file. For a one-off task, keep a task-specific file elsewhere and set `<PREFIX>_SCOPE_FILE` in the environment the agent runs in.

Why: authority must come from a person, not from the agent or anything it reads. Unknown keys are rejected because a typo would otherwise be silent and could widen the grant: `"target": [...]` leaves `targets` absent, which means every target. Duplicate keys, and keys that differ only in capitals, are rejected because parsers disagree on which value wins, so a reviewer could read one grant while the tool applies another: Go's JSON decoder applied `"Dangerous": true` from a file that also said `"dangerous": false`. Relative paths are rejected because they would resolve against whatever directory the agent runs in.

Check: aicli scope tests (unknown key, duplicate key, wrong `tool`, relative path: all `invalid`); review that no command writes to the scope path.

### SEC-012 Scope checks

At dry-run, at the refusal of a bare call and at confirm, the gate MUST refuse unless every check below passes; `details.scope.status` names the first that failed:

- the file exists and is valid (`missing`, `invalid`), is not past `expires_at` (`expired`) and is not revoked (`revoked`);
- every non-`observe` effect of the command is in `effects`, and a dangerous command has `"dangerous": true` (`insufficient`);
- when `targets` is present, a selector matches the target key (`insufficient`). `exact` matches an equal key. `subtree` matches an equal key, or a key that continues with `/`, `!`, `#`, `:` or `@`; a selector key that ends in one of those characters matches anything after it. So `ops/` covers `ops/n-42` and `acme/web` covers `acme/web!5`, but neither covers `ops-archive/n-1` or `acme/webstore`;
- every destination in the preview matches a `destinations` entry (`insufficient`), by the destination rule, not the target rule (contract.json `scope.destination_match`). An entry containing `://` matches an equal destination, or one with the same scheme and host and a path at or below the entry's, compared by whole segments; a destination with user info, a query, a fragment or a dot segment matches only an equal entry. Any other entry (an email address, a handle, a channel id) matches only an equal destination, or, when the entry ends with `/`, a destination that continues after it at a `/` boundary. `@`, `:`, `#` and `!` are ordinary characters here, so `bob` does not cover `bob@evil.example`;
- every local path the command writes, after resolving symlinks and `..`, lies inside `outputs` when it does not exist yet and inside `roots` when it does, so absent `roots` means nothing may be overwritten (`insufficient`, with `missing.outputs` or `missing.roots`); a junction or other reparse point in the path that cannot be resolved fails the check, and `WriteOutput` and `OpenOutput` check again when they write. A command that declares `artifact` without `persist` never overwrites: an existing path is `E_VALIDATION` (`invalid_value`, `details.path`), at the dry-run and when it writes, not a scope refusal, since no grant could allow it;
- fewer than `limits.max_operations` operations have been dispatched under this `scope_id`, counted in `scope-usage.json`, under its own lock, in the same step that consumes the token (`budget_spent`), so concurrent confirms cannot overrun it and pruning cannot refill it.

The scope file's digest is bound into the token (CLI-054); a file changed between dry-run and confirm is `E_CONFLICT` `scope_changed` (CLI-057). Target keys MUST be canonical: one spelling per object after URL, name and alias resolution, so `ACME/Web` and `acme/web` are one key when the upstream treats them alike. Otherwise a second spelling could fall outside a selector the person wrote, or inside one they did not mean. A sub-resource's key continues its parent's (`acme/web#12/notes/99`, CLI-048), so a grant for the parent covers it.

Why: re-checking at confirm makes a narrowed or revoked scope apply to tokens already issued. Boundary-aware matching keeps `ops` from covering `ops-archive`; a separate destination rule keeps a grant for one recipient from covering an address that merely starts with it; and resolving symlinks keeps a path inside `outputs` from writing somewhere else. In the 2.0 release review, a Windows junction under an allowed directory let a write land outside it, because Go's `filepath.EvalSymlinks` no longer follows junctions, and a grant of `outputs` alone let a command overwrite an existing file there. In the gitlab-cli pilot (2026-10), a download whose file already existed asked a person for a `roots` grant that could not help, because the command never overwrites.

Check: G07, G10; aicli tests for each status, selector boundaries, destination matching, symlink escape and budget counting; review how the tool builds target keys.

### SEC-013 Honest protection statement

SECURITY.md and every Skill MUST describe the scope as protection against injected instructions, agent drift and wrong targets, and MUST NOT present a scope file or a confirm token as proof of human approval. They MUST say what it cannot stop: any process running as the same OS user, the agent included, can write the scope file, the confirm key and the journal, or point `<PREFIX>_SCOPE_FILE` or `<PREFIX>_STATE_DIR` elsewhere (a fresh state directory also resets the operation budget and forgets unresolved operations). A tool that claims approval an agent cannot forge MUST name, in `reference` and SECURITY.md, the channel the agent cannot write to (another OS account, an upstream approval workflow, a hardware key).

Text for SECURITY.md (adapt the bracketed parts):

> Writes are refused until a person grants a scope file ([path]). Every write is checked against it before anything is sent, which stops injected instructions, an agent drifting off-task and wrong targets. It is not proof of approval: any program running as your OS user, the agent included, can edit the file. Write scope files yourself, or review any that an agent writes at your request; keep them narrow and short-lived. [For approval an agent cannot forge, use … / This tool provides no such channel.]

Why: scope files and tokens live where the agent's OS user can write. Overstating them leads people to grant broad, long-lived scopes in the belief that each write is approved separately.

Check: review the wording of SECURITY.md and SKILL.md.

## The write gate

### SEC-020 Write-gate security properties

Every non-`observe` command MUST go through aicli's gate (CLI-050). For reviewers, these are the properties the gate provides, where each is specified and how it is tested:

| Property | Meaning | Rule | Check |
|---|---|---|---|
| Shared implementation | no tool-local token, binding, key or single-use store | CLI-050 | review |
| Preview first | a token exists only with the preview that produced it: a dry-run, or the refusal of a bare call | CLI-051, CLI-052 | G01, G02 |
| Complete binding | command path; every supplied flag and positional after normalisation, except the declared control flags; content digests of file and stdin inputs; resolved account, host and target; the active profile and every declared setting; scope digest; tool version; base version | CLI-053, CLI-054 | G03 |
| Keyed tokens | HMAC-SHA256 with a 32-byte machine-local key, never an unkeyed fallback; expiry at most 15 minutes | CLI-055 | G08 |
| Canonical encoding | only `^ct2_[a-z2-7]{26}_[0-9a-f]{32}$` is accepted; single use is keyed by operation id | CLI-055 | G05 |
| Atomic single use | one exclusive-create marker per operation, across processes | CLI-055 | G04 |
| Fail closed | no usable key, no token; a journal that cannot be written, or a corrupt record, no write | CLI-055, CLI-058 | G08 |
| No mode bypass | no output mode, flag or environment variable turns the gate off | CLI-055 | G06 |
| Dangerous second flag | `--dangerous` in both steps | CLI-056 | G09 |
| Re-check at confirm | scope, base version and plan (`state_drift`), account, host and target, under a per-target lock held until the record says `dispatching` | CLI-057 | G10 |
| Record before send | the operation record is written before dispatch, or nothing is sent | CLI-058 | G08 |

No library can check the following, so review MUST:

- compare each command's declared effects with what it does: a write declared `observe` skips the gate, the scope and the journal;
- confirm that every input that shapes the request arrives as a declared parameter, a file input read through aicli (`ReadFileInput`) or the identity (account, host); a value read anywhere else at apply time (an environment variable, a config file, a path opened directly, a relative path resolved later) is not bound;
- confirm that `Preview` only reads, and that `Apply` sends only what the preview described;
- confirm that `NotDispatched` is returned only when nothing reached the upstream and `Rejected` only when the upstream definitively refused; anything uncertain stays an unknown outcome (CLI-015);
- confirm that a lookup before `App.Write` reads nothing a confirmed write can change: it runs at every confirm before the token checks, so a target lookup belongs in `WriteSpec.ResolveTarget` (CLI-057);
- confirm that text a command sends cannot carry effects the command does not declare: where the upstream runs commands written in free text (GitLab quick actions such as `/merge` or `/assign @x` in a comment or a description), `Preview` blocks such lines, or the command declares the effects they can cause.

Why: v1 stated the right rule and let each tool implement it. The 2026-10 audit of the fleet's ten gated tools found the same failures in tool after tool:

- every binding missed inputs: fields picked by hand; files bound by path, not content; batch paths that bound only the target list; an account, region or timezone read from environment or config and left unbound;
- nine had forgeable tokens: an unkeyed fallback when the key failed, no key at all, or an empty key accepted;
- eight let a token be reused when the single-use store failed, as v1 required, and four replayed re-encoded tokens;
- two skipped the gate in text output mode, and some writes ran outside the gate or were declared as reads.

Ten implementations produced ten sets of holes. The gitlab-cli pilot (2026-10) added one that no library can see: GitLab runs a `/merge` line in a comment as a command, so a command that posts comments could merge a merge request.

Check: the conformance suite runs in CI with a sample for every write command (VER-001, G00 to G10); the fleet gate audit is re-run for each spec release (VER-007).

## Secrets, state and credentials

### SEC-021 Secrets

Values of parameters declared `secret` (CLI-024), credentials, and tokens derived from them MUST NOT appear in stdout, stderr, previews (shown as `"***"`), confirm tokens, operation records, logs, notices, error details or `reference` examples. A tool MUST NOT publish an unsalted hash of a secret, whole or truncated; where a fingerprint is needed to tell credentials apart, it MUST be a keyed hash (HMAC with a machine-local key). aicli binds secret values as HMAC digests under the confirm key and never stores them raw. It replaces with `***` every value it knows to be secret, also in its URL-escaped forms, in error messages and details (an HTTP client's error often quotes the request URL) and in debug lines: secret parameters, the flag of a secret setting and the literal twin of a secret file input (CLI-100, CLI-048), secret settings, and values the tool registers with `App.AddSecret` (a token read from the keyring). Secrets the upstream returns (a CI/CD variable's value, a key shown once) are never output either: a command reports that such a value exists and whether a write changed it, a read-back compares it in memory and shows `***`, and no flag shows it; a person reads it in the upstream's own interface. A flag is secret when any of its values can be (a variable's `--value`, masked or not).

Why: an unsalted hash of a token or password can be matched or brute-forced offline, so publishing it leaks the secret. The 2026-10 fleet audit found a login dry-run that printed the full unsalted SHA-256 of an access token, and a token payload that carried an unsalted SHA-256 of an argument list including a password. gitlab-cli 1.x showed CI/CD variable values behind a `--show-values` switch; its 2.0 pilot (2026-10) dropped the switch.

Check: every credential-carrying parameter is listed in `Meta.Secrets`; the tool's tests run each write with a sentinel secret and search every envelope, record and log for the sentinel and for its unsalted SHA-256; review.

### SEC-022 Outbound contact

A tool MUST connect only to the upstream services the user configured (the hosts in its settings or credentials, CLI-100) and, in `update` and `doctor`, to its release source: the GitHub API and release downloads, and the Sigstore trust-root service that verifies them (CLI-070, CLI-071). `update` refreshes the trust root with the same timeout, proxy, CA file and User-Agent as its other requests (CLI-110); the Python library's refresh ignores the CA file and the User-Agent (CLI-070). It MUST NOT send telemetry, analytics, crash reports or usage pings, to its authors or to anyone else. Any other destination a feature needs (an identity provider's token endpoint, a CDN that serves attachments) MUST be listed in SECURITY.md with what is sent there.

Why: the tool runs with the user's credentials over the user's data, and neither the user nor the agent sees what it sends besides its answer, so every destination has to be readable in one place. A usage ping would also carry the commands an agent ran, and those describe the user's work.

Check: review every network call site and hard-coded host against SECURITY.md; no analytics or crash-reporting dependency.

### SEC-030 State store

The state directory is `App.StateDir` (tests), else `<PREFIX>_STATE_DIR`, else `~/.<tool>`. It holds `confirm.key`, `operations/` (records and single-use markers), `locks/` (per-target confirm locks, CLI-057), `scope-usage.json` (the operation budget per `scope_id`, SEC-012), the default `scope.json`, the default `settings.json` (CLI-100) and `update-check.json`. aicli MUST:

- on POSIX, create the directory 0700 and its files 0600, setting the mode at creation, and report `posix-0700` in `context.state.protection`;
- on Windows, report what was verified for the resolved state directory: `windows-profile-acl` when it lies inside the user profile, `windows-explicit-acl` when an explicit owner-only ACL was set and verified, otherwise `windows-unverified` (and `doctor`'s `state_store` check warns). Never claim owner-only access that was not set;
- refuse with `E_IO` a state path that is a symlink or not a directory;
- treat an unreadable key, a key that is not exactly 32 bytes, or an unreadable or corrupt record as a failure: refuse the write, or block the record's target, and never treat it as empty, absent or approved.

Deleting `confirm.key` makes the tool create a new key; tokens already issued stop verifying.

Why: a store that fails open turns a disk or permission error into a forged or replayed write. The 2026-10 fleet audit found keys that fell back to an unkeyed hash when they could not be read or saved, and single-use stores that let a token through when they could not be read or written; v1 required the latter ("never block the write").

Check: G08; aicli tests for a symlinked or non-directory state path, a wrong-size key and a corrupt record; `context.state.protection` on every CI operating system.

### SEC-040 Credentials at rest

A tool that stores credentials MUST follow v1's keyring pattern, in order of preference:

1. Passwords are used once and exchanged for tokens at login, never stored; a durable secret the protocol needs (Basic auth, for example) goes into the keyring.
2. Secrets live in the OS keyring (Windows Credential Manager, macOS Keychain, Linux Secret Service), which the OS binds to the user's login.
3. Config files hold no secrets: only metadata (URL, account name, region) and which backend holds the secret. aicli refuses a settings file that holds a setting declared secret (CLI-100).

Also:

- File encryption (AES-256-GCM, key from a machine-bound KDF) is a fallback for hosts without a keyring, not a peer: it resists copying the file off the machine, not a local attacker. `context.credentials.source` reports the active backend (`env`, `keyring` or `encrypted_file`), so the fallback is visible.
- Environment variables are the non-interactive channel. Secret flags are not the documented path: argv shows up in process lists, shell history and the agent's transcript. A person sets credentials up before the session; agents never handle secret values.
- Commands that store, change or remove credentials or profiles, `auth login` and `auth logout` among them (CLI-101), declare `configure` (contract `effects.configure`), so they are T2 and dangerous by default: an agent cannot change the account the tool acts as without a scope that grants `configure` and `dangerous`.
- 0600 is a POSIX statement. On Windows, protection comes from the profile ACL, or from holding no secret file at all; do not claim owner-only access without setting an ACL.
- Secrets stay in memory only as long as needed and never reach logs, stdout or stderr (SEC-021).

Why: copying a config file off the machine should yield nothing usable, and the keyring ties secrets to the user's login. Credentials passed on the command line leak to every process listing and to the agent's transcript.

Check: review the config files and `context.credentials`; a test that the file fallback reports `encrypted_file`; credential and profile commands declare `configure`.

## Supply chain

### SEC-050 Supply chain

Kept from v1:

- **Signed releases.** CI signs `checksums.txt` with Sigstore keyless signing from the tagged release workflow (`cosign sign-blob --new-bundle-format`); npm packages are published with provenance.
- **In-process verification.** On the binary path, aicli's `update` verifies the signature in-process (Go `sigstore-go`; Python `sigstore` inside the frozen binary): the trust root is embedded rather than trusted on first use, and a trust root that cannot be refreshed stops the update with `E_NETWORK` before anything is installed; the certificate identity is the exact release workflow at the release's tag, with the GitHub OIDC issuer. It then verifies the archive checksum. Any failure is `E_INTEGRITY` (not retryable), with no "proceed anyway" path.
- **Dependency audit** for every ecosystem the tool ships in (`govulncheck` and `npm audit` for a Go tool behind an npm wrapper; `pip-audit` and `npm audit` for Python), failing on high severity, on the merge path and again on the release path before anything is signed (VER-008).
- **Traceable builds**: release artifacts are built in CI from tagged source; no hand-uploaded binaries.
- **No downloads at install**: the npm wrapper runs the platform package already installed; no install script fetches code.

New in 2.0, implemented by aicli's `update` built-in (CLI-070) and the vendored library:

- **No update code in the tool.** A tool sets `App.Update` and writes no update path of its own; the tree check rejects a tool-defined `update` (CLI-021).
- **Strict semver targets.** Every version the update path uses, whether from `--target-version` or from release metadata such as a tag name, passes `ParseVersion` before any network call, file write or exec, and so does the version an update notice shows. A `--target-version` that fails is `E_USAGE` (`invalid_value`); a release tag that fails is skipped, never used. A release-endpoint override for tests skips no check.
- **No shell.** Package-manager commands are argv lists built by `PackageManagerArgs` from a fixed package name and the validated version, and run without a shell. On Windows, npm, pnpm and yarn are `.cmd` shims run by cmd.exe, where argument quoting does not neutralise `&`, `|`, `<`, `>`, `^` or `%`; validating the version first is what makes the call safe.
- **No silent downgrade.** A target older than the running version needs `--allow-downgrade`; `CheckUpdateTarget` checks the version and the downgrade in one call.
- **Honest status.** `signature_status` is `verified` only when in-process verification ran and passed; a package-manager path reports `not_checked`, or `unavailable` when no signature exists.
- **Skills from the installed package.** `update` copies Skills from the installed package into the Skill directories where they are installed, never from a network source (SKL-009); the README's Skill installer runs at a pinned version too (`npx skills@<version> …`, REPO-010).
- **Drift guard.** `scripts/check-spec.js` fails CI when a vendored aicli file (`internal/aicli/`, `<pkg>/_aicli/`) differs from its `MANIFEST.json` digest (offline, strict), or a vendored spec file differs from the pinned tag (when online). Fix the gate in the template, never in a vendored copy.

Why: the 2026-10 fleet audit found `update` commands passing an unvalidated version string to `npm.cmd`, which is arbitrary command execution on Windows in one ungated call: exploitable through a flag in two tools, reachable through a crafted release tag in five more; one tool validated the version. Four tools accepted an explicit older version as an "update". v1 itself prescribed an unpinned Skill fetch from the repository on every update, so whoever controlled the default branch could change agent instructions without a release. Each of those tools had written its own update path; one built-in, tested once, replaces them.

Check: aicli tests reject `1.0.12&calc`, `1.2` and `1.2.3 || x` and accept `1.2.3` and `v1.2.3-rc.1`, and cover a bad signature, a wrong identity and a bad checksum; review the release workflow, the CI audits and the `UpdateConfig`; the drift guard runs on merge and on release (VER-008).

## Checklist

### SEC-060 Checklist by tier

A review ticks every item up to the tool's tier. Each item names the rule that defines it.

T0, every tool:

- [ ] Tier in `reference.risk_tier` and SECURITY.md, at least the floor of the declared effects, worst case in one sentence (SEC-001)
- [ ] External text listed in `meta.untrusted` and in each schema's `untrusted_fields`; fields agents act on built only from tool-controlled values (SEC-002)
- [ ] No secret, and no unsalted hash of one, in output, previews, tokens, records, logs or examples, secrets the upstream returns included (SEC-021)
- [ ] The tool connects only to its configured upstream and, in `update` and `doctor`, to its release source; no telemetry; any other destination listed in SECURITY.md (SEC-022)
- [ ] Stored credentials follow the keyring pattern; env vars are the documented channel; no secret in config or settings files; `context.credentials.source` is truthful (SEC-040)
- [ ] Signed checksums, in-process verification, dependency audits for every shipped ecosystem on merge and release, CI builds from tags, no install-time downloads (SEC-050)
- [ ] `update` is aicli's built-in from `App.Update`, with no update code of the tool's own: strict semver, argv without a shell, `--allow-downgrade`, truthful `signature_status`, Skills copied from the installed package (SEC-050)
- [ ] Vendored aicli and spec files pass the drift guard (SEC-050)

T1, also:

- [ ] Every non-`observe` command registered with truthful effects and run through aicli's gate; no token, key or store code in the tool (SEC-020)
- [ ] Conformance suite in CI with a sample for every write command; inputs reach commands only as declared parameters, aicli file inputs or the identity (SEC-020)
- [ ] Writes refused without a scope; the tool never writes the scope file; target keys canonical (SEC-010 to SEC-012)
- [ ] SECURITY.md gives the scope path, how to grant and withdraw it, and the honest protection statement (SEC-011, SEC-013)
- [ ] State store permissions, and `context.state.protection`, correct on every shipped OS (SEC-030)

T2, also:

- [ ] Irreversible and account-level commands declared dangerous, bulk ones only when what they do is; `deliver` and `configure` left dangerous (SEC-001)
- [ ] `deliver` previews list their destinations, and `destinations` is enforced (SEC-012)
- [ ] Credential and profile commands declare `configure` (SEC-040)
- [ ] The Skill stops before dangerous actions the user did not explicitly request (SKL-005)

Part of [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec). Copyright (c) 2026 Sean Guo. MIT License; see `LICENSE` in the spec repository.
