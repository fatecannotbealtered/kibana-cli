package fieldmap

import "testing"

func TestMatchIndexRule_firstWin(t *testing.T) {
	var nilMap *Map
	if _, ok := nilMap.MatchIndexRule("x"); ok {
		t.Fatal("nil map")
	}

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

func TestApplyIndexRuleViaResolve(t *testing.T) {
	m := &Map{
		Defaults: Defaults{TraceMode: TraceModeField},
		IndexRules: []IndexRule{
			{
				Match:         "*full*",
				TimeField:     "event.time",
				ServiceFields: []string{"svc"},
				LevelFields:   []string{"lvl"},
				MessageFields: []string{"body"},
				TraceIDFields: []string{"tid"},
				TraceMode:     TraceModeMsg,
			},
		},
	}
	r, err := ResolveSearchOptions(m, "", "app-full-index", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "index_rule:*full*" {
		t.Fatalf("profile %q", r.Profile)
	}
	if r.TimeField != "event.time" || r.TraceMode != TraceModeMsg {
		t.Fatalf("%+v", r)
	}
	if len(r.ServiceFields) != 1 || r.ServiceFields[0] != "svc" {
		t.Fatalf("service %v", r.ServiceFields)
	}
	if len(r.LevelFields) != 1 || len(r.MessageFields) != 1 || len(r.TraceIDFields) != 1 {
		t.Fatalf("%+v", r)
	}
}
