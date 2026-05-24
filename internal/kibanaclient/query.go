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
	SortDesc      bool
	ServiceValue  string
	ServiceFields []string
	LevelValue    string
	LevelFields   []string
	TraceID       string
	TraceFields   []string
	TraceMode     string // field | msg (from field-map profile)
	MessageField  string // primary message field for trace_mode msg
	// MsgOnly restricts --query to match_phrase on msg (no other fields).
	MsgOnly bool
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
			must = append(must, map[string]any{
				"match_phrase": map[string]any{msgField: q},
			})
		} else {
			must = append(must, map[string]any{
				"query_string": map[string]any{
					"query":            q,
					"default_field":    "*",
					"lenient":          true,
					"analyze_wildcard": true,
				},
			})
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
		return fieldmap.BuildValueORQuery(traceFields, traceID)
	}
}
