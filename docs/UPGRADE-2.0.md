# Upgrading from kibana-cli 1.x to 2.0

kibana-cli 2.0 follows ai-native-cli-spec 2.0. It fixes searches on Kibana 9, works
when Kibana 8 or 9 hides its version, and handles Elasticsearch 9 data streams; it
also renames commands and flags, replaces contexts with profiles, and asks for a
scope before it writes a local file. [CHANGELOG.md](../CHANGELOG.md) lists every
change; this page gives the steps.

## 1. Install 2.0 and its Skill

```bash
npm install -g @fateforge/kibana-cli
npx skills@1.7.2 add "$(npm root -g)/@fateforge/kibana-cli" -y -g
```

`kibana-cli update` from 1.x also works; it copies the new Skill. An agent that
still has the 1.x Skill loaded should re-read it.

## 2. Turn each context into a profile

2.0 does not read `~/.kibana-cli/config.json`. Its contexts become profiles in
`~/.kibana-cli/settings.json`, and the passwords stay where 1.x saved them: the OS
keyring entry `kibana-cli` / `<host>|basic|<user>` (Windows Credential Manager,
macOS Keychain, Linux Secret Service).

`auth login` creates a profile, checks the password with Kibana and stores host and
user in it. It changes your sign-in, so it needs a scope that grants `configure`
and `dangerous` on `profiles/<name>` (README, Authorization). For each context in
`config.json` (`contexts.<name>.host` and `.username`), run in your own terminal:

```bash
kibana-cli auth login --profile <name> --host <host> --user <username> --dangerous --dry-run
kibana-cli auth login --profile <name> --host <host> --user <username> --dangerous --confirm <confirm_token>
```

When 1.x saved a password for that host and user, Kibana checks it and it is reused
(`password_source: keyring_existing`); nothing is typed. Otherwise add
`--password-file -` to both lines and type the password when asked. The first login
sets `default_profile`; set it to your 1.x `currentContext` if that was another one.

Then copy the rest of each context into its profile: `defaultIndex` becomes
`default_index`, `fieldMapFile` becomes `field_map_file` (relative to
`~/.kibana-cli`, as before) and `kibanaVersion` becomes `kibana_version` (optional
now). The result looks like this:

```json
{"default_profile": "prod",
 "profiles": {"prod": {"host": "https://kibana.example.com", "user": "alice",
                       "default_index": "app-log-*", "field_map_file": "prod-fields.yaml"},
              "test": {"host": "https://kibana-test.example.com", "user": "alice"}}}
```

`kibana-cli profile list` shows the profiles and `kibana-cli doctor --profile <name>`
checks one. When everything works, delete `~/.kibana-cli/config.json`.

Agents that only use environment variables need none of this:
`KIBANA_CLI_HOST`, `KIBANA_CLI_USER` and `KIBANA_CLI_PASSWORD` work as before.

## 3. Update scripts and prompts

| 1.x | 2.0 |
|---|---|
| `search` | `log search` (`search` still works) |
| `agg` | `log aggregate` (`agg` still works) |
| `patterns list`, `fields`, `infer` | `pattern list`, `fields`, `infer` (`patterns` still works) |
| `patterns infer --write` | `field-map add-profile` (`pattern infer` returns the line as `next_command`) |
| `objects list`, `objects get --type T --id ID` | `object list`, `object get T/ID` (`objects`, `--type`/`--id` still work) |
| `config show`, `config init` | `field-map get`, `field-map init` (`config` still works) |
| `context list` | `profile list` |
| `context use NAME` | `--profile NAME` per call, or `default_profile` |
| `--context NAME`, `KIBANA_CLI_CONTEXT` | `--profile NAME`, `KIBANA_CLI_PROFILE` |
| `search --profile P`, `agg --profile P` | `--field-profile P` |
| `--from`, `--to` | `--since`, `--until` (the old names still work, with a notice) |
| `search --size`, `agg --buckets` | `--limit` (the old names still work, with a notice) |
| `objects list --search` | `object list --query` |
| `--search-after` | `--offset <next_offset>` |
| `--insecure`, `KIBANA_CLI_INSECURE` | `KIBANA_CLI_CA_FILE` with your CA's PEM file |
| `--timeout N` | `KIBANA_CLI_TIMEOUT` (at most `30s`) |
| `--dry-run` on a search (DSL preview) | `--explain` (`scope.request`) |
| `--field-map-file` on search and agg | `KIBANA_CLI_FIELD_MAP_FILE`, or the profile's `field_map_file` |
| `_untrusted` | `meta.untrusted` |

Output keys are snake_case and lists are pages: read `data.items`, `data.total`,
`data.has_more` and `data.next_offset`. A hit's `ref` (`<index>/<_id>`) goes back
into `log get`. `log search` returns 20 hits by default (1.x: 50).

## 4. Grant a scope for local writes

Searching needs no scope. `field-map init` needs `artifact`, `field-map
add-profile` needs `artifact` and `persist`, with the field-map file as the target
and its folder in `outputs`; `auth login` and `auth logout` need `configure` and
`dangerous` on `profiles/<name>`. The README's Authorization section has an example.
Write the scope file yourself; the tool never does.
