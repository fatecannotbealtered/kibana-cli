package kibanaclient

import "testing"

func TestBuildTraceQuery_msgMode(t *testing.T) {
	q := buildTraceQuery("aabbccdd00112233445566778899aabb", nil, "msg", "msg")
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

func TestBuildTraceQuery_fieldMode(t *testing.T) {
	q := buildTraceQuery("abc", []string{"traceId"}, "field", "msg")
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
	if BuildQuery(SearchOptions{})["match_all"] == nil {
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
	q := buildTraceQuery("abc123", []string{"traceId"}, "field", "msg")
	should := q["bool"].(map[string]any)["should"].([]map[string]any)
	if len(should) != 2 {
		t.Fatalf("expected configured-field OR free-text fallback, got %#v", q)
	}
}
