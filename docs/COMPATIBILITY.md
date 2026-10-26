# Compatibility

kibana-cli talks to Kibana only. Searches and counts go to Elasticsearch through
Kibana's **Console Proxy** (`POST /api/console/proxy`); data views, fields and saved
objects come from Kibana's own APIs. It never connects to Elasticsearch directly and
never writes to Kibana or Elasticsearch.

## Kibana versions

| Kibana | Status in 2.0 | Notes |
|---|---|---|
| 9.x | supported; live on 9.5.2 (a local Docker cluster and a test cluster in production use); the tests' fake Kibana behaves like 9.5.2 | needs `x-elastic-internal-origin`, which every request sends; `GET /api/status` hides the version from read-only accounts; Elasticsearch errors come back as HTTP 200 with the status in the body; log indices are often data streams (`.ds-…` backing indices) |
| 8.x | supported; tested against the fake (data views API, internal fields route), not yet live | `GET /api/status` hides the version from accounts without monitoring privileges |
| 7.x | supported; live on managed 7.7.1 and 7.10.0; tested against the fake as 7.17 | no data views API: index-pattern saved objects and the 7.x fields route |

Live runs of `scripts/live-run.py` (VER-005, [E2E.md](E2E.md)), every read command
with a read-only account, 2026-10-25 and 26: Kibana 9.5.2 in Docker 39 of 39 on two
index styles (the records in [evidence/](evidence/) are the `live:` evidence of the
verified commands); a 9.5.2 test cluster 39 of 39; managed 7.10.0 40 of 40 and 7.7.1
41 of 41, both reading the password kibana-cli 1.x had saved in the OS keyring. The
company clusters' records stay out of the repo because they name internal indices.
Details: [migration-2.0/notes.md](migration-2.0/notes.md), section "Live runs".

### Kibana 9

Kibana 9 opens its internal APIs, the Console Proxy among them, only to requests
that carry `x-elastic-internal-origin`. Checked on 9.5.2:

| Request to `/api/console/proxy` | Kibana 9.5.2 answers |
|---|---|
| `POST` with `kbn-xsrf` only | 400 `uri [/api/console/proxy] with method [post] exists but is not available with the current configuration` |
| `POST` with `x-elastic-internal-origin` only | 400 `Request must contain a kbn-xsrf header.` |
| `POST` with both | 200 |
| `GET` | 404 |

kibana-cli sends `kbn-xsrf: true` and `x-elastic-internal-origin: kibana` on every
request; Kibana 7 and 8 ignore the second. If a reverse proxy in front of Kibana
drops unknown headers, the first 400 still arrives; kibana-cli then reports
`E_UNSUPPORTED` saying the header did not arrive, and `doctor`'s `console_proxy`
check says the same. Let the proxy pass the header through.

### The Kibana version

kibana-cli needs no version. Kibana 8 and 9 answer `GET /api/status` with only
`{"status":{"overall":{"level":"available"}}}` for an account without monitoring
privileges, and no header carries the version. When it is useful (`doctor`, `auth
status`), kibana-cli takes it from the `kibana_version` setting, else from
`/api/status`, else from Elasticsearch's `GET /` through the Console Proxy (Kibana
and Elasticsearch share their version; that needs the `monitor` cluster privilege),
and otherwise reports it as unknown. It never sends `kbn-version`.

### Data views and fields

With no version known, the newer API is asked first and the older one on Kibana's
404 for a missing route:

| What | 8.x and 9.x | 7.x |
|---|---|---|
| list data views (`pattern list`) | `GET /api/data_views` | `GET /api/saved_objects/_find?type=index-pattern` |
| one data view (`--data-view`) | `GET /api/data_views/data_view/<id>` | `GET /api/saved_objects/index-pattern/<id>` |
| fields (`pattern fields`, `pattern infer`, field lookups) | `GET /internal/data_views/_fields_for_wildcard` with `Elastic-Api-Version: 1` | `GET /api/index_patterns/_fields_for_wildcard` |

The data views list of 8.x and 9.x has no time field, so `pattern list` shows
`time_field` on 7.x only; `--data-view` reads it from the single data view.
`kibana_version` set to a 7.x version makes the 7.x APIs go first.

## Kibana APIs used

| API | Commands |
|---|---|
| `GET /api/status` | `doctor`, `auth status` (version) |
| `POST /api/console/proxy?path=<index>/_search&method=POST` | `log search`, `log get`, `log aggregate`, `pattern infer` |
| `POST /api/console/proxy?path=_security/_authenticate&method=GET` | `auth login`, `auth status`, `doctor` |
| `POST /api/console/proxy?path=/&method=GET` | `doctor`, `auth status` (version, when `/api/status` hides it) |
| data views, fields (above) | `pattern`, `--data-view`, field lookups |
| `GET /api/saved_objects/_find`, `GET /api/saved_objects/<type>/<id>` | `object list`, `object get` |

The Console Proxy carries nothing else: `kibanaclient.proxyAllowed` refuses any
other path or method before it is sent. `/api/features` is not used (a read-only
account gets 403).

## Elasticsearch

- **Data streams** (Elasticsearch 9 makes `logs-*-*` data streams): a search on the
  stream's pattern returns hits from backing indices such as
  `.ds-logs-app-default-2026.10.25-000001`. The hit's `ref` keeps that index, `log
  get` reads it, and `allowed_index_prefixes` and the field map match it by the
  stream's name (`logs-app-default`).
- **Paging** uses `from` and `size` up to Elasticsearch's 10,000-hit window
  (`max_result_window`). 1.x's `--search-after` sorted on `_id`, which Elasticsearch
  8 and later refuse, so it is gone.
- **Counts** are exact: searches send `track_total_hits: true`.
- `log aggregate` on a `text` field retries on its `.keyword` sub-field when
  Elasticsearch refuses the first request.

## Query languages

- Lucene is the default; its boolean operators must be uppercase. An unquoted
  lowercase `and`, `or` or `not` is refused with `E_VALIDATION` rather than sent
  with a different meaning.
- `--query-language kql` compiles a strict subset of Kibana's KQL locally to
  Elasticsearch DSL: field terms and phrases, booleans in any case, grouping and
  lists, ranges, `exists`, value wildcards, nested groups and escaping. Syntax that
  needs data-view field metadata, such as wildcard field names other than the
  all-fields `*`, fails closed with `E_VALIDATION`.
- `--data-view` takes the data view's index pattern and time field. It does not
  import Discover or dashboard state (filters, saved queries, pinned filters, URL
  time).
- Kibana Advanced Settings such as `query:allowLeadingWildcards` or a custom date
  time zone are not read; use `--dsl` for a deployment-specific body.
- `--trace-id` in field mode matches the configured trace fields and, for recall,
  the quoted id in all fields. Use `--field trace.id=<id>` (or the real field name)
  for a strict field filter.

## Requirements

- The Kibana user needs read privileges on the log indices (and, for data streams,
  on the stream names) and access to Kibana with the Console enabled.
- TLS is always verified; a private CA goes in `KIBANA_CLI_CA_FILE`.
- Each request times out after 30 s at most (`KIBANA_CLI_TIMEOUT`).
