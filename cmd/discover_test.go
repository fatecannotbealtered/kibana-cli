package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fatecannotbealtered/kibana-cli/internal/discover"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// The Discover link of --discover-url (#11). Its important property: the link's
// query is exactly what the CLI searched, minus the time range, which is in the time
// picker; each test decodes the link and compares it with the request the fake
// Kibana received.

// linkOf parses scope.discover_url of a run.
func linkOf(t *testing.T, r cliResult) *discover.Parsed {
	t.Helper()
	raw, ok := r.scope()["discover_url"].(string)
	if !ok || r.exit != 0 {
		t.Fatalf("no discover_url: %s", r.out)
	}
	if _, has := r.scope()["discover_url_reason"]; has {
		t.Fatalf("a link and a reason: %s", r.out)
	}
	if !hasString(r.untrusted(), "data.scope.discover_url") {
		t.Fatalf("discover_url is not marked untrusted: %v", r.untrusted())
	}
	p, err := discover.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return p
}

// reasonOf is scope.discover_url_reason of a run that built no link.
func reasonOf(t *testing.T, r cliResult) string {
	t.Helper()
	v, has := r.scope()["discover_url"]
	reason, _ := r.scope()["discover_url_reason"].(string)
	if r.exit != 0 || !has || v != nil || reason == "" {
		t.Fatalf("want discover_url null with a reason: %s", r.out)
	}
	if !hasString(r.untrusted(), "data.scope.discover_url_reason") {
		t.Fatalf("the reason is not marked untrusted: %v", r.untrusted())
	}
	return reason
}

// withoutRange is a search body's query minus its time range clause: what the link
// must carry.
func withoutRange(t *testing.T, body map[string]any) any {
	t.Helper()
	q, _ := body["query"].(map[string]any)
	b, ok := q["bool"].(map[string]any)
	if !ok {
		return q
	}
	out := map[string]any{}
	for k, v := range b {
		out[k] = v
	}
	var keep []any
	for _, f := range listOf(b["filter"]) {
		if _, isRange := f.(map[string]any)["range"]; !isRange {
			keep = append(keep, f)
		}
	}
	delete(out, "filter")
	if len(keep) > 0 {
		out["filter"] = keep
	}
	if len(out) == 0 {
		return map[string]any{"match_all": map[string]any{}}
	}
	return map[string]any{"bool": out}
}

// checkLink checks a link against the run: its base, the time range in scope, the
// data view and the query the fake received.
func checkLink(t *testing.T, e *testEnv, r cliResult, base, dataView string) *discover.Parsed {
	t.Helper()
	p := linkOf(t, r)
	if p.Base != e.kb.URL+base {
		t.Fatalf("base %s, want %s", p.Base, e.kb.URL+base)
	}
	if !reflect.DeepEqual(p.G, map[string]any{"filters": []any{}, "time": map[string]any{"from": r.scope()["since"], "to": r.scope()["until"]}}) {
		t.Fatalf("_g %#v, scope %v", p.G, r.scope())
	}
	if p.A["index"] != dataView {
		t.Fatalf("data view %v, want %s", p.A["index"], dataView)
	}
	meta := listOf(p.A["filters"])[0].(map[string]any)["meta"].(map[string]any)
	if meta["index"] != dataView || meta["type"] != "custom" || meta["key"] != "query" || meta["negate"] != false || meta["disabled"] != false {
		t.Fatalf("filter meta: %#v", meta)
	}
	if !reflect.DeepEqual(p.A["query"], map[string]any{"language": "kuery", "query": ""}) {
		t.Fatalf("the query bar is not empty: %#v", p.A["query"])
	}
	got, err := p.FilterQuery()
	want := withoutRange(t, e.kb.lastSearch())
	if err != nil || !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("the link's query is not what was searched:\nlink   %s\nsearch %s", gotJSON, wantJSON)
	}
	return p
}

// dataViewReads counts the requests that list or read data views.
func dataViewReads(e *testEnv) int {
	n := 0
	for _, r := range e.kb.Requests() {
		if strings.HasPrefix(r.Path, "/api/data_views") || strings.HasPrefix(r.Path, "/api/saved_objects") {
			n++
		}
	}
	return n
}

func TestDiscoverURLForASearchByIndex(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("log", "search", "--index", "legacy-log-*", "--service", "gateway", "--level", "WARN", "--trace-id", "abc123",
		"--field", "http.status=504", "--field", "env=prod", "--query", `slow AND "can't" !x 超时`, "--since", "2h", "--discover-url")
	p := checkLink(t, e, r, "/app/discover#/", "dv-legacy")
	meta := listOf(p.A["filters"])[0].(map[string]any)["meta"].(map[string]any)
	if meta["alias"] != `kibana-cli: service gateway, level WARN, trace abc123, http.status=504, env=prod, query "slow AND "can't" !x 超时"` {
		t.Fatalf("label: %v", meta["alias"])
	}
	if r.scope()["since"] != "2026-10-25T06:10:00Z" || r.scope()["until"] != "2026-10-25T08:10:00Z" {
		t.Fatalf("scope: %v", r.scope())
	}
	link := r.scope()["discover_url"].(string)
	_, fragment, _ := strings.Cut(link, "#")
	if strings.ContainsAny(fragment, " \"+") || strings.Count(link, "&") != 1 || strings.Count(link, "=") != 2 {
		t.Fatalf("unescaped link: %s", link)
	}
	// One more read, the data view list, and nothing written.
	if dataViewReads(e) != 1 || e.kb.Writes() != 0 {
		t.Fatalf("requests: %d data view reads, %d writes", dataViewReads(e), e.kb.Writes())
	}
	// A KQL query and a field profile go into the label; the filter is the compiled DSL.
	e.writeFieldMap("version: 1\nprofiles:\n  legacy:\n    index_patterns: [\"legacy-log-*\"]\n    service_fields: [service_name]\n")
	r = e.run("log", "search", "--index", "legacy-log-*", "--query", "log.level:WARN and not service_name:x", "--query-language", "kql", "--discover-url")
	p = checkLink(t, e, r, "/app/discover#/", "dv-legacy")
	if meta = listOf(p.A["filters"])[0].(map[string]any)["meta"].(map[string]any); meta["alias"] != `kibana-cli: kql "log.level:WARN and not service_name:x", profile legacy` {
		t.Fatalf("kql label: %v", meta["alias"])
	}
}

// TestDiscoverURLIsOptIn: without the flag, no data view is read and the scope has
// no link.
func TestDiscoverURLIsOptIn(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("log", "search", "--index", "legacy-log-*")
	if _, has := r.scope()["discover_url"]; has || r.exit != 0 || dataViewReads(e) != 0 {
		t.Fatalf("without the flag: %d data view reads, %s", dataViewReads(e), r.out)
	}
	r = e.run("log", "aggregate", "--index", "legacy-log-*", "--terms", "log.level")
	if _, has := r.scope()["discover_url"]; has || r.exit != 0 || dataViewReads(e) != 0 {
		t.Fatalf("aggregate without the flag: %s", r.out)
	}
}

func TestDiscoverURLWithDataView(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("log", "search", "--data-view", "index-pattern/dv-app", "--level", "ERROR", "--discover-url")
	checkLink(t, e, r, "/app/discover#/", "dv-app")
	// The data view was resolved to search it; no list was read for the link.
	for _, q := range e.kb.Requests() {
		if q.Path == "/api/data_views" || q.Path == "/api/saved_objects/_find" {
			t.Fatalf("listed data views: %s", q.Path)
		}
	}
	// A data view without a time field: Discover would ignore the range.
	r = e.run("log", "search", "--data-view", "dv-notime", "--time-field", "@timestamp", "--discover-url")
	if reason := reasonOf(t, r); !strings.Contains(reason, "index-pattern/dv-notime has no time field") || !strings.Contains(reason, "--data-view") {
		t.Fatalf("no time field: %s", reason)
	}
	// A --time-field other than the data view's: another window.
	r = e.run("log", "search", "--data-view", "dv-app", "--time-field", "event.time", "--discover-url")
	if reason := reasonOf(t, r); !strings.Contains(reason, "filters time on @timestamp but this search used event.time") ||
		!strings.Contains(reason, "--time-field @timestamp") {
		t.Fatalf("other time field: %s", reason)
	}
}

func TestDiscoverURLNeedsOneDataView(t *testing.T) {
	e := newTestEnv(t)
	// Two data views have the pattern exactly: no guess.
	r := e.run("log", "search", "--index", "app-log-*", "--discover-url")
	if reason := reasonOf(t, r); !strings.HasPrefix(reason, "2 data views have the index pattern app-log-* (index-pattern/dv-app (app-log-*), index-pattern/dv-notime (app-log-*))") ||
		!strings.Contains(reason, "--data-view <ref>") {
		t.Fatalf("two matches: %s", reason)
	}
	if len(r.items()) != 3 {
		t.Fatalf("the search itself is complete: %s", r.out)
	}
	// None has it, two cover it.
	r = e.run("log", "search", "--index", "app-log-2026.10.25", "--discover-url")
	if reason := reasonOf(t, r); !strings.Contains(reason, "No Kibana data view has exactly the index pattern app-log-2026.10.25") ||
		!strings.Contains(reason, "index-pattern/dv-app (app-log-*), index-pattern/dv-notime (app-log-*)") || !strings.Contains(reason, "--data-view <ref>") {
		t.Fatalf("covering views: %s", reason)
	}
	// None covers it.
	r = e.run("log", "search", "--index", "zzz-*", "--discover-url")
	if reason := reasonOf(t, r); !strings.HasPrefix(reason, "No Kibana data view has the index pattern zzz-*, so Discover cannot show this search") {
		t.Fatalf("no view: %s", reason)
	}
	// The closest first.
	e.kb.views = append(e.kb.views, fakeView("dv-all", "*"), fakeView("dv-app-2026", "app-log-2026*"))
	r = e.run("log", "search", "--index", "app-log-2026.10.25", "--discover-url")
	if reason := reasonOf(t, r); !strings.Contains(reason, "are index-pattern/dv-app-2026 (app-log-2026*), index-pattern/dv-app (app-log-*), index-pattern/dv-notime (app-log-*):") {
		t.Fatalf("closest first, three at most: %s", reason)
	}
	// The data view list cannot be read: the search still answers.
	e.kb.fail(fakeFault{Match: "/api/data_views", Status: 403, Body: `{"statusCode":403,"error":"Forbidden","message":"no"}`})
	r = e.run("log", "search", "--index", "legacy-log-*", "--discover-url")
	if reason := reasonOf(t, r); !strings.Contains(reason, "may not list Kibana's data views (HTTP 403)") || len(r.items()) != 1 {
		t.Fatalf("list refused: %s", r.out)
	}
}

func TestDiscoverURLForDSL(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("log", "search", "--index", "legacy-log-*", "--dsl", `{"query":{"bool":{"must":[{"match_phrase":{"message":"slow upstream"}}],"filter":[{"range":{"@timestamp":{"gte":"now-1d"}}}]}},"size":5}`, "--discover-url")
	p := linkOf(t, r)
	got, _ := p.FilterQuery()
	if !reflect.DeepEqual(got, e.kb.lastSearch()["query"]) {
		t.Fatalf("the body's query, its own range included: %#v", got)
	}
	if !reflect.DeepEqual(p.G["time"], map[string]any{"from": "1970-01-01T00:00:00Z", "to": "2026-10-25T08:10:00Z"}) || p.A["index"] != "dv-legacy" {
		t.Fatalf("dsl link: %#v %#v", p.G, p.A["index"])
	}
	r = e.run("log", "search", "--index", "legacy-log-*", "--dsl", `{"size":1}`, "--discover-url")
	if got, _ = linkOf(t, r).FilterQuery(); !reflect.DeepEqual(got, map[string]any{"match_all": map[string]any{}}) {
		t.Fatalf("no query: %#v", got)
	}
}

func TestDiscoverURLForAggregate(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("log", "aggregate", "--index", "legacy-log-*", "--terms", "log.level", "--service", "gateway", "--discover-url")
	p := checkLink(t, e, r, "/app/discover#/", "dv-legacy")
	if meta := listOf(p.A["filters"])[0].(map[string]any)["meta"].(map[string]any); meta["alias"] != "kibana-cli: service gateway" {
		t.Fatalf("label: %v", meta["alias"])
	}
	r = e.run("log", "aggregate", "--index", "legacy-log-*", "--agg-type", "date_histogram", "--interval", "10m", "--discover-url")
	checkLink(t, e, r, "/app/discover#/", "dv-legacy")
}

// TestDiscoverURLOnKibana7: Kibana 7.0 to 7.8 serve Discover at /app/kibana#/discover,
// 7.9 and later at /app/discover#/. Without a known version a 7.x Kibana (no data
// views API) gets the legacy path.
func TestDiscoverURLOnKibana7(t *testing.T) {
	for _, c := range []struct {
		fake, setting, base string
	}{
		{"7.7.1", "", "/app/kibana#/discover"},
		{"7.7.1", "7.7.1", "/app/kibana#/discover"},
		{"7.10.0", "7.10.0", "/app/discover#/"},
		{"7.10.0", "", "/app/kibana#/discover"},
		{"8.19.0", "", "/app/discover#/"},
	} {
		t.Run(c.fake+"/"+c.setting, func(t *testing.T) {
			e := newTestEnv(t)
			e.kb.version = c.fake
			e.vars["KIBANA_CLI_KIBANA_VERSION"] = c.setting
			r := e.run("log", "search", "--index", "legacy-log-*", "--level", "WARN", "--discover-url")
			checkLink(t, e, r, c.base, "dv-legacy")
			r = e.run("log", "search", "--data-view", "dv-legacy", "--discover-url")
			checkLink(t, e, r, c.base, "dv-legacy")
		})
	}
	// Kibana 7 lists data views with their time field: a search on another one
	// gets no link.
	e := newTestEnv(t)
	e.kb.version = "7.10.0"
	r := e.run("log", "search", "--index", "legacy-log-*", "--time-field", "event.time", "--discover-url")
	if reason := reasonOf(t, r); !strings.Contains(reason, "filters time on @timestamp but this search used event.time") {
		t.Fatalf("7.x time field: %s", reason)
	}
}

// TestDiscoverURLKeepsTheBudget: a default page of long messages still fits in
// 8,000 characters with a long link in scope (CLI-042).
func TestDiscoverURLKeepsTheBudget(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 20; i++ {
		e.kb.docs = append(e.kb.docs, fakeDoc{Index: "big-log-2026.10.25", ID: fmt.Sprintf("b%02d", i), Source: map[string]any{
			"@timestamp": fmt.Sprintf("2026-10-25T08:05:%02d.000Z", 59-i), "level": "INFO", "log_app": "order-svc",
			"msg": fmt.Sprintf(`{"event":"request","n":%d,"body":"%s"}`, i, strings.Repeat("z", 480))}})
	}
	e.kb.views = append(e.kb.views, fakeView("dv-big", "big-log-*"))
	query := strings.Repeat(`"connection reset by peer" OR `, 20) + `"timeout"`
	plain := e.run("log", "search", "--index", "big-log-*", "--query", query)
	r := e.run("log", "search", "--index", "big-log-*", "--query", query, "--discover-url")
	link, _ := r.scope()["discover_url"].(string)
	n := len(r.items())
	if r.exit != 0 || len(link) < 1200 || n == 0 || n > len(plain.items()) || utf8.RuneCountInString(strings.TrimSpace(r.out)) > 8000 {
		t.Fatalf("%d items (%d without the link), link %d, %d chars: %.300s", n, len(plain.items()), len(link), utf8.RuneCountInString(r.out), r.out)
	}
	if r.data()["has_more"] != true || num(r.data()["next_offset"]) != n || !hasString(r.notices(), "page_shortened") {
		t.Fatalf("continuation: %.600s", r.out)
	}
	checkLink(t, e, r, "/app/discover#/", "dv-big")
}

func TestPatternCovers(t *testing.T) {
	for _, c := range []struct {
		title, index string
		want         bool
	}{
		{"app-log-*", "app-log-2026.10.25", true},
		{"app-log-*", "app-log-*", true},
		{"app-*", "app-log-*", true},
		{"app-log-*", "app-*", false},
		{"*", "anything", true},
		{"a-*,b-*", "a-1,b-2", true},
		{"a-*,b-*", "a-1,c-2", false},
		{"a-*,-a-old*", "a-1", true},
		{"-a-*", "a-1", false},
		{"logs-[", "logs-[", false},
	} {
		if got := patternCovers(c.title, c.index); got != c.want {
			t.Errorf("patternCovers(%q, %q) = %v", c.title, c.index, got)
		}
	}
}

// fakeView is a data view of the fake with a time field.
func fakeView(id, title string) kibanaclient.DataView {
	return kibanaclient.DataView{ID: id, Title: title, TimeFieldName: "@timestamp"}
}
