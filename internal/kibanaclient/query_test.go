package kibanaclient

import (
	"reflect"
	"testing"
)

func TestBuildTraceQuery_msgMode(t *testing.T) {
	q := buildTraceQuery("aabbccdd00112233445566778899aabb", nil, "msg", "msg", nil)
	if q == nil {
		t.Fatal("expected query")
	}
	mp, ok := q["match_phrase"].(map[string]any)
	if !ok {
		t.Fatalf("expected match_phrase on msg only, got %#v", q)
	}
	if mp["msg"] != "aabbccdd00112233445566778899aabb" {
		t.Fatalf("msg phrase: %#v", mp)
	}
}

// TestBuildTraceQuery_msgModeEveryMessageField: without a field map the message
// fields are message, msg and log.message, and an IoT index writes the trace id into
// msg (live run on 9.5.2: looking only in message found nothing).
func TestBuildTraceQuery_msgModeEveryMessageField(t *testing.T) {
	q := buildTraceQuery("trace1000", nil, "msg", "message", []string{"message", "msg", "log.message"})
	should, _ := q["bool"].(map[string]any)["should"].([]map[string]any)
	if len(should) != 3 || should[1]["match_phrase"].(map[string]any)["msg"] != "trace1000" {
		t.Fatalf("%#v", q)
	}
}

func TestBuildTraceQuery_fieldMode(t *testing.T) {
	q := buildTraceQuery("abc", []string{"traceId"}, "field", "msg", nil)
	if q == nil {
		t.Fatal("expected query")
	}
	if _, ok := q["match_phrase"]; ok {
		t.Fatal("field mode must not search msg")
	}
}

func TestBuildQuery_msgOnlyUsesMessageField(t *testing.T) {
	q := buildQuery(SearchOptions{
		Query:        "timeout",
		MsgOnly:      true,
		MessageField: "message",
		TimeField:    "@timestamp",
		From:         "now-1d",
	})
	boolQ := q["bool"].(map[string]any)
	must := boolQ["must"].([]map[string]any)
	mp := must[0]["match_phrase"].(map[string]any)
	if mp["message"] != "timeout" {
		t.Fatalf("expected message field, got %#v", mp)
	}
}

func TestBuildQuery_exportAndBranches(t *testing.T) {
	if buildQuery(SearchOptions{})["match_all"] == nil {
		t.Fatal("match_all")
	}
	q := buildQuery(SearchOptions{
		To:            "now",
		TimeField:     "@ts",
		Fields:        map[string]string{"host.keyword": "x", "svc": "api"},
		ServiceValue:  "pay",
		ServiceFields: []string{"service"},
		LevelValue:    "ERROR",
		LevelFields:   []string{"level"},
		Query:         "err",
		TraceID:       "tid",
		TraceFields:   []string{"traceId"},
	})
	boolQ := q["bool"].(map[string]any)
	if len(boolQ["must"].([]map[string]any)) < 4 {
		t.Fatalf("must: %#v", boolQ["must"])
	}
}

func TestTermFilterClauses(t *testing.T) {
	if termFilterClauses("", "v") != nil {
		t.Fatal("empty key")
	}
	if len(termFilterClauses("host.keyword", "x")) != 1 {
		t.Fatal("keyword suffix skips duplicate")
	}
	if len(termFilterClauses("host", "x")) != 2 {
		t.Fatal("text field adds .keyword")
	}
}

func TestBuildQuery_msgOnly(t *testing.T) {
	q := buildQuery(SearchOptions{
		Query:     "timeout",
		MsgOnly:   true,
		TimeField: "@timestamp",
		From:      "now-1d",
	})
	boolQ, ok := q["bool"].(map[string]any)
	if !ok {
		t.Fatalf("expected bool query: %#v", q)
	}
	must, ok := boolQ["must"].([]map[string]any)
	if !ok || len(must) != 1 {
		t.Fatalf("must: %#v", boolQ["must"])
	}
	mp, ok := must[0]["match_phrase"].(map[string]any)
	if !ok || mp["msg"] != "timeout" {
		t.Fatalf("match_phrase: %#v", must[0])
	}
}

// Default (MsgOnly=false) searches across all fields via query_string default_field "*".
func TestBuildQuery_broadDefault(t *testing.T) {
	q := buildQuery(SearchOptions{Query: "timeout", TimeField: "@timestamp", From: "now-1d"})
	must := q["bool"].(map[string]any)["must"].([]map[string]any)
	qs, ok := must[0]["query_string"].(map[string]any)
	if !ok || qs["default_field"] != "*" {
		t.Fatalf("expected broad query_string over *, got %#v", must[0])
	}
}

func TestBuildQuery_precompiledClauseTakesPrecedence(t *testing.T) {
	clause := map[string]any{"term": map[string]any{"msg.keyword": "exact"}}
	q := buildQuery(SearchOptions{
		Query:       "legacy lucene query",
		QueryClause: clause,
	})
	must := q["bool"].(map[string]any)["must"].([]map[string]any)
	if len(must) != 1 || !reflect.DeepEqual(must[0], clause) {
		t.Fatalf("expected precompiled clause only, got %#v", must)
	}
}

// Precise mode searches match_phrase across ALL configured message fields, not just the first.
func TestBuildQuery_preciseMultiMessageField(t *testing.T) {
	q := buildQuery(SearchOptions{
		Query: "timeout", MsgOnly: true, TimeField: "@timestamp", From: "now-1d",
		MessageFields: []string{"msg", "message"},
	})
	must := q["bool"].(map[string]any)["must"].([]map[string]any)
	should := must[0]["bool"].(map[string]any)["should"].([]map[string]any)
	if len(should) != 2 {
		t.Fatalf("expected match_phrase across 2 message fields, got %#v", must[0])
	}
}

// Trace field mode falls back to a free-text match across all fields (no silent zero).
func TestBuildTraceQuery_fieldModeFreeTextFallback(t *testing.T) {
	q := buildTraceQuery("abc123", []string{"traceId"}, "field", "msg", nil)
	should := q["bool"].(map[string]any)["should"].([]map[string]any)
	if len(should) != 2 {
		t.Fatalf("expected configured-field OR free-text fallback, got %#v", q)
	}
}

func TestBuildTraceQuery_emptyAndNoFields(t *testing.T) {
	if q := buildTraceQuery("", []string{"traceId"}, "field", "msg", nil); q != nil {
		t.Fatalf("empty trace should not build query: %#v", q)
	}
	q := buildTraceQuery("abc123", nil, "field", "msg", nil)
	if _, ok := q["query_string"]; !ok {
		t.Fatalf("expected free-text fallback when no trace fields, got %#v", q)
	}
}

// TestQueryWithoutTimeRange: the query a Discover link carries is the query the
// search sends minus its range clause, the field filters in a fixed order.
func TestQueryWithoutTimeRange(t *testing.T) {
	opts := SearchOptions{Index: "app-*", From: "2026-10-25T07:00:00Z", To: "2026-10-25T08:00:00Z", TimeField: "@timestamp",
		Fields: map[string]string{"z.f": "1", "a.f": "2", "m.f": "3"}, LevelValue: "ERROR", LevelFields: []string{"level"}}
	full := BuildSearchBody(opts)["query"].(map[string]any)["bool"].(map[string]any)
	filter := full["filter"].([]map[string]any)
	if _, isRange := filter[0]["range"]; !isRange {
		t.Fatalf("the range comes first: %#v", filter)
	}
	got := QueryWithoutTimeRange(opts)["bool"].(map[string]any)
	if !reflect.DeepEqual(got["filter"], filter[1:]) || !reflect.DeepEqual(got["must"], full["must"]) {
		t.Fatalf("without range:\n%#v\nfull:\n%#v", got, full)
	}
	var order []string
	for _, f := range filter[1:] {
		for _, c := range f["bool"].(map[string]any)["should"].([]map[string]any) {
			for k := range c["term"].(map[string]any) {
				order = append(order, k)
			}
		}
	}
	if want := []string{"a.f", "a.f.keyword", "m.f", "m.f.keyword", "z.f", "z.f.keyword"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("field filter order: %v", order)
	}
	if q := QueryWithoutTimeRange(SearchOptions{From: "x", To: "y", TimeField: "@timestamp"}); q["match_all"] == nil {
		t.Fatalf("only a range: %#v", q)
	}
	agg := AggOptions{Index: "app-*", From: "a", To: "b", TimeField: "@timestamp", Query: "timeout", LevelValue: "ERROR", LevelFields: []string{"level"}}
	if !reflect.DeepEqual(BuildAggBody(agg)["query"], buildQuery(agg.SearchOptions())) {
		t.Fatal("the aggregation's query is its SearchOptions' query")
	}
}
