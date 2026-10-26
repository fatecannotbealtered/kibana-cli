# Live runs

`go test ./...` runs every command against the fake Kibana in `cmd/fakekibana_test.go`
and is safe anywhere: it never touches a real Kibana or the OS keyring. That alone
makes a command `experimental` (VER-005); it becomes `verified` only with a recorded
run against a real Kibana, which `scripts/live-run.py` makes. The read commands are
`verified` by the records in [evidence/](evidence/); `auth login`, `auth logout`,
`field-map init` and `field-map add-profile` stay `experimental`.

## What the live run does

`scripts/live-run.py` builds the binary from this checkout (or takes `--binary`),
runs every **read** command against a Kibana you name, checks every stdout against
the vendored contract, and writes a VER-005 live record to `docs/evidence/`:
`<date>-kibana-<version>.json` and its Markdown twin. Per call the record keeps the
argv, the exit code, the duration, what was expected, the checks that failed and a
SHA-256 of stdout; it lists the commands that passed every call
(`verified_commands`) and those not run (the writes).

It runs the built-ins (`--version`, `context`, `doctor`, `reference --index`,
`reference --command`, `changelog`, `operation list`), `auth status`, `profile
list`, `pattern list|fields|infer`, `field-map get`, `log search` (paging,
`--level`, Lucene, KQL, `--explain`, `--dsl`, zero hits, a field profile, a trace
id), `log get` (also `--format raw` and a missing document), `log aggregate`
(terms and a date histogram), `object list` for every type and `object get`, and
two refusals that send nothing.

It never runs `auth login`, `auth logout`, `field-map init`, `field-map
add-profile` or `update`, and the scope file it sets does not exist, so no write
could be granted. Each run gets a fresh home folder and state directory, deleted
afterwards: your settings, scope file, journal and field map are not read or
changed. The field map that `field-map get` and `--field-profile` read is written by
the script into that folder from `pattern infer`'s output.

## Environment mode

Use a read-only Kibana user and a narrow index:

```bash
export KIBANA_CLI_HOST=https://kibana.example.com
export KIBANA_CLI_USER=<read-only user>
export KIBANA_CLI_PASSWORD=<password>
python scripts/live-run.py --index 'logs-*' --since 24h
```

The password is passed to the binary through the environment only, scanned for in
every output, and never recorded.

## Profile mode

When kibana-cli 1.x (or `auth login`) saved the user's password in the OS keyring,
the run can use it without anyone typing it:

```bash
python scripts/live-run.py --host https://kibana.example.com --user alice [--index 'app-log-*']
```

The run writes a scratch `settings.json` with one profile (host and user, no
secret); the binary reads the password from the keyring entry `kibana-cli` /
`<host>|basic|<user>`. Only read commands run, so the entry is read and never
written or removed. Without `--index`, the first data view with a time field is
searched. The OS keyring belongs to the user, not to the scratch home folder; on
macOS the Keychain may not be found under the scratch `HOME`, in which case use
environment mode.

## Options

- `--since 24h`, `--interval 1h`: the time range of the log calls and the histogram
  bucket (widened to keep at most 120 buckets).
- `--trace-id <id>`: a trace to follow; default, the first hit's.
- `--spec <ai-native-cli-spec checkout>`: also run the spec's Python validator on
  every stdout.
- `--include-output`: keep stdout and stderr in the record. They hold log content,
  so do not commit such a record.
- `--keep-host`: keep the Kibana host and user; by default they are replaced with
  `<host>` and `<user>`.
- `--keep-run`: keep the run folder for inspection.

The script exits 0 when every call passed and 1 otherwise. Review the record before
committing it; then set `Maturity: aicli.Verified` with a `live:docs/evidence/<file>.md`
reference on the commands in `verified_commands`. A change to a command's request,
parsing or output voids its live evidence.

## Runs so far

Every read command passed on Kibana 9.5.2 (local Docker on two index styles, and a
test cluster), managed 7.10.0 and 7.7.1, with read-only accounts, 2026-10-25 and 26.
What the runs found is built into the fake Kibana: the redacted `GET /api/status`,
the Console Proxy's 400 and 404 answers, Elasticsearch errors returned as HTTP 200
with the status in the body, data views and index-pattern saved objects,
`/api/features` returning 403, and data stream backing indices. Details and what is
still unchecked (Kibana 8.x, macOS and Linux keyrings): "Live runs" in
[migration-2.0/notes.md](migration-2.0/notes.md).
