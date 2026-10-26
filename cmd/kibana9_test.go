package cmd

import (
	"strings"
	"testing"
)

// The tests in this file come from the live check on Kibana 9.5.2 with a read-only
// account (2026-10): a redacted GET /api/status, data streams, and Kibana 7 found by
// capability rather than by version.

// TestCommandsWorkWhenKibanaHidesItsVersion: Kibana 8 and 9 answer GET /api/status
// with only the overall level for an account without monitoring privileges, and
// here Elasticsearch's GET / is refused too. 1.x failed every command; 2.0 needs no
// version: the APIs are picked by trying them, and no request carries kbn-version.
func TestCommandsWorkWhenKibanaHidesItsVersion(t *testing.T) {
	e := newTestEnv(t)
	e.kb.esRootForbidden = true
	for _, args := range [][]string{
		{"log", "search", "--index", "app-log-*"},
		{"log", "aggregate", "--index", "app-log-*", "--agg-type", "date_histogram"},
		{"log", "get", "app-log-2026.10.25/a1"},
		{"pattern", "list"},
		{"pattern", "fields", "--index", "app-log-*"},
		{"pattern", "fields", "--data-view", "dv-legacy"},
		{"object", "list", "--type", "dashboard"},
		{"object", "get", "index-pattern/dv-app"},
	} {
		if r := e.run(args...); r.exit != 0 {
			t.Errorf("%v: %s", args, r.out)
		}
	}
	st := e.run("auth", "status")
	if st.exit != 0 || st.data()["kibana_version"] != nil || st.data()["kibana_version_source"] != "unknown" {
		t.Fatalf("auth status: %s", st.out)
	}
	c := doctorChecks(t, e.run("doctor"))
	if c["kibana_version"]["status"] != "pass" || !strings.HasPrefix(c["kibana_version"]["message"].(string), "Unknown") {
		t.Fatalf("doctor: %v", c)
	}
	usedDataViews := false
	for _, q := range e.kb.Requests() {
		if q.Header.Get("kbn-version") != "" {
			t.Fatalf("%s %s sent kbn-version", q.Method, q.Path)
		}
		usedDataViews = usedDataViews || q.Path == "/api/data_views"
	}
	if !usedDataViews || e.kb.Writes() != 0 {
		t.Fatalf("data views API used: %v, writes: %d", usedDataViews, e.kb.Writes())
	}
	// context reads no network: the version is the setting's, or unknown.
	if r := e.run("context"); !strings.Contains(r.out, `"kibana_version":{"source":"unknown","value":null}`) {
		t.Fatalf("context: %s", r.out)
	}
	e.vars["KIBANA_CLI_KIBANA_VERSION"] = "9.5.2"
	if r := e.run("context"); !strings.Contains(r.out, `"kibana_version":{"source":"setting","value":"9.5.2"}`) {
		t.Fatalf("context with the setting: %s", r.out)
	}
}

// TestKibana7IsFoundByCapability: with no version known, the data views API's 404
// on Kibana 7 falls back to the index-pattern saved objects, and the internal
// fields route's 404 to the 7.x fields route.
func TestKibana7IsFoundByCapability(t *testing.T) {
	e := newTestEnv(t)
	e.kb.version, e.kb.esRootForbidden = "7.17.0", true
	r := e.run("pattern", "list")
	if r.exit != 0 || len(r.items()) != 3 || r.items()[0]["time_field"] != "@timestamp" {
		t.Fatalf("pattern list: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--index", "legacy-log-*"); r.exit != 0 || len(r.items()) != 5 {
		t.Fatalf("pattern fields: %s", r.out)
	}
	if r = e.run("pattern", "fields", "--data-view", "dv-legacy"); r.exit != 0 || r.scope()["index"] != "legacy-log-*" {
		t.Fatalf("pattern fields by data view: %s", r.out)
	}
	var paths []string
	for _, q := range e.kb.Requests() {
		paths = append(paths, q.Path)
	}
	joined := strings.Join(paths, " ")
	for _, want := range []string{"/api/data_views", "/api/saved_objects/_find", "/api/index_patterns/_fields_for_wildcard"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%s not asked: %v", want, paths)
		}
	}
}

// TestLogSearchOnADataStream: Elasticsearch 9 makes logs-*-* data streams, so a
// hit's _index is a backing index (.ds-<stream>-<date>-<generation>), not the
// searched pattern. The ref names the backing index, log get reads it back, and
// the allowlist admits it by its stream's name.
func TestLogSearchOnADataStream(t *testing.T) {
	e := newTestEnv(t)
	e.vars["KIBANA_CLI_ALLOWED_INDEX_PREFIXES"] = "logs-"
	r := e.run("log", "search", "--index", "logs-app-*", "--since", "1h")
	const ref = ".ds-logs-app-default-2026.10.25-000001/d1"
	items := r.items()
	if r.exit != 0 || len(items) != 1 || r.scope()["index"] != "logs-app-*" {
		t.Fatalf("search: %s", r.out)
	}
	if h := items[0]; h["ref"] != ref || h["service"] != "cart" || h["level"] != "error" ||
		h["trace_id"] != "0af7651916cd43dd8448eb211c80319c" || h["message"] != "cart checkout failed" {
		t.Fatalf("hit: %v", h)
	}
	g := e.run("log", "get", ref)
	if g.exit != 0 || g.data()["ref"] != ref || g.data()["index"] != ".ds-logs-app-default-2026.10.25-000001" || g.data()["service"] != "cart" {
		t.Fatalf("log get: %s", g.out)
	}
	if r = e.run("log", "search", "--index", "logs-app-*", "--trace-id", "0af7651916cd43dd8448eb211c80319c", "--since", "1h"); r.exit != 0 {
		t.Fatalf("trace search: %s", r.out)
	}
	// A backing index of a stream outside the allowlist stays refused.
	if r = e.run("log", "get", ".ds-other-default-2026.10.25-000001/x"); r.code() != "E_FORBIDDEN" {
		t.Fatalf("outside the allowlist: %s", r.out)
	}
}
