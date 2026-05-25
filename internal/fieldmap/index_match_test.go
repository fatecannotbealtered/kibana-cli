package fieldmap

import "testing"

func TestProfileIndexPatterns(t *testing.T) {
	if got := ProfileIndexPatterns(Profile{}); got != nil {
		t.Fatalf("empty: %v", got)
	}
	if got := ProfileIndexPatterns(Profile{Index: "  legacy-*  "}); len(got) != 1 || got[0] != "legacy-*" {
		t.Fatalf("legacy index: %v", got)
	}
	if got := ProfileIndexPatterns(Profile{IndexPatterns: []string{"a-*", "b-*"}}); len(got) != 2 {
		t.Fatalf("patterns: %v", got)
	}
}

func TestIndexMatchesPattern_glob(t *testing.T) {
	if IndexMatchesPattern("", "logs-*") || IndexMatchesPattern("logs-*", "") {
		t.Fatal("empty index or pattern")
	}
	if IndexMatchesPattern("index", "[bad") {
		t.Fatal("invalid pattern should not match")
	}
	if !IndexMatchesPattern("Exact-Index", "exact-index") {
		t.Fatal("case-insensitive literal")
	}

	cases := []struct {
		index, pattern string
		want           bool
	}{
		{"app-v3-test-log-*", "*v3*log*", true},
		{"app-v3-prod-log-2025.05", "*v3*log*", true},
		{"platform-b-test-log-*", "*legacy*log*", false},
		{"platform-legacy-test-log-*", "*legacy*log*", true},
		{"other-index", "*v3*log*", false},
	}
	for _, c := range cases {
		if got := IndexMatchesPattern(c.index, c.pattern); got != c.want {
			t.Fatalf("%q ~ %q = %v want %v", c.index, c.pattern, got, c.want)
		}
	}
}

func TestPatternSpecificity(t *testing.T) {
	if patternSpecificity("") != 0 {
		t.Fatal("empty pattern")
	}
	if patternSpecificity("logs-*") <= patternSpecificity("*") {
		t.Fatal("more specific pattern should score higher")
	}
}

func TestMatchProfileByIndex_scoring(t *testing.T) {
	m := &Map{
		Profiles: map[string]Profile{
			"platform": {IndexPatterns: []string{"platform-*"}},
			"trace-in-msg": {
				IndexPatterns: []string{"*v3*log*"},
				TraceMode:     TraceModeMsg,
			},
			"trace-in-field": {
				IndexPatterns: []string{"*legacy*log*"},
				TraceIDFields: []string{"log_traceId"},
				TraceMode:     TraceModeField,
			},
		},
	}
	name, p, ok := m.MatchProfileByIndex("app-v3-renamed-log-*")
	if !ok || name != "trace-in-msg" || p.TraceMode != TraceModeMsg {
		t.Fatalf("v3: name=%q mode=%q ok=%v", name, p.TraceMode, ok)
	}
	name, p, ok = m.MatchProfileByIndex("svc-legacy-prod-log-*")
	if !ok || name != "trace-in-field" || p.TraceMode != TraceModeField {
		t.Fatalf("legacy: name=%q mode=%q ok=%v", name, p.TraceMode, ok)
	}
}

func TestMatchProfileByIndex_edgeCases(t *testing.T) {
	var nilMap *Map
	if _, _, ok := nilMap.MatchProfileByIndex("x"); ok {
		t.Fatal("nil map")
	}
	m := &Map{Profiles: map[string]Profile{}}
	if _, _, ok := m.MatchProfileByIndex("x"); ok {
		t.Fatal("empty profiles")
	}
}
