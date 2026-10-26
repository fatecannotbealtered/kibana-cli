package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/kql"
)

// The log commands share one way to say what to search: an index (or a data view, or
// a field-map profile, or the default_index setting), a time range, a query in Lucene
// or KQL, and the field-map's logical filters (service, level, trace id). Nothing in
// this file sends anything; resolveLogQuery reads Kibana only to resolve --data-view.

const (
	queryLanguageLucene = "lucene"
	queryLanguageKQL    = "kql"
	// maxResultWindow is Elasticsearch's default index.max_result_window: from + size
	// of a search must stay within it.
	maxResultWindow = 10000
)

// queryFlags adds the flags every log command takes to say what to search.
func queryFlags(c *cobra.Command, defaultSince string) {
	f := c.Flags()
	f.String("index", "", "Index pattern to search, like 'app-log-*' (default: the field profile's, else the default_index setting)")
	f.String("data-view", "", "Data view ref (index-pattern/<id>) or id, instead of --index")
	f.String("field-profile", "", "Field-map profile that names this index's fields (1.x --profile)")
	f.String("service", "", "Service name, matched across the service fields")
	f.String("level", "", "Log level, like ERROR, matched across the level fields")
	f.String("trace-id", "", "Trace id, in the trace fields or, with trace mode msg, the message")
	f.String("trace-mode", "", "Where the trace id is: field or msg")
	f.StringSlice("trace-field", nil, "Trace id fields, overriding the field map")
	f.String("query", "", "Query text in --query-language")
	f.String("query-language", queryLanguageLucene, "lucene or kql (a strict subset of Kibana's)")
	f.Bool("precise", false, "Match the Lucene --query as one phrase in the message fields")
	f.String("since", defaultSince, "Range start: 15m, 2h, 7d, an ISO time or date, or now-15m")
	f.String("until", "now", "Range end: now, a duration ago, an ISO time or date")
	f.String("time-field", "", "Time field (default: the data view's or field map's, else @timestamp)")
	f.StringArray("field", nil, "Exact filter field=value (repeatable)")
	c.MarkFlagsMutuallyExclusive("index", "data-view")
}

// queryRenamedFlags are the 1.x names of the query flags (CLI-028, Meta.RenamedFlags).
func queryRenamedFlags(extra map[string]string) map[string]string {
	out := map[string]string{"from": "since", "to": "until"}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// queryParams documents the enum flags of the query (Meta.Params).
func queryParams() map[string]aicli.ParamDoc {
	return map[string]aicli.ParamDoc{
		"query-language": {Type: "enum", Enum: []string{queryLanguageLucene, queryLanguageKQL}},
		"trace-mode":     {Type: "enum", Enum: []string{fieldmap.TraceModeField, fieldmap.TraceModeMsg}},
	}
}

// logQuery is a resolved log query: what to search, over which time range, with which
// filters, and the field map's names for the hits.
type logQuery struct {
	Index, DataView, TimeField string
	Since, Until               time.Time
	Resolved                   fieldmap.ResolvedSearch
	Service, Level, TraceID    string
	Query, Language            string
	Clause                     map[string]any
	Precise                    bool
	Fields                     map[string]string
	fieldOrder                 []string
	// defaulted names the logical fields (service, level, message, trace) the field
	// map left empty, filled with kibana-cli's defaults.
	defaulted map[string]bool
	// client is the client --data-view was resolved with; nil until one is needed.
	client *kibanaclient.Client
}

// resolveLogQuery reads the query flags, the field map and the settings. It reads
// Kibana only for --data-view (the data view's index pattern and time field).
// fallbackIndex, when not empty, is searched when nothing names an index (--dsl
// searches * as 1.x did; the allowlist still applies).
func resolveLogQuery(app *aicli.App, cmd *cobra.Command, fallbackIndex string) (*logQuery, error) {
	q := &logQuery{}
	var err error
	if q.Language, q.Clause, err = compileQuery(cmd); err != nil {
		return nil, err
	}
	q.Query, _ = cmd.Flags().GetString("query")
	q.Precise, _ = cmd.Flags().GetBool("precise")
	if q.Since, q.Until, err = timeRange(app, cmd); err != nil {
		return nil, err
	}
	if q.Fields, q.fieldOrder, err = fieldFilters(cmd); err != nil {
		return nil, err
	}
	fm, _, err := loadFieldMap(app, cmd)
	if err != nil {
		return nil, err
	}
	profile := flagString(cmd, "field-profile")
	q.Service, q.Level, q.TraceID = flagString(cmd, "service"), flagString(cmd, "level"), flagString(cmd, "trace-id")

	index := flagString(cmd, "index")
	var viewTime string
	if raw := flagString(cmd, "data-view"); raw != "" {
		id := dataViewID(raw)
		if id == "" {
			return nil, aicli.Validation("--data-view", "a data view ref (index-pattern/<id>) or id", "--data-view names no data view.")
		}
		client, _, err := newClient(app)
		if err != nil {
			return nil, err
		}
		view, err := client.ResolveDataView(cmd.Context(), id)
		if errors.Is(err, kibanaclient.ErrNoDataView) {
			return nil, aicli.NotFound("Kibana has no data view with that id, or this account cannot see it.",
				"Run `kibana-cli pattern list` to find the data view's ref.")
		}
		if err != nil {
			return nil, readError(err)
		}
		q.client, q.DataView, index, viewTime = client, "index-pattern/"+view.ID, view.Title, strings.TrimSpace(view.TimeFieldName)
	}
	if index == "" && profile == "" {
		def, _, e := app.Setting(nil, "default_index")
		if e != nil {
			return nil, e
		}
		index = strings.TrimSpace(def)
	}
	resolved, err := fieldmap.ResolveSearchOptions(fm, profile, index, q.Service, q.Level)
	if errors.Is(err, fieldmap.ErrNoIndex) && fallbackIndex != "" {
		resolved, err = fieldmap.ResolveSearchOptions(fm, profile, fallbackIndex, q.Service, q.Level)
	}
	if err != nil {
		return nil, fieldMapError(err)
	}
	if err := checkIndex(app, resolved.Index, "--index"); err != nil {
		return nil, err
	}
	switch {
	case flagString(cmd, "time-field") != "":
		resolved.TimeField = flagString(cmd, "time-field")
	case q.DataView != "" && viewTime == "":
		e := aicli.Usage("missing_parameter", "--time-field", "The data view has no time field, so the time range needs --time-field.")
		e.Details["next_action"] = "Add --time-field <field> (`kibana-cli pattern fields --data-view <ref>` lists the date fields)."
		return nil, e
	case viewTime != "":
		resolved.TimeField = viewTime
	}
	if tm := flagString(cmd, "trace-mode"); tm != "" {
		resolved.TraceMode = fieldmap.NormalizeTraceMode(tm)
	}
	if tf, _ := cmd.Flags().GetStringSlice("trace-field"); len(tf) > 0 {
		resolved.TraceIDFields = fieldmap.UniqueStrings(tf)
	}
	q.defaulted = applyFieldDefaults(&resolved)
	q.Index, q.TimeField, q.Resolved = resolved.Index, resolved.TimeField, resolved
	return q, nil
}

// The field names kibana-cli tries when the field map names none for a logical
// field (no field map, or a profile without them): the common ECS, Logstash and
// 1.x example names. Filters match across all of them (lenient, so a missing field
// matches nothing); output takes the first one a document has.
var (
	defaultServiceFields = []string{"service.name", "service_name", "service", "log_app", "app_name", "application", "kubernetes.labels.app"}
	defaultLevelFields   = []string{"log.level", "level", "severity", "log_level"}
	defaultMessageFields = []string{"message", "msg", "log.message"}
	defaultTraceFields   = []string{"trace.id", "traceId", "trace_id"}
)

// applyFieldDefaults fills the logical field lists the field map left empty and
// reports which it filled.
func applyFieldDefaults(r *fieldmap.ResolvedSearch) map[string]bool {
	filled := map[string]bool{}
	for name, pair := range map[string]struct {
		dst *[]string
		def []string
	}{"service": {&r.ServiceFields, defaultServiceFields}, "level": {&r.LevelFields, defaultLevelFields},
		"message": {&r.MessageFields, defaultMessageFields}, "trace": {&r.TraceIDFields, defaultTraceFields}} {
		if len(*pair.dst) == 0 {
			*pair.dst = append([]string{}, pair.def...)
			filled[name] = true
		}
	}
	return filled
}

// clientFor returns the client of the query, building it on first use.
func (q *logQuery) clientFor(app *aicli.App) (*kibanaclient.Client, error) {
	if q.client != nil {
		return q.client, nil
	}
	c, _, err := newClient(app)
	if err != nil {
		return nil, err
	}
	q.client = c
	return c, nil
}

// searchOptions is the query as kibanaclient searches it.
func (q *logQuery) searchOptions() kibanaclient.SearchOptions {
	r := q.Resolved
	return kibanaclient.SearchOptions{Index: q.Index, Query: q.Query, QueryClause: q.Clause, MsgOnly: q.Precise, Fields: q.Fields,
		From: isoTime(q.Since), To: isoTime(q.Until), TimeField: q.TimeField, SortDesc: true,
		ServiceValue: q.Service, ServiceFields: r.ServiceFields, LevelValue: q.Level, LevelFields: r.LevelFields,
		TraceID: q.TraceID, TraceFields: r.TraceIDFields, TraceMode: r.TraceMode,
		MessageField: r.PrimaryMessageField(), MessageFields: r.MessageFields}
}

// scope is what a list of this query covers (CLI-041): the index, the time range in
// UTC, every filter applied and the query language.
func (q *logQuery) scope() map[string]any {
	s := map[string]any{"index": q.Index, "time_field": q.TimeField, "since": isoTime(q.Since), "until": isoTime(q.Until)}
	if q.DataView != "" {
		s["data_view"] = q.DataView
	}
	if p := q.Resolved.Profile; p != "" {
		s["field_profile"] = p
	}
	for k, v := range map[string]string{"service": q.Service, "level": q.Level, "trace_id": q.TraceID} {
		if v != "" {
			s[k] = v
		}
	}
	if q.TraceID != "" {
		s["trace_mode"] = q.Resolved.TraceMode
	}
	if strings.TrimSpace(q.Query) != "" {
		s["query"] = q.Query
		s["query_language"] = q.Language
		if q.Precise {
			s["precise"] = true
		}
	}
	if len(q.fieldOrder) > 0 {
		filters := make([]map[string]any, 0, len(q.fieldOrder))
		for _, k := range q.fieldOrder {
			filters = append(filters, map[string]any{"field": k, "value": q.Fields[k]})
		}
		s["filters"] = filters
	}
	return s
}

// compileQuery checks --query against --query-language: KQL is compiled here (and
// never retried as Lucene); Lucene with KQL-style lowercase and/or/not is refused
// unless --query-language lucene says so, because Lucene would read them as terms.
func compileQuery(cmd *cobra.Command) (string, map[string]any, error) {
	language := strings.ToLower(flagString(cmd, "query-language"))
	if language == "" {
		language = queryLanguageLucene
	}
	if language != queryLanguageLucene && language != queryLanguageKQL {
		return "", nil, aicli.Validation("--query-language", "lucene or kql", "--query-language must be lucene or kql.")
	}
	query, _ := cmd.Flags().GetString("query")
	precise, _ := cmd.Flags().GetBool("precise")
	if language == queryLanguageKQL {
		if precise {
			e := aicli.Usage("conflicting_flags", "--precise", "--precise works with Lucene only; express the phrase in KQL instead.")
			e.Details["next_action"] = "Drop --precise and quote the phrase in the KQL query, like message:\"connection reset\"."
			return "", nil, e
		}
		if strings.TrimSpace(query) == "" {
			return language, nil, nil
		}
		clause, err := kql.Parse(query)
		if err != nil {
			e := aicli.Validation("--query", "a KQL query this tool can compile", "The KQL query cannot be compiled; details.reason says why.")
			e.Details["reason"] = err.Error()
			e.Details["next_action"] = "Fix the KQL syntax, or use --query-language lucene."
			e.Untrusted = []string{"reason"} // it quotes parts of the query
			return "", nil, e
		}
		return language, clause, nil
	}
	if !precise && ambiguousLuceneBoolean(query) && !cmd.Flags().Changed("query-language") {
		e := aicli.Validation("--query", "uppercase AND/OR/NOT in Lucene, or --query-language kql",
			"The query has lowercase and/or/not, which Lucene reads as words, not operators.")
		e.Details["next_action"] = "Add --query-language kql, or write AND/OR/NOT in uppercase; add --query-language lucene if the words are meant literally."
		return "", nil, e
	}
	return language, nil, nil
}

// ambiguousLuceneBoolean reports an unquoted lowercase and/or/not used as a word.
func ambiguousLuceneBoolean(query string) bool {
	visible := outsideQuotedText(query)
	for i := 0; i < len(visible); i++ {
		if i > 0 && !booleanBoundary(visible[i-1]) {
			continue
		}
		for _, keyword := range []string{"and", "or", "not"} {
			end := i + len(keyword)
			if end > len(visible) || !strings.EqualFold(visible[i:end], keyword) {
				continue
			}
			if end < len(visible) && !booleanBoundary(visible[end]) {
				continue
			}
			if visible[i:end] != strings.ToUpper(visible[i:end]) {
				return true
			}
		}
	}
	return false
}

func booleanBoundary(b byte) bool {
	return b == '(' || b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// outsideQuotedText blanks quoted and escaped text, keeping positions.
func outsideQuotedText(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	inQuote, escaped := false, false
	for _, r := range value {
		switch {
		case escaped:
			out.WriteByte(' ')
			escaped = false
		case r == '\\':
			out.WriteByte(' ')
			escaped = true
		case r == '"':
			out.WriteByte(' ')
			inQuote = !inQuote
		case inQuote:
			out.WriteByte(' ')
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// timeRange reads --since and --until (CLI-028) as UTC times; since must be before
// until.
func timeRange(app *aicli.App, cmd *cobra.Command) (time.Time, time.Time, error) {
	now := timeNow(app)
	since, err := parseTimeFlag(flagString(cmd, "since"), now)
	if err != nil {
		return time.Time{}, time.Time{}, aicli.Validation("--since", "15m, 2h, 7d, now-15m, 2026-10-25 or 2026-10-25T08:00:00Z", "--since is not a time this tool reads.")
	}
	until, err := parseTimeFlag(flagString(cmd, "until"), now)
	if err != nil {
		return time.Time{}, time.Time{}, aicli.Validation("--until", "now, 5m, now-5m, 2026-10-25 or 2026-10-25T08:00:00Z", "--until is not a time this tool reads.")
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, aicli.Validation("--since", "a time before --until", "--since must be before --until.")
	}
	return since, until, nil
}

// dateMath is the part of Elasticsearch date math 1.x documented: now, or now minus a
// whole number of seconds, minutes, hours, days or weeks.
var dateMath = regexp.MustCompile(`^now(?:-([0-9]+)([smhdw]))?$`)

// parseTimeFlag reads a time flag: aicli.ParseTime's forms (a duration ago, an ISO
// date or time) or 1.x's now / now-15m.
func parseTimeFlag(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if m := dateMath.FindStringSubmatch(raw); m != nil {
		if m[1] == "" {
			return now, nil
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, err
		}
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return now.Add(-time.Duration(n) * unit), nil
	}
	return aicli.ParseTime(raw, now)
}

// isoTime is a time as the scope and the query state it: UTC with Z.
func isoTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fieldFilters reads --field field=value pairs, in the order given.
func fieldFilters(cmd *cobra.Command) (map[string]string, []string, error) {
	pairs, _ := cmd.Flags().GetStringArray("field")
	out := map[string]string{}
	var order []string
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, nil, aicli.Validation("--field", "field=value", "Each --field is field=value, like --field http.status=500.")
		}
		if _, seen := out[k]; !seen {
			order = append(order, k)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, order, nil
}

// checkIndex refuses an index list that is not a plain index pattern or that falls
// outside the allowed_index_prefixes setting (config.ValidateIndexTarget).
func checkIndex(app *aicli.App, index, param string) error {
	raw, _, e := app.Setting(nil, "allowed_index_prefixes")
	if e != nil {
		return e
	}
	prefixes := config.ParseIndexPrefixes(raw)
	if err := config.ValidateIndexTarget(index, prefixes); err != nil {
		if len(prefixes) > 0 && strings.Contains(err.Error(), "allowed_index_prefixes") {
			// details.required names what would be allowed (CLI-014): a policy refusal
			// before anything is sent, with no scope involved.
			return aicli.Forbidden("The index is outside the allowed_index_prefixes setting, so it is not searched.", map[string]any{
				"parameter": param, "allowed_index_prefixes": prefixes,
				"required":    "an index pattern that starts with one of " + strings.Join(prefixes, ", "),
				"next_action": "Search an index that starts with one of details.allowed_index_prefixes, or ask the user to change the setting."})
		}
		e := aicli.Validation(param, "an index or index pattern of letters, digits and . _ - + * :, comma-separated",
			fmt.Sprintf("%s is not an index this tool searches: %s.", param, err.Error()))
		e.Details["next_action"] = "Use an index pattern like app-log-* (`kibana-cli pattern list` lists the data views)."
		return e
	}
	return nil
}

// fieldMapError maps a field-map resolution error to a contract error.
func fieldMapError(err error) error {
	var unknown *fieldmap.UnknownNameError
	switch {
	case errors.As(err, &unknown):
		param := "--field-profile"
		if unknown.Kind == "service" {
			param = "--service"
		}
		e := aicli.Validation(param, "a name the field map defines", fmt.Sprintf("The field map has no %s of that name; details.known lists the ones it has.", unknown.Kind))
		e.Details["known"] = nonNilStrings(unknown.Known)
		e.Details["next_action"] = "Use a name from details.known (`kibana-cli field-map get` shows the file)."
		return e
	case errors.Is(err, fieldmap.ErrNoIndex):
		e := aicli.Usage("missing_parameter", "--index", "No index to search: give --index, --data-view or --field-profile, or set the default_index setting.")
		e.Details["next_action"] = "Add --index <pattern>; `kibana-cli pattern list` lists the data views and their index patterns."
		return e
	}
	return err
}

// dataViewID is the id of a --data-view value: the ref index-pattern/<id> (or
// data-view/<id>) or the bare id.
func dataViewID(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, prefix := range []string{"index-pattern/", "data-view/", "data_view/"} {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(raw, prefix))
		}
	}
	return raw
}

func flagString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(v)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
