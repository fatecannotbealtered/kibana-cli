package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// The log group: search, read and aggregate log documents through Kibana's Console
// Proxy. Every request is a search (POST <index>/_search through the proxy), so
// every command here is a read.

const logEvidence = "simulated:cmd/log_test.go"

// The live runs of scripts/live-run.py every read command passed (VER-005): Kibana
// 9.5.2 with a read-only account, on an index with the usual field names and on one
// with the IoT names (log_level, log_app, msg).
const (
	liveEvidenceSvc = "live:docs/evidence/2026-10-25-kibana-9.5.2-svc-log.md"
	liveEvidenceIoT = "live:docs/evidence/2026-10-25-kibana-9.5.2-iot-log.md"
)

const (
	// messageLimit is how much of a message a list shows (CLI-042); log get shows it
	// whole.
	messageLimit = 500
	// listValueLimit cuts long text values of source fields in lists.
	listValueLimit = 1000
	// getValueLimit cuts long text values of log get's source.
	getValueLimit   = 4000
	searchMaxLimit  = 500
	bucketsMaxLimit = 100
	// maxHistogramBuckets keeps a date histogram within the response budget.
	maxHistogramBuckets = 120
)

var logHitFields = []string{"ref", "index", "id", "timestamp", "service", "level", "message", "message_truncated",
	"trace_id", "span_id", "source_fields", "source"}

var logHitUntrusted = []string{"service", "level", "message", "trace_id", "span_id", "source_fields", "source"}

func registerLog(app *aicli.App) {
	group := addGroup(app, "log", "Search, read and aggregate log documents", "logs")
	app.Schema("log_hit_page", aicli.SchemaDef{Shape: "page", Fields: logHitFields,
		DefaultFields: []string{"ref", "timestamp", "service", "level", "message", "message_truncated", "trace_id", "source_fields"},
		Untrusted:     logHitUntrusted})
	app.Schema("log_hit", aicli.SchemaDef{Shape: "object", Fields: logHitFields,
		DefaultFields: []string{"ref", "index", "id", "timestamp", "service", "level", "message", "trace_id", "span_id", "source_fields", "source"},
		Untrusted:     logHitUntrusted})
	app.Schema("log_bucket_page", aicli.SchemaDef{Shape: "page", Fields: []string{"key", "count", "metric"},
		Untrusted: []string{"key"}})
	addLogSearch(app, group)
	addLogGet(app, group)
	addLogAggregate(app, group)
	// 1.x's root commands, as shortcuts (CLI-022).
	app.AddShortcut("search", "log", "search")
	app.AddShortcut("agg", "log", "aggregate")
}

// ---------------------------------------------------------------- log search

func addLogSearch(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "search", RunE: func(cmd *cobra.Command, _ []string) error {
		if raw := flagString(cmd, "dsl"); raw != "" {
			return runDSLSearch(app, cmd, raw)
		}
		limit, offset, err := pageFlags(cmd, searchMaxLimit)
		if err != nil {
			return err
		}
		q, err := resolveLogQuery(app, cmd, "")
		if err != nil {
			return err
		}
		client, err := q.clientFor(app)
		if err != nil {
			return err
		}
		opts := q.searchOptions()
		opts.Size, opts.Offset = limit, offset
		res, err := client.Search(cmd.Context(), opts)
		if err != nil {
			return readError(err)
		}
		view := newHitView(q.Resolved)
		view.maxMessage, view.maxValue = messageLimit, listValueLimit
		scope := q.scope()
		scope["limit"], scope["offset"] = limit, offset
		explain(cmd, scope, opts.Index, kibanaclient.BuildSearchBody(opts))
		page := hitsPage(app, cmd, res, view, limit, offset, scope)
		if res.Total == 0 && len(res.Hits) == 0 {
			zeroHits(app, cmd.Context(), client, opts, q, scope)
		}
		return app.List(cmd, page)
	}}
	queryFlags(c, "15m")
	c.Flags().Int("limit", 20, fmt.Sprintf("Hits, 1 to %d", searchMaxLimit))
	c.Flags().Int("offset", 0, "Hits to skip (next_offset)")
	c.Flags().StringSlice("source-fields", nil, "Source fields to add as source_fields, like http.status (a name keeps the fields under it)")
	c.Flags().String("dsl", "", "A whole _search body as JSON; still a search on the index")
	c.Flags().Bool("explain", false, "Add the request sent to scope.request")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Search log documents by time range, service, level, trace id, text or exact field values, newest first",
		Effects: []aicli.Effect{aicli.Observe}, Output: "log_hit_page", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{logEvidence, liveEvidenceSvc, liveEvidenceIoT},
		Positional: "query", RenamedFlags: queryRenamedFlags(map[string]string{"size": "limit"}),
		Params: withParams(queryParams(), map[string]aicli.ParamDoc{"dsl": {Type: "json"}, "limit": {Unit: "hits"}}),
		Examples: []string{
			"kibana-cli log search --index 'app-log-*' --level ERROR --since 1h",
			"kibana-cli log search --index 'app-log-*' --trace-id 4bf92f3577b34da6a3ce929d0e0e4736 --since 1d",
			"kibana-cli log search timeout --data-view index-pattern/17f4cc60 --service order-svc --since 30m --source-fields http.status",
		}})
}

// pageFlags reads --limit and --offset; offset + limit stays within Elasticsearch's
// 10,000-hit window.
func pageFlags(cmd *cobra.Command, max int) (int, int, error) {
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	if limit < 1 || limit > max {
		return 0, 0, aicli.Validation("--limit", fmt.Sprintf("1 to %d", max), fmt.Sprintf("--limit must be between 1 and %d.", max))
	}
	if offset < 0 {
		return 0, 0, aicli.Validation("--offset", "0 or more", "--offset must not be negative.")
	}
	if offset+limit > maxResultWindow {
		e := aicli.Validation("--offset", fmt.Sprintf("offset + limit at most %d", maxResultWindow),
			fmt.Sprintf("Elasticsearch pages at most %d hits deep, and --offset plus --limit is past that.", maxResultWindow))
		e.Details["next_action"] = "Narrow the time range instead: set --until to the oldest timestamp you have seen and start again at --offset 0."
		return 0, 0, e
	}
	return limit, offset, nil
}

// hitsPage turns one search page into the list output, with the continuation, the
// total and the notices about cut messages and failed shards.
func hitsPage(app *aicli.App, cmd *cobra.Command, res *kibanaclient.SearchResult, view hitView, limit, offset int, scope map[string]any) aicli.Page {
	view.sourceFields, _ = cmd.Flags().GetStringSlice("source-fields")
	view.sourceFields = fieldmap.UniqueStrings(view.sourceFields)
	wantSource := aicli.WantsField(cmd, "source")
	items := make([]map[string]any, 0, len(res.Hits))
	cut := false
	for _, h := range res.Hits {
		v := view
		v.withSource = wantSource
		hit, truncated := v.logHit(h)
		if len(view.sourceFields) > 0 {
			hit["source_fields"] = flattenDoc(h.Source, view.sourceFields, view.maxValue)
		}
		cut = cut || truncated
		items = append(items, hit)
	}
	// A default search page stays within the success budget (CLI-042): long messages
	// (a production page of 20 reached 9,990 characters in the live run) end the page
	// early, and next_offset continues where it stopped. An explicit --limit or a
	// --dsl size gets what it asked for.
	shortened := false
	if !cmd.Flags().Changed("limit") && flagString(cmd, "dsl") == "" {
		if fitted := fitToBudget(items, scope); len(fitted) < len(items) {
			items, shortened = fitted, true
			app.Notice(aicli.Notice{Type: "page_shortened", Severity: "info",
				Message: fmt.Sprintf("%d of the %d hits fit in one response; next_offset continues, or pass --limit to get them all at once.", len(items), len(res.Hits))})
		}
	}
	page := aicli.Page{Items: items, Count: len(items), Scope: scope, Sort: "timestamp_desc"}
	next := offset + len(items)
	if res.TotalRelation == "gte" {
		page.HasMore = len(items) == limit || shortened
	} else {
		total := int(res.Total)
		page.Total = &total
		page.HasMore = next < total
	}
	if page.HasMore {
		if next+limit > maxResultWindow {
			page.ContinuationReason = fmt.Sprintf("Elasticsearch pages at most %d hits deep; set --until to the oldest timestamp shown and start again at --offset 0.", maxResultWindow)
		} else {
			page.NextOffset = &next
		}
	}
	if cut {
		app.Notice(aicli.Notice{Type: "message_truncated", Severity: "info",
			Message: fmt.Sprintf("Messages longer than %d characters are cut (message_truncated: true); `kibana-cli log get <ref>` shows the whole document.", messageLimit)})
	}
	shardNotice(app, res.Shards, res.TimedOut, scope)
	return page
}

// successBudget is CLI-042's success budget in characters (contract.json
// size_budgets); budgetReserve is left for the envelope, meta and notices.
const (
	successBudget = 8000
	budgetReserve = 1200
)

// fitToBudget keeps the leading items whose compact JSON, with the scope's, fits in
// the success budget; at least one item is kept.
func fitToBudget(items []map[string]any, scope map[string]any) []map[string]any {
	room := successBudget - budgetReserve - jsonChars(scope)
	used := 0
	for i, it := range items {
		used += jsonChars(it) + 1
		if used > room && i > 0 {
			return items[:i]
		}
	}
	return items
}

// jsonChars is the length of v's compact JSON in characters, as the budget counts.
func jsonChars(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return utf8.RuneCount(b)
}

// shardNotice reports a search that failed on some shards or timed out: its hits and
// total may be incomplete. The reasons are Elasticsearch's own text.
func shardNotice(app *aicli.App, sh kibanaclient.Shards, timedOut bool, scope map[string]any) {
	if sh.Failed == 0 && !timedOut {
		return
	}
	msg := fmt.Sprintf("%d of %d shards failed", sh.Failed, sh.Total)
	if timedOut {
		msg = "Elasticsearch's search timed out"
		if sh.Failed > 0 {
			msg += fmt.Sprintf(" and %d of %d shards failed", sh.Failed, sh.Total)
		}
	}
	app.Notice(aicli.Notice{Type: "partial_results", Severity: "warning", Message: msg + ", so the results may be incomplete; scope.shard_failures has the reasons."})
	scope["shards_failed"] = sh.Failed
	if timedOut {
		scope["timed_out"] = true
	}
	if len(sh.FailureReasons) > 0 {
		scope["shard_failures"] = sh.FailureReasons
	}
}

// zeroHits runs 1.x's zero-hit probes (two counts at most) and reports why nothing
// matched: no documents in the window, a query that matches in other fields, or
// filters that excluded everything.
func zeroHits(app *aicli.App, ctx context.Context, client *kibanaclient.Client, opts kibanaclient.SearchOptions, q *logQuery, scope map[string]any) {
	base := kibanaclient.SearchOptions{Index: opts.Index, From: opts.From, To: opts.To, TimeField: opts.TimeField}
	window, err := client.Count(ctx, base)
	if err != nil {
		app.Debugf("zero-hit probe: %v", err)
		return
	}
	probe := map[string]any{"window_total": window}
	reason, hint := "filters_excluded_all",
		fmt.Sprintf("%d documents exist in the time range but none matched; relax --level, --service, --field or the query.", window)
	if window == 0 {
		reason, hint = "no_data_in_window", "The index has no documents in this time range; widen --since or check the index pattern."
	} else if strings.TrimSpace(opts.Query) != "" || len(opts.QueryClause) > 0 {
		broad := base
		broad.Query, broad.QueryClause = opts.Query, opts.QueryClause
		if n, err := client.Count(ctx, broad); err == nil {
			probe["broad_match_total"] = n
			if n > 0 {
				reason = "matched_in_other_fields"
				hint = fmt.Sprintf("%d documents match the query across all fields, but the other filters excluded them.", n)
				if q.Precise {
					hint += " Drop --precise to search every field."
				}
			}
		}
	}
	probe["reason"] = reason
	scope["zero_hits"] = probe
	app.Notice(aicli.Notice{Type: "zero_hits", Severity: "info", Message: hint})
}

// runDSLSearch sends a caller's whole _search body (--dsl). It is still a search on
// the resolved index, which the allowlist checks; the hits come back as log hits.
func runDSLSearch(app *aicli.App, cmd *cobra.Command, raw string) error {
	for _, name := range []string{"query", "query-language", "precise", "service", "level", "trace-id", "trace-mode", "trace-field",
		"field", "since", "until", "offset", "limit"} {
		if cmd.Flags().Changed(name) {
			e := aicli.Usage("conflicting_flags", "--"+name, fmt.Sprintf("--dsl is a whole search body, so it cannot be combined with --%s.", name))
			e.Details["next_action"] = fmt.Sprintf("Put what --%s says into the --dsl body, or drop --dsl.", name)
			return e
		}
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil || body == nil {
		return aicli.Validation("--dsl", "a JSON object: an Elasticsearch _search body", "--dsl is not a JSON object.")
	}
	size := 10
	if v, ok := body["size"]; ok {
		n, isNum := v.(float64)
		if !isNum || n < 0 || n != math.Trunc(n) || n > searchMaxLimit {
			return aicli.Validation("--dsl", fmt.Sprintf("size 0 to %d", searchMaxLimit), fmt.Sprintf("The body's size must be a whole number from 0 to %d.", searchMaxLimit))
		}
		size = int(n)
	}
	q, err := resolveLogQuery(app, cmd, "*")
	if err != nil {
		return err
	}
	client, err := q.clientFor(app)
	if err != nil {
		return err
	}
	res, err := client.SearchRaw(cmd.Context(), q.Index, body)
	if err != nil {
		return readError(err)
	}
	view := newHitView(q.Resolved)
	view.maxMessage, view.maxValue = messageLimit, listValueLimit
	scope := map[string]any{"index": q.Index, "query_language": "dsl", "time_field": q.TimeField}
	if q.DataView != "" {
		scope["data_view"] = q.DataView
	}
	page := hitsPage(app, cmd, res, view, max(size, 1), 0, scope)
	// The body may page itself (from, search_after); only the first page's position
	// is known here, so more hits are reported without a way to continue.
	if page.HasMore {
		page.NextOffset = nil
		page.ContinuationReason = "The --dsl body controls paging: add from (or search_after) to the body to read further."
	}
	page.Sort = "upstream"
	scope["order"] = "the --dsl body's sort, else Elasticsearch's relevance"
	return app.List(cmd, page)
}

// ---------------------------------------------------------------- log get

// logRefPattern is a log hit ref: an index name (no '/') and the document's _id.
var logRefPattern = regexp.MustCompile(`^([^/\s]+)/(.+)$`)

func addLogGet(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "get <hit>", RunE: func(cmd *cobra.Command, _ []string) error {
		raw := flagString(cmd, "hit")
		m := logRefPattern.FindStringSubmatch(raw)
		if raw == "" {
			e := aicli.Usage("missing_parameter", "--hit", "Name the document by its ref, <index>/<_id>, as log search returns it.")
			e.Details["next_action"] = "Run `kibana-cli log search` and pass a hit's ref, like `kibana-cli log get app-log-2026.10.25/AbC123`."
			return e
		}
		if m == nil {
			return aicli.Validation("--hit", "<index>/<_id>", "A log hit's ref is <index>/<_id>, as log search returns it.")
		}
		index, id := m[1], m[2]
		if err := checkIndex(app, index, "--hit"); err != nil {
			return err
		}
		fm, _, err := loadFieldMap(app, cmd)
		if err != nil {
			return err
		}
		// A data stream's hit names its backing index (.ds-<stream>-<date>-<generation>);
		// the field map's patterns name the stream, so match by the stream's name.
		resolved, err := fieldmap.ResolveSearchOptions(fm, flagString(cmd, "field-profile"), strings.TrimPrefix(index, ".ds-"), "", "")
		if err != nil {
			return fieldMapError(err)
		}
		if tf := flagString(cmd, "time-field"); tf != "" {
			resolved.TimeField = tf
		}
		applyFieldDefaults(&resolved)
		client, _, err := newClient(app)
		if err != nil {
			return err
		}
		hit, err := client.GetDoc(cmd.Context(), index, id)
		if err != nil {
			return readError(err)
		}
		if hit == nil {
			return aicli.NotFound("The index has no document with that _id, or this account cannot see it.",
				"Check the ref against `kibana-cli log search`; an index that rolled over or expired no longer holds the document.")
		}
		if aicli.OutputFormat(cmd) == "raw" {
			b, err := json.Marshal(map[string]any{"_index": hit.Index, "_id": hit.ID, "_source": hit.Source})
			if err != nil {
				return err
			}
			return app.OK(cmd, append(b, '\n'))
		}
		view := newHitView(resolved)
		view.maxValue = getValueLimit
		view.withSource = aicli.WantsField(cmd, "source")
		out, _ := view.logHit(*hit)
		if sf, _ := cmd.Flags().GetStringSlice("source-fields"); len(sf) > 0 {
			out["source_fields"] = flattenDoc(hit.Source, fieldmap.UniqueStrings(sf), getValueLimit)
		}
		return app.OK(cmd, out)
	}}
	c.Flags().String("hit", "", "The document's ref, <index>/<_id>, as log search returns it")
	c.Flags().String("field-profile", "", "Profile of the field map that names this index's fields")
	c.Flags().String("time-field", "", "Time field (default: the field map's, else @timestamp)")
	c.Flags().StringSlice("source-fields", nil, "Source fields to list as source_fields as well")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Read one log document by its ref (<index>/<_id>), with its whole message and every source field",
		Effects: []aicli.Effect{aicli.Observe}, Output: "log_hit", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{logEvidence, liveEvidenceSvc, liveEvidenceIoT}, Positional: "hit", RawOutput: true,
		Examples: []string{"kibana-cli log get app-log-2026.10.25/AbC123", "kibana-cli log get app-log-2026.10.25/AbC123 --format raw"}})
}

// ---------------------------------------------------------------- log aggregate

var intervalPattern = regexp.MustCompile(`^([0-9]+)(ms|s|m|h|d)$`)

func addLogAggregate(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "aggregate", RunE: func(cmd *cobra.Command, _ []string) error {
		aggType, terms, interval, metric, metricField, err := aggFlags(cmd)
		if err != nil {
			return err
		}
		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 1 || limit > bucketsMaxLimit {
			return aicli.Validation("--limit", fmt.Sprintf("1 to %d", bucketsMaxLimit), fmt.Sprintf("--limit must be between 1 and %d buckets.", bucketsMaxLimit))
		}
		q, err := resolveLogQuery(app, cmd, "")
		if err != nil {
			return err
		}
		if aggType == kibanaclient.AggTypeDateHistogram {
			step, _ := aicli.ParseDuration(interval)
			if n := q.Until.Sub(q.Since) / step; n > maxHistogramBuckets {
				e := aicli.Validation("--interval", fmt.Sprintf("at most %d buckets over the time range", maxHistogramBuckets),
					fmt.Sprintf("The time range holds more than %d buckets of --interval; use a larger interval or a shorter range.", maxHistogramBuckets))
				e.Details["next_action"] = fmt.Sprintf("Use --interval %s or larger.", suggestInterval(q.Until.Sub(q.Since)))
				return e
			}
		}
		client, err := q.clientFor(app)
		if err != nil {
			return err
		}
		field := terms
		if aggType == kibanaclient.AggTypeTerms {
			if field, err = termsField(cmd.Context(), client, q, terms); err != nil {
				return err
			}
		}
		opts := aggOptions(q, aggType, field, interval, metric, metricField, limit)
		res, err := client.Aggregate(cmd.Context(), opts)
		if err != nil {
			return readError(err)
		}
		items := make([]map[string]any, 0, len(res.Buckets))
		withMetric := metric != "" && metric != "count"
		for _, b := range res.Buckets {
			item := map[string]any{"key": b.Key, "count": b.Count}
			if withMetric {
				item["metric"] = nil
				if b.Metric != nil && !math.IsNaN(*b.Metric) && !math.IsInf(*b.Metric, 0) {
					item["metric"] = *b.Metric
				}
			}
			items = append(items, item)
		}
		scope := q.scope()
		scope["agg_type"], scope["matched_documents"] = aggType, res.Total
		if withMetric {
			scope["metric"], scope["metric_field"] = metric, metricField
		}
		page := aicli.Page{Items: items, Count: len(items), Scope: scope}
		if aggType == kibanaclient.AggTypeDateHistogram {
			scope["interval"] = interval
			page.Sort = "key_asc"
			n := len(items)
			page.Total = &n
		} else {
			scope["field"], scope["limit"] = res.Field, limit
			scope["other_documents"] = res.OtherDocs
			page.Sort = "count_desc"
			if res.OtherDocs > 0 {
				page.HasMore = true
				page.ContinuationReason = fmt.Sprintf("More values exist (scope.other_documents documents are in buckets past these); raise --limit (at most %d) or narrow the filters.", bucketsMaxLimit)
			} else {
				n := len(items)
				page.Total = &n
			}
		}
		shardNotice(app, res.Shards, res.TimedOut, scope)
		explain(cmd, scope, opts.Index, kibanaclient.BuildAggBody(opts))
		return app.List(cmd, page)
	}}
	queryFlags(c, "1h")
	c.Flags().Bool("explain", false, "Add the first request sent to scope.request")
	c.Flags().String("terms", "", "Field to count by; service and level use the field map's fields")
	c.Flags().String("agg-type", kibanaclient.AggTypeTerms, "terms (per value) or date_histogram (per time bucket)")
	c.Flags().String("interval", "1h", "Date histogram bucket: 30s, 5m, 1h, 1d")
	c.Flags().String("metric", "", "avg, sum, min or max of --metric-field per bucket")
	c.Flags().String("metric-field", "", "Numeric field of --metric")
	c.Flags().Int("limit", 10, fmt.Sprintf("Terms buckets, 1 to %d", bucketsMaxLimit))
	parent.AddCommand(c)
	params := withParams(queryParams(), map[string]aicli.ParamDoc{
		"agg-type": {Type: "enum", Enum: []string{kibanaclient.AggTypeTerms, kibanaclient.AggTypeDateHistogram}},
		"metric":   {Type: "enum", Enum: []string{"avg", "sum", "min", "max", "count"}},
		"limit":    {Unit: "buckets"},
	})
	app.Register(c, aicli.Meta{
		Summary: "Count log documents per value of a field (top services, error counts per level) or per time bucket, with an optional metric",
		Effects: []aicli.Effect{aicli.Observe}, Output: "log_bucket_page", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{logEvidence, liveEvidenceSvc, liveEvidenceIoT},
		Params: params, RenamedFlags: queryRenamedFlags(map[string]string{"buckets": "limit"}),
		Examples: []string{
			"kibana-cli log aggregate --index 'app-log-*' --terms service --level ERROR --since 1h",
			"kibana-cli log aggregate --index 'app-log-*' --agg-type date_histogram --interval 5m --level ERROR --since 6h",
			"kibana-cli log aggregate --index 'app-log-*' --terms service --metric avg --metric-field duration_ms --since 1h",
		}})
}

// aggFlags reads and checks the aggregation flags.
func aggFlags(cmd *cobra.Command) (aggType, terms, interval, metric, metricField string, err error) {
	switch strings.ToLower(flagString(cmd, "agg-type")) {
	case "", kibanaclient.AggTypeTerms:
		aggType = kibanaclient.AggTypeTerms
	case kibanaclient.AggTypeDateHistogram:
		aggType = kibanaclient.AggTypeDateHistogram
	default:
		return "", "", "", "", "", aicli.Validation("--agg-type", "terms or date_histogram", "--agg-type must be terms or date_histogram.")
	}
	terms, interval = flagString(cmd, "terms"), flagString(cmd, "interval")
	if aggType == kibanaclient.AggTypeTerms && terms == "" {
		e := aicli.Usage("missing_parameter", "--terms", "A terms aggregation needs --terms: the field to count by.")
		e.Details["next_action"] = "Add --terms service, --terms level or --terms <field> (`kibana-cli pattern fields` lists the aggregatable fields)."
		return "", "", "", "", "", e
	}
	if aggType == kibanaclient.AggTypeDateHistogram {
		if m := intervalPattern.FindStringSubmatch(interval); m == nil || strings.TrimLeft(m[1], "0") == "" {
			return "", "", "", "", "", aicli.Validation("--interval", "a whole number above 0 and ms, s, m, h or d, like 5m", "--interval must be like 30s, 5m, 1h or 1d.")
		}
	}
	metric, metricField = strings.ToLower(flagString(cmd, "metric")), flagString(cmd, "metric-field")
	switch metric {
	case "", "count":
	case "avg", "sum", "min", "max":
		if metricField == "" {
			e := aicli.Usage("missing_parameter", "--metric-field", fmt.Sprintf("--metric %s needs --metric-field, the numeric field to compute it over.", metric))
			e.Details["next_action"] = "Add --metric-field <numeric field>."
			return "", "", "", "", "", e
		}
	default:
		return "", "", "", "", "", aicli.Validation("--metric", "avg, sum, min, max or count", "--metric must be avg, sum, min, max or count.")
	}
	return aggType, terms, interval, metric, metricField, nil
}

// termsField maps --terms to the field to count by: the logical names service and
// level are the field map's first field for them (as 1.x did); without one, the
// first of kibana-cli's default names the index has (one fields read). Anything
// else is a field name.
func termsField(ctx context.Context, client *kibanaclient.Client, q *logQuery, terms string) (string, error) {
	var fields []string
	switch terms {
	case "service":
		fields = q.Resolved.ServiceFields
	case "level":
		fields = q.Resolved.LevelFields
	default:
		return terms, nil
	}
	if !q.defaulted[terms] {
		return fields[0], nil
	}
	have, err := client.ListFields(ctx, q.Index)
	if err != nil {
		return "", readError(err, onNotFound("No index matches that pattern.", "Use an index pattern from `kibana-cli pattern list`."))
	}
	names := map[string]bool{}
	for _, f := range have {
		names[f.Name] = true
	}
	for _, f := range fields {
		if names[f] {
			return f, nil
		}
	}
	e := aicli.Validation("--terms", "a field of the index", fmt.Sprintf("The index has none of the usual %s fields (%s), and the field map names none.", terms, strings.Join(fields, ", ")))
	e.Details["next_action"] = fmt.Sprintf("Pass the field itself, like --terms <field> (`kibana-cli pattern fields --index %s --aggregatable` lists them), or add a field-map profile.", q.Index)
	return "", e
}

func aggOptions(q *logQuery, aggType, field, interval, metric, metricField string, limit int) kibanaclient.AggOptions {
	so := q.searchOptions()
	opts := kibanaclient.AggOptions{Index: so.Index, From: so.From, To: so.To, TimeField: so.TimeField, TermsField: field, BucketSize: limit,
		ServiceValue: so.ServiceValue, ServiceFields: so.ServiceFields, LevelValue: so.LevelValue, LevelFields: so.LevelFields,
		TraceID: so.TraceID, TraceFields: so.TraceFields, TraceMode: so.TraceMode, Fields: so.Fields, Query: so.Query,
		QueryClause: so.QueryClause, MsgOnly: so.MsgOnly, MessageField: so.MessageField, MessageFields: so.MessageFields,
		AggType: aggType, Interval: interval, Metric: metric, MetricField: metricField}
	if aggType == kibanaclient.AggTypeDateHistogram {
		lo, hi := q.Since.UnixMilli(), q.Until.UnixMilli()
		opts.BoundsMin, opts.BoundsMax = &lo, &hi
	}
	return opts
}

// suggestInterval is the smallest round interval that fits the range in the bucket
// limit.
func suggestInterval(span time.Duration) string {
	for _, s := range []string{"1m", "5m", "10m", "30m", "1h", "3h", "6h", "12h", "1d", "7d"} {
		d, _ := aicli.ParseDuration(s)
		if span/d <= maxHistogramBuckets {
			return s
		}
	}
	return "30d"
}

// explain adds the Elasticsearch request to the scope when --explain asks for it
// (1.x showed it with --dry-run; a read now runs, and changes nothing). The body is
// JSON text: its keys are Elasticsearch's (@timestamp, field names), not snake_case.
func explain(cmd *cobra.Command, scope map[string]any, index string, body map[string]any) {
	if on, _ := cmd.Flags().GetBool("explain"); !on {
		return
	}
	b, err := json.Marshal(body)
	if err != nil {
		return
	}
	scope["request"] = map[string]any{"method": "POST", "path": index + "/_search", "body": string(b)}
}

// withParams merges reference metadata maps.
func withParams(base, extra map[string]aicli.ParamDoc) map[string]aicli.ParamDoc {
	for k, v := range extra {
		base[k] = v
	}
	return base
}
