# Migration notes: kibana-cli on spec 2.0

kibana-cli 1.1.17 (spec 1.6.1) moves to ai-native-cli-spec v2.0.1 as kibana-cli 2.0.0.
Follow `MIGRATION-2.0.md` in the spec repository; this file records how it is done
here and the decisions that hold for every later change. The finished reference is
gitlab-cli 2.0.0 (`gitlab-cli-pilot`): its `cmd/label.go`, `cmd/auth.go`, conformance,
coverage and command-map tests are the patterns copied here.

## State and order of work

1. `89ae3fe` synced the spec (v2.0.1: `.agent/`, `contract/`, `internal/aicli/`,
   sync scripts). `cc33dfd` fixed Kibana 9 (below).
2. The 1.x command code (`cmd/*.go`, `internal/output`, `internal/audit`,
   `internal/config/{config,store,credentials}.go`, `internal/contract`, the
   hand-written reference, the own confirm gate, the own update) is deleted; the old
   code stays readable with `git show 538837d:<path>`.
3. Every command is rebuilt on aicli from one `register<Group>` function per file.
4. `caba953` applied the live findings from Kibana 9.5.2 (below). Then the docs
   (README, `docs/UPGRADE-2.0.md`, COMPATIBILITY, E2E, the template's AGENTS,
   SECURITY and CONTRIBUTING), `scripts/live-run.py`, the Skill and the 2.0.0
   version and CHANGELOG.
5. Still to do, outside this branch: run `scripts/live-run.py` against a real Kibana,
   commit its record and raise the commands that passed to `verified`; run the
   VER-006 agent eval with `skills/kibana-cli/test-prompts.json`.

## Command tree

| Path | Effects | 1.x |
|---|---|---|
| `log search` | observe | `search` (root shortcut `search`) |
| `log get <hit>` (`<index>/<_id>`) | observe | new |
| `log aggregate` | observe | `agg` (root shortcut `agg`) |
| `pattern list` | observe | `patterns list` (group alias `patterns`) |
| `pattern fields` | observe | `patterns fields` |
| `pattern infer` | observe | `patterns infer` without `--write` |
| `object list` | observe | `objects list` (group alias `objects`) |
| `object get <object>` (`<type>/<id>`) | observe | `objects get` |
| `field-map get` | observe | `config show` (root shortcut `config`, verb alias `show`) |
| `field-map init` | artifact | `config init` |
| `field-map add-profile` | artifact, persist | `patterns infer --write` |
| `auth login` | configure (dangerous) | `auth login` |
| `auth status` | observe | `auth status` |
| `auth logout` | configure (dangerous) | `auth logout` |
| `profile list` | observe | `context list` (`context` is now aicli's built-in) |

1.x `context use/add/remove/current` are gone: `auth login --profile <name>` creates a
profile, settings.json's `default_profile` picks the default, `--profile` or
`KIBANA_CLI_PROFILE` picks one per call, and the built-in `context` reports the active
one.

## Decisions

- **Profiles replace contexts** (CLI-101). Settings (`App.Settings`): `host`, `user`,
  `password` (secret: `KIBANA_CLI_PASSWORD` only), `kibana_version`, `default_index`
  (the 1.x context's default index), `field_map_file` (the 1.x per-context field-map
  file; relative to `~/.kibana-cli`), `allowed_index_prefixes` (was the env-only
  `KIBANA_CLI_ALLOWED_INDEX_PREFIXES`; the env name is unchanged), `timeout`.
- **Keyring compatibility.** Service `kibana-cli`, account `<host>|basic|<user>`, the
  password itself: the 1.x entry. A profile with the same host and user signs in with
  a 1.x saved password without a new login. `auth login` without a password reuses an
  existing entry after checking it with Kibana and says so (`password_source:
  keyring_existing`). The password read from the keyring is registered with
  `app.AddSecret`.
- **1.x `config.json` is not read**, and there is no import command: the CHANGELOG
  and README give the `auth login` line per old context.
- **Field-map profile flag.** aicli owns `--profile` (the settings profile), so the 1.x
  `--profile <field-map profile>` of `search`/`agg` is `--field-profile`. It cannot be
  a renamed flag (the old name now means something else); the CHANGELOG lists it.
- **Renamed flags** (`Meta.RenamedFlags`, CLI-028, kept until 3.0): `--from` →
  `--since`, `--to` → `--until` (both still take 1.x's `now-15m`/`now` as well as
  `15m` and ISO times), `--size` → `--limit` (log search), `--buckets` → `--limit`
  (log aggregate), `--search` → `--query` (object list).
- **Removed flags**: `--format text` tables (aicli's text mode replaces them),
  `--json`, `--quiet` (aicli's), `--insecure`/`KIBANA_CLI_INSECURE` (no TLS-skip:
  `KIBANA_CLI_CA_FILE`), `--timeout` (the `timeout` setting, at most 30 s; 1.x
  defaulted to 60 s), `--force` of `config init` (field-map init only creates; move the
  old file away to start over), `--write` of `patterns infer` (now `field-map
  add-profile`), `--search-after` (see below), `--context` (`--profile`).
- **`--search-after` is gone.** 1.x paired it with an `_id` tiebreak sort, which
  Elasticsearch 8+ refuses by default (fielddata on `_id` is disabled), so it failed
  where it mattered. 2.0 pages with `--offset` (`next_offset`); deeper than 10,000 hits
  (Elasticsearch's `max_result_window`) the command says to narrow `--until`.
- **Local-file writes** (decision 5): `field-map init` is an `artifact` write through
  `App.WriteOutput` (new file only). Appending an inferred profile moved from `patterns
  infer --write` into its own command, `field-map add-profile` (`artifact` + `persist`,
  because it creates the file when missing and rewrites it otherwise), with a preview
  that shows the profile and blocks a name the file already has; its plan is built
  from flags only, so a confirm never re-infers. `pattern infer` returns the exact
  `field-map add-profile` line as `next_command`. The file is edited as a YAML node
  tree, so comments survive (`internal/fieldmap/edit.go`).
- **No upstream writes.** Every Kibana request is a read: GETs of Kibana APIs and
  `POST /api/console/proxy` carrying `GET _security/_authenticate` or `POST
  <index>/_search` only (`kibanaclient.proxyAllowed`); counts are `_search` with
  `size: 0`. The index allowlist (`allowed_index_prefixes`) checks every part of an
  index list, `--dsl` included (1.x skipped it for `*`).
- **Network** (CLI-110): aicli's transport and User-Agent, per-request timeout ≤ 30 s,
  reads retried at most twice (connection refused, 429, 502, 503, 504, Retry-After up
  to 10 s), no `--verbose`/`--debug` (`KIBANA_CLI_DEBUG`).
- **Kibana 9**: every request carries `x-elastic-internal-origin: kibana` (and
  `kbn-xsrf: true`); `internal/kibanaclient/kibana9_test.go` is the regression test. A
  400 "is not available with the current configuration" that still arrives (a proxy
  stripped the header) is `E_UNSUPPORTED` saying the internal-API restriction refused
  the call because the header did not arrive, never "Console Proxy disabled".
- **The Kibana version is optional** (live check on 9.5.2). Kibana 8 and 9 redact
  `GET /api/status` to `{"status":{"overall":{"level":"available"}}}` for an account
  without monitoring privileges, and no header carries the version; 1.x then failed
  every command ("could not detect Kibana version"). 2.0 never fails for it:
  `kibanaclient.VersionInfo` takes the `kibana_version` setting, else `/api/status`'s
  `version.number`, else Elasticsearch's `GET /` through the Console Proxy (Kibana
  and Elasticsearch share their version; it needs the `monitor` cluster privilege),
  else unknown. Only `doctor` (check `kibana_version`, with the source) and `auth
  status` (`kibana_version`, `kibana_version_source`) ask; `context` reports the
  configured version or `unknown` without a request. No request sends `kbn-version`.
- **APIs by capability, not version.** With no version known the client asks the 8.x+
  API first and falls back on Kibana's bare 404 for a missing route: `GET
  /api/data_views` then the index-pattern saved objects (`_find`), `GET
  /internal/data_views/_fields_for_wildcard` (with `Elastic-Api-Version: 1`) then
  `/api/index_patterns/_fields_for_wildcard`, `GET
  /api/data_views/data_view/<id>` then the saved object. A configured 7.x version
  asks the 7.x API first. `/api/features` (403 for a read account) is not used.
- **Data streams** (live check on Elasticsearch 9): `logs-*-*` indices are data
  streams, so a hit's `_index` is a backing index (`.ds-logs-app-default-2026.10.25-000001`),
  not the searched pattern. The hit `ref` keeps the backing index (`log get` reads it
  back with an ids search on it); the allowlist admits a backing index when its
  stream's name (without `.ds-`) has an allowed prefix; `log get` matches the field
  map's index rules by the stream's name.
- **`--explain` instead of a `--dry-run` preview** of DSL: aicli runs a read when
  `--dry-run` is given, so `log search --explain` and `log aggregate --explain` add the
  exact Elasticsearch request (path and body as JSON text) to `scope.request`.
- **Field defaults without a field map.** `--service`, `--level`, `--trace-id` and
  the message fields work with no field map: `service.name, service_name, service,
  log_app, app_name, application, kubernetes.labels.app`; `log.level, level,
  severity, log_level` (`log_level` added after the live run: the IoT indices use it);
  `message, msg, log.message`; `trace.id, traceId, trace_id`. A field map
  profile or index rule overrides them; a `--service` the field map does not define is
  a value match, not an error.
- **The Skill and `--profile`.** SKL-009 forbids telling agents to pass `--profile`
  unless the user asks for it. The Skill says to pass `--profile <name>` when the
  user names which Kibana (prod, test, a system), and not otherwise; naming the
  Kibana is the user asking.
- **CHANGELOG size.** The `changelog` built-in returns the newest entry as data, and
  conformance R04 holds it to the 8,000-character budget; aicli drops tables, code
  blocks and non-standard sections from it. The 2.0.0 entry is therefore bullets
  only (about 5,800 characters) and the step-by-step upgrade is in
  `docs/UPGRADE-2.0.md`.
- **`--field-map-file`** stays on the `field-map` commands only; log and pattern
  commands read the `field_map_file` setting (`KIBANA_CLI_FIELD_MAP_FILE`, or the
  profile's `field_map_file`).

## Output

- `ref` formats (also accepted back as input):
  - log hit: `<index>/<_id>` (the index never holds `/`, so the first `/` splits it)
  - saved object: `<type>/<id>` (`dashboard/7adf…`, `index-pattern/17f4…`)
  - data view: `index-pattern/<id>`, the same as its saved object; `--data-view`
    takes the ref or the bare id
  - profile: `profiles/<name>` (also the scope target of `auth login`/`logout`)
  - field-map writes: the file's absolute path (target and scope output)
- Elasticsearch documents and saved-object attributes have keys that are not
  snake_case (`@timestamp`, `log.level`, `panelsJSON`), which CLI-007 forbids as JSON
  keys. They are output as lists of `{name, value}` (nested objects flattened to
  dotted names, arrays of objects as JSON text); `--format raw` on `log get` and
  `object get` returns the upstream JSON unchanged.
- Untrusted (schema `Untrusted`): log `message`, `service`, `level`, `trace_id`,
  `span_id`, `source`; saved-object `title`, `description`, `attributes`, references'
  names; data view `title`, `name`; field `name`; account and role names.
- Log messages are cut at 500 characters in lists with `message_truncated: true` and a
  notice; `log get` returns the whole message.
- Lists carry `total` from `hits.total` (exact: searches send `track_total_hits`),
  `scope` with the index, the time range in UTC, the filters, the field profile and
  the query language; `sort` is `timestamp_desc` for searches (on `scope.time_field`), `count_desc` /
  `key_asc` for aggregations, `title_asc` / `name_asc` for client-sorted lists and
  `upstream` for saved objects (Kibana's `_find` order).
- A search with no hits runs the 1.x zero-hit probes (window total, all-fields match)
  and reports them in `scope.zero_hits` with a notice.

## Live runs (2026-10-25)

`scripts/live-run.py` ran every read command, all as expected:

- Kibana 9.5.2 in local Docker (`kibana:9.5.2`, `elasticsearch:9.5.2`), a read-only
  account (index `read` and `view_index_metadata`, Kibana base `read`), environment
  mode: [svc-log](../evidence/2026-10-25-kibana-9.5.2-svc-log.md) (level, service,
  message, traceId; 39 of 39) and [iot-log](../evidence/2026-10-25-kibana-9.5.2-iot-log.md)
  (log_level, log_app, msg with the trace id inside; 39 of 39). These records are the
  `live:` evidence of the verified commands.
- Company clusters (records kept out of the repo, they name internal indices and
  objects): a managed Kibana 7.10.0 in production and a 7.7.1 test cluster, profile
  mode, where 2.0 read the password kibana-cli 1.x had saved in the Windows Credential
  Manager under the same service and account (`credentials_source: keyring`): 40 of 40
  and 41 of 41. A Kibana 9.5.2 test cluster (2026-10-26), environment mode: 39 of 39,
  hits from data stream backing indices, version from `/api/status`; kibana-cli 1.1.17
  failed there with `could not detect Kibana version`.

What the runs found and this branch fixed: Kibana 9 answers Elasticsearch errors with
HTTP 200 and the status in the body (`wrappedESError`); msg-mode trace ids were looked
for only in `message`; `log_level` was not a usual level field; a default search page of
long production messages reached 9,990 characters (now cut to the budget with
`next_offset`). Confirmed as designed: the redacted `/api/status` (version unknown for
the read-only account on 9.5.2, from `/api/status` on 7.10); the proxy's three
400/404 answers; data views and fields on 9.5.2 and the index-pattern APIs on 7.10;
saved objects of every type; zero-hit probes; terms on a text field retried on
`.keyword`; date histograms. Production data quirk seen: some documents carry the
literal trace id `%{[log][traceId]}` (an unexpanded pipeline placeholder).

Not run live, still `experimental`: `auth login`, `auth logout`, `field-map init`,
`field-map add-profile` (they write the keyring, settings or local files; their
gates are covered by the conformance suite). Not checked live: Kibana 8.x; macOS and
Linux keyrings; KQL and Lucene counts against Discover.
