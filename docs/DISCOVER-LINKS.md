# Discover links

`--discover-url` on `log search` (also with `--dsl`) and `log aggregate` adds
`scope.discover_url`: a link to Kibana's Discover that shows the same documents the
command matched, for a person to open from a Jira comment or a Feishu card (#11).

```bash
kibana-cli log search --index 'app-log-*' --service order-svc --level ERROR --since 1h --discover-url
```

When no link can be built, `scope.discover_url` is `null` and
`scope.discover_url_reason` says why, in one sentence, with what to pass instead.
The search itself is never affected. The flag is opt-in because a link is 1 to 3 KB;
a default `log search` page still fits the 8,000-character budget with the link in
`scope` (the page ends earlier, with `next_offset`). Both keys are listed in
`meta.untrusted`: they carry the query text and data view titles.

## The link

```
<host>/app/discover#/?_g=(filters:!(),time:(from:'<since>',to:'<until>'))&_a=(index:<data view id>,filters:!((meta:(alias:'<label>',disabled:!f,negate:!f,type:custom,key:query,index:<data view id>),query:<query>)),query:(language:kuery,query:''))
```

- `<host>` is the `host` setting (the Kibana base URL).
- `<since>` and `<until>` are `scope.since` and `scope.until`, the UTC range the
  command searched, in Discover's time picker.
- `<query>` is the Elasticsearch query the command sent, without its time range
  clause, as one custom filter. The query bar stays empty, so Discover matches what
  the CLI matched whatever the query language was (Lucene, KQL, `--precise`, field
  filters, trace ids). With `--dsl` it is the body's `query` (`match_all` without
  one), and the time picker covers 1970-01-01 to the time of the search.
- `<label>` is the filter's name in Discover: `kibana-cli: ` and the filters
  (service, level, trace id, `--field` values, the query text, the field profile),
  cut to 120 characters.
- `_g` and `_a` are RISON (`internal/discover`), percent-encoded for the URL
  fragment: letters, digits and `()!:,'*~-_.$/` stay, every other byte is encoded.
  Kibana 9 rewrites `index` into its `dataSource` itself.

The query in the link is the one in the request (`--explain` shows it); the tests
decode every link and compare its time range, data view and filter with the request
the fake Kibana received (`cmd/discover_test.go`).

## The data view

- `--data-view <ref>`: that data view.
- Otherwise (`--index`, the field profile's index or `default_index`): the one data
  view whose index pattern (`title`) is exactly the index searched; only then is the
  data view list read (one extra read). Several exact matches give a reason listing
  their refs; none gives a reason naming up to three data views whose pattern covers
  the index (re-run with `--data-view <ref>`, which searches that data view's pattern,
  so CLI and link agree again), or saying that none exists.
- When the data view's time field is known and differs from the time field searched,
  or the data view has none, there is no link: Discover would show another window.
  The time field is known for `--data-view` and on Kibana 7.x (the index-pattern
  list carries it); the data views API of 8.x and 9.x lists data views without it,
  so there it is not checked.

## Discover's path

| Kibana | Path | Picked by |
|---|---|---|
| 7.0 to 7.8 | `/app/kibana#/discover?_g=…&_a=…` | the version |
| 7.9 to 7.17 | `/app/discover#/?_g=…&_a=…` | the version |
| 8.x, 9.x | `/app/discover#/?_g=…&_a=…` | the version, else the data views API answered |
| version unknown, no data views API (7.x) | `/app/kibana#/discover?_g=…&_a=…` | the 404 of the data views API |

The version is the one the client knows without a request: the `kibana_version`
setting (or one found earlier in the same run). `--discover-url` never asks for it,
so on 7.9 and 7.10 without the setting the link takes the legacy path.

## Limits

- Kibana spaces: data views are looked up in the default space, and the link has
  no `/s/<space>` prefix.
- A data view is needed: Kibana cannot show an index pattern no data view has.
- `--offset` and `--limit` are not carried: Discover shows every match,
  newest first on the data view's time field.
- `--dsl`: only the body's `query` is carried; `post_filter`, `min_score`,
  `collapse` and paging keys are not, and documents dated after the search fall
  outside the time picker.
- Follow-ups not done: a link for `log get`, and a link per hit.

## Checks

- Kibana 9.5.2, by hand in a browser: `log search` reported `total` 12 and Discover
  showed 12 hits for the same link.
- Open, to check in a browser: Kibana 7.10 (and 7.9) with the version unknown, whether
  `/app/kibana#/discover?_g=…&_a=…` redirects to the new Discover with the state kept;
  the legacy link on 7.7.1; Kibana 8.x; a data view in another space.
