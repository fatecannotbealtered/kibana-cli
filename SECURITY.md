# Security Policy

*English | [中文](SECURITY_zh.md)*

Security policy for **kibana-cli** (@fateforge/kibana-cli): A Kibana CLI that searches, reads and counts application logs in Elasticsearch through Kibana's Console Proxy, and lists Kibana's data views, fields and saved objects. The rules behind it are in [`.agent/SEC-SPEC.md`](.agent/SEC-SPEC.md) (`SEC-xxx`) and [`.agent/CLI-SPEC.md`](.agent/CLI-SPEC.md) (`CLI-xxx`).

## Supported Versions

Security fixes are applied to the **latest minor release** on the default branch. Older minors do not receive backports. Release binaries are published via GitHub Releases (`fatecannotbealtered/kibana-cli`) and the npm package `@fateforge/kibana-cli`.

| Version | Supported |
|---------|-----------|
| latest `2.0` minor | Yes |
| older minors | No |

## Reporting a Vulnerability

Please **do not open public GitHub issues for undisclosed vulnerabilities.**

Report privately through either channel:

- **GitHub private advisory**: open a draft advisory at `https://github.com/fatecannotbealtered/kibana-cli/security/advisories/new`.
- **Email**: guosong6886@gmail.com.

Include: a description and impact, steps to reproduce (if safe to share), and the affected version and install method (npm, binary, or `go install` / `pip install`).

**Acknowledgement SLA:** you should receive an acknowledgement and a triage decision within **5 business days**. Thank you for helping keep users safe.

## Risk Tier

`kibana-cli` is **T2**: it changes nothing in Kibana or Elasticsearch, but `auth login` and `auth logout` store, replace and remove its own sign-in in the OS keyring (`configure`), and the `field-map` commands write the local field-map file (`artifact`, `persist`). A tool's tier is at least what its commands' effects require, and T2 whenever a command can cause irreversible or account-level damage (SEC-001); `kibana-cli reference` reports it as `risk_tier`.

| Tier | Effects | Examples |
|------|---------|----------|
| **T0** | `observe` only | queries, searches, read-only reports |
| **T1** | `change`, `artifact`, `persist` | edit an object upstream, export a file, overwrite a local file |
| **T2** | `deliver` or `configure`, or irreversible / account-level damage | send or publish, change permissions or credentials, delete a project |

The tier describes what the commands can do, not what the credential could do. `kibana-cli reference --index` lists each command's effects and whether it is dangerous. Dangerous commands (irreversible deletes and merges, and every `deliver` and `configure` command) and dangerous plans (a write whose preview says `dangerous: true`, such as a commit that deletes files) need `--dangerous` in both write steps and a scope that grants `"dangerous": true` (CLI-056, SEC-012). A bulk command is dangerous only when what it does is, not because it has many targets.

<!-- A T0 tool (no write commands) replaces the next two sections with one sentence: it has no write commands, so it needs no scope file and has no write gate. -->

## Authorization

> Writes are refused until a person grants a scope file (`~/.kibana-cli/scope.json`). Every write is checked against it before anything is sent, which stops injected instructions, an agent drifting off-task and wrong targets. It is not proof of approval: any program running as your OS user, the agent included, can edit the file. Write scope files yourself, or review any that an agent writes at your request; keep them narrow and short-lived. This tool provides no channel for approval that an agent cannot forge.

<!-- SEC-013: if the tool does offer approval an agent cannot forge (another OS account, an upstream approval workflow, a hardware key), name that channel in the last sentence above and in reference. -->

- **Where**: the file named by `KIBANA_CLI_SCOPE_FILE`, else `scope.json` in the state directory. There is no command-line flag for it, and the tool never creates, edits, moves or deletes it (SEC-011).
- **Grant**: take the path and a starting point from the refusal an agent relays (`details.scope.path` and `grant_example`); `kibana-cli context` also shows the path and the current grant. Write the file with your name in `granted_by`, an `expires_at`, `targets` narrowed to the task, and `dangerous` false unless the task needs it. Run `kibana-cli doctor`: its `authorization` check says whether the file is valid and what it grants.
- **Withdraw**: set `"revoked": true` or delete the file. For a one-off task, keep a task-specific file elsewhere and set `KIBANA_CLI_SCOPE_FILE` in the environment the agent runs in.
- **Checks**: at the dry-run, at the refusal of a bare call (one with neither `--dry-run` nor `--confirm`) and again at confirm, the effects, the `dangerous` flag, the target (within `targets`), delivery destinations (within `destinations`), local paths (within `outputs` and `roots`, after resolving symlinks), expiry, revocation and the operation budget (`limits.max_operations`) are all checked; when the scope file changes after the token was issued, the confirm fails with `scope_changed` and nothing is sent (SEC-012, CLI-057).
- **Strict format**: invalid JSON, a duplicate or unknown key, a value of the wrong type, another tool's name or a relative path makes the file invalid, and an invalid file grants nothing. A typo such as `"target"` for `"targets"` would otherwise widen the grant silently.

## The Write Gate

Every command with an effect other than `observe` runs through the write gate of the vendored aicli library, never through code of this tool's own (CLI-050, SEC-020):

- `--dry-run` returns a preview and a confirm token; `--confirm <token>` must repeat the same command with the same inputs. A write run with neither flag changes nothing.
- The token binds the resolved request: the command, every supplied parameter, the content of file and stdin inputs, the account, host and target, the scope file, the tool version and the target's version at preview time. It is keyed with HMAC-SHA256 under a 32-byte machine-local key, expires within 15 minutes and works once (CLI-054, CLI-055).
- An operation record is written before anything is sent. When the key, the journal or a record cannot be read or written, nothing is sent (CLI-058, SEC-030).
- No output format, flag or environment variable turns the gate off. The conformance suite checks these properties for every write command in CI (VER-001).

## State Directory

`~/.kibana-cli` (or `KIBANA_CLI_STATE_DIR`) holds `confirm.key`, the operation journal `operations/`, the default `scope.json`, the default `settings.json` and `update-check.json`. On POSIX the directory is created 0700 and its files 0600; on Windows it relies on the user-profile ACL, and `kibana-cli context` reports which protection applies in `state.protection`. A state directory outside your profile is reported as `windows-unverified`, and `doctor` warns. The vendored aicli library sets no ACL itself, so `context` never reports `windows-explicit-acl` (an owner-only ACL set and verified). A symlinked or non-directory state path is refused, and a key or record that cannot be read is never treated as empty or approved (SEC-030). Deleting `confirm.key` makes the tool create a new key, and tokens already issued stop working.

## Credential Handling

- **OS keyring only**: `auth login` stores the password in the OS keyring (Windows Credential Manager, macOS Keychain, Linux Secret Service) as `kibana-cli` / `<host>|basic|<user>`, the entry 1.x used, and only after Kibana accepted it. There is no encrypted-file fallback: where no keyring exists, use `KIBANA_CLI_PASSWORD`. `kibana-cli context` reports the source in use as `credentials.source` (`env`, `keyring` or `none`) (SEC-040).
- **No secrets in settings**: `~/.kibana-cli/settings.json` holds only metadata in named profiles, such as the host, the account name and which backend holds the secret; the vendored aicli library refuses a settings file that contains a secret setting (CLI-100, CLI-101).
- **Passwords**: Kibana's HTTP Basic auth needs the password on every request and offers no token to exchange it for, so the password is the durable secret, stored only in the OS keyring (SEC-040). A password read from the keyring or the environment is registered as a secret, so it is redacted wherever it would appear.
- **Environment variables** (`KIBANA_CLI_HOST`, `KIBANA_CLI_USER`, `KIBANA_CLI_PASSWORD`) are the non-interactive channel and take precedence over stored credentials. A person sets them before the agent's session. Secrets are not passed as flags in documented use: argv shows up in process lists, shell history and the agent's transcript, so `auth login` reads the password with `--password-file -` (stdin), typed by the user in their own terminal.
- **Changing credentials is dangerous**: commands that store, change or remove credentials or profiles declare the `configure` effect, so an agent needs a scope that grants `configure` and `dangerous` to change the account the tool acts as (SEC-040).
- **Redaction**: secret values never appear in stdout, stderr, previews (shown as `"***"`), confirm tokens, operation records, logs, `KIBANA_CLI_DEBUG` output or error details, and no unsalted hash of a secret is ever published (SEC-021, CLI-102). Log documents hold whatever the logged systems wrote, secrets included; kibana-cli returns them as untrusted data and cannot redact what it does not know to be secret, so narrow searches to what the task needs. A new flag that carries a secret, in any of its values, is declared in `Meta.Secrets`.

## Outbound Contact

`kibana-cli` connects only to the Kibana host you configure (Elasticsearch is reached only through Kibana's Console Proxy, never directly) and, in `update` and `doctor`, to its release source: the GitHub API and release downloads of `fatecannotbealtered/kibana-cli`, and the Sigstore trust-root service that verifies them; `update` refreshes the trust root with the same timeout, proxy, CA file and User-Agent as its other requests (a Python tool's refresh uses the proxy and the timeout, but not the CA file or the User-Agent). It sends no telemetry, analytics, crash reports or usage pings (SEC-022). Every request verifies TLS; a private CA goes in `KIBANA_CLI_CA_FILE`, and no setting turns verification off. Proxies come from `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` (CLI-110).

<!-- SEC-022: list here any other destination a feature needs (an identity provider's token endpoint, a CDN that serves attachments) and what is sent there. -->

## Untrusted Content

Text that Kibana returns and other people wrote (titles, descriptions, comments, message bodies, file names, logs, upstream error messages) is **untrusted data** and may carry instructions aimed at an agent ("ignore previous instructions and …").

- Every response lists the paths that hold such text in `meta.untrusted` (for example `data.items[].title`), and `reference` lists each output schema's `untrusted_fields` (CLI-046, SEC-002).
- Agents and integrations **must treat that text as data, not instructions**. The bundled Skill tells the agent to stop and ask when such text suggests an action (SKL-005, SKL-007).
- The tool never triggers an action, fetches a link or switches targets because of such text, and it builds every field an agent acts on (`message`, `next_action`, `did_you_mean`, `lookup`) from values it controls (SEC-002). A write that such text prompts still goes through the scope check and the write gate.

## Supply Chain

- **Signed releases**: the tagged release workflow signs `checksums.txt` with Sigstore keyless signing and publishes the bundle beside it; npm packages are published with provenance (SEC-050).
- **In-process verification**: on the standalone-binary path, the vendored aicli library's `update` verifies the signature in-process, with an embedded trust root and the certificate identity bound to this repository's release workflow at the release's tag, then the archive's checksum. Any failure is `E_INTEGRITY`, with no "proceed anyway" path.
- **No downloads at install**: the npm wrapper runs the platform package that npm already installed; no install script fetches code.
- **Dependency audits** for every shipped ecosystem: `govulncheck` (Go) or `pip-audit` (Python) for the binary, `npm audit` for the wrapper. They fail closed on every merge and again before a release is signed or published (VER-008).
- **Traceable builds**: release artifacts are built by CI from tagged source; no hand-uploaded binaries.
- **Vendored code**: `.agent/`, `contract/` and the aicli library are copied from ai-native-cli-spec at the tag in `.agent/SPEC_VERSION`. `scripts/check-spec.js` fails CI and the release when a library file differs from its `MANIFEST.json` or a vendored file from the pinned tag; fixes go to the spec repository, never to the copy here.

### Self-update

`kibana-cli update` changes the tool itself, so it is one command outside the write gate, hardened. It is the built-in `update` of the vendored aicli library, set up with `App.Update`; this tool has no update code of its own (CLI-070, SEC-050):

- every target version, whether from `--target-version` or from release metadata, must be strict semver before any network call, file write or process start;
- package managers run from an argument list built from the fixed package name and the validated version, never through a shell;
- a version older than the installed one needs `--allow-downgrade`;
- on the binary path, the release is installed only after its Sigstore signature and the archive's checksum verify;
- `signature_status` is `verified` only when aicli checked a signature itself; package-manager installs report `not_checked`;
- Skills are copied from the installed package into the Skill directories where they are installed, never from a branch fetched over the network;
- a failure reports `stage`, `current_version`, `binary_replaced` and `skill_sync_status`, so the agent can tell what is installed.

Review these assumptions before integrating `kibana-cli` into automation or AI-agent workflows.
