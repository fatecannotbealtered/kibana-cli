package kibanaclient

import (
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
)

// SearchOptions configures a log search.
type SearchOptions struct {
	Index         string
	Query         string
	Fields        map[string]string
	From          string
	To            string
	TimeField     string
	Size          int
	Offset        int
	SortDesc      bool
	ServiceValue  string
	ServiceFields []string
	LevelValue    string
	LevelFields   []string
	TraceID       string
	TraceFields   []string
	TraceMode     string   // field | msg (from field-map profile)
	MessageField  string   // primary message field (trace_mode msg / fallback)
	MessageFields []string // all configured message fields (precise multi-field search)
	// MsgOnly (precise) restricts --query to match_phrase across message fields only.
	MsgOnly bool
	// SearchAfter is the ES search_after cursor (sort values of the last seen hit).
	// When set, it replaces offset-based paging: the query adds an _id tiebreaker
	// sort and omits `from`. Mutually exclusive with Offset at the caller level.
	SearchAfter []any
}

func buildQuery(opts SearchOptions) map[string]any {
	var filter []map[string]any
	var must []map[string]any
	if opts.From != "" || opts.To != "" {
		rangeQ := map[string]any{}
		if opts.From != "" {
			rangeQ["gte"] = opts.From
		}
		if opts.To != "" {
			rangeQ["lte"] = opts.To
		}
		filter = append(filter, map[string]any{
			"range": map[string]any{opts.TimeField: rangeQ},
		})
	}
	for k, v := range opts.Fields {
		clauses := termFilterClauses(k, v)
		if len(clauses) == 1 {
			filter = append(filter, clauses[0])
			continue
		}
		filter = append(filter, map[string]any{
			"bool": map[string]any{
				"should":               clauses,
				"minimum_should_match": 1,
			},
		})
	}
	if q := fieldmap.BuildValueORQuery(opts.ServiceFields, opts.ServiceValue); q != nil {
		must = append(must, q)
	}
	if q := fieldmap.BuildValueORQuery(opts.LevelFields, opts.LevelValue); q != nil {
		must = append(must, q)
	}
	msgField := strings.TrimSpace(opts.MessageField)
	if msgField == "" {
		msgField = "msg"
	}
	if q := buildTraceQuery(opts.TraceID, opts.TraceFields, opts.TraceMode, msgField); q != nil {
		must = append(must, q)
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		if opts.MsgOnly {
			must = append(must, buildMessagePhraseQuery(opts.MessageFields, msgField, q))
		} else {
			must = append(must, broadQueryString(q))
		}
	}
	boolQ := map[string]any{}
	if len(must) > 0 {
		boolQ["must"] = must
	}
	if len(filter) > 0 {
		boolQ["filter"] = filter
	}
	if len(boolQ) == 0 {
		return map[string]any{"match_all": map[string]any{}}
	}
	return map[string]any{"bool": boolQ}
}

// BuildQuery exports the Elasticsearch query body for dry-run previews.
func BuildQuery(opts SearchOptions) map[string]any {
	return buildQuery(opts)
}

func termFilterClauses(key, value string) []map[string]any {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	clauses := []map[string]any{{"term": map[string]any{key: value}}}
	if !strings.HasSuffix(key, ".keyword") {
		clauses = append(clauses, map[string]any{"term": map[string]any{key + ".keyword": value}})
	}
	return clauses
}

// buildTraceQuery matches trace ID per profile: field (ELK traceId) or msg (MDC prefix in message).
// In field mode it also falls back to a free-text match of the id across all fields, so a
// present-but-differently-named trace field never silently returns zero hits.
func buildTraceQuery(traceID string, traceFields []string, traceMode, msgField string) map[string]any {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return nil
	}
	switch fieldmap.NormalizeTraceMode(traceMode) {
	case fieldmap.TraceModeMsg:
		return map[string]any{
			"match_phrase": map[string]any{msgField: traceID},
		}
	default:
		fieldQ := fieldmap.BuildValueORQuery(traceFields, traceID)
		free := freeTextQuery(traceID)
		if fieldQ == nil {
			return free
		}
		return map[string]any{
			"bool": map[string]any{
				"should":               []map[string]any{fieldQ, free},
				"minimum_should_match": 1,
			},
		}
	}
}

// broadQueryString searches a keyword/Lucene query across all fields (recall-first default).
func broadQueryString(q string) map[string]any {
	return map[string]any{
		"query_string": map[string]any{
			"query":            q,
			"default_field":    "*",
			"lenient":          true,
			"analyze_wildcard": true,
		},
	}
}

// freeTextQuery matches a quoted value across all fields (used as trace-id fallback).
func freeTextQuery(value string) map[string]any {
	v := strings.ReplaceAll(value, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return map[string]any{
		"query_string": map[string]any{
			"query":         `"` + v + `"`,
			"default_field": "*",
			"lenient":       true,
		},
	}
}

// buildMessagePhraseQuery (precise mode) does match_phrase across all configured message fields.
func buildMessagePhraseQuery(fields []string, fallback, q string) map[string]any {
	fs := fieldmap.UniqueStrings(fields)
	if len(fs) == 0 {
		fs = []string{strings.TrimSpace(fallback)}
	}
	if len(fs) == 1 {
		return map[string]any{"match_phrase": map[string]any{fs[0]: q}}
	}
	should := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		should = append(should, map[string]any{"match_phrase": map[string]any{f: q}})
	}
	return map[string]any{
		"bool": map[string]any{
			"should":               should,
			"minimum_should_match": 1,
		},
	}
}
