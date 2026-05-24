package fieldmap

import "testing"

func TestBuildValueORQuery(t *testing.T) {
	q := BuildValueORQuery([]string{"log_app", "service_name"}, "order-svc")
	if q == nil {
		t.Fatal("expected query")
	}
	qs := q["query_string"].(map[string]any)["query"].(string)
	if !containsAll(qs, "log_app", "service_name", "order", "OR") {
		t.Fatalf("query: %s", qs)
	}
}

func TestResolveSearchOptionsProfile(t *testing.T) {
	m := &Map{
		Defaults: Defaults{Index: "logs-*", ServiceFields: []string{"service"}},
		Profiles: map[string]Profile{
			"platform": {Index: "platform-*", ServiceFields: []string{"service_name"}},
		},
		Services: map[string]Service{
			"gw": {MatchFields: []string{"device_service"}},
		},
	}
	r, err := ResolveSearchOptions(m, "platform", "", "gw", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Index != "platform-*" {
		t.Fatalf("index %s", r.Index)
	}
	if len(r.ServiceFields) != 1 || r.ServiceFields[0] != "device_service" {
		t.Fatalf("fields %v", r.ServiceFields)
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
