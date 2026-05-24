package fieldmap

import "testing"

func TestMatchProfileByIndex(t *testing.T) {
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

func TestResolveSearchOptions_indexRule(t *testing.T) {
	m := &Map{
		Defaults: Defaults{TraceMode: TraceModeField},
		IndexRules: []IndexRule{
			{Match: "*v3*", TraceMode: TraceModeMsg, MessageFields: []string{"msg"}},
		},
	}
	r, err := ResolveSearchOptions(m, "", "app-v3-any-index-*", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.TraceMode != TraceModeMsg {
		t.Fatalf("traceMode=%q profile=%q", r.TraceMode, r.Profile)
	}
}
