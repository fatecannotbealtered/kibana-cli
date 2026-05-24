package fieldmap

import "testing"

func TestIndexMatchesPattern_glob(t *testing.T) {
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

func TestMatchIndexRule_firstWin(t *testing.T) {
	m := &Map{
		IndexRules: []IndexRule{
			{Match: "*v3*", TraceMode: TraceModeMsg},
			{Match: "*", TraceMode: TraceModeField},
		},
	}
	rule, ok := m.MatchIndexRule("app-v3-any-name")
	if !ok || rule.TraceMode != TraceModeMsg {
		t.Fatalf("got %+v ok=%v", rule, ok)
	}
}
