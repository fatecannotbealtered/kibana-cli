# CLI-SPEC: the machine contract

**中文版 → [CLI-SPEC_zh.md](CLI-SPEC_zh.md)**

**Spec 2.0.1** · envelope `schema_version` `"2.0"` · machine twin: [`contract/contract.json`](../contract/contract.json) · rules CLI-001 to CLI-110

This is the contract between an AI-native CLI and the agent that calls it: what the tool prints, how it fails, how its commands are shaped and found, how it reports reads, how it performs writes, and how it updates itself, reads its settings and talks to the network. It replaces CLI-SPEC 1.6.3; maintainers of 1.6.x tools follow `MIGRATION-2.0.md` in the ai-native-cli-spec repository. Companion specs in this directory: [SEC-SPEC.md](SEC-SPEC.md) (security, `SEC-xxx`), [SKILL-SPEC.md](SKILL-SPEC.md) (Skills, `SKL-xxx`) and [VERIFY-SPEC.md](VERIFY-SPEC.md) (verification and agent eval, `VER-xxx`). [AGENT.md](AGENT.md) is the playbook.

## How to read this spec

- MUST, MUST NOT, SHOULD and MAY have their RFC 2119 meaning.
- Each rule has a stable ID and a title, the requirement, `Why:` and `Check:`; IDs are never renumbered, and the gaps between groups are deliberate. `Check:` names the conformance check when aicli runs it: `RunConformance` runs `G00`–`G10` for every write command and `R01`–`R05` once per tool (VER-001), and `R06` runs separately through `CheckCoverage` (VER-003).
- `contract.json` is authoritative for codes, exit codes, enums and key sets. Where this text and the contract disagree, the contract wins and the text is a bug.
- Evidence comes from the 2026-10 gitlab-cli agent eval (720 trials over 5 arms, 3 models, Skill on and off) and the audit of the 11 fleet tools, 10 of which have a confirm gate. Numbers are quoted from that report and from the v2 principles draft that carries its evidence.
- Examples use a GitLab-like tool called `tool`, with state directory `~/.tool` and environment prefix `TOOL_`. JSON examples are valid JSON; long ones are wrapped for reading.

## The caller (informative)

The primary caller is a language model working through a harness such as Claude Code.

- **Every call is a round trip.** The model writes a command, waits, and reads the result; each round trip costs seconds of model time. Start-up adds to it: through the npm launcher 1.1–2.3 s per call on a Windows test machine, against 0.4–0.7 s for the binary alone.
- **Output passes through a window.** Claude Code shows a successful result inline up to about 30,000 characters, then only a 2,000-character preview and a file path. A failed result appears up to about 10,000 characters; beyond that only its start and end survive, and nothing is saved. The default command timeout is 2 minutes, and the model sees stderr too.
- **The model reads text.** It follows conventions it has seen (`--help`, `<noun> <verb>`, gh and glab flags), reads error messages, and copies identifiers from earlier output. It miscounts long lists and invents identifiers it was not given.

**Accuracy has three layers**, and the contract serves each:

1. Call accuracy: calls are valid the first time or fixable in one step (CLI-012, CLI-022, CLI-048).
2. Report accuracy: everything the tool reports was observed and is true. Unknown stays unknown, partial never rounds up to success, and an incomplete list says so (CLI-015, CLI-041, CLI-059).
3. Answer accuracy: the agent's final answer matches the evidence, helped by computing in the tool instead of shipping raw lists (CLI-044) and by marking external text (CLI-046).

**Speed has three costs**: the number of round trips (CLI-034, CLI-049, CLI-052), tokens per round trip (CLI-003, CLI-040, CLI-042), and overhead per call (CLI-090).

**Authority comes from people.** A person grants a scope file and the tool enforces it (SEC-010). A preview and its confirm token bind the agent's own plan to the request that runs; they are not an approval.

## What aicli gives you

> aicli is the shared library in this spec's template: one flat Go package (`template/go/aicli`, vendored to `internal/aicli/`) and one Python package (`template/python/aicli`, vendored to `<pkg>/_aicli/`). `scripts/sync-spec.js` copies it pinned to the spec tag, and `scripts/check-spec.js` fails CI when the copy drifts. Never edit it in a tool repo.
>
> | Rules | aicli does | The tool still does |
> |---|---|---|
> | Output: CLI-001–006, 046, 047 | one envelope writer (a panic becomes `E_UNKNOWN`); compact JSON and `--pretty`; `--fields` projection; `meta`; format checks and `OutputFormat`; quiet stderr; notices; an NDJSON stream writer (CLI-006) | declare `raw` and streaming commands; name the paths of external text |
> | Errors: CLI-010–018 | the contract's code table; constructors for every details shape; enriched usage errors; `FromHTTP`; extension codes declared in `App.ExtCodes` (`CodeSpec`), checked and published | messages written for the model (CLI-017); `next_action` on its own errors; the role on a 403; `contract-ext.json` |
> | Command model: CLI-020–029 | the registry (cobra or click tree plus `aicli.Meta`, with `Meta.Params` (`ParamDoc`) for types, enums and units); tree checks; path-first parsing; positionals; accepted guesses (`AddShortcut`, `ShortFlags`, `FlagAliases`, `Meta.FlagAliases`, `Meta.Aliases`), suggested ones (`FlagSynonyms`, `VerbSynonyms`, and default synonyms of the shared flag names) and whole-tree suggestions; the Skill's command map (`App.CommandMap`) | true `Meta`: effects, output schema, error codes, examples, maturity, evidence; its guess list; the shared flag names and verb meanings (CLI-028, CLI-029) |
> | Discovery: CLI-030–037 | `--help`, `reference` (full, `--index`, `--command`), `context` (with `profile` when the tool declares settings), `doctor` (its `version` check reads the installed Skill from `App.SkillDirs`; a `settings` check when the tool declares settings), `changelog` | the `Identity`, `DoctorChecks` and `ContextExtra` hooks; the embedded `CHANGELOG.md` |
> | Reads: CLI-040–049 | projection and `WantsField`; `Page` with the completeness rules; `Ambiguous`; file and stdin inputs; `ParseTime` and `ParseDuration`; `--output <file>` files written through the gate (`WriteOutput`, `OpenOutput`, CLI-042) | default fields, a true `scope`, server-side filters, refs, URLs and names, size (CLI-040–045); `--input` (CLI-048); multi-ref reads and `--wait` (CLI-049) |
> | Writes: CLI-050–065 | the whole gate: binding (`SetResolved` binds a value resolved from another form), tokens, key, journal, scope checks, receipts and `ok`, batches (with native bulk calls through `ApplyAll`), `operation show/list/resolve`, `HumanRequired` | the `Target`, or `ResolveTarget` when finding it takes a lookup; a read-only, deterministic `Preview` with the base version; an `Apply` that classifies failures honestly and reads back what it changed; long-running jobs (CLI-064) |
> | Self-update: CLI-070, 071 | the `update` built-in, set up with `App.Update` (`UpdateConfig`): release discovery, Sigstore and checksum verification, the atomic swap or the package-manager run, the Skill sync, staged errors; `RecordUpdateCheck`, the cached notice in `meta.notices` and `doctor`'s refresh | an `UpdateConfig` with the repository and the package names; release archives with `skills/` beside the binary (REPO-043) |
> | Versions: CLI-080, 081 | `spec_version` and `schema_version` in `reference`; deprecation from `Meta.Deprecated`, `Meta.DeprecatedFlags` and `Meta.RenamedFlags`: the `Deprecated:` prefix, the `deprecated` notice, a command map without deprecated commands | what is deprecated and its replacement; the `CHANGELOG.md` entries |
> | Settings and diagnostics: CLI-100–102 | `App.Settings` and `App.Setting`: the settings file, profiles and `--profile`; `App.Debugf` and `<PREFIX>_DEBUG` | the declared settings; the `auth` commands (CLI-101) |
> | Network: CLI-110 | the rule for its own requests (`update`, `doctor`'s refresh) | the upstream client: timeouts, read-only retries, proxy, TLS, User-Agent |
> | Verification | `RunConformance` (`G00`–`G10`, `R01`–`R05`), `CheckCoverage` (`R06`, run from `TestMain`), `ValidateEnvelope` | a conformance sample per write command; command-level tests; the agent eval (VERIFY-SPEC) |

## Output

### CLI-001 One JSON document on stdout

In `json` mode a command MUST write exactly one JSON document to stdout, the envelope (CLI-002), followed by `\n`; only commands declared as streams use NDJSON (CLI-006), and `--help` prints text (CLI-030). Progress, warnings and diagnostics go to stderr; stdout carries no banners, prompts, progress bars or colour codes. Both streams are UTF-8 without BOM with `\n` line endings on every platform. No command prompts when stdin is not a terminal.

Why: the agent parses stdout and checks `ok`. One stray byte turns a valid result into a parse error, and a prompt hangs the call until the harness kills it.

Check: aicli writes every envelope through one writer, including panics; `R04` validates every envelope the suite produces.

### CLI-002 Envelope

A success envelope MUST have exactly the keys `ok`, `schema_version`, `data`, `meta`; a failure envelope exactly `ok`, `schema_version`, `error`, `meta`. `schema_version` is the string `"2.0"`. The payload lives in `data`, never at the top level, and a failure never carries `data`.

```json
{"ok":true,"schema_version":"2.0","data":{"ref":"acme/web!5","title":"Add retry","state":"opened","url":"https://gitlab.example.com/acme/web/-/merge_requests/5"},"meta":{"duration_ms":12,"untrusted":["data.title"]}}
```

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_NOT_FOUND","message":"No such merge request in this project, or this account cannot see it.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Run `tool mr list --project acme/web` to find the right one."}},"meta":{"duration_ms":3}}
```

Why: an agent checks `ok` and reads `data` or `error` the same way in every tool, and exact key sets let a validator catch drift mechanically.

Check: `R04` (`ValidateEnvelope` on every envelope).

### CLI-003 Compact JSON by default

JSON output MUST be compact by default: no indentation and no spaces between tokens. `--pretty` indents for people. `--compact` is accepted as a no-op, so older Skills and scripts keep working.

Why: a 50-item list is about 14,000 characters indented and about 10,000 compact. The eval could not show a gain, because agents already passed `--compact` on 74–96% of calls, but agents that do not know the flag still pay for the indentation.

Check: aicli's writer.

### CLI-004 meta

`meta.duration_ms` (integer milliseconds) MUST be present on every envelope, success and failure. The only other keys are `notices` (CLI-047, CLI-071) and `untrusted` (CLI-046), each omitted when empty.

Why: agents and evals read the cost of a call from `duration_ms`; keeping `meta` closed stops it from turning into a second, undocumented payload.

Check: `R04`.

### CLI-005 Formats

`--format` MUST accept `json` (the default and the only format for agents) and `text` (for people; it may change and is never parsed), and `raw` on commands that declare it (`Meta.RawOutput`): logs, diffs and file bodies passed through as bytes. `--json` is an alias of `--format json`. A format the command does not support fails with `E_USAGE`, `problem: unsupported_format`, emitted as JSON. A `raw` command that fails before writing any output emits the JSON error envelope. A command whose raw and JSON forms differ (a file's bytes against an object that describes it) asks `aicli.OutputFormat(cmd)` (Python `output_format(ctx)`) which format this run uses. The format never changes exit codes or the write gate (CLI-055).

Why: in the eval, `--format raw` on a JSON-only command failed with a plain-text error the agent could not parse, one of the frictions that made "assign an MR and label it" (T07) expensive.

Check: aicli rejects undeclared formats; `G06` runs every write with `--format text`.

### CLI-006 NDJSON streams

A command whose output is a stream (following a log, an event subscription, a long export) MAY emit NDJSON. Each line MUST then be a full envelope with one added key, `type`, which is `item` or `summary`, and the last line MUST be the `summary`: `ok: true` with what the stream covered (counts, plus the completeness keys of CLI-041 when it stopped early), or `ok: false` with `error`. The exit code matches the summary line, and a stream that ends without one is incomplete. The command's summary and examples say that it streams. aicli provides a stream writer that emits the item lines and the closing summary. In `--format raw` a stream writes the bytes alone, with no envelope and no summary line, and its exit code tells how it ended. An item line lists the untrusted paths of the item's own schema, not those of the command's output schema.

```jsonl
{"ok":true,"schema_version":"2.0","type":"item","data":{"time":"2026-10-12T08:00:01Z","line":"Running tests"},"meta":{"duration_ms":40,"untrusted":["data.line"]}}
{"ok":true,"schema_version":"2.0","type":"summary","data":{"count":1,"has_more":false},"meta":{"duration_ms":41}}
```

Why: a stream lets the agent stop early and keeps memory flat; the summary line tells a finished stream from a killed one. In the gitlab-cli pilot (2026-10), every `job log --follow` line listed the job object's untrusted field, and raw lines ended in a JSON summary that a pipe could not use.

Check: review; the tool's stream tests parse every line.

### CLI-007 Naming and values

Keys MUST be snake_case everywhere, including `data` and `details`. IDs are strings even when the upstream uses numbers. Times are ISO 8601 UTC with a `Z` suffix (`2026-10-12T08:00:00Z`), and a key ending in `_at` holds only such a time; a date without a time goes under a key ending in `_on` (`expires_on`). Enum values are lowercase snake_case unless contract.json fixes them otherwise. Quantities carry their unit in the key (`duration_ms`, `retry_after_ms`) or in the parameter's `unit` (CLI-024).

Why: agents copy keys and values between tools and between calls; gitlab-cli 1.2.17 answered `hasMore` where the rest of the fleet said `has_more`. GitLab returns some dates under `_at` keys (a member's `expires_at` of `2026-12-31`), which the gitlab-cli pilot (2026-10) renamed.

Check: `R04` checks key case and that `_at` values are UTC times; review for the other values.

## Errors

### CLI-010 Error object and code table

`error` MUST have exactly the keys `code`, `message`, `retryable`, `details`. The exit code and `retryable` follow from `code` by the table below (contract.json `error_codes.core`), and a tool MUST NOT change them for a core code. Tool-specific codes come only from `contract-ext.json` (CLI-018). `retryable: true` means nothing changed, so the call may be tried again after a back-off: it was a read, or a write the tool knows was not applied (CLI-015). A retried write starts again from `--dry-run` (CLI-055).

| code | exit | retryable | meaning |
|---|---|---|---|
| `E_USAGE` | 2 | false | The command line is wrong: unknown command or flag, unexpected argument, missing or invalid parameter. |
| `E_VALIDATION` | 2 | false | The request is well-formed, but the upstream or the tool rejected its content. |
| `E_AMBIGUOUS` | 2 | false | A name matched more than one resource; pick one of `details.candidates`. |
| `E_UNSUPPORTED` | 2 | false | A valid request this build, upstream version or mode cannot carry out. |
| `E_NOT_FOUND` | 3 | false | The resource does not exist or is not visible to this account. |
| `E_AUTH` | 4 | false | Credentials are missing, invalid or expired. |
| `E_FORBIDDEN` | 4 | false | Not allowed: no granted scope (`details.scope`), or the upstream refused the account (`details.required`). |
| `E_CONFIG` | 4 | false | Local configuration is missing or invalid. |
| `E_CONFIRMATION_REQUIRED` | 5 | false | A write needs `--dry-run` then `--confirm <token>`, or `--dangerous`; nothing was changed. |
| `E_CONFLICT` | 6 | false | A precondition failed (`details.conflict`). |
| `E_OUTCOME_UNKNOWN` | 6 | false | The change may have been applied; look it up with `details.lookup` before doing anything else. |
| `E_NETWORK` | 7 | true | The upstream could not be reached; nothing was sent. |
| `E_RATE_LIMITED` | 7 | true | The upstream rate limit was hit; wait `details.retry_after_ms`. |
| `E_SERVER` | 7 | true | The upstream failed while handling the request and nothing changed: a read, or a write the tool knows was not applied. Safe to retry; a write starts again from `--dry-run`. |
| `E_TIMEOUT` | 8 | true | A read timed out. |
| `E_HUMAN_REQUIRED` | 9 | false | A person must act first (`details.action`), then run `details.resume`. |
| `E_PARTIAL` | 1 | false | Some of the change took effect; read the receipt or items, and never resend the whole request. |
| `E_VERIFY_FAILED` | 1 | false | The change was applied, but the read-back disagrees with the request. |
| `E_VERIFY_INCOMPLETE` | 1 | false | The change was applied, but the required read-back could not be completed. |
| `E_INTEGRITY` | 1 | false | A release signature or checksum failed; do not retry. |
| `E_IO` | 1 | false | A local file or the tool's state store failed; nothing was sent. |
| `E_INTERRUPTED` | 130 | true | Interrupted by a signal before anything lasting happened. |
| `E_UNKNOWN` | 1 | false | An internal error; read `message` and `details`. |

| exit | meaning | the agent's next step |
|---|---|---|
| 0 | success | continue |
| 1 | partial, verify, integrity, io or unknown | read `error.details` |
| 2 | usage, validation, ambiguous, unsupported | fix the request; do not retry it unchanged |
| 3 | not found | find the right target |
| 4 | auth, forbidden, config | fix credentials, permission or scope, or ask the user |
| 5 | confirmation required | use the preview and token, or add `--dangerous` |
| 6 | conflict or outcome unknown | re-read state, or look the operation up |
| 7 | network, rate limit, server | back off and retry |
| 8 | timeout | back off and retry |
| 9 | human action required | relay `details.action`, then run `details.resume` |
| 130 | interrupted by a signal | check state, then retry |

Why: agents decide from `code`, exit code and `retryable` whether to retry, back off, fix the call or ask the user. That decision only carries over between tools if the three agree everywhere.

Check: aicli builds every error from the table generated from contract.json; `R04`.

### CLI-011 dispatch_state and next_action

Every error's `details` MUST carry:

- `dispatch_state`: whether a state-changing request was sent. `not_started`: none was. That covers every read, including one the upstream answered with an error such as 404 or 429, and everything before a write's apply step: usage and validation errors, ambiguous names, refusals by the gate or the scope, local failures. It also covers an apply step that failed before the request left the machine (a connection failure, `NotDispatched`). `dispatched`: a write's request reached the upstream and its answer is known; the receipt says whether the change applied. `unknown`: a write's request may have reached the upstream and no answer was observed (CLI-015).
- `next_action`: one sentence telling the agent what to do next, with the exact command in backticks when there is one.

Why: the agent always reads the error, so the error is the cheapest place to steer it; `dispatch_state` tells it whether a change may have reached the upstream, and so whether a retry could repeat it.

Check: `R04`; aicli fills both on every error it raises (parsing, gate, scope, journal).

### CLI-012 Usage and validation details

`E_USAGE` and `E_VALIDATION` MUST carry `problem`: `unknown_command`, `unknown_flag`, `unexpected_argument`, `missing_parameter`, `invalid_value`, `conflicting_flags`, `unsupported_format` or `input_too_large` (`confirmation_required` and `dangerous_flag_required` belong to `E_CONFIRMATION_REQUIRED`). Where they apply, they MUST also carry `parameter` (the flag as typed, or the word that is not a command), `expected` (the accepted values or format), `did_you_mean` (an array of runnable command paths that start with the tool name, such as `tool mr get`, or of flags), `usage` (the command's synopsis with its flags, as in the example below) and, for `unknown_command`, `available` (the commands in that group). `next_action` SHOULD be the corrected command when the tool can build it.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_USAGE","message":"mr list has no such flag; it filters by assignee with --assignee.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Run `tool mr list --project acme/web --assignee carol`.","problem":"unknown_flag","parameter":"--assignee-name","did_you_mean":["--assignee"],"usage":"tool mr list [--project <ref>] [--state opened|merged|closed|all] [--assignee <user>] [--limit <n>]"}},"meta":{"duration_ms":2}}
```

Why: an error that names the fix helps, but accepting the guess helps more (CLI-022). In the eval, structured usage errors on their own moved invalid calls only from 0.44 to 0.40 per trial (arm B against A, all trials); accepting the measured guesses took them from 0.22 to 0.07 (arm D against C).

Check: `R03` (unknown command, unknown flag, unexpected positional).

### CLI-013 Conflict details

`E_CONFLICT` MUST carry `conflict`, one of:

| `conflict` | meaning | more details |
|---|---|---|
| `token_mismatch` | the token does not belong to this request | `parameter`: the first differing input as its flag (`--assignee`; a file input by its literal flag when the command has one, `--body`), `args` for positional arguments not bound to a flag, `command` or `tool_version` for another command or build, `--idempotency-key`, `account`, `host` or `target` for a changed context, and `--confirm` for a malformed or unknown token |
| `token_expired` | the token is past its `expires_at` | |
| `token_used` | the token was already consumed | |
| `state_drift` | the target changed since the dry-run: its base version or the re-run preview differs (CLI-057) | |
| `scope_changed` | the scope file changed since the dry-run | |
| `unresolved_operation` | an earlier operation on this target is `unknown` or still `dispatching` | `active_operation` |
| `lock_held` | the operation's lease is still live (`operation resolve`) | `active_operation` |
| `upstream_conflict` | the upstream refused because of its current state (HTTP 409 or 412) | `http_status` |
| `idempotency_mismatch` | the idempotency key was used for a different request | |

`active_operation` is the id of the blocking operation; `operation show` reports it.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_CONFLICT","message":"The confirm token was issued for different inputs; nothing was sent.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Repeat the previewed command exactly, or run it with --dry-run to get a token for the new inputs.","conflict":"token_mismatch","parameter":"--assignee"}},"meta":{"duration_ms":5}}
```

Why: in the eval, a mismatch with a generic hint ("another change may have happened concurrently") was wrong for its case, a `--body` versus `--body-file` confirm. Naming the input ends the guessing.

Check: `G03` (the mismatch names the parameter); aicli builds every gate conflict.

### CLI-014 Forbidden details

A refusal by the local scope (SEC-010) MUST carry `details.scope`: `status` and `path`, and where known `scope_id`, `missing` and `grant_example` (format in SEC-011); and `details.preview` when the preview could be built. An upstream HTTP 403 SHOULD carry `details.required` when the tool knows what the account lacks: a role name (`"maintainer"`), or `{"role": …, "current_role": …}` when it also knows the caller's role.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_FORBIDDEN","message":"No granted scope allows this change; a person must grant one first.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Ask the user to grant this scope by creating C:\\Users\\me\\.tool\\scope.json with the grant_example content (adjusted); do not create it yourself.","scope":{"status":"missing","path":"C:\\Users\\me\\.tool\\scope.json","scope_id":null,"missing":{"effects":["change"],"dangerous":false,"target":"acme/web!5"},"grant_example":{"scope_id":"scp_2026-10-12_mr","tool":"tool","granted_by":"<your name>","granted_at":"2026-10-12T08:00:00Z","expires_at":"2026-10-19T08:00:00Z","effects":["change"],"dangerous":false,"targets":[{"key":"acme/web"}]}}}},"meta":{"duration_ms":6}}
```

The `details` of an upstream 403 met while applying a write (its `receipt`, CLI-059, left out):

```json
{"dispatch_state":"dispatched","next_action":"Tell the user that creating CI/CD variables needs the maintainer role; this account is a developer.","required":{"role":"maintainer","current_role":"developer"},"http_status":403}
```

Why: in the eval's forbidden-write task (T10), agents spent 2–4 calls after the 403 probing `auth status`, `project get` and `variable list` to explain it, and the task got more expensive (6.2 to 6.8 calls), because the error did not name the role.

Check: `G07` (without a scope, dry-run and confirm return `E_FORBIDDEN` with `details.scope` and nothing is sent).

### CLI-015 Unknown outcomes after dispatch

When a change request may have reached the upstream and the tool did not observe the result (a timeout, a dropped connection, a 5xx, an interrupt during the call), the error MUST be `E_OUTCOME_UNKNOWN` (exit 6, not retryable) with `operation_id` and `lookup`, the exact command that shows the operation. It is never `E_NETWORK`, `E_SERVER`, `E_TIMEOUT` or `E_INTERRUPTED`: those codes mean nothing changed.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_OUTCOME_UNKNOWN","message":"The update was sent but no reply arrived; it may have been applied.","retryable":false,"details":{"dispatch_state":"unknown","next_action":"Run `tool operation show --id op_4hx2mq7tz3b5kd6wqe2nrc7vya` and re-read the merge request before anything else; do not resend the update.","operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya","lookup":"tool operation show --id op_4hx2mq7tz3b5kd6wqe2nrc7vya"}},"meta":{"duration_ms":30012}}
```

Why: 1.6.3 marked `E_NETWORK` and `E_TIMEOUT` retryable even after a write was sent, so an agent that followed the contract could post a comment or send a message twice.

Check: aicli reports every error from `Apply` that the tool did not wrap with `NotDispatched` or `Rejected` as unknown; the tool's lost-reply test (VER-004).

### CLI-016 Upstream HTTP mapping

Upstream failures MUST be mapped in one function, by HTTP status or the upstream's error type, never by message text. aicli's `FromHTTP(status, message, upstream, retryAfter)` implements this table:

| upstream answer | code | details |
|---|---|---|
| 400, 422 | `E_VALIDATION` | `problem: invalid_value` |
| 401 | `E_AUTH` | |
| 403 | `E_FORBIDDEN` | `required` when known (CLI-014) |
| 404, 410 | `E_NOT_FOUND` | |
| 408 | `E_TIMEOUT` | |
| 409, 412 | `E_CONFLICT` | `conflict: upstream_conflict` |
| 413 | `E_VALIDATION` | `problem: input_too_large` |
| 429 | `E_RATE_LIMITED` | `retry_after_ms` from `Retry-After` |
| other 4xx | `E_VALIDATION` | `problem: invalid_value` |
| 5xx | `E_SERVER` | |
| connection failure before sending (DNS, refused, TLS) | `E_NETWORK` | |

Upstream error text goes to `details.upstream_message`, listed in `meta.untrusted`, and the status to `details.http_status`. `FromHTTP` sets `dispatch_state: not_started`, because a read sends no state-changing request (CLI-011). While a write is being applied, the table applies only to answers that prove nothing changed, a 4xx (including 429): the tool wraps the error with `Rejected`, and the gate reports `dispatched` with the receipt (CLI-057, step 14). A 5xx, a timeout or a dropped connection after sending is an unknown outcome (CLI-015). A write's apply step reports `E_SERVER` only when the tool knows that nothing was applied (receipt rule 4, CLI-059).

Why: matching on message text misclassifies messages that merely contain words like "not found", and one function keeps status, code and exit code from drifting apart.

Check: aicli's `FromHTTP` tests; the tool's upstream-failure tests (VER-004).

### CLI-017 Messages written for the model

`message` MUST be one sentence, two at most, that names the violated constraint and the next step in plain English. It MUST NOT echo input values (they may hold secrets or injected text), stack traces or upstream text; upstream text goes to `details.upstream_message`. What the tool writes for the agent, `message`, `next_action`, notices and help, is English whatever the locale; upstream content (titles, bodies, upstream error text) passes through untranslated.

Why: 1.6.3 told agents not to read `message` and had every Skill copy an error decision tree instead. Agents read the message anyway, so it has to be right. One language means a Skill, an eval grader or an agent's own notes match the same words in every tool, and passing upstream text through unchanged keeps what its authors wrote.

Check: review; `R04` holds every error to 2,000 characters (CLI-042).

### CLI-018 Extension codes

A tool-specific code MUST be declared in `contract-ext.json`: its name matches `^E_[A-Z0-9][A-Z0-9_]*$` and is not a core name; it declares `exit`, `retryable` and `meaning`; its exit is a documented exit code, and exit 9 is only for codes that wait on a person; its details carry `dispatch_state` and `next_action`. Prefer a core code with precise details over a new code. Known extension: `E_2FA_REQUIRED` (exit 9, archery-cli). With aicli, each code from `contract-ext.json` is also declared in `App.ExtCodes` as a `CodeSpec{Exit, Retryable, Meaning}`; the tree check rejects one that breaks these rules, and `reference` publishes it.

Why: the core codes stay portable across the fleet only if no tool redefines them.

Check: contract validation of the tool's real output (VER-002).

## Command model

### CLI-020 One registry

Each tool MUST have one registry that defines its command tree. For every command it holds the path, aliases, summary, typed parameters, effects, the dangerous flag, output schema, error codes, examples, maturity and evidence. `--help`, `reference`, the Skill's command map (`App.CommandMap()`, SKL-002) and the coverage enumeration (VER-003) are generated from it; nothing is maintained by hand next to it.

With aicli the registry is the cobra (Go) or click (Python) tree plus one `aicli.Meta` per leaf, attached with `App.Register`. Groups get their summary from `App.Group`; output schemas are declared with `App.Schema`, and a schema name registered twice is a defect that aicli reports.

```go
app.Register(mrUpdate, aicli.Meta{
    Summary:    "Update a merge request's title, assignees, reviewers or labels",
    Effects:    []aicli.Effect{aicli.Change},
    Maturity:   "verified",
    Evidence:   []string{"simulated:e2e/mr_update_test.go", "live:docs/E2E.md#mr-update"},
    Output:     "merge_request",
    ErrorCodes: []string{"E_NOT_FOUND", "E_FORBIDDEN", "E_VALIDATION"},
    Positional: "mr",
    Examples: []string{
        "tool mr update acme/web!5 --assignee carol --dry-run",
        "tool mr update acme/web!5 --assignee carol --confirm <confirm_token>",
    },
})
```

Why: under 1.6.3 every tool invented its own `reference` shape (`params` in one, `flags` plus `positionalArgs` in another), so agents could not carry knowledge between tools, and nothing caught the drift.

Check: `R01` (reference validates); `R02` (every leaf has a summary, an output schema and an example); `R06` (`CheckCoverage`, VER-003).

### CLI-021 Tree shape

The tree MUST satisfy all of these:

- Paths are unique, and a path is either a group or a command, never both.
- Aliases are unique among siblings: `label create` and `milestone create` may both accept `add`, but no alias repeats a sibling's name or alias, and no root alias shadows a root command or a built-in.
- The built-in names `reference`, `context`, `doctor`, `changelog`, `update`, `operation`, `help` and `version` are reserved at the root. aicli's tree check rejects a tool command with one of these names, `update` included: it is aicli's built-in (CLI-070).
- No parameter shadows a global control flag (`--format`, `--json`, `--pretty`, `--compact`, `--fields`, `--quiet`, `--dry-run`, `--confirm`, `--dangerous`), and the reserved flags `--idempotency-key` (CLI-062), `--allow-downgrade` (CLI-070) and `--profile` (CLI-101) mean only what those rules say.
- Commands are `<noun> <verb>`, using `list`, `get`, `search`, `create`, `update` and `delete`, with the meanings of CLI-029, before domain verbs such as `merge` or `approve`.

New tools SHOULD use a flat tree: `branch delete`, not `repo branch delete`.

Why: agents type the shapes they know from gh and glab, and nested nouns were the most common unknown-command guess in every eval arm without root shortcuts.

Check: aicli's tree check; `R02`.

### CLI-022 Root reachability and measured guesses

Every noun a user would name MUST be reachable from the root: a noun nested under exactly one parent gets a root shortcut (`branch` for `repo branch`). A noun nested under several parents (`comment` under `issue` and `mr`) gets none, or a qualified one (`issue-comment`), because one word cannot name two paths. Plural noun aliases SHOULD be accepted. The forms agents demonstrably guess first MUST be accepted, or answered with an exact `did_you_mean`: each tool keeps a guess list, starting from the fleet list below and extended from its own eval's failed calls. Suggestions MUST search the whole tree, not only the siblings of the mistyped word.

The fleet list, from the 2026-10 eval:

- root nouns for nested groups: `branch`, `file`, `commit` and `tree` under `repo`; `member` and `members` for `project members`;
- plural nouns: `issues`, `mrs`;
- verbs: `view` and `show` mean `get`; `note` and `notes` mean `comment`; `list` and `search` stand in for each other, and so do `create` and `add`;
- flags: `-p`, `-R` and `--repo` mean `--project`; `--all` is answered with `--limit` and a hint; `--branch` means `--name` on branch commands;
- positional identifiers (`branch delete old-experiment`), and `--project` on commands that take the project positionally;
- `--format raw` on a JSON-only command is `E_USAGE` with `unsupported_format`.

aicli accepts guesses through `App.AddShortcut(word, path...)` (root nouns), `App.ShortFlags` (`-p`), `App.FlagAliases` (`--repo` for `--project`, on every command), `Meta.FlagAliases` (an alias on one command only, such as `--branch` for `--name`) and `Meta.Aliases` (other command names), and suggests through `App.FlagSynonyms` and `App.VerbSynonyms`; its unknown-command and unknown-flag errors search the whole tree. `AddShortcut` refuses a word already mapped to another path, and the tree check reports a word in `App.Shortcuts` whose target is not a registered path.

Why: accepting these guesses, together with the v3 Skill's command map, cut invalid calls per trial from 0.22 to 0.07 (arm D against C), and from 0.54 to 0.07 against gitlab-cli 1.2.17 run in the same hour (arm D against A′); with the Skill off, where only the CLI changed, they fell 75% against C, to 0.04. In the gitlab-cli pilot (2026-10), the issue and merge request groups each needed a root `comment`, and whichever registered last would have taken the word silently.

Check: `R02` (a root shortcut for every noun under one parent, and no word mapped twice); `R03`; the tool's tests run every entry of its guess list.

### CLI-023 Parsing

The parser MUST match the command path first (after aliases and shortcuts), then parse only that command's declared parameters. An unknown flag is `E_USAGE` `unknown_flag`, an unexpected positional argument is `E_USAGE` `unexpected_argument`, and neither is ever ignored. A command whose identifying value is natural as a positional MUST accept it and bind it to the declared flag (`Meta.Positional`): `branch delete old-experiment` and `branch delete --name old-experiment` are the same request, for the confirm token too (CLI-054).

Why: gitlab-cli 1.2.17 silently ignored the positional in `repo branch delete old-experiment` and then failed on the missing `--name`.

Check: `R03`.

### CLI-024 Parameters

Each parameter MUST declare a `type`: `string`, `path`, `integer`, `number`, `boolean`, `json` or `enum`. It MAY declare `enum` (the allowed values), `unit` (for quantities), `default` (never on a required parameter), `multiple` (repeatable; `string`, `integer` and `number` values also split on commas, `path` and `json` values never do), `secret` (never echoed, stored, logged or bound in clear; SEC-021) and `file_input: true` (on a `--<name>-file <path|->` flag whose content is bound as `<name>`, CLI-048). In `reference` a parameter is `{name, kind, type, required}`, with `kind` `flag` or `positional`, plus those optional keys and a `description`. With aicli, `type` follows the flag's pflag or click type, and `Meta.Params` (`aicli.ParamDoc{Type, Enum, Unit}`) sets `path`, `json` or `enum`, the allowed values (checked before the command runs) and the unit.

Why: a typed parameter lets the tool reject a wrong value before anything is sent, and lets `reference` and `--help` describe it exactly.

Check: `R01`.

### CLI-025 Effects

Each command MUST declare its effects, and the declaration MUST match what the command does:

| effect | meaning |
|---|---|
| `observe` | reads only |
| `change` | modifies state on the upstream service |
| `deliver` | content reaches people or systems outside the target: send, publish, notify, share |
| `configure` | changes permissions, settings, credentials, integrations or automation, upstream or in the tool's own credentials and profiles |
| `artifact` | creates a new local file |
| `persist` | overwrites an existing local file |

A command with any effect other than `observe` is a write (`type: write` in `reference`) and goes through the gate (CLI-050). `deliver` and `configure` make a command dangerous by default, and a write that cannot be undone (a delete, a merge, a purge) MUST be declared dangerous (SEC-001). A bulk command is dangerous only when what it does is, through its effects or an action that cannot be undone; acting on many targets does not make it so (`issue bulk close` is not dangerous, since each issue can be reopened). A plan can be dangerous although its command is not (a commit that deletes files): `Preview.Dangerous` makes that run dangerous (CLI-056). A comment stored on a resource is `change`, even when the upstream notifies the resource's watchers; `deliver` is for content meant to reach someone, such as a message, a mail or a share, and names its destinations (CLI-051). The tool's `risk_tier` is at least what its effects require: T0 for `observe`; T1 for `change`, `artifact` and `persist`; T2 for `deliver` and `configure` (SEC-001). The tool's own records (its journal, confirm key and notice cache) and its self-update (`update`, CLI-070) are not effects. So the built-ins `reference`, `context`, `doctor`, `changelog` and `operation` are reads, and `update`, which changes the tool itself and stays outside the gate, declares only `observe`: a documented exception, not a contradiction.

Why: the fleet audit found writes outside any gate (wechat-mp `token refresh`, confluence `page attachment download` to a server-chosen file name) and a gated write that `reference` reported as a read (kibana `patterns infer --write`). In the gitlab-cli pilot (2026-10), the specs, the template and the review Skill gave different answers for bulk commands, and a comment that notifies watchers seemed to fit both `change` and `deliver`.

Check: `G00` (every write command has a conformance sample); `R02`; effect tests (VER-004).

### CLI-026 Error codes per command

Each command MUST list the error codes it can return, and `reference --command` shows them. aicli publishes `Meta.ErrorCodes` together with the codes the library itself raises for that command (usage errors; `E_CONFIG` on every tool command when the tool declares settings, since an unknown profile stops any of them, CLI-101; gate, scope and journal errors for writes). A code that cannot occur is not listed.

Why: agents plan for the branches that can happen; a listed but unreachable code sends them down a path that does not exist.

Check: `R01`.

### CLI-027 Maturity

Each command MUST declare its maturity, `draft`, `experimental`, `verified` or `stable`, with `evidence` references: test files, recorded runs, eval reports. VER-005 defines what each level needs; evidence from simulated upstreams caps a command at `experimental`. This replaces 1.6.3's `release_readiness`, which is gone from `reference` and `doctor`.

Why: a tool's claims about itself need evidence, and the self-declared readiness level of 1.6.3 had none behind it.

Check: `R01`; VER-005.

### CLI-028 Shared vocabulary

When a command offers one of these concepts, it MUST use the standard flag name with this meaning:

- `--limit <n>`: the page size. The command's help states its default and its maximum; a value above the maximum is `E_USAGE` `invalid_value`, never silently lowered.
- `--cursor <cursor>` or `--offset <n>`: where the next page starts, copied verbatim from the previous page's `next_cursor` or `next_offset` (CLI-041).
- `--query <text>`: what a `search` command looks for (CLI-029).
- `--since <time>` and `--until <time>`: a time range in ISO 8601 UTC or relative (`7d`), echoed as absolute UTC in `scope` (CLI-048). The one exception is `changelog --since <version>` (CLI-037), whose list is ordered by version.
- `--sort <field>` and `--order asc|desc`: the order of a list, echoed in the page's `sort`.
- `--state <state>`: a filter by state; `all` drops it.
- `--refs a,b,c`: several resources in one read (CLI-049).
- `--wait <duration>`: return when the state changes or the duration ends (CLI-049, CLI-064).
- `--output <path>`: write the result to a local file and return a summary (CLI-042).
- `--input <path|->` and `--<param>-file <path|->`: every parameter as one JSON object, or one long value, from a file or stdin (CLI-048).
- `--profile <name>`: the account profile (CLI-101).
- `--idempotency-key <key>`: makes a create safe to repeat (CLI-062).

A duration is `<n>ms`, `<n>s`, `<n>m`, `<n>h` or `<n>d`, in `--wait`, in relative times and in any other flag that takes one; `aicli.ParseDuration` and `aicli.ParseTime` (Python `parse_duration`, `parse_time_arg`) parse durations and times, absolute or relative, so `7d` means the same in every tool. aicli suggests the standard name for common synonyms when the command has the standard flag and the tool's `FlagSynonyms` has no entry for the word: `--per-page`, `--page-size`, `--max-results` and `--max` for `--limit`; `--page-token`, `--next-token` and `--next` for `--cursor`; `--search`, `--keyword`, `--keywords` and `--term` for `--query`; `--order-by` and `--sort-by` for `--sort`.

Why: agents type the flags they know from other tools. The fleet's measured first guesses include `-R` and `--repo` for `--project`, and `--all` for `--limit` (CLI-022). One vocabulary turns what an agent learned on one tool into a valid call on the next, before any guess list is needed.

Check: review every flag against this list; `R03` and aicli's suggestion tests.

### CLI-029 Verb meanings

The standard verbs (CLI-021) MUST mean the same in every tool:

- `list` returns a page of matches (CLI-041). No match is an empty page with `ok: true`, never `E_NOT_FOUND`.
- `get` returns one resource by ref, URL or name (CLI-048). A missing resource is `E_NOT_FOUND`; a name that matches several is `E_AMBIGUOUS`.
- `search` returns a page of matches for `--query`, and the page's `sort` states the order the page really has: `relevance` only where the upstream ranks by relevance, otherwise the order it uses (`created_at_desc`). When the tool cannot tell which (GitLab ranks by relevance only with advanced search), `sort` is `upstream` and `scope` says what that order can be.
- `create` returns the new resource's `ref` in `result`, beside the receipt (CLI-059), and SHOULD accept `--idempotency-key` (CLI-062).
- `update` changes only the fields given. A field is cleared only on request, with an empty value or `--clear-<field>`, never because a flag was left out. A call that names no field to change is `E_USAGE` (`missing_parameter`) before the gate, so no token is minted for it.
- `delete` reports a missing target as `E_NOT_FOUND` at the dry-run, never as success.
- A domain verb (`merge`, `approve`, `share`) declares its precise effects (CLI-025), and its summary says what changes.

Why: agents carry what they learned on one tool to the next (CLI-022), so a verb that means something else in one tool turns a correct habit into a wrong call or a wrong report. A `list` that fails on no match, or a `delete` that reports success for a target that never existed, puts a false statement in the agent's answer; an `update` that clears every field it was not given destroys data the user never mentioned. In the gitlab-cli pilot (2026-10), `relevance` would have been false on most self-managed GitLab instances, whose basic search lists issues and merge requests newest first.

Check: review; the empty and invalid-input rows of VER-004.

## Discovery

### CLI-030 Help

`<path> --help` MUST be generated from the registry: summary, usage line, parameters with types, defaults and allowed values, effects, and examples. Root `--help` lists every command, one line each. The help of a write command states its effects and the recipe: preview with `--dry-run` (a call without it returns the preview too), then repeat the command with `--confirm <token>`, and add `--dangerous` to both steps for a dangerous command. Help is text on stdout with exit 0, the one output that is not an envelope; `reference --command` is its machine twin and agrees with it. There is no shell-completion command: aicli adds none and turns off cobra's default `completion` command.

Why: without a Skill, agents made 3.6–4.4 help calls per task in every eval arm, so help is their hot path: it must be short, and it must be right. Agents do not use shell completion, so a `completion` command would only lengthen every root `--help`.

Check: aicli renders help from the registry; `R02`.

### CLI-031 reference --index

`reference --index` MUST return one compact entry per command, `{path, type, effects, dangerous, maturity, summary}`, inside `{tool, version, commands}`, in at most 8,000 characters. A tool whose index would be larger returns groups instead, `{tool, version, groups}` with `{path, summary, command_count}` per group, and `reference --index <group>` returns that group's commands.

```json
{"ok":true,"schema_version":"2.0","data":{"tool":"tool","version":"2.0.0","commands":[
  {"path":"mr list","type":"read","effects":["observe"],"dangerous":false,"maturity":"stable","summary":"List merge requests with assignees, labels and pipeline status"},
  {"path":"mr update","type":"write","effects":["change"],"dangerous":false,"maturity":"verified","summary":"Update a merge request's title, assignees, reviewers or labels"},
  {"path":"branch delete","type":"write","effects":["change"],"dangerous":true,"maturity":"verified","summary":"Delete a branch"}]},
 "meta":{"duration_ms":3}}
```

Why: a full `reference` was 17,000–113,000 characters across the fleet, and 8 of 10 tools exceeded the 30,000-character inline window, so the step Skills prescribed first returned a 2,000-character preview. An index costs 270–1,300 tokens.

Check: `R01` (valid, and at most 8,000 characters or grouped).

### CLI-032 reference --command

`reference --command <path>` MUST return `{tool, version, command, schemas, error_codes}`: the command's full entry (`path`, `aliases`, `summary`, `type`, `effects`, `dangerous`, `maturity`, `evidence`, `params`, `output`, `error_codes`, `examples`), the schema entry its `output` names (with the command's own default fields when it sets `Meta.DefaultFields`), and the `{exit, retryable}` of each code it lists, in at most 6,000 characters. It accepts aliases; an unknown path is `E_USAGE` `invalid_value` (`parameter: "--command"`) with `did_you_mean`.

```json
{"ok":true,"schema_version":"2.0","data":{"tool":"tool","version":"2.0.0",
 "command":{"path":"branch delete","aliases":["branches delete"],"summary":"Delete a branch","type":"write","effects":["change"],"dangerous":true,"maturity":"verified","evidence":["simulated:e2e/branch_test.go","live:docs/E2E.md#branch-delete"],
  "params":[{"name":"project","kind":"flag","type":"string","required":true,"description":"Project ref, for example acme/web"},{"name":"name","kind":"positional","type":"string","required":true,"description":"Branch to delete; --name also works"}],
  "output":"branch_deleted","error_codes":["E_USAGE","E_AUTH","E_FORBIDDEN","E_NOT_FOUND","E_CONFIRMATION_REQUIRED","E_CONFLICT","E_OUTCOME_UNKNOWN","E_NETWORK","E_RATE_LIMITED","E_IO"],
  "examples":["tool branch delete old-experiment --project acme/web --dangerous --dry-run","tool branch delete old-experiment --project acme/web --dangerous --confirm <confirm_token>"]},
 "schemas":{"branch_deleted":{"shape":"object","fields":["operation_id","receipt","result"],"default_fields":["operation_id","receipt","result"],"untrusted_fields":[]}},
 "error_codes":{"E_USAGE":{"exit":2,"retryable":false},"E_AUTH":{"exit":4,"retryable":false},"E_FORBIDDEN":{"exit":4,"retryable":false},"E_NOT_FOUND":{"exit":3,"retryable":false},"E_CONFIRMATION_REQUIRED":{"exit":5,"retryable":false},
  "E_CONFLICT":{"exit":6,"retryable":false},"E_OUTCOME_UNKNOWN":{"exit":6,"retryable":false},"E_NETWORK":{"exit":7,"retryable":true},"E_RATE_LIMITED":{"exit":7,"retryable":true},"E_IO":{"exit":1,"retryable":false}}},
 "meta":{"duration_ms":2}}
```

Why: one command costs about 130–200 tokens, against 4,000–28,000 for a full `reference`. The budget is 6,000 because in the gitlab-cli pilot (2026-10) a write with 15 flags (`mr create`) needed 4,552 characters, most of them its parameters and its error codes.

Check: `R01` (valid, and at most 6,000 characters).

### CLI-033 Full reference

`reference` without flags MUST return the full registry for tooling (validators, generators, the Skill map generator): `{tool, version, spec_version, schema_version, risk_tier, global_flags, commands, schemas, error_codes, exit_codes}`. `schemas` maps each output schema name to `{shape, fields, default_fields, untrusted_fields}`, with `shape` `object`, `array` or `page`. It is exempt from the 8,000-character success budget (CLI-042). Skills never tell agents to load it (SKL-003).

Why: validators and generators need the whole registry; agents need a slice of it (CLI-031, CLI-032).

Check: `R01`.

### CLI-034 No mandatory first step

Every command MUST work without a prior `context`, `doctor` or `reference` call. When one is needed (no credentials, a broken configuration, a stale Skill), the error's `next_action` says which.

Why: the 1.2.17 Skill's `context`, `doctor` and `reference` bootstrap cost 1.6 calls per task with the Skill on and gained no accuracy; dropping it paid off together with a command map in the Skill.

Check: review; SKL-003 keeps bootstrap sequences out of Skills.

### CLI-035 context

`context` MUST report, from local state only and without network calls:

| key | content |
|---|---|
| `tool`, `version`, `spec_version` | which build this is |
| `account`, `host` | whom the tool acts as and where, with secrets removed (no tokens or passwords in URLs) |
| `credentials` | `{configured, source}`; never a secret or a hash of one |
| `state` | `{dir, available, protection}`; `protection` is `posix-0700`, `windows-profile-acl`, `windows-explicit-acl` or `windows-unverified`, computed for the resolved state directory (SEC-030); `dir` and `protection` are `null` only when the store is unavailable |
| `authorization` | `{status, scope_id, path, effects, dangerous, expires_at}` from the scope file (SEC-011); `status` is `granted` or why not (`missing`, `invalid`, `expired`, `revoked`, `budget_spent`) |
| `profile` | only when the tool declares settings (CLI-100): `{name, source, profiles, file}`, the active profile or `null`, where it came from (`flag`, `env`, `config`, `single` or `none`, CLI-101), every profile name in sorted order, and the settings file's path, or `null` when there is no file. An invalid file reports `name: null`, `source: "none"`, `profiles: []` and its path, and `context` still succeeds |

aicli builds this from `App.Identity`, the state directory, the scope file and the settings file; `App.ContextExtra` adds tool-specific keys.

```json
{"ok":true,"schema_version":"2.0","data":{"tool":"tool","version":"2.0.0","spec_version":"2.0.0","account":"ci-bot","host":"gitlab.example.com",
 "credentials":{"configured":true,"source":"keyring"},
 "state":{"dir":"C:\\Users\\me\\.tool","available":true,"protection":"windows-profile-acl"},
 "authorization":{"status":"granted","scope_id":"scp_2026-10-12_triage","path":"C:\\Users\\me\\.tool\\scope.json","effects":["change"],"dangerous":false,"expires_at":"2026-10-19T08:00:00Z"}},
 "meta":{"duration_ms":4}}
```

Why: the agent learns whom it acts as and what it may do without a network round trip.

Check: `R05`.

### CLI-036 doctor

`doctor` MUST return `data.checks`, each `{check, status, message, fix}` with `status` `pass`, `warn` or `fail`, and `fix` `null` when the check passes. It MUST include the checks `credentials`, `connectivity`, `state_store`, `authorization` and `version`, and `settings` when the tool declares settings (CLI-100): `pass` when there is no settings file or it is valid, naming the active profile and its source, and `fail` when it is invalid or the requested profile does not exist, even without a file, with a `fix` that names the file. `version` compares the tool with the installed Skill's `metadata.min_version` and `metadata.version`, read from `<dir>/<tool>/SKILL.md` for each of `App.SkillDirs` (default `~/.agents/skills` and `~/.claude/skills`): no Skill → `warn`, whose `fix` points at the README's install command (`update` does not install a missing Skill); a `min_version` newer than the tool → `fail` (update the tool); a Skill `version` older than the tool → `warn` (run `update` to sync the Skill). A network failure fails only `connectivity`. `doctor` returns `ok: true` whatever its checks say and changes nothing; when `App.Update` is set, it refreshes the update notice cache before its `version` check, with a 3 s timeout, and ignores a failure (CLI-071). aicli runs `credentials` (from `App.Identity`), `state_store`, `authorization`, `version` and `settings`, and reports `connectivity` as `warn` until the tool supplies one; a check from `App.DoctorChecks` replaces the library check of the same name, and any other is added.

```json
{"ok":true,"schema_version":"2.0","data":{"checks":[
  {"check":"credentials","status":"pass","message":"A token from the keyring is configured.","fix":null},
  {"check":"connectivity","status":"fail","message":"gitlab.example.com did not answer within 5 s.","fix":"Check the VPN or HTTPS_PROXY, then run `tool doctor` again."},
  {"check":"state_store","status":"pass","message":"The state directory is writable and protected by the Windows profile ACL.","fix":null},
  {"check":"authorization","status":"warn","message":"No scope is granted, so every write will be refused.","fix":"Ask the user to grant a scope in C:\\Users\\me\\.tool\\scope.json."},
  {"check":"version","status":"pass","message":"tool 2.0.0 matches its installed Skill (version 2.0.0, min_version 2.0.0).","fix":null}]},
 "meta":{"duration_ms":5012}}
```

Why: the agent gets every problem and its fix in one call, and an unreachable network does not hide a broken scope or state store.

Check: `R05`.

### CLI-037 changelog

`changelog [--since <version>]` MUST report changes from the `CHANGELOG.md` embedded at build time (Keep a Changelog): `{current_version, entries}`, plus `since` and `count` when filtered. Each entry is `{version, date, changes}`, with `changes` keyed by `added`, `changed`, `fixed`, `deprecated`, `removed` and `security`. `--since` returns only versions newer than the one given. Without `--since`, it lists the newest entries that fit the success budget (CLI-042), and when it leaves older ones out it adds `truncated: true`, `truncated_reason` and `omitted`. aicli parses `App.Changelog`.

```json
{"ok":true,"schema_version":"2.0","data":{"current_version":"2.1.0","since":"2.0.0","count":1,"entries":[{"version":"2.1.0","date":"2026-11-02","changes":{"added":["mr list --reviewer filter"],"fixed":["branch delete accepts branch names that contain slashes"]}}]},"meta":{"duration_ms":1}}
```

Why: after an update, an agent refreshes what it knows in one call instead of reusing stale patterns.

Check: aicli's changelog tests; `R04`.

### CLI-038 Expiring credentials

A tool whose credentials expire (OAuth access tokens, sessions, temporary keys) MUST renew them during a call when it can, without involving the agent. When it cannot, it MUST return `E_AUTH` with a `next_action` that names the sign-in step, or `E_HUMAN_REQUIRED` with `action: sign_in` when only a person can sign in (CLI-065). It SHOULD report `expires_at` and `refreshable` in `context.credentials`, and its `doctor` `credentials` check SHOULD `warn` before expiry. Renewing the tool's own session during a call is not an effect; a command whose job is to create, replace or revoke credentials declares `configure` (CLI-025). Refresh tokens are secrets (SEC-021).

Why: an expired session otherwise surfaces as a confusing failure in the middle of a task. 1.6.3 carried this as an optional pattern for tools such as wechat-mp-cli, whose access tokens last about two hours.

Check: review; the tool's auth tests (VER-004).

## Reads and output size

### CLI-040 Default fields

Each list command MUST declare default fields chosen from the questions users ask, not from the API schema: what an agent needs to choose and verify an item (`ref`, a title or name, state, `updated_at`, `url`) and the fields common tasks ask about (for merge requests: assignees, reviewers, labels, pipeline status). `--fields a,b` replaces the defaults, `--fields +a` adds to them, and `--fields all` returns everything. An unknown field is `E_USAGE` (`invalid_value`, `parameter: "--fields"`, `expected` listing the fields). Projection applies to each item, to the object a `get` returns, and to a confirmed write's `data.result`, which without `--fields` is cut to the default fields as a read is (never to its receipt); the completeness keys (CLI-041) are always kept. A default field that costs an extra upstream read per item is allowed when tasks need it: the tool reads it only when the response will show it (`aicli.WantsField(cmd, name)`), in parallel and up to a cap that `scope` states; a field not worth that cost stays out of the defaults. aicli projects; the tool chooses the defaults (`Meta.DefaultFields` for one command, else the schema's `default_fields`), and `--help` and `reference` show the command's own: its `output` then names a copy of the schema, `<output>_for_<command words joined by _>`, that carries those defaults.

Why: putting assignees, labels and pipeline status on MR items, with a pipeline filter computed in the tool, cut "which MRs have failed pipelines" (T02) from 13.6 to 6.1 calls and "assign an MR and label it" (T07) from 13.0 to 9.5, with the Skill held constant. That default costs one GitLab read per merge request, because GitLab's merge request list carries no pipeline status (gitlab-cli pilot, 2026-10).

Check: aicli's projection tests; review of the chosen defaults.

### CLI-041 Completeness

Every list MUST return `items`, `count` (the items in this response) and `has_more`, and SHOULD return `total`, the number of items in the whole list, when the upstream reports it, so a count question needs no paging (CLI-044). When `has_more` is true, exactly one of `next_cursor`, `next_offset` or `continuation_reason` is present (`offset` may echo the current position). A list the tool capped carries `truncated: true` and `truncated_reason`. Every list SHOULD state `sort` and `scope` (optional keys in contract.json); `scope` says what the list covers: the container, the filters actually applied and resolved times. A list whose coverage is narrower than a reader would assume (direct members only; a client-side filter over one page) MUST say so in `scope`, and a read that fans out over several containers names in `scope` the containers it could not read, never dropping them silently.

```json
{"ok":true,"schema_version":"2.0","data":{"items":[{"ref":"@carol","username":"carol","access":"developer","membership":"inherited"}],"count":1,"has_more":false,"scope":{"project":"acme/payments-service","membership":"effective: direct and inherited from groups"},"sort":"username_asc"},"meta":{"duration_ms":41}}
```

Why: gitlab-cli 1.2.17's `project members` returned `items: []` and `hasMore: false` for a project whose members all come through its group, because it listed direct members only. An agent then told the user that the bot account "can see the project … but the members list doesn't include it", which was false. The item says `membership: inherited` rather than the group it comes through, because GitLab's REST API does not name that group (gitlab-cli pilot, 2026-10).

Check: aicli's `Page` enforces the key rules and `R04` validates every page; review checks that `scope` is true.

### CLI-042 Size budgets

With default flags, a success response MUST stay within 8,000 characters, and every error response within 2,000 (whole envelope). The success budget is for default invocations: a call that asks for more (`--limit 100`, `--fields all`) may exceed it, and a list whose default page could break it (long titles, many default fields) lowers its default `--limit` or its default fields. The full `reference` (CLI-033) and `raw` output are exempt from the success budget (contract.json `size_budgets.exempt`); `reference --index` and `reference --command` keep theirs (CLI-031, CLI-032). Larger results go to paging, to a dedicated fetch command (CLI-043), or to a command that writes them to `--output <file>` and returns a summary and the path. Writing a local file is an `artifact` effect for a new file and `persist` for an existing one, so such a command is a gated write: a new file's path must lie in the scope's `outputs` and an existing one's in `roots` (SEC-012). A command that declares `artifact` without `persist` never overwrites: an existing path is `E_VALIDATION` (`invalid_value`, `details.path`), not a scope refusal. aicli offers two ways to write: `App.WriteOutput` runs a small in-memory result through the gate on its own, with the file as the target; inside the `Apply` of an `App.Write` whose target is the upstream resource (the job whose artifacts are fetched, with the file in `Preview.Outputs`), `App.OpenOutput(path, overwrite)` streams into a temporary file beside the target that `Commit` moves into place. Both check the scope again when they write.

Why: the harness shows a success inline only up to about 30,000 characters and a failure up to about 10,000, after which the agent gets a preview or loses the middle. A 50-item list is about 4,000 characters with three default fields, against 14,000 with all fields, indented. In the gitlab-cli pilot (2026-10), a download whose output file already existed was refused for a missing `roots` grant that could not help, and a whole artifacts archive had to fit in memory.

Check: `R01` (reference budgets); `R04` (the success budget on the suite's default invocations, the error budget on every envelope; `ValidateEnvelope` checks only the error budget); response-size tests over fixtures (VER-004).

### CLI-043 Heavy sub-resources

Diffs, logs, file bodies and attachments MUST be separate commands, never inlined in lists or in default `get` output; lists carry bounded summaries (counts, paths, sizes). The heavy commands return structured data (a diff as per-file entries with paths and line counts), so `--fields` can project them down to an inventory. One body can still exceed the success budget: a long file, a large diff, a description with pasted logs. Its JSON form then carries a bounded window: lines or files paged with `--offset` and `--limit`, or the text cut at a stated length with `<field>_truncated: true` and a notice that names the command for the rest. `--format raw` (`Meta.RawOutput`) returns the whole body, and a long text field stays out of the default fields, so `--fields +description` adds it on request.

Why: the agent pays for a heavy payload only for the item it chose, and can ask for the inventory first. In the gitlab-cli pilot (2026-10), single files, diffs and release notes were longer than 8,000 characters.

Check: review.

### CLI-044 Compute in the tool

Counting, grouping, filtering and finding the latest or the top item MUST happen in the tool, on the server where possible; a command never ships a list to the model for it to count. A client-side filter pages through the full set first and says so in `scope`; when it cannot, the list is `truncated`. A list returns `total` when the upstream reports it, so a count needs no paging (CLI-041); a grouped count (who opened the most issues) is a page of `{<key>, count}` items sorted `count_desc`, whose `scope` says what was counted, and which is `truncated` when the tool could not read the whole set.

Why: in "which open MRs have a failed pipeline" (T02), 1.2.17 agents listed MRs and fetched each one. With `mr list --pipeline-status failed` computed in the tool, the task took 6.1 calls instead of 13.6, and 2.8 once the Skill named the filter. In the gitlab-cli pilot (2026-10), finding the top author of 46 issues still left the agent counting a 46-item page.

Check: review; the agent eval (VER-006).

### CLI-045 Rich text as Markdown

Rich text (HTML mail, Confluence storage format, Jira's document format) SHOULD be returned as Markdown by default, with the raw format on request, and accepted as Markdown on input.

Why: models read and write Markdown natively; raw markup costs tokens and invites mistakes.

Check: review.

### CLI-046 Untrusted content paths

A response that carries external content (text written by people or other systems: titles, descriptions, comments, mail bodies, file contents, upstream error text, and a `ref` that embeds such text, such as a branch, label or file name in `acme/web/-/branches/<name>`) MUST list where it is in `meta.untrusted`, once per response, as paths: dot-separated keys from the envelope root, with `[]` for every element of an array (`data.items[].title`, `data.description`, `error.details.upstream_message`). The list follows `--fields`: it keeps the paths of the fields that remain and drops those the projection removed. The per-object `_untrusted` key of 1.6.3 is gone.

aicli adds the paths from the output schema's `untrusted_fields`, `App.OK(cmd, data, untrusted...)`, `Page.Untrusted` and `Error.Untrusted`, and drops those of fields the projection removed. How agents and tools treat that content is SEC-002.

Why: repeating `_untrusted` on every item cost about 16 % more characters in a 50-item list response; one list of paths says the same. A ref is what an agent copies into its next command, and in the gitlab-cli pilot (2026-10) branch, tag and file refs carried text people wrote.

Check: `R04`; the injected-instruction scenario of the agent eval (VER-006).

### CLI-047 Quiet stderr, typed notices

When stderr is not a terminal, the tool MUST NOT write progress bars, spinners, banners or hints to it; warnings travel as `meta.notices`, each `{type, severity, message}` with `severity` `info` or `warning`. `--quiet` suppresses all non-error stderr. The `update_available` notice (CLI-071) is read from the local cache only and attached at most once a day; a `deprecated` notice (CLI-081) comes with every use of a deprecated command or flag.

Why: the harness shows stderr to the model, so every stray line costs tokens on every call; a typed notice can be acted on or ignored.

Check: aicli's writer and notice tests.

### CLI-048 Accept what the agent has

- Every resource in output MUST carry one `ref` string (`acme/web!5`, `acme/web#12`), and every command that takes that resource MUST accept it, and the resource's web URL too.
- A sub-resource's ref is its parent's ref, then its collection and its id: `acme/web#12/notes/99`, `acme/web!5/discussions/<id>`, so a grant for the parent covers it (SEC-012). Web URLs and their anchors (`…/-/issues/12#note_99`) stay accepted as input.
- Where names are natural input (projects, users, boards), an ambiguous name MUST be `E_AMBIGUOUS` (exit 2) with `candidates`, each `{ref, label}`, never a silent pick.
- Relative times (`7d`, `24h`) SHOULD be accepted and echoed as absolute UTC in `scope`; `--input -` or `--input <file>` SHOULD take the parameters as one JSON object; long text SHOULD come through `--<param>-file <path|->` (`Meta.FileInputs`), bound by its content (CLI-054).

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_AMBIGUOUS","message":"More than one project matches that name; choose one by ref.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Ask the user which project they mean, then re-run with its ref.","candidates":[{"ref":"acme/payments-service","label":"acme / payments-service"},{"ref":"legacy/payments","label":"legacy / payments"}]}},"meta":{"duration_ms":120,"untrusted":["error.details.candidates[].label"]}}
```

aicli supplies the `Ambiguous` constructor, file and stdin inputs (`ReadFileInput`) and positional binding; resolving refs, URLs and names, and `--input`, are the tool's code (a resolved value is bound with `SetResolved`, each input's own part, CLI-054).

Why: agents copy identifiers from earlier output, the user's message and URLs, and invent one when a command demands an ID they do not have. The eval's ambiguous-project task (T04) had two projects named "payments" on purpose. In the gitlab-cli pilot (2026-10), issue and merge request comments, threads and changed files had no ref form, and each command group invented its own.

Check: for each resource type, tests that its ref, URL and name resolve to the same target (VER-004).

### CLI-049 Fewer round trips

Reads that agents would otherwise loop over SHOULD accept several refs (`get --refs a,b,c`). Status commands SHOULD accept `--wait <duration>` and return when the state changes or the wait ends. Every command SHOULD finish within a bounded time well under the harness's 2-minute limit; longer work becomes long-running work (CLI-064).

Why: each call costs a round trip of model time, and a command the harness kills at 2 minutes returns nothing at all.

Check: review.

## Writes

Every write follows one flow, implemented once in aicli. A person's scope (SEC-010 to SEC-012) is checked at the dry-run and again at confirm; without one, every write is refused before anything is sent.

```text
tool mr update acme/web!5 --assignee carol --dry-run            -> ok: preview, confirm_token, expires_at, operation_id
tool mr update acme/web!5 --assignee carol                      -> E_CONFIRMATION_REQUIRED carrying the same four
tool mr update acme/web!5 --assignee carol --confirm <token>    -> re-checks, Apply, receipt; ok derived from the receipt
```

### CLI-050 One shared gate

Every command with an effect other than `observe` MUST go through aicli's gate: `App.Write` for one target, `App.WriteBatch` for many. Tools MUST NOT implement their own token, binding, key, journal or single-use store, and MUST NOT call an upstream write anywhere but inside `Apply`.

```go
return app.Write(cmd, aicli.WriteSpec{
    Target: aicli.Target{Key: mr.Ref, Kind: "merge_request", Label: mr.Label},
    Preview: func(ctx context.Context) (aicli.Preview, error) { // read-only and deterministic; runs at dry-run, refusal and confirm
        cur, err := api.GetMR(ctx, mr.Ref)
        if err != nil {
            return aicli.Preview{}, err
        }
        return aicli.Preview{Action: "Update merge request", Changes: diff(cur, want),
            BaseVersion: cur.UpdatedAt, BaseSource: "updated_at", CanApply: true}, nil
    },
    Apply: func(ctx context.Context, op aicli.Op) (aicli.Result, error) {
        got, err := api.UpdateMR(ctx, mr.Ref, want)
        if err != nil {
            return aicli.Result{}, classify(err) // NotDispatched, Rejected, or unchanged (= outcome unknown)
        }
        return aicli.Result{
            Outcome: aicli.Outcome{Execution: "completed", Change: "applied", Delivery: "not_applicable",
                Persistence: "not_applicable", Verification: verify(got, want)},
            Observed: map[string]any{"assignees": got.Assignees, "labels": got.Labels},
            Data:     got,
        }, nil
    },
})
```

`classify` belongs to the tool. It wraps with `aicli.NotDispatched` an error raised before anything left the machine, and with `aicli.Rejected` an upstream answer that proves nothing changed (a 4xx, including 429, mapped by `FromHTTP`). Anything else it returns unchanged, and the gate reports it as `E_OUTCOME_UNKNOWN`. Wrap the `*aicli.Error` that `FromHTTP` built, never the raw transport error: `aicli.NotDispatched(aicli.FromHTTP(0, msg, "", 0))` reports `E_NETWORK`, and `aicli.Rejected(aicli.FromHTTP(status, msg, body, 0))` the status's code; around a plain error the gate has no code and falls back to `E_SERVER` (receipt rule 4). Python has the same API in snake_case (`app.write`, `WriteSpec`).

When finding the target takes an upstream lookup (a name or an ID to resolve, a variable's environment scope, the account's own namespace), set `WriteSpec.ResolveTarget` instead of `Target`. The gate calls it at a dry-run or a bare call before the scope check, and at confirm only after the token checks (CLI-057), so a repeated confirm is `token_used` before any tool code runs; its error is returned as the tool's own, with nothing sent. Code before `App.Write` runs at every confirm ahead of those checks, so it reads nothing upstream that the write itself can change.

Why: the fleet audit found the per-tool gates broken in the same ways. Bindings missed inputs in all 10 gated tools (gitlab-cli: 23 of its 59 write commands; 72 confirmed findings in the other 9), tokens were forgeable in 9 and reusable in 8 when the store failed, and `--format text` skipped the gate in 2. The rule was right; ten implementations of it were not.

Check: `G00`–`G10` for every write command.

### CLI-051 Dry-run

`--dry-run` MUST change nothing upstream (it may read) and return `ok: true` with `data` = `{preview, confirm_token, expires_at, operation_id}`.

- The preview has `operation_id`, `command`, `action`, `effects`, `dangerous`, `inputs`, `can_apply` and `blocked_by`, and either `target` and `changes` (one target) or `targets`, `total` and `items` (a batch, CLI-063). Optional: `base` (the version the plan was made against), and `omitted` in a shortened batch preview (CLI-063). Required by effect: `destinations` (where a `deliver` command sends) and `outputs` (the local paths an `artifact` or `persist` command writes), from `Preview.Destinations` and `Preview.Outputs`; the gate refuses a `Preview` that omits them, because the scope checks them (SEC-012).
- `target` is `{key, kind, label}`; each change (`aicli.FieldChange`) is `{field}` plus `before`, `after`, `add`, `remove` or `note`.
- `inputs` echoes the bound inputs (CLI-054), with secrets as `"***"`, file inputs as `{"sha256": …, "bytes": …}`, an input named `id`, `iid` or ending in `_id` as a string (CLI-007), and, when the tool declares settings, the active profile as `profile` and the settings as `setting`, an object keyed by setting name (CLI-054).
- When `can_apply` is false, `blocked_by` lists `{kind, message}` entries and `confirm_token` and `expires_at` are `null`. The response is still `ok: true`, so the agent sees why.
- With `--idempotency-key`, a dry-run whose key matches an earlier applied request answers with that request's replay instead of a preview (CLI-062): nothing is planned or sent.

```json
{"ok":true,"schema_version":"2.0","data":{
  "preview":{"operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya","command":"mr update","action":"Update merge request",
    "target":{"key":"acme/web!5","kind":"merge_request","label":"!5 Add retry"},"effects":["change"],"dangerous":false,
    "changes":[{"field":"assignees","before":["bob"],"after":["carol"]},{"field":"labels","add":["needs-review"]}],
    "inputs":{"mr":"5","project":"acme/web","assignee":"carol","add_labels":["needs-review"]},
    "base":{"version":"2026-10-11T18:30:24Z","source":"updated_at"},"can_apply":true,"blocked_by":[]},
  "confirm_token":"ct2_4hx2mq7tz3b5kd6wqe2nrc7vya_9f2a61c04be87d35a0c6e1f24b7d9e03",
  "expires_at":"2026-10-12T08:15:00Z","operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya"},
 "meta":{"duration_ms":182,"untrusted":["data.preview.target.label"]}}
```

Why: the preview is what the agent checks against the user's request, and the `operation_id` is known before anything runs, so a lost reply can still be looked up (CLI-058).

Check: `G02` (exit 0, a canonical token, a valid preview, no upstream write).

### CLI-052 A write without --dry-run or --confirm

A write called with neither flag MUST change nothing and return `E_CONFIRMATION_REQUIRED` (exit 5, `problem: confirmation_required`), whose details carry the same `preview`, `confirm_token`, `expires_at` and `operation_id` a dry-run returns. This holds in every output format.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_CONFIRMATION_REQUIRED","message":"This command changes data and nothing was changed; check the preview, then confirm with its token.","retryable":false,"details":{
  "dispatch_state":"not_started","problem":"confirmation_required","next_action":"If details.preview matches the request, re-run the same command with --confirm ct2_7mq2xk4vz6bn3dt5wy2hce7rpa_0c4e8a1f6b2d9e3a7c5f1b8d4e0a6c2f.",
  "preview":{"operation_id":"op_7mq2xk4vz6bn3dt5wy2hce7rpa","command":"issue close","action":"Close issue","target":{"key":"acme/web#12","kind":"issue","label":"#12 Timeout in checkout"},"effects":["change"],"dangerous":false,"changes":[{"field":"state","before":"opened","after":"closed"}],"inputs":{"issue":"12","project":"acme/web"},"can_apply":true,"blocked_by":[]},
  "confirm_token":"ct2_7mq2xk4vz6bn3dt5wy2hce7rpa_0c4e8a1f6b2d9e3a7c5f1b8d4e0a6c2f",
  "expires_at":"2026-10-12T08:20:00Z","operation_id":"op_7mq2xk4vz6bn3dt5wy2hce7rpa"}},
 "meta":{"duration_ms":95,"untrusted":["error.details.preview.target.label"]}}
```

Why: in the eval, agents confirmed straight from this refusal in 51 of 71 cases. With 1.2.17, whose refusal carried no preview, 1 of 39 did; the others spent a call re-running `--dry-run`.

Check: `G01` (exit 5, preview and token in `details`, no upstream write); `G06` (the same with `--format text`).

### CLI-053 Confirm repeats the request

`--confirm <token>` MUST be given on the same command with the same inputs, and with `--dangerous` again for a dangerous command; a token never executes a stored plan on its own. Any difference is `E_CONFLICT` `token_mismatch` with `parameter` naming the first differing input, and nothing is sent. `--confirm` together with `--dry-run` is `E_USAGE` `conflicting_flags`.

Why: the token binds the agent's own plan to the request that runs. Binding the whole invocation costs almost nothing: across 288 trials in arms C and D, one legitimate confirm hit a mismatch, a `--body` versus `--body-file` case that content binding (CLI-054) now accepts.

Check: `G03` (each sample input mutated: a value changed, a positional changed, a flag dropped, a flag added); `G04`.

### CLI-054 What the token binds

The token MUST bind the resolved request:

- the canonical command path, after aliases and shortcuts;
- every supplied parameter, flag or positional, after normalisation, except the control flags `format`, `json`, `pretty`, `compact`, `fields`, `quiet`, `dry-run`, `confirm`, `dangerous` and `help`;
- the SHA-256 of the content of every file or stdin input;
- the resolved account and host (from `App.Identity`) and target (`Target.Key`);
- when the tool declares settings (CLI-100), the active profile's name whatever chose it (`--profile`, `<PREFIX>_PROFILE`, `default_profile` or the only profile), as the input `profile`, and the resolved value of every declared setting, as the input `setting.<name>`;
- the scope digest (SHA-256 of the scope file's bytes), the tool version, the target's base version when the preview has one (`Preview.BaseVersion`), the idempotency key, and the expiry.

aicli normalises before binding:

- Flag names are canonical (`--repo` binds as `project`). Scalars bind as the string form of the parsed value; repeated or comma-separated values bind as an array in input order. Positionals bind as `args` unless `Meta.Positional` turns them into a flag, so `branch delete x` and `branch delete --name x` are the same request.
- A file or stdin input is read once and binds as `sha256:<hex>` of its content under the parameter it fills; a literal value for that parameter binds as `sha256:<hex>` of the literal. So `--body X` and `--body-file f`, where `f` contains `X`, are the same request.
- Secret values bind as an HMAC under the confirm key, never in clear (SEC-021).
- A value the tool accepts in several forms (id, ref, URL, name) binds as given unless the tool resolves it first: it calls `aicli.SetResolved(cmd, flag, canonical)` before `App.Write`, so every form binds the same value. It may also set a flag the user did not give, when that value came inside another input (the project in `acme/web!5`). Bind each input's own part, the iid as `mr` and the project as `project`, never a ref that embeds both: a changed `--project` is then named as `--project`, not as `--mr` (G03). A structured value that names a file (`--action 'update:path=a;content_file=b'`) binds that file's content only when the tool reads the file once before `App.Write` and binds, with `SetResolved`, a form that holds the content's SHA-256.

The claims live in the operation record, not in the token: `{v, tool, tool_version, op, cmd, inv, inputs, account, host, target, scope, base, idem, exp}`, where `v` is 2 and every other value is a string or a digest. `inputs` holds a digest per input name, so a mismatch can name the input, and `inv` is the SHA-256 of the canonical inputs. Canonical JSON means sorted keys, no whitespace, UTF-8.

Why: the fleet audit found bindings that missed inputs in every gated tool: file contents bound by path only (jira `issue attach`, outlook attachments), batch paths that bound only the target list (an archery `query run` token accepted a different `--sql`), accounts resolved from environment or configuration left out, and a gitlab-cli token minted for `--assignee alice` that assigned the MR to dave. In the gitlab-cli pilot (2026-10), an issue bound whole (`acme/web#12`) beside its project made a changed `--project` read as a changed `--issue`.

Check: `G03`.

### CLI-055 Token mechanics

All of this is aicli's; a tool configures none of it:

- **Format.** `ct2_`, the operation id's 26 base32 characters, `_`, then 32 lowercase hex characters: the first 16 bytes of HMAC-SHA256(key, `"aicli-confirm/2\x00"` + canonical claims). Only `^ct2_[a-z2-7]{26}_[0-9a-f]{32}$` is accepted; anything else is `token_mismatch` with `parameter: "--confirm"`.
- **Key.** 32 random bytes in `<state_dir>/confirm.key`, created with exclusive create and mode 0600. A key file that is not exactly 32 readable bytes is `E_IO`, whose `next_action` says to delete the file so that a new key is created. There is never an unkeyed fallback.
- **Expiry.** At most 15 minutes after the dry-run.
- **Single use by operation id.** The confirm creates `operations/<id>.consumed` with exclusive create before anything is sent, so neither a re-encoded token nor a concurrent confirm can run an operation twice. The token stays consumed even when nothing is sent afterwards; a retry starts with a new dry-run.
- **Fail closed.** No usable key means no token and no write; a journal that cannot be written means no write. No output format, flag or environment variable turns the gate off, test switches included.

Why: the audit found unkeyed SHA-256 fallbacks when the key could not be loaded in 6 tools (gitlab-cli among them); single use skipped when the consumed store failed in 8 of 10, as 1.6.3 §9 required; re-encoded tokens that replayed in 4 (one outlook `mail send` token sent 4 messages); and no gate at all under `--format text` in jira-cli and confluence-cli.

Check: `G04` (a second confirm is `token_used`); `G05` (upper-cased, padded, truncated and foreign tokens are rejected); `G06` (text mode); `G08` (a corrupt key and a blocked journal are `E_IO`, and nothing is sent).

### CLI-056 Dangerous commands

A dangerous command MUST be called with `--dangerous` in both steps, the dry-run (or the bare call) and the confirm. Without it the gate returns `E_CONFIRMATION_REQUIRED` with `problem: dangerous_flag_required` before doing anything else. The scope must also grant `dangerous: true` (SEC-012). A plan can be dangerous although its command is not (a `commit create` that deletes a file): `Preview.Dangerous` makes the gate treat that run as dangerous, with `--dangerous` in both steps, `preview.dangerous: true` and a scope that grants `dangerous: true`. The binding covers it, so a plan that becomes dangerous between the dry-run and the confirm is `state_drift`.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_CONFIRMATION_REQUIRED","message":"This command is dangerous and needs --dangerous in both the dry-run and the confirm step.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Re-run with --dangerous --dry-run, check the preview, then repeat the command with --dangerous --confirm <confirm_token>.","problem":"dangerous_flag_required"}},"meta":{"duration_ms":2}}
```

Why: typing `--dangerous` twice is a statement of intent that a copied command or an injected instruction does not carry by accident, and forgetting it costs one call (10 of 813 calls in the eval's arm D). In the gitlab-cli pilot (2026-10), a `repo commit create` with only delete actions removed files without `--dangerous`, while `repo file delete` needed it.

Check: `G09`.

### CLI-057 Re-checks at confirm, in a fixed order

At confirm the gate MUST re-run the command's `Preview` and compare: a changed base version is `E_CONFLICT` `state_drift`; a changed scope file is `scope_changed`; a changed account, host or target is `token_mismatch` with `parameter` `account`, `host` or `target`. The re-run preview must also plan the same change: a different `action`, `changes`, `destinations`, `outputs` or `dangerous`, or `can_apply: false`, is `state_drift` too, so `Preview` MUST be deterministic: no timestamps, and a stable order in `Changes` and in list values. aicli runs every check in this order and stops at the first failure.

Confirm (`--confirm <token>`):

1. Parse the token; not canonical → `token_mismatch` (`parameter: "--confirm"`).
2. Load the key; unusable → `E_IO`.
3. Load the operation record; missing → `token_mismatch`.
4. Verify the HMAC over the stored claims; wrong → `token_mismatch`.
5. Consumed marker present, or the record is no longer `planned` → `token_used`, whatever the token's age.
6. Past `expires_at` → `token_expired`, with a new plan for the same command.
7. Recompute the inputs digest; different → `token_mismatch` naming the first differing parameter.
8. `--dangerous` missing on a dangerous command, or on a plan the dry-run found dangerous (`Preview.Dangerous`) → `E_CONFIRMATION_REQUIRED` (`dangerous_flag_required`). Only now does tool code run: `WriteSpec.ResolveTarget` (a batch's `BatchSpec.ResolveItems`), when set, finds the target, and its error is the tool's own.
9. Take the target's lock (`locks/<hash>.lock` in the state directory; a batch takes every item's, in sorted order), waiting at most 10 s → `lock_held`. Steps 10 to 15 run under it, so two confirms on the same target never interleave. Check the consumed marker again → `token_used`.
10. Scope: missing or insufficient → `E_FORBIDDEN` with `details.scope`; digest changed → `scope_changed`; operation budget spent → `E_FORBIDDEN` (`scope.status: budget_spent`).
11. An unresolved operation on the same target → `unresolved_operation`. Re-run `Preview`: base version or plan changed, or `can_apply: false` → `state_drift`. Recompute account, host and target: different → `token_mismatch` (`parameter` `account`, `host` or `target`).
12. An earlier operation with the same idempotency key (CLI-062) → the original receipt with `replayed: true`, or `idempotency_mismatch`, `idempotency_resolved` or `unresolved_operation`.
13. Create the consumed marker and count the operation against the scope's budget; the marker exists → `token_used`; the budget is spent → `E_FORBIDDEN` (`budget_spent`); any other error → `E_IO`.
14. Write the record as `dispatching` with `lease_until`; failure → `E_IO`.
15. Release the lock.
16. Run `Apply`.
17. Classify: `NotDispatched` → `dispatch_state: not_started`, change `not_applied`; `Rejected` → `dispatched`, `not_applied`, with the error's code; any other error → `E_OUTCOME_UNKNOWN`; success → the receipt from the `Result`.
18. Write the final record; if that fails, the result is still returned, with a `journal_write_failed` notice.
19. Emit according to the receipt rules (CLI-059). A `result` the tool built but that cannot be encoded is replaced by `{}`, with a `result_unencodable` notice; the receipt stands.

Every failure in steps 1 to 14 has `dispatch_state: not_started`: nothing has been sent. Once step 14 has written `dispatching`, nothing reports `not_started`: an error the library cannot classify is `E_OUTCOME_UNKNOWN` with the operation id.

Dry-run and bare call:

1. `--dangerous` missing on a dangerous command → `dangerous_flag_required`.
2. Resolve the target with `ResolveTarget` when the command sets it (its error is the tool's own), then check the scope; a refusal is `E_FORBIDDEN` with `details.scope`, and with `details.preview` when the preview could be built.
3. An unresolved operation on the same target → `unresolved_operation`.
4. Run `Preview`; for a plan it marks dangerous (`Preview.Dangerous`), `--dangerous` missing → `dangerous_flag_required`, and a scope without `dangerous: true` → `E_FORBIDDEN`.
5. Allocate the operation id.
6. Write the record as `planned`; failure → `E_IO`.
7. Mint the token, unless `can_apply` is false.
8. Emit: `ok: true` with `data` for `--dry-run`; `E_CONFIRMATION_REQUIRED` with the same content in `details` for a bare call.

Why: binding the base version stops a write planned against an old state from running against a new one, and a fixed order makes every tool refuse the same way, always before anything is sent. The order matters. In the 2.0 release review, a used token checked for expiry first came back after 10 minutes as `token_expired` with a fresh token, and following it sent the write twice. Without the lock, two confirms planned against the same version both passed the drift check, and the second overwrote the first. In the gitlab-cli pilot (2026-10), a lookup the tool ran before `App.Write` answered a repeated confirm of `variable delete` with `E_NOT_FOUND`, hiding `token_used`; `ResolveTarget` runs after the token checks for that reason.

Check: `G10` (the scope file changed between dry-run and confirm → `scope_changed`, nothing sent); aicli's gate tests cover the other steps.

### CLI-058 Operation records

The `operation_id` (`op_` and 16 random bytes as 26 lowercase RFC 4648 base32 characters without padding; `opb_` for a batch) MUST be assigned at dry-run and returned with the preview. The record MUST be written before anything is sent; if it cannot be written, nothing is sent (`E_IO`, `dispatch_state: not_started`). This reverses 1.6.3 §9, which let the write go ahead when its token store failed.

aicli keeps the journal in the state directory: `App.StateDir`, else `<PREFIX>_STATE_DIR`, else `~/.<tool>`, created with mode 0700; a symlinked path or one that is not a directory is `E_IO` (SEC-030).

- `operations/<id>.json` holds the record, replaced atomically (temporary file, fsync, rename); `operations/<id>.consumed` is the single-use marker.
- A record goes `planned` → `dispatching` → `applied`, `not_applied`, `partial`, `failed` or `unknown`, and becomes `resolved` through `operation resolve`. A `planned` record past `expires_at` reads as `expired`; a `dispatching` record past `lease_until` reads as `unknown` (the lease is `WriteSpec.Timeout`, default 120 s, plus 5 s, so it outlasts `Apply`'s deadline). `Apply` MUST honour its deadline (the context in Go, `op.timeout` in Python); a record resolved while `Apply` was still running keeps its resolution, and the confirm reports `E_OUTCOME_UNKNOWN`.
- Unresolved means `unknown` or `dispatching`. A record that cannot be read, or that lacks `operation_id`, `status`, `command`, `created_at` or `effects`, is unreadable: it reads as `unknown`, blocks its target, and `operation show` reports it as unreadable.
- Records older than 30 days are pruned unless unresolved, and the journal keeps at most 2,000, dropping the oldest resolved first. A batch's items are pruned with the batch record. The scope budget is counted in `scope-usage.json`, which pruning never touches (SEC-011).

Why: a write that leaves no record cannot be looked up after a lost reply, and a store that fails open lets one token drive repeated writes, which 8 of the 10 gated tools allowed under 1.6.3 §9.

Check: `G08` (journal path blocked → `E_IO`, nothing sent).

### CLI-059 Receipts

A confirmed write MUST return `data` = `{operation_id, receipt, result}`. The receipt has `operation_id`, `command`, `target`, `effects`, `outcome`, `observed` and `replayed`, and may have `mismatched`, `failed_stage` and `notes`. `outcome` reports five dimensions separately; a dimension that does not apply is `not_applicable`.

| dimension | values |
|---|---|
| `execution` | `not_started`, `running`, `completed`, `failed`, `interrupted`, `unknown` |
| `change` | `not_applicable`, `not_applied`, `applied`, `partial`, `rolled_back`, `unknown` |
| `delivery` | `not_applicable`, `not_attempted`, `delivered`, `partial`, `rejected`, `unknown` |
| `persistence` | `not_applicable`, `unchanged`, `saved`, `partial`, `unknown` |
| `verification` | `not_applicable`, `not_run`, `passed`, `failed`, `incomplete` |

- `failed_stage` (`apply`, `deliver`, `persist`, `verify`) is present when execution failed or was interrupted.
- `observed` is the read-back of the fields the write meant to change, from the write's response or a re-read. `verification` is `passed` only when `observed` matches the request, and `mismatched` lists the fields that do not.
- `result` is the command's own payload, cut to its default fields unless `--fields` asks for others (CLI-040); for a create, it carries the new resource's `ref` and `url`.

`ok` and the error code are derived from the receipt, never chosen: the first matching rule of contract.json `receipt_rules` decides.

| # | the receipt has | ok | code |
|---|---|---|---|
| 1 | `unknown` in execution, change, delivery or persistence, or execution `running` | false | `E_OUTCOME_UNKNOWN` |
| 2 | `partial` in change, delivery or persistence | false | `E_PARTIAL` |
| 3 | execution `failed`, `interrupted` or `not_started` with a lasting effect (change `applied`, delivery `delivered` or persistence `saved`) | false | `E_PARTIAL` |
| 4 | execution `failed`, `interrupted` or `not_started` without a lasting effect | false | the apply step's cause code (default `E_SERVER`) |
| 5 | change `rolled_back` | false | the cause code that made the tool roll back (default `E_VERIFY_FAILED`) |
| 6 | delivery `rejected` | false | the delivery step's cause code (default `E_FORBIDDEN`) |
| 7 | verification `failed` | false | `E_VERIFY_FAILED` |
| 8 | verification `incomplete` | false | `E_VERIFY_INCOMPLETE` |
| 9 | anything else | true | none |

This is the base profile, in which `change: applied` with `verification: not_run` is `ok: true`. A stricter driver profile, defined in tool-driver-cli-spec, makes the same receipt `E_VERIFY_INCOMPLETE`. When `ok` is false, the receipt moves to `error.details.receipt` (the examples under CLI-014 and CLI-015 leave it out).

```json
{"ok":true,"schema_version":"2.0","data":{"operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya",
  "receipt":{"operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya","command":"mr update","target":{"key":"acme/web!5","kind":"merge_request"},"effects":["change"],
    "outcome":{"execution":"completed","change":"applied","delivery":"not_applicable","persistence":"not_applicable","verification":"passed"},
    "observed":{"assignees":["carol"],"labels":["backend","needs-review"]},"mismatched":[],"replayed":false},
  "result":{"ref":"acme/web!5","title":"Add retry","state":"opened","assignees":["carol"],"labels":["backend","needs-review"],"url":"https://gitlab.example.com/acme/web/-/merge_requests/5"}},
 "meta":{"duration_ms":420,"untrusted":["data.result.title"]}}
```

Why: write results that carry the changed fields cut agents' re-reads after their last write from 1.00 to 0.37 calls per writing trial (arm C against A). Deriving `ok` from observed outcomes keeps partial and unknown results from being reported as success.

Check: aicli derives `ok` from the contract's table and is tested on the contract's example receipts; `R04`.

### CLI-060 Unknown outcomes stay unknown

An unknown outcome MUST be reported as `E_OUTCOME_UNKNOWN` (CLI-015), and the operation stays `unknown` until `operation resolve` records what was observed. While it is unresolved, new dry-runs and confirms on the same target SHOULD be refused with `E_CONFLICT` `unresolved_operation` and `active_operation`; aicli does this by default. The agent's recovery: `operation show --id <id>`, re-read the target, `operation resolve --id <id> --outcome applied|not_applied|partial --evidence "<what the re-read showed>"`, then continue. It never resends before the lookup.

Why: a lost reply must not turn into a duplicate write, and blocking the target until someone has looked makes "retry and hope" impossible.

Check: aicli's journal tests; the lost-reply scenario of the agent eval (VER-006).

### CLI-061 Operation built-ins

aicli adds three built-ins that read or write only the journal:

- `operation show --id <id>` returns the record: `operation_id`, `command`, `status`, `target`, `effects`, `scope_id`, `created_at`, `expires_at`, `dispatched_at`, `finished_at`, `preview`, `receipt`, `resolution`, `batch_id`, `items`. It keeps the success budget (CLI-042): for a batch too large for one response, the preview is shortened as in CLI-063, `items` lists the items that need attention (status other than `applied` or `resolved`) before the others, and `items_omitted` counts the items left out; a notice names the paging command.
- `operation list [--state <status>] [--limit <n>]` returns a page of records (CLI-041), newest first, with `total`; a page that would break the success budget (CLI-042) ends early with `truncated` and `next_offset`. `operation list --batch <batch id>` pages through the items of one batch in input order.
- `operation resolve --id <id> --outcome applied|not_applied|partial --evidence <text>` records `resolution` (`{outcome, evidence, resolved_at}`) on an unresolved operation and keeps the original receipt as it was. It needs a scope that covers the operation's effects, and it is refused with `E_CONFLICT` `lock_held` while the operation's lease is live.

Why: after a lost reply the agent finds out what happened from the operation id it already holds, without sending anything again.

Check: `R05` (`operation list` on an empty journal); aicli's built-in tests.

### CLI-062 Idempotency keys

Create-type commands SHOULD accept `--idempotency-key <key>`. aicli binds the key into the token and passes it to `Apply` as `Op.IdempotencyKey`; the tool forwards it upstream only where the API honours it (an `Idempotency-Key` header, for example): Go's `net/http` treats a request that carries that header as safe to repeat and re-sends it after a failure on a reused connection, a hidden duplicate write when the upstream ignores the header (CLI-110). aicli looks the key up in the journal across all commands, at confirm step 12 (CLI-057) and, before `Preview`, at a dry-run or a bare call, so a repeat is recognised even where the tool's `Preview` would block a duplicate. It considers every earlier operation with the key, not only the newest, and answers with the first of these that applies:

- the same key with any difference in command path, inputs digest, target, account or host, a different command included → `E_CONFLICT` `idempotency_mismatch`;
- an earlier operation still unresolved → `unresolved_operation`;
- an earlier operation resolved by hand as `applied` or `partial`, or one that ended `partial` → `E_CONFLICT` `idempotency_resolved` with `operation_id`, and nothing is sent; one resolved as `not_applied` does not block a new send;
- an earlier operation `applied` → the original receipt and result with `replayed: true` (a replay of a replay answers with the original), and nothing is sent.

A batch (CLI-063) is looked up the same way under its key. Each item's `Op.IdempotencyKey` is `<key>/<hash16>`, where `<hash16>` is the first 16 hex characters of the SHA-256 of the item's target key: stable across dry-runs, so a repeated batch is recognised upstream, and distinct per target, so an upstream that honours the key never folds two items into one. The user's key stays bound into the batch token.

Why: a create that may or may not have happened can only be repeated safely if the repeat is recognised. Many upstreams ignore `Idempotency-Key`, so the journal lookup is often the only protection, and in the 2.0 release review each gap in it sent a create twice: a key resolved as applied after a lost reply, and a batch repeated with the same key.

Check: aicli's gate tests.

### CLI-063 Batches

A command that acts on many targets MUST take them in one plural flag (`--ids 1,2,3`, or `--ids` repeated), de-duplicated with input order kept; an empty list is `E_USAGE` (`missing_parameter`). It runs through `App.WriteBatch`:

- **Preview.** It lists every target: `targets`, `total`, and `items`, each `{target, operation_id, changes, can_apply, blocked_by}`, under the batch's own `opb_` id. If any item cannot apply, the batch cannot either. One token covers the whole batch. When the whole preview would break its size budget (CLI-042), `targets` and `items` keep their first entries in input order and `omitted` counts the rest (`total` = listed targets + `omitted`); the inputs and the token still cover every target. The preview's `action` is `BatchSpec.Action`, or the first item's when that is empty.
- **Result.** One item per target, `{target, operation_id, ok}`, where `target` is the target's key, plus `receipt`, `error` (`{code, retryable, message}`) or `result`, and `summary` `{total, succeeded, failed, unknown, skipped}`, where every target counts exactly once.
- **ok.** The batch is `ok: true` only when every item succeeded. Otherwise it fails with `E_OUTCOME_UNKNOWN` if any item is unknown, else `E_PARTIAL` if any item applied, else the first failed item's code. Its details carry `operation_id` (the batch id), `items` and `summary`. When the full item list would break the size budget (CLI-042), the result, in `data` or in `details`, first drops the `receipt` and `result` of the items that succeeded, then lists fewer items, every item with `ok: false` before any that succeeded, and `omitted` counts those left out; `summary` still counts every target, and `operation list --batch <batch id>` lists them all (CLI-061).
- **Stopping.** `--continue-on-error` defaults to true, and to false for dangerous commands. After a stop, the remaining items are `skipped`: `ok: false` with no `error`.
- **Upstream caps.** A native bulk endpoint with a per-call cap is called in chunks behind the same contract: one token, one result, each chunk's outcome mapped onto its items. A batch never claims an atomicity the upstream does not give. With aicli, `BatchSpec.ApplyAll` makes one call per chunk of at most `MaxPerCall` items (all at once when 0) and returns one `ItemOutcome{Result, Err}` per item, in item order, which the gate maps onto its item. A missing or extra outcome is a defect that leaves the chunk's items `unknown`, and so does an error of the whole call, unless it is `NotDispatched` or `Rejected`, which then applies to each item.
- **Shared inputs.** What every item shares (a user to assign, the project) is resolved once, in `BatchSpec.ResolveItems`, which runs where `ResolveTarget` does (CLI-057): an input problem common to all items is then one error with its `parameter` and `next_action`, not a block on every item.

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_PARTIAL","message":"Three of the four issues were labelled; one was refused.","retryable":false,"details":{
  "dispatch_state":"dispatched",
  "next_action":"Report the refused issue to the user; do not resend the batch.",
  "operation_id":"opb_7kq3zv5mxn2c4wd6ty3ehb7rpa",
  "items":[{"target":"acme/web#11","operation_id":"op_2bq7xk3mz5nd4tw6vy7hce2rpa","ok":true},
           {"target":"acme/web#12","operation_id":"op_6tn3wq2hz7ck5md4xb6vy2erpa","ok":true},
           {"target":"acme/web#13","operation_id":"op_3hv5mz7qk2wd4nc6tb2xy7erpa","ok":true},
           {"target":"acme/web#14","operation_id":"op_5wd2nk6tq3zm7hc4vb2xy6erpa","ok":false,"error":{"code":"E_FORBIDDEN","retryable":false,"message":"The issue is locked; only maintainers can label it."}}],
  "summary":{"total":4,"succeeded":3,"failed":1,"unknown":0,"skipped":0}}},
 "meta":{"duration_ms":930}}
```

Why: 1.6.3 let a batch in which every item failed report `ok: true`; the audit found batch paths that bound only the target list (an archery `workflow audit --ids` token accepted a different `--action`); and in the eval, agents labelling four issues one by one sometimes re-minted every token, at 4 extra calls. In the gitlab-cli pilot (2026-10), `repo commit create`, one GitLab request for all its files, had every item after the first report the first item's outcome, and an unknown assignee blocked each issue of a bulk assign separately.

Check: aicli's batch tests; `G00`–`G10` run on batch commands too; `R04`.

### CLI-064 Long-running work

Work that outlasts a command's bounded run time (CLI-049) SHOULD be split into a gated submit and a read that follows it. The submit's receipt is a normal receipt for the submission only: `execution: completed` and `change: applied` mean the upstream accepted the job, and `data.result.job` carries `{ref, status, poll}`, where `status` is one of `planned`, `running`, `succeeded`, `failed`, `partial`, `unknown`, `cancelled` and `poll` is the exact status command. The status command is a read that accepts `--wait <duration>` and never resubmits. It reports `succeeded` or `failed` only as the upstream reports them, and `cancelled` only once the stop was observed, not merely requested. The operation journal records the submission and does not track the job. A finished job's large result is fetched by its own command (CLI-043). The status read and `--wait` are the tool's code; aicli has no job helper.

```json
{"job":{"ref":"acme/web/exports/81","status":"running","poll":"tool export get acme/web/exports/81 --wait 60s"}}
```

Why: the harness kills a call after 2 minutes, and a killed call reports nothing. A status read can be repeated freely; a submit cannot.

Check: review.

### CLI-065 Human action

When a person must act first (sign in, approve, solve a challenge, scan a QR code, install a component), the command MUST return `E_HUMAN_REQUIRED` (exit 9) with `action` (`sign_in`, `approve`, `solve_challenge`, `scan_qr`, `install_component` or `other`), `resume` (the exact command to run afterwards) and `dispatch_state`, instead of waiting or guessing. aicli's `HumanRequired(action, resume, message)` builds it. Extension codes for the same purpose also use exit 9 (CLI-018).

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_HUMAN_REQUIRED","message":"Signing in needs a code from the account owner's phone.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Ask the user to run `tool auth login` in their own terminal, then run the resume command.","action":"sign_in","resume":"tool mr list --project acme/web"}},"meta":{"duration_ms":38}}
```

Why: a tool that waits for a person hangs the agent until the harness kills it, and one that guesses can lock the account.

Check: `R04` validates the details; review.

## Self-update

### CLI-070 update

`update` is aicli's built-in. A tool turns it on by setting `App.Update` to an `UpdateConfig` (Python `App(update=UpdateConfig(...))`): `Repo`, the GitHub `owner/name` that publishes the releases; `Workflow`, the release workflow that signs `checksums.txt` (default `release.yml`); and the names the tool is published under, `NPMPackage` (also used for pnpm and yarn), `GoPackage`, `PyPIPackage` and `BrewFormula`, each empty when the tool does not ship that way. Without `App.Update` the tool has no `update` command. A tool MUST NOT write its own: the tree check rejects a tool-defined root `update` (CLI-021).

`update` changes the tool, not a target, so it stays outside the confirm gate: a bare `update` performs the whole update in one call, with no leaf commands and no token. Like the journal, the tool's own installation is not a target, so its registry entry declares only `observe` (CLI-025). Its flags are `--check`, `--target-version <version>` and `--allow-downgrade`. `--dry-run` is a synonym of `--check`, and `--confirm` is `E_USAGE` (`invalid_value`), since there is nothing to confirm. aicli's `update` MUST:

1. Validate every version it uses as strict semver with an optional leading `v` (`ParseVersion`) before anything else, whether it came from `--target-version` or from a release's `tag_name`. A `--target-version` that is not one, or that no release has, is `E_USAGE` `invalid_value`; a release whose `tag_name` is not one is skipped, never used. `CheckUpdateTarget(current, target, allowDowngrade)` does this and item 3 in one call.
2. Run a package manager only with the argument vector from `PackageManagerArgs(manager, pkg, version)` (npm, pnpm, yarn, go, pip, brew), built from validated parts, never through a shell, and found only in absolute `PATH` entries, never in the current directory (a cloned repository's `npm.bat` must not run). pip is the running interpreter's `python -m pip`. On Windows `npm` resolves to `npm.cmd`, which runs under cmd.exe, so validation is the only defence.
3. Refuse a downgrade unless both `--target-version` and `--allow-downgrade` are given (`E_USAGE`, `invalid_value`, `parameter: "--target-version"`).
4. On the binary path, verify the Sigstore bundle for `checksums.txt` in-process and then the archive's SHA-256. The signature is bound to the exact identity of the release workflow at the release's tag, `https://github.com/<Repo>/.github/workflows/<Workflow>@refs/tags/v<version>`, issued by `https://token.actions.githubusercontent.com`. Any failure is `E_INTEGRITY`; verification is never skipped (SEC-050).
5. On a package-manager path, drive the manager (never edit the files it manages, never just print its command), confirm the installed version with `<tool> --version`, and report `signature_status` honestly: `verified` only when aicli verified a signature itself, otherwise `not_checked` or `unavailable`.
6. Sync Skills by copying them from the installed package into the Skill directories where they are installed, never by fetching anything; only when the sync could not finish (`failed`, or `E_PARTIAL` at `skill_sync`) is `skill_sync_command` `<tool> update`. A Skill that is not installed is never a reason for it: `update` does not install Skills.
7. Be idempotent: at the target version it returns `status: up_to_date`, runs no package manager, and finishes a Skill sync that an earlier run left undone.

**Install method.** aicli works out how the running copy was installed; the first match wins. A Python copy that is neither frozen nor under `site-packages` or `dist-packages` runs from source and cannot update itself (`E_UNSUPPORTED`). A path with `store/v<n>/links` segments (the pnpm 11 global store) is `pnpm`. A path with a `node_modules` segment is `pnpm` (with a `.pnpm` segment), `yarn` (with a `.yarn` segment, or under `/yarn/global/`) or `npm`. A `Cellar` segment, `/opt/homebrew/` or `/home/linuxbrew/` is `brew`. A Go build in the `go install` directory (`GOBIN`, else `GOPATH/bin`, else `~/go/bin`) is `go`, a Python package under `site-packages` or `dist-packages` is `pip`, and anything else is `binary`. A package manager whose package name the `UpdateConfig` leaves empty is `E_CONFIG` before anything runs.

**Discovery.** With `--target-version`, aicli reads the release with that tag. Without it, it takes the highest version among the latest 50 releases, skipping drafts, tags with a semver prerelease part (whatever the release's prerelease flag says) and tags that are not semver; only when no release has a semver tag is the result `E_UNSUPPORTED`. A prerelease is reachable only through `--target-version`, and never appears in an update notice (CLI-071). A target that is not newer than the running version, without `--allow-downgrade`, is up to date: nothing is downloaded and no package manager runs. `update --check` stops here: it records the result in the notice cache and returns `status` `available` or `up_to_date`, with `command`, the argument vector a package-manager install would run.

**Package-manager path.** In stage `replace`, aicli runs the `PackageManagerArgs` vector for at most 10 minutes, then reads `data.version` from `<tool> --version`, finding `<tool>` again through absolute `PATH` entries (a pnpm upgrade leaves the old store path behind). That version decides: if it is the target, the install succeeded, even when the manager reported an error; otherwise the result is `E_IO`, naming the command to run by hand.

**Binary path.** `download` fetches `<tool>-<version>-<os>-<arch>.tar.gz` (`.zip` on Windows, where arm64 uses the amd64 build), `checksums.txt` and `checksums.txt.sigstore.json` into a temporary directory beside the executable. `verify_signature` and `verify_checksum` check them (item 4). In stage `replace`, aicli extracts only the binary and `skills/`, refusing an unsafe entry (an absolute path, `..`, a `:` in a name, a link, a device file, a duplicate name, or more than 10,000 entries or 1 GiB in all) with `E_INTEGRITY`, and runs the new binary's `--version`, which must report the target, in the 2.0 envelope or as a plain line that ends in the version (a 1.x release prints `gitlab-cli version 1.2.17`). It then swaps the binary in and, in `<exe dir>/skills`, replaces only the Skills the release ships, one `skills/<name>` at a time: several tools can share a directory, so other names stay, and a release without `skills/` leaves it alone (REPO-043).

**Skill sync.** The source is the Skills the release ships, in `<exe dir>/skills`, for `binary`, and `skills/` in the npm package's root for `npm`, `pnpm` and `yarn`; the other methods have none. Copies never follow links. For each Skill in the source and each directory in `App.SkillDirs` (default `~/.agents/skills` and `~/.claude/skills`), an installed copy that is a real directory is replaced by a fresh copy, and a symlink or junction is left alone. A Skill that is not installed there is not created: installing is the README's job (REPO-010). `skill_sync_status` is `synced` when anything was copied, `not_needed` when every installed copy already has the tool's version, and `skipped` when there is no source or no installed Skill.

**Network.** Every request has a 30 s idle timeout, a download also a 10-minute deadline, and every request follows CLI-110: the proxy from `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`, TLS always verified with `<PREFIX>_CA_FILE` added to the system roots, and the aicli User-Agent. The refresh of the Sigstore trust root uses the same timeout, proxy, CA file and User-Agent in Go; in Python, sigstore-python's own fetcher honours the proxy variables and the timeout but not `<PREFIX>_CA_FILE` or the User-Agent, so behind a TLS-inspecting proxy `update` fails safe at `verify_signature` with `E_NETWORK`. `GITHUB_TOKEN`, else `GH_TOKEN`, is sent only to the API host. Assets and redirects must be `https`. Responses are capped at 4 MiB for release metadata, 1 MiB each for `checksums.txt` and the bundle, and 512 MiB for the archive and for any file in it.

The result's `data` has `status` (`up_to_date`, `updated` or `available`; `partial` appears only in the details of a partial update, below), `current_version`, `target_version`, `update_available`, `install_method` (`binary`, `npm`, `pnpm`, `yarn`, `go`, `pip` or `brew`), `signature_status` (`verified`, `not_checked`, `unavailable`) and `skill_sync_status` (`synced`, `not_needed`, `failed`, `skipped`), plus, as they apply, `previous_version`, `command`, `binary_replaced`, `signature_verified`, `checksum_verified`, `skill_sync_command` and `stage`. The values describe the state after the command: after an install, `current_version` equals `target_version` and `update_available` is false. The agent then reads `changelog --since <previous_version>` (CLI-037).

```json
{"ok":true,"schema_version":"2.0","data":{"status":"updated","current_version":"2.1.0","target_version":"2.1.0","previous_version":"2.0.0","update_available":false,"install_method":"binary","signature_status":"verified","signature_verified":true,"checksum_verified":true,"skill_sync_status":"synced","binary_replaced":true},"meta":{"duration_ms":8412}}
```

The update runs in stages, `discover`, `download`, `verify_signature`, `verify_checksum`, `replace`, `skill_sync`, with one atomic commit point: the swap in `replace`. Every failure carries `stage`, `current_version`, `binary_replaced` and `skill_sync_status` (`skipped` until the sync runs) in `error.details`, and `target_version` and `install_method` once they are known. `dispatch_state` is `not_started` until a package manager runs or the swap happens, `dispatched` from then on, and `unknown` when a failed swap could not be undone.

| stage | failure | code (exit) | retryable | state after | `next_action` says |
|---|---|---|---|---|---|
| `discover`, `download` | network, timeout, rate limit, a redirect to plain `http` | `E_NETWORK`, `E_RATE_LIMITED` (7), `E_TIMEOUT` (8) | true | old version, unchanged | transient: re-run `update`, which is idempotent |
| `discover` | a `--target-version` that is not semver or has no release, or a downgrade without the flag | `E_USAGE` (2) | false | unchanged | fix the version, or add `--allow-downgrade` |
| `discover` | a copy that runs from source, a development build (CLI-080), or no release with a semver tag | `E_UNSUPPORTED` (2) | false | unchanged | update the checkout with git, or report the release tags to the tool's maintainers |
| `discover` | `<PREFIX>_CA_FILE` cannot be read or holds no certificate | `E_CONFIG` (4) | false | unchanged | fix or unset `<PREFIX>_CA_FILE` |
| `download` | no build for this OS and architecture, or an asset that is not served over `https` | `E_UNSUPPORTED` (2) | false | unchanged | install with the README's package-manager command |
| `download` | the running executable cannot be located | `E_IO` (1) | false | unchanged | reinstall with the README's install command |
| `verify_signature` | the Sigstore trust root cannot be loaded | `E_NETWORK` (7) | true | old version, unchanged | check the network, then re-run `update` |
| `verify_signature`, `verify_checksum` | missing or invalid signature, identity mismatch, missing or wrong checksum | `E_INTEGRITY` (1) | false | old version; install refused | integrity failure: do not retry; report it |
| `replace` | an unsafe archive entry (absolute path, `..`, link, device file), or a new binary that reports another version | `E_INTEGRITY` (1) | false | old version; install refused | integrity failure: do not retry; report it |
| `replace` | the package name is not configured, or the manager is not on PATH | `E_CONFIG` (4) | false | unchanged | install the manager, or reinstall with the README's install command |
| `replace` | the manager cannot install the target (pip and a prerelease; an environment without pip) | `E_UNSUPPORTED` (2) | false | unchanged | the command for the right installer (`pipx upgrade`, `uv tool upgrade`) |
| `replace` | the swap failed and the old binary could not be put back | `E_IO` (1), `dispatch_state: unknown` | false | the tool may be missing | rename the named `<exe>.old` file back to the executable's name, then run `<tool> --version` |
| `replace` | permission, full disk, locked file, a new binary that does not run, a failed package-manager run | `E_IO` (1) | false | old version; nothing committed | the concrete fix, or the manager command to run by hand; then re-run |
| `skill_sync` | the Skill sync failed or was interrupted after the swap | `E_PARTIAL` (1), details with `status: partial`, `target_version`, `skill_sync_command` | false | new binary, old Skill | the binary is at the new version; run `update` again to finish the sync, then `changelog --since <previous>` |
| before the swap | SIGINT or SIGTERM | `E_INTERRUPTED` (130) | true | old version, unchanged | cancelled; nothing changed |

These rules keep every message honest:

- Everything before the swap works in a temporary directory beside the executable, on the same filesystem. A failure or an interrupt there leaves the installed tool untouched (`binary_replaced: false`) and removes the directory; a later run never trusts a partial download.
- The swap is atomic. On POSIX the verified binary is renamed into place in the same directory. On Windows the running `<exe>` is renamed to `<exe>.old` (or `<exe>.old-<n>` when a stale one cannot be removed) and the new file to `<exe>`; if the second rename fails, the first is undone, and if that fails too, the error says so and names the file to rename back. The next `update` removes stale `<exe>.old*` files. A crash mid-swap leaves the old or the new binary, never a mix.
- aicli traps SIGINT and SIGTERM and still emits a terminal envelope on stdout. A running package manager is never killed: aicli waits for it, within its 10-minute cap. An interrupt after the swap is reported as what is true, a completed update or `E_PARTIAL` at `skill_sync`, never as `E_INTERRUPTED`.
- Every terminal envelope states the version now installed. An integrity failure is never retryable. A replaced binary with a stale Skill is never `ok: true`.

Why: the update-path audit found a crafted `--target-version` (`1.0.12&calc`) reaching `npm.cmd` unvalidated in cnstock-cli and wechat-mp-cli, which is command execution on Windows in one ungated call. Five more Go tools passed the release `tag_name` the same way, only cliproxyapi-cli validated semver, and four tools accepted an explicit older version as an "update". Each of those tools had written its own update; one built-in, tested once, replaces them. 1.6.3 itself prescribed `signature_status: not_checked` on the npm path and an unpinned `npx skills add <repo> -y -g` on every update.

Check: aicli's update tests cover every stage and install method against a fake release server and a fake verifier; the tool tests its `UpdateConfig` (VER-004).

### CLI-071 Update notices

aicli contacts the release source only in `update` (including `--check`) and, when `App.Update` is set, in `doctor`, which asks only for the latest release, with a 3 s timeout, and ignores a failure. Both record the result with `RecordUpdateCheck(latest, severity)`, which writes `<state_dir>/update-check.json`. A tool MUST NOT contact the release source anywhere else (SEC-022). Other commands MAY attach the cached notice to `meta.notices`, reading only the cache, at most once a day (CLI-047); aicli attaches it from that file. The notice is typed:

```json
{"type":"update_available","severity":"info","message":"tool 2.1.0 is available; this is 2.0.0.","current_version":"2.0.0","latest_version":"2.1.0","command":"tool update","checked_at":"2026-10-12T06:00:00Z"}
```

`severity` is `warning` when a release note between the two versions has a `Security` heading or the major version changes, and `info` otherwise. aicli attaches no notice once the installed version has reached the cached one, so no response advertises the version just installed.

Why: an update notice is useful once and costs tokens on every call after that, and business commands must not contact the network to produce it.

Check: aicli's notice and `doctor` tests.

## Versioning and performance

### CLI-080 Versions

`schema_version` (`"2.0"`) is the version of the envelope contract, not of the tool; its major part changes only with a breaking envelope change, and additions keep it. `reference` MUST report `spec_version` (the version of this spec the tool follows, `2.0.0`) and `schema_version`; `--version`, `context` and `reference` report the tool's own version. A build without a semantic version (`dev`, as a build without `-ldflags` has) runs as the next patch after the newest release in its CHANGELOG with the prerelease `dev` (`1.2.18-dev`); `update`, `--check` included, refuses it with `E_UNSUPPORTED`, and `doctor`'s `version` check warns that its version cannot be compared. Deprecated commands, flags and fields follow CLI-081. Skills declare `metadata.min_version` (SKL-001); `doctor`'s `version` check fails when the installed Skill needs a newer tool, and warns when the Skill is missing or older than the tool (CLI-036).

Why: a Skill written for a newer tool silently calls commands that do not exist; the version check turns that into one clear `doctor` failure.

Check: `R01` (`spec_version`, `schema_version`); `R05` (the `version` check).

### CLI-081 Deprecation

A command or flag that is going away MUST be deprecated first and keep working until it is removed. With aicli, `Meta.Deprecated` holds a command's replacement text, such as "use `mr update --label`", and `Meta.DeprecatedFlags` maps a flag's name to its replacement text; `Meta.RenamedFlags` maps a renamed flag's old name to its new one (`search` to `query`), and aicli rewrites the old name to the new one before parsing, so it binds as the new one; giving both is `E_USAGE` `conflicting_flags`, `--help` leaves the old name out, and `reference` lists it as a parameter whose description starts with ``Deprecated: use `--<new>`.`` aicli then:

- starts the command's summary in help and `reference` with `Deprecated: <text>.`, and a deprecated flag's description the same way;
- adds a `deprecated` notice (`severity: warning`, CLI-047) to every run of a deprecated command, naming the version that removes it, and to every run that uses a deprecated flag;
- leaves deprecated commands out of `App.CommandMap`, so the Skill and the README teach only current commands (SKL-002).

```json
{"type":"deprecated","severity":"warning","message":"`mr label` is deprecated: use `mr update --label`. It will be removed in tool 3.0.0."}
```

A deprecated command or flag keeps working through at least one MINOR release and for at least 90 days, and is removed only in a MAJOR release (REPO-030). `CHANGELOG.md` lists it under `Deprecated` in the release that deprecates it, and under `Removed` in the release that removes it (CLI-037). A deprecated output field keeps its key for the same window.

Why: agents keep using what a Skill, a README or an earlier transcript taught them. A command that vanishes in a MINOR release breaks those calls without warning; a notice on each use names the replacement while the old form still works, and `changelog` says the same after an update. In the gitlab-cli pilot (2026-10), keeping `mr list --search` working beside `--query` took a hidden flag, a copy and a conflict check in the tool's own code.

Check: aicli's deprecation tests; review that each deprecation is in `CHANGELOG.md` and that nothing is removed outside a MAJOR release.

### CLI-090 Start-up time

The command on `PATH` SHOULD be the native binary, or a launcher that adds no more than 100 ms; CI measures it (REPO-SPEC).

Why: through the npm launcher a call took 1.1–2.3 s, against 0.4–0.7 s for the binary alone, on a Windows test machine, and agents pay that on every call.

Check: CI timing of the installed command.

## Settings, accounts and diagnostics

### CLI-100 Configuration

A setting (a host, a region, a default project) MUST resolve from these sources, the first with a value winning: a flag on the running command, the environment variable, the active profile in the settings file (CLI-101), the default. The variable for setting `<name>` is `<PREFIX>_<NAME>` by default, where `<PREFIX>` is the tool name in upper snake case (`TOOL_HOST` for the `host` setting of `tool`).

The settings file is the one `<PREFIX>_SETTINGS_FILE` names (an absolute path), else `<state_dir>/settings.json`; aicli reads settings from no other file. It is parsed as strictly as the scope file (SEC-011): a regular file, not a link; on POSIX owned by the user and writable by nobody else; UTF-8 without BOM, one JSON value, no duplicate keys, every key spelled exactly, at most 64 KiB, and this shape:

```json
{"default_profile": "work",
 "profiles": {"work": {"host": "https://gitlab.example.com", "group": "acme"},
              "personal": {"host": "https://gitlab.com"}}}
```

`default_profile` is a profile name or `null`. Profile names match `^[a-z0-9][a-z0-9_-]{0,63}$`, and each profile maps declared settings to strings, numbers or booleans. An unknown top-level key, an unknown setting, a value that is not a scalar, or any setting declared secret makes the whole file invalid: a command that reads a setting fails with `E_CONFIG` (exit 4, `dispatch_state: not_started`), and the message names the file and the reason. A missing file is not an error; the other sources apply.

The settings file holds no secrets: they live in the OS keyring or come from the environment (SEC-040). A person writes the file, or a `configure` command writes it through the gate (CLI-025, CLI-101); nothing else does.

With aicli the tool declares `App.Settings`, each a `Setting{Name, Env, Flag, Default, Secret, Description}`. `Name` is snake_case; `Env` defaults to the upper-case `Name` and is read as `<PREFIX>_<Env>`; `Flag` names a command flag that overrides the setting when it is given on the running command; `Secret` keeps the value out of the file, out of diagnostics and errors (CLI-102, SEC-021), and makes `Flag` a secret flag everywhere, as if it were in `Meta.Secrets`. `App.Setting(cmd, name)` (Python `app.setting(name)`) returns the value and its source: `flag`, `env`, `profile` or `default`. `App.Profile()` (Python `app.profile()`) returns the active profile's name and source, `App.Profiles()` (Python `app.profiles()`) the profile names in file order, and `App.SettingsPath()` (Python `app.settings_path()`) the file, so a tool can key its keyring entries per profile and write the profile's settings. `context` reports the profile (CLI-035), and `doctor`'s `settings` check validates the file (CLI-036).

Why: an agent and a person can predict which host or account a command uses only if every tool resolves settings the same way, and `context` can say where a value came from only if one resolver produced it. The 2026-10 fleet audit found accounts, regions and time zones read from the environment or a config file inside write paths (SEC-020); declared settings make those reads visible. The file refuses secrets because copying it off the machine must yield nothing usable (SEC-040). It has its own name because fleet tools keep a 1.x `config.json` in the same directory, which aicli does not read.

Check: aicli's settings tests (the order of sources, strict parsing, a secret in the file); review that the tool reads settings only through `App.Setting`.

### CLI-101 Accounts and profiles

A tool that can act as more than one account, or against more than one host, MUST offer named profiles in the settings file (CLI-100). The active profile is the first of: `--profile <name>`, a root flag aicli registers when the tool declares settings; `<PREFIX>_PROFILE`; the file's `default_profile`; the only profile, when there is exactly one; otherwise none. `context.profile` reports it and its source, `flag`, `env`, `config`, `single` or `none` (CLI-035). `--profile ""` counts as not given. An unknown name is `E_CONFIG` before any tool command runs, with the name in `details.profile` (never in `message`, CLI-017) and `did_you_mean` listing the closest profile names; the built-ins (`context`, `doctor`, `reference`, `changelog`, `operation`, `update`, `help`, `version`) still run, and `context` reports the problem. The active profile's name, whatever chose it, and the resolved value of every declared setting are bound into confirm tokens (CLI-054), so a preview made under one profile, or with one setting value, cannot be confirmed under another (`token_mismatch` with `parameter` `profile` or `setting.<name>`), and no command defines a `--profile` of its own (CLI-021).

A tool with sign-in commands MUST give them these names:

- `auth login` stores a credential: the secret in the OS keyring and the profile's other settings in the settings file (SEC-040). It declares `configure` (CLI-025) and `Meta.AllowUnknownProfile`, so it can run under a profile the file does not have yet, and writes the settings with `App.SetProfileSettings` (Python `app.set_profile_settings`), which validates them like the reader, writes atomically and writes nothing on an error, so `Apply` returns that error as `NotDispatched`; when it creates the file's first profile, it also makes that profile the default, and it never picks a default among existing profiles. For such a command, `App.Profile()` returns the requested profile and its source. When a person must act (a browser sign-in, a one-time code), it returns `E_HUMAN_REQUIRED` with `action: sign_in` (CLI-065).
- `auth status` is a read: it checks the credential with the upstream and reports the account, the host and when the credential expires, never a secret.
- `auth logout` removes the credential from the keyring. It declares `configure`, and `Meta.AllowUnknownProfile` too, so it can clean up after a profile deleted from the settings file.

Why: people work with several accounts and hosts, such as a company GitLab and gitlab.com. Without profiles an agent switches between them by changing environment variables between calls, and nothing tells it when it gets one wrong. Binding `--profile` next to the resolved account and host keeps a plan made for one account from running as another: the 2026-10 fleet audit found per-tool gates that left an account resolved from the environment or configuration out of the binding (CLI-054). Standard `auth` names let an agent that signed in with one tool find the same commands in the next. In the gitlab-cli pilot (2026-10), a second `auth login` left no profile active until someone set `default_profile` by hand.

Check: aicli's profile tests (the order of sources, an unknown name, the binding); review the `auth` commands' effects.

### CLI-102 Diagnostics

Diagnostic output is on only when `<PREFIX>_DEBUG` is set to anything but empty or `0`. It goes to stderr as lines that start with `debug:`, never to stdout, and it never changes the envelope or the exit code. Values of secret parameters (`Meta.Secrets`, SEC-021), of secret settings (CLI-100) and of secrets the tool registers with `App.AddSecret(value)` (Python `app.add_secret(value)`, for a token read from the keyring) appear as `***`, as do their URL-escaped forms; a line break in a message is escaped, so every line starts with `debug:`. A tool MUST NOT add `--verbose` or `--debug` flags. aicli writes these lines through `App.Debugf(format, args...)` (Python `app.debug(message)`), which several goroutines or threads may call at once, and on its own logs only the state directory, the scope file path, the settings file and the active profile, and the start of each `update` stage and the release tags it skips. A Skill never sets `<PREFIX>_DEBUG` unless the user asks (SKL-009).

Why: a person chasing a failure needs more than the envelope, but every stderr line costs an agent tokens on every call (CLI-047). A variable that a person sets keeps diagnostics out of agent sessions by default, where a flag would end up in Skills and transcripts; redaction keeps a debug log safe to paste into an issue.

Check: aicli's tests (stdout and the exit code unchanged with `<PREFIX>_DEBUG` set, secrets redacted); review that no command defines `--verbose` or `--debug`.

## Network

### CLI-110 Network behaviour

Every upstream request MUST have a timeout: at most 30 s by default, and never longer than CLI-049's bound on a command's run time allows. A tool MAY retry an idempotent read after a connection failure or an HTTP 429, 502, 503 or 504, at most twice, honouring `Retry-After` up to 10 s; when the upstream asks for a longer wait, the tool returns the error instead, with `retry_after_ms` on `E_RATE_LIMITED` (CLI-016). It MUST NOT re-send a write that may have been dispatched (CLI-015, CLI-060).

The tool MUST honour `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`. TLS certificates are always verified: a private CA comes from `<PREFIX>_CA_FILE`, a PEM file added to the system roots, and no flag, variable or setting turns verification off. Requests carry `User-Agent: <tool>/<version> aicli/<spec version>`. When `doctor`'s `connectivity` check fails on a certificate, its message says so and its `fix` names `<PREFIX>_CA_FILE` (CLI-036). aicli's own requests (`update`, `doctor`'s refresh) follow this rule, and a tool gets the same proxy, CA file and TLS settings from `App.HTTPTransport()`, which reads the proxy variables through `App.Env` as it reads the CA file, and the header from `App.UserAgent()` (Python `app.ssl_context()`, `app.user_agent()`).

Why: the harness kills a call after 2 minutes, and a killed call reports nothing (CLI-049), so a request without a timeout can cost the whole answer. Re-sending a write is how one change gets applied twice: gitlab-cli's 1.x HTTP client retried 5xx and network errors for every method, so a timed-out POST could apply twice. Agents run on corporate networks behind proxies and private CAs; a CA file makes such a network work without turning off the check that stops interception.

Check: review the upstream client (timeouts, retries by method, proxy, TLS); the upstream-failure and apply-failure rows of VER-004.

## Checklist

Output

- [ ] CLI-001 one JSON document on stdout; everything else on stderr; UTF-8 without BOM; no prompts
- [ ] CLI-002 exact envelope keys; `schema_version` `"2.0"`
- [ ] CLI-003 compact JSON by default; `--pretty`; `--compact` accepted
- [ ] CLI-004 `meta.duration_ms` always; only `notices` and `untrusted` besides
- [ ] CLI-005 `json` default; `text` for people; `raw` where declared; unsupported format → `E_USAGE` in JSON
- [ ] CLI-006 streams: an envelope with `type` per line, ending in a `summary`
- [ ] CLI-007 snake_case keys, string IDs, UTC times with `Z` under `_at` keys, dates under `_on`, snake_case enums

Errors

- [ ] CLI-010 code, exit code and `retryable` from contract.json
- [ ] CLI-011 `dispatch_state` and `next_action` on every error
- [ ] CLI-012 `problem`, with `parameter`, `expected`, `did_you_mean`, `usage`, `available` where they apply
- [ ] CLI-013 `conflict`, with `parameter` or `active_operation` where required
- [ ] CLI-014 `details.scope` on scope refusals; the `required` role on upstream 403s
- [ ] CLI-015 no reply after dispatch → `E_OUTCOME_UNKNOWN` with `operation_id` and `lookup`
- [ ] CLI-016 one HTTP mapping function, by status
- [ ] CLI-017 short messages for the model, in English; no echoed input, no upstream text
- [ ] CLI-018 extension codes only through `contract-ext.json`

Command model

- [ ] CLI-020 one registry; help, `reference`, the Skill map and coverage generated from it
- [ ] CLI-021 tree shape; reserved names and flags; `<noun> <verb>` with the standard verbs
- [ ] CLI-022 root shortcuts for nouns under one parent, plurals, a guess list, whole-tree suggestions
- [ ] CLI-023 path first; unknown flags and stray positionals rejected; positionals bound to flags
- [ ] CLI-024 typed parameters; `secret`; no default on a required parameter
- [ ] CLI-025 true effects (a comment is `change`); writes that cannot be undone are dangerous, bulk ones only when what they do is; risk tier at least what they require
- [ ] CLI-026 error codes per command
- [ ] CLI-027 maturity with evidence
- [ ] CLI-028 standard flag names: `--limit`, `--cursor`, `--query`, `--since`, `--sort`, `--state`, `--wait`, `--output`, `--input`, `--profile`
- [ ] CLI-029 standard verb meanings: an empty `list` is a page, `update` changes only what is given, a missing `delete` target is `E_NOT_FOUND`, `search` states its real order

Discovery

- [ ] CLI-030 generated help; root lists every command; write help gives the recipe; no completion command
- [ ] CLI-031 `reference --index` within 8,000 characters, or grouped
- [ ] CLI-032 `reference --command` within 6,000 characters
- [ ] CLI-033 full `reference` for tooling only
- [ ] CLI-034 no mandatory first step
- [ ] CLI-035 `context` from local state, with the authorization summary and, with settings, the profile
- [ ] CLI-036 `doctor` with the five required checks, plus `settings` when the tool declares settings
- [ ] CLI-037 `changelog [--since]` from the embedded CHANGELOG
- [ ] CLI-038 expiring credentials renewed, or `E_AUTH` naming the sign-in step

Reads and output size

- [ ] CLI-040 task-shaped default fields; `--fields`
- [ ] CLI-041 `items`, `count`, `has_more`, continuation, `truncated`, `scope`, `sort`
- [ ] CLI-042 default success within 8,000 characters (full `reference` and `raw` exempt); errors within 2,000
- [ ] CLI-043 heavy sub-resources as separate, structured commands
- [ ] CLI-044 counting and filtering in the tool
- [ ] CLI-045 Markdown for rich text
- [ ] CLI-046 `meta.untrusted` paths, refs that hold people's text included, following `--fields`
- [ ] CLI-047 quiet stderr; typed notices
- [ ] CLI-048 refs (a sub-resource's under its parent's), URLs, names with `E_AMBIGUOUS`, relative times, `--input`, `--<param>-file`
- [ ] CLI-049 multi-ref reads, `--wait`, bounded run time

Writes

- [ ] CLI-050 every write through aicli's gate; no token code of the tool's own
- [ ] CLI-051 dry-run returns preview, token, expiry and operation id
- [ ] CLI-052 a bare call returns `E_CONFIRMATION_REQUIRED` carrying the preview and token
- [ ] CLI-053 confirm repeats the same request
- [ ] CLI-054 the token binds the whole resolved request
- [ ] CLI-055 canonical, keyed, single-use tokens; fail closed; no bypass
- [ ] CLI-056 `--dangerous` in both steps, for a dangerous command or plan
- [ ] CLI-057 re-checks at confirm, in the fixed order
- [ ] CLI-058 operation record written before anything is sent
- [ ] CLI-059 receipts; `ok` from `receipt_rules`
- [ ] CLI-060 unknown outcomes block the target until resolved
- [ ] CLI-061 `operation show`, `list`, `resolve`
- [ ] CLI-062 `--idempotency-key` on creates
- [ ] CLI-063 batches: one plural flag, one token, per-item results, `ok: false` on any failure
- [ ] CLI-064 long-running work: a gated submit and a status read with `--wait`
- [ ] CLI-065 `E_HUMAN_REQUIRED` with `action` and `resume`

Self-update, versions, performance

- [ ] CLI-070 aicli's built-in from `App.Update`: strict semver, no shell, no silent downgrade, verified binaries, Skills copied from the package, staged failures
- [ ] CLI-071 typed update notice read from the local cache; the release source contacted only by `update` and `doctor`
- [ ] CLI-080 `schema_version`, `spec_version`, Skill `min_version`
- [ ] CLI-081 deprecations marked, noticed on every use, kept for a MINOR release and 90 days, removed only in a MAJOR
- [ ] CLI-090 launcher overhead at most 100 ms

Settings, accounts and diagnostics

- [ ] CLI-100 settings from flag, environment, profile, default; a strict settings file with no secrets
- [ ] CLI-101 named profiles; `--profile` bound into tokens; `auth login`, `auth status`, `auth logout`
- [ ] CLI-102 `<PREFIX>_DEBUG` lines on stderr with secrets redacted; no `--verbose` or `--debug`

Network

- [ ] CLI-110 timeouts; at most two read retries, writes never re-sent; proxy variables; TLS always verified, `<PREFIX>_CA_FILE`; User-Agent

Part of [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec). Copyright (c) 2026 Sean Guo. MIT License; see `LICENSE` in the spec repository.
