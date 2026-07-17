package kql

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseEmptyQuery(t *testing.T) {
	want := map[string]any{"match_all": map[string]any{}}
	for _, query := range []string{"", "  \t\r\n "} {
		got, err := Parse(query)
		if err != nil {
			t.Fatalf("Parse(%q): %v", query, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Parse(%q) = %#v, want %#v", query, got, want)
		}
	}
}

func TestParseFieldTerms(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  map[string]any
	}{
		{
			name:  "unquoted numeric",
			query: "status:200",
			want:  matchQuery("status", float64(200)),
		},
		{
			name:  "unquoted keeps spaces",
			query: "message:connection reset",
			want:  matchQuery("message", "connection reset"),
		},
		{
			name:  "quoted phrase",
			query: `message:"connection reset"`,
			want:  phraseQuery("message", "connection reset"),
		},
		{
			name:  "keywords inside quotes",
			query: `message:"rock and roll or not"`,
			want:  phraseQuery("message", "rock and roll or not"),
		},
		{
			name:  "boolean literal",
			query: "enabled:false",
			want:  matchQuery("enabled", false),
		},
		{
			name:  "null literal",
			query: "deleted_at:null",
			want:  matchQuery("deleted_at", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertParse(t, tt.query, tt.want)
		})
	}
}

func TestParseTermsWithoutField(t *testing.T) {
	tests := []struct {
		query string
		want  map[string]any
	}{
		{
			query: "connection reset",
			want: map[string]any{"multi_match": map[string]any{
				"query":   "connection reset",
				"type":    "best_fields",
				"lenient": true,
			}},
		},
		{
			query: `"connection reset"`,
			want: map[string]any{"multi_match": map[string]any{
				"query":   "connection reset",
				"type":    "phrase",
				"lenient": true,
			}},
		},
		{
			query: "err*",
			want: map[string]any{"query_string": map[string]any{
				"query": "err*",
			}},
		},
	}

	for _, tt := range tests {
		assertParse(t, tt.query, tt.want)
	}
}

func TestParseBooleanPrecedenceAndGrouping(t *testing.T) {
	a := matchQuery("a", float64(1))
	b := matchQuery("b", float64(2))
	c := matchQuery("c", float64(3))

	assertParse(t, "a:1 oR b:2 AnD NoT c:3", orQuery(
		a,
		andQuery(b, notQuery(c)),
	))
	assertParse(t, "(a:1 OR b:2) AND c:3", andQuery(
		orQuery(a, b),
		c,
	))
	assertParse(t, "a:1 OR b:2 OR c:3", orQuery(a, orQuery(b, c)))
	assertParse(t, "a:1 AND b:2 AND c:3", andQuery(a, andQuery(b, c)))
}

func TestParseFieldValueLists(t *testing.T) {
	assertParse(t, "status:(200 OR 201)", orQuery(
		matchQuery("status", float64(200)),
		matchQuery("status", float64(201)),
	))

	assertParse(t, "tag:((prod or staging) and not legacy)", andQuery(
		orQuery(matchQuery("tag", "prod"), matchQuery("tag", "staging")),
		notQuery(matchQuery("tag", "legacy")),
	))
}

func TestParseRanges(t *testing.T) {
	tests := []struct {
		query string
		op    string
		value any
	}{
		{"bytes > 1000", "gt", float64(1000)},
		{"bytes >= 1000", "gte", float64(1000)},
		{"bytes < 8000", "lt", float64(8000)},
		{"bytes <= 8000", "lte", float64(8000)},
		{`@timestamp >= "2020-01-01T00:00:00Z"`, "gte", "2020-01-01T00:00:00Z"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			field := "bytes"
			if strings.HasPrefix(tt.query, "@timestamp") {
				field = "@timestamp"
			}
			assertParse(t, tt.query, rangeQuery(field, tt.op, tt.value))
		})
	}

	assertParse(t, "bytes > 1000 and bytes < 8000", andQuery(
		rangeQuery("bytes", "gt", float64(1000)),
		rangeQuery("bytes", "lt", float64(8000)),
	))
}

func TestParseExistsAndWildcards(t *testing.T) {
	assertParse(t, "field:*", map[string]any{
		"exists": map[string]any{"field": "field"},
	})
	assertParse(t, "*:*", map[string]any{"match_all": map[string]any{}})
	assertParse(t, "message:err*", queryString([]string{"message"}, "err*"))
	assertParse(t, "message:*err", queryString([]string{"message"}, "*err"))
	assertParse(t, `message:foo\:bar*`, queryString([]string{"message"}, `foo\:bar*`))
	assertParse(t, `message:err\**`, queryString([]string{"message"}, `err\**`))
	assertParse(t, "*timeout", map[string]any{
		"query_string": map[string]any{"query": "*timeout"},
	})
}

func TestParseWildcardValueMustBeSingleToken(t *testing.T) {
	assertParse(t, "message:foo*", queryString([]string{"message"}, "foo*"))

	for _, query := range []string{
		`message:foo* \AND bar`,
		"message:foo* bar",
		"foo* bar",
	} {
		got, err := Parse(query)
		if err == nil || got != nil {
			t.Fatalf("Parse(%q) = %#v, %v; want nil query and error", query, got, err)
		}
		if !strings.Contains(err.Error(), "explicit field boolean clauses") {
			t.Fatalf("Parse(%q) error = %v, want rewrite guidance", query, err)
		}
		if !strings.Contains(err.Error(), "position") {
			t.Fatalf("Parse(%q) error lacks a position: %v", query, err)
		}
	}
}

func TestParseEscapes(t *testing.T) {
	tests := []struct {
		query string
		want  map[string]any
	}{
		{`message:foo\*bar`, matchQuery("message", "foo*bar")},
		{`message:foo\:bar`, matchQuery("message", "foo:bar")},
		{`message:foo \and bar`, matchQuery("message", "foo and bar")},
		{`message:"say \"and\""`, phraseQuery("message", `say "and"`)},
		{`"field*":value`, matchQuery("field*", "value")},
	}

	for _, tt := range tests {
		assertParse(t, tt.query, tt.want)
	}
}

func TestParseNestedQueries(t *testing.T) {
	want := map[string]any{
		"nested": map[string]any{
			"path": "user",
			"query": andQuery(
				phraseQuery("user.name", "Ada"),
				map[string]any{
					"nested": map[string]any{
						"path": "user.address",
						"query": orQuery(
							queryString([]string{"user.address.city"}, "Lon*"),
							rangeQuery("user.address.zip", "gte", float64(10000)),
						),
						"score_mode": "none",
					},
				},
			),
			"score_mode": "none",
		},
	}

	assertParse(t, `user:{name:"Ada" and address:{city:Lon* or zip >= 10000}}`, want)
}

func TestParseMetadataDependentSyntaxFailsClosed(t *testing.T) {
	tests := []struct {
		query   string
		message string
	}{
		{"bytes* > 10", "data-view field metadata"},
		{"machine*:osx", "data-view field metadata"},
		{"labels.*:*", "data-view field metadata"},
		{"*:timeout", "data-view field metadata"},
		{"*:(timeout or error)", "data-view field metadata"},
		{"items:{attrs.*:foo}", "data-view field metadata"},
		{"items:{*:*}", "data-view field metadata"},
		{"items:{free text}", "without a field"},
		{"items*:{name:foo}", "nested paths cannot contain wildcards"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got, err := Parse(tt.query)
			if err == nil || got != nil {
				t.Fatalf("Parse(%q) = %#v, %v; want nil query and error", tt.query, got, err)
			}
			if !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("Parse(%q) error = %v, want substring %q", tt.query, err, tt.message)
			}
		})
	}
}

func TestParseRejectsInvalidOrUnsupportedSyntax(t *testing.T) {
	tests := []string{
		`message:"unterminated`,
		"field:",
		"field and ",
		"field OR (other",
		"field)",
		"()",
		"field:()",
		"field:(a or )",
		"items:{name:foo",
		"items*:{name:foo}",
		"items:{free text}",
		"bytes* > 10",
		`field:\q`,
		"field:not value",
		"field:bar AND(baz)",
		"foo:bar || baz:qux",
		"foo:bar && baz:qux",
		"foo:ba?",
		"foo:/ba.*/",
		"foo:ba~",
		"foo:ba^2",
		"foo:[1 TO 10]",
		"_exists_:foo",
		"+foo:bar",
		"-foo:bar",
		"!foo:bar",
		`"":foo`,
		`foo\**:bar`,
		`"foo*":*`,
	}

	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			got, err := Parse(query)
			if err == nil {
				t.Fatalf("Parse(%q) unexpectedly succeeded: %#v", query, got)
			}
			if got != nil {
				t.Fatalf("Parse(%q) returned a query with an error: %#v", query, got)
			}
			if !strings.Contains(err.Error(), "position") {
				t.Fatalf("Parse(%q) error lacks a position: %v", query, err)
			}
		})
	}
}

func FuzzParseDoesNotPanic(f *testing.F) {
	for _, query := range []string{
		"",
		"field:value",
		`message:"and or not"`,
		"field:(a or b)",
		"nested:{child:value}",
		"\x00",
		strings.Repeat("(", 32),
	} {
		f.Add(query)
	}
	f.Fuzz(func(t *testing.T, query string) {
		_, _ = Parse(query)
	})
}

func assertParse(t *testing.T, query string, want map[string]any) {
	t.Helper()
	got, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q) = %#v, want %#v", query, got, want)
	}
}

func matchQuery(field string, value any) map[string]any {
	return map[string]any{"match": map[string]any{field: value}}
}

func phraseQuery(field, value string) map[string]any {
	return map[string]any{"match_phrase": map[string]any{field: value}}
}

func rangeQuery(field, op string, value any) map[string]any {
	return map[string]any{"range": map[string]any{field: map[string]any{op: value}}}
}

func andQuery(children ...map[string]any) map[string]any {
	return map[string]any{"bool": map[string]any{"filter": children}}
}

func orQuery(children ...map[string]any) map[string]any {
	return map[string]any{"bool": map[string]any{
		"should":               children,
		"minimum_should_match": 1,
	}}
}

func notQuery(child map[string]any) map[string]any {
	return map[string]any{"bool": map[string]any{"must_not": child}}
}

func queryString(fields []string, query string) map[string]any {
	return map[string]any{"query_string": map[string]any{
		"fields": fields,
		"query":  query,
	}}
}
