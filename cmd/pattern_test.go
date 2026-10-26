package cmd

import (
	"strings"
	"testing"
)

func TestPatternListOnKibana9And7(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("pattern", "list")
	items := r.items()
	if r.exit != 0 || len(items) != 3 || num(r.data()["total"]) != 3 || r.data()["sort"] != "title_asc" {
		t.Fatalf("list: %s", r.out)
	}
	if items[0]["ref"] != "index-pattern/dv-app" || items[0]["title"] != "app-log-*" || items[0]["name"] != "App logs" ||
		items[2]["title"] != "legacy-log-*" {
		t.Fatalf("items: %v", items)
	}
	if !hasString(r.untrusted(), "data.items[].title") {
		t.Fatalf("untrusted: %v", r.untrusted())
	}
	usedAPI := false
	for _, q := range e.kb.Requests() {
		usedAPI = usedAPI || q.Path == "/api/data_views"
	}
	if !usedAPI {
		t.Fatal("Kibana 9 lists data views through the data views API")
	}
	if r = e.run("pattern", "list", "--query", "LEGACY"); len(r.items()) != 1 || r.scope()["query"] != "LEGACY" {
		t.Fatalf("query: %s", r.out)
	}
	if r = e.run("patterns", "ls", "--limit", "1", "--offset", "1"); len(r.items()) != 1 || num(r.data()["next_offset"]) != 2 {
		t.Fatalf("paging through the 1.x group name: %s", r.out)
	}
	// Kibana 7: no data views API, index-pattern saved objects instead, with the
	// time field.
	e.kb.version = "7.17.0"
	e.vars["KIBANA_CLI_KIBANA_VERSION"] = "7.17.0"
	r = e.run("pattern", "list")
	if r.exit != 0 || len(r.items()) != 3 || r.items()[0]["time_field"] != "@timestamp" {
		t.Fatalf("Kibana 7: %s", r.out)
	}
	for _, q := range e.kb.Requests()[len(e.kb.Requests())-1:] {
		if q.Path != "/api/saved_objects/_find" {
			t.Fatalf("Kibana 7 reads saved objects: %s", q.Path)
		}
	}
	if r = e.run("pattern", "list", "--limit", "0"); r.code() != "E_VALIDATION" {
		t.Fatalf("bad limit: %s", r.out)
	}
}

func TestPatternFields(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("pattern", "fields", "--index", "app-log-*")
	if r.exit != 0 || len(r.items()) != 5 || num(r.data()["total"]) != 5 || r.data()["sort"] != "name_asc" || r.scope()["index"] != "app-log-*" {
		t.Fatalf("fields: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--index", "app-log-*", "--query", "LOG"); len(r.items()) != 1 || r.items()[0]["name"] != "log_app" {
		t.Fatalf("query: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--index", "app-log-*", "--type", "number"); len(r.items()) != 1 || r.items()[0]["name"] != "http.status" {
		t.Fatalf("type: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--index", "app-log-*", "--aggregatable"); len(r.items()) != 4 || r.scope()["aggregatable"] != true {
		t.Fatalf("aggregatable: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--data-view", "index-pattern/dv-legacy"); r.exit != 0 || r.scope()["data_view"] != "index-pattern/dv-legacy" || len(r.items()) != 5 {
		t.Fatalf("data view: %s", r.out)
	}
	// Kibana 9 reads the internal fields route with its API version header.
	var internal fakeRequest
	for _, q := range e.kb.Requests() {
		if q.Path == "/internal/data_views/_fields_for_wildcard" {
			internal = q
		}
	}
	if internal.Header.Get("Elastic-Api-Version") != "1" {
		t.Fatalf("internal route headers: %v", internal.Header)
	}
	if r = e.run("pattern", "fields"); r.code() != "E_USAGE" || r.details()["parameter"] != "--index" {
		t.Fatalf("no index: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--index", "nothing-*"); r.code() != "E_NOT_FOUND" || !strings.Contains(r.message(), "No index") {
		t.Fatalf("no such pattern: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--data-view", "nope"); r.code() != "E_NOT_FOUND" {
		t.Fatalf("no such data view: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--index", "_internal"); r.code() != "E_VALIDATION" {
		t.Fatalf("unsafe index: %s", r.out)
	}
}

func TestPatternInfer(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("pattern", "infer", "--index", "app-log-*")
	d := r.data()
	profile, _ := d["profile"].(map[string]any)
	if r.exit != 0 || d["profile_name"] != "app-log" || profile["trace_mode"] != "msg" || stringsOf(profile["service_fields"])[0] != "log_app" ||
		stringsOf(profile["message_fields"])[0] != "msg" {
		t.Fatalf("infer: %s", r.out)
	}
	want := "kibana-cli field-map add-profile --name app-log --index 'app-log-*' --service-field log_app --level-field level --message-field msg --trace-mode msg"
	if d["next_command"] != want || !strings.Contains(d["yaml"].(string), "app-log:") {
		t.Fatalf("next_command: %v", d["next_command"])
	}
	if len(e.kb.searches()) != 1 {
		t.Fatalf("one sample search: %d", len(e.kb.searches()))
	}
	// A trace field: no sample search, trace mode field.
	before := len(e.kb.searches())
	r = e.run("pattern", "infer", "--index", "legacy-log-*", "--name", "legacy")
	profile, _ = r.data()["profile"].(map[string]any)
	if r.exit != 0 || profile["trace_mode"] != "field" || stringsOf(profile["trace_id_fields"])[0] != "traceId" || len(e.kb.searches()) != before {
		t.Fatalf("legacy: %s", r.out)
	}
	// The next command runs as it is.
	parts := strings.Fields(strings.ReplaceAll(want, "'", ""))
	if r = e.run(append(parts[1:], "--dry-run")...); r.exit != 0 || r.preview()["action"] == nil {
		t.Fatalf("next_command does not run: %s", r.out)
	}
	for _, args := range [][]string{{"--index", "app-log-*", "--name", "bad name"}, {"--index", "app-log-*", "--sample", "101"}} {
		if r = e.run(append([]string{"pattern", "infer"}, args...)...); r.code() != "E_VALIDATION" {
			t.Errorf("%v: %s", args, r.out)
		}
	}
	if r = e.run("pattern", "infer"); r.code() != "E_USAGE" {
		t.Fatalf("no index: %s", r.out)
	}
}
