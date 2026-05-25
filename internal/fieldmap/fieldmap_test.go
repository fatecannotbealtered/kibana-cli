package fieldmap

import (
	"strings"
	"testing"
)

func TestBuildValueORQuery(t *testing.T) {
	if BuildValueORQuery(nil, "x") != nil {
		t.Fatal("nil fields")
	}
	if BuildValueORQuery([]string{}, "x") != nil {
		t.Fatal("empty fields")
	}
	if BuildValueORQuery([]string{"log_app"}, "") != nil {
		t.Fatal("empty value")
	}
	if BuildValueORQuery([]string{"", "  "}, "v") != nil {
		t.Fatal("blank field names")
	}
	if BuildValueORQuery([]string{"log_app"}, "   ") != nil {
		t.Fatal("blank value")
	}

	q := BuildValueORQuery([]string{"log_app", "service_name"}, "order-svc")
	if q == nil {
		t.Fatal("expected query")
	}
	qs := q["query_string"].(map[string]any)["query"].(string)
	if !containsAll(qs, "log_app", "service_name", "order", "OR") {
		t.Fatalf("query: %s", qs)
	}

	qkw := BuildValueORQuery([]string{"msg.keyword"}, "v")
	qs = qkw["query_string"].(map[string]any)["query"].(string)
	if strings.Contains(qs, "msg.keyword.keyword") {
		t.Fatalf("duplicate keyword suffix: %s", qs)
	}
}

func TestQuoteFieldAndValue(t *testing.T) {
	if quoteField("plain") != "plain" {
		t.Fatal("plain field")
	}
	if quoteField("service.name") != "service.name" {
		t.Fatal("dotted field")
	}
	got := quoteQueryValue(`a"b\c`)
	if got != `"a\"b\\c"` {
		t.Fatalf("quoted %q", got)
	}
}

func TestFirstNonEmptyAndUniqueStrings(t *testing.T) {
	if firstNonEmpty("", "  ", "x", "y") != "x" {
		t.Fatal("firstNonEmpty")
	}
	if firstNonEmpty("", "   ") != "" {
		t.Fatal("all empty")
	}
	got := UniqueStrings([]string{"a", "a", "", " b ", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("UniqueStrings = %v", got)
	}
}

func TestPrimaryMessageField(t *testing.T) {
	if (ResolvedSearch{}).PrimaryMessageField() != "msg" {
		t.Fatal("default msg")
	}
	r := ResolvedSearch{MessageFields: []string{"  "}}
	if r.PrimaryMessageField() != "msg" {
		t.Fatal("whitespace first field")
	}
	r = ResolvedSearch{MessageFields: []string{" log.message "}}
	if r.PrimaryMessageField() != "log.message" {
		t.Fatal("custom field")
	}
}

func TestNormalizeTraceMode(t *testing.T) {
	if NormalizeTraceMode("MSG") != TraceModeMsg {
		t.Fatal("msg mode")
	}
	if NormalizeTraceMode("") != TraceModeField {
		t.Fatal("default field mode")
	}
}

func TestResolveSearchOptionsProfile(t *testing.T) {
	m := &Map{
		Defaults: Defaults{Index: "logs-*", ServiceFields: []string{"service"}},
		Profiles: map[string]Profile{
			"platform": {
				Index:         "platform-*",
				TimeField:     "ts",
				ServiceFields: []string{"service_name"},
				LevelFields:   []string{"level"},
				MessageFields: []string{"message"},
				TraceIDFields: []string{"traceId"},
				TraceMode:     TraceModeMsg,
			},
		},
		Services: map[string]Service{
			"gw": {MatchFields: []string{"device_service"}},
		},
	}
	r, err := ResolveSearchOptions(m, "platform", "", "gw", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Index != "platform-*" || r.Profile != "platform" {
		t.Fatalf("index/profile %s %s", r.Index, r.Profile)
	}
	if r.TimeField != "ts" || r.TraceMode != TraceModeMsg {
		t.Fatalf("time/trace %+v", r)
	}
	if len(r.ServiceFields) != 1 || r.ServiceFields[0] != "device_service" {
		t.Fatalf("fields %v", r.ServiceFields)
	}
}

func TestResolveSearchOptionsNilMap(t *testing.T) {
	r, err := ResolveSearchOptions(nil, "", "logs-*", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Index != "logs-*" || r.TimeField != "@timestamp" {
		t.Fatalf("%+v", r)
	}
}

func TestResolveSearchOptionsErrors(t *testing.T) {
	if _, err := ResolveSearchOptions(&Map{}, "", "", "", ""); err == nil {
		t.Fatal("missing index")
	}
	if _, err := ResolveSearchOptions(&Map{}, "nope", "logs-*", "", ""); err == nil {
		t.Fatal("unknown profile")
	}
	if _, err := ResolveSearchOptions(&Map{}, "", "logs-*", "nope", ""); err == nil {
		t.Fatal("unknown service")
	}
}

func TestResolveSearchOptionsServiceIndex(t *testing.T) {
	m := &Map{
		Defaults: Defaults{Index: "default-*"},
		Services: map[string]Service{
			"orders": {Index: "order-*"},
		},
	}
	r, err := ResolveSearchOptions(m, "", "", "orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Index != "order-*" {
		t.Fatalf("index %s", r.Index)
	}
}

func TestResolveSearchOptionsServiceProfile(t *testing.T) {
	m := &Map{
		Services: map[string]Service{
			"orders": {Profiles: []string{"java-app"}},
		},
		Profiles: map[string]Profile{
			"java-app": {Index: "java-app-*"},
		},
	}
	r, err := ResolveSearchOptions(m, "", "", "orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Index != "java-app-*" || r.Profile != "java-app" {
		t.Fatalf("%+v", r)
	}
}

func TestResolveSearchOptionsIndexFlagOverrides(t *testing.T) {
	m := &Map{
		Defaults: Defaults{Index: "logs-*"},
		Profiles: map[string]Profile{
			"p": {Index: "profile-*"},
		},
	}
	r, err := ResolveSearchOptions(m, "p", "override-*", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Index != "override-*" {
		t.Fatalf("index %s", r.Index)
	}
}

func TestResolveSearchOptionsMatchProfileByIndex(t *testing.T) {
	m := &Map{
		Profiles: map[string]Profile{
			"java-app": {
				Index:         "java-app-*",
				MessageFields: []string{"msg"},
			},
		},
	}
	r, err := ResolveSearchOptions(m, "", "java-app-prod", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Profile != "java-app" || r.Index != "java-app-prod" {
		t.Fatalf("%+v", r)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || len(s) > 0 && findSub(s, sub)))
}

func findSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
