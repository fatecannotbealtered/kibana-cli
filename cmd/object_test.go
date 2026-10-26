package cmd

import (
	"encoding/json"
	"testing"
)

func TestObjectList(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("object", "list", "--type", "dashboard")
	items := r.items()
	if r.exit != 0 || len(items) != 2 || num(r.data()["total"]) != 2 || r.data()["sort"] != "upstream" || items[0]["ref"] != "dashboard/db-1" ||
		items[0]["title"] != "Payments overview" || items[0]["updated_at"] != "2026-10-20T10:00:00Z" {
		t.Fatalf("list: %s", r.out)
	}
	if _, has := items[0]["references"]; has {
		t.Fatalf("references are not a default field of the list: %v", items[0])
	}
	if !hasString(r.untrusted(), "data.items[].title") {
		t.Fatalf("untrusted: %v", r.untrusted())
	}
	if r = e.run("object", "list", "--type", "dashboard", "--query", "pay*"); len(r.items()) != 1 || r.scope()["query"] != "pay*" {
		t.Fatalf("query: %s", r.out)
	}
	// 1.x's --search is renamed --query (CLI-028).
	if r = e.run("objects", "list", "--type", "dashboard", "--search", "gate"); len(r.items()) != 1 || !hasString(r.notices(), "deprecated") {
		t.Fatalf("1.x --search: %s", r.out)
	}
	// An offset on a page boundary reads one page; another reads the first
	// offset+limit and keeps the end.
	if r = e.run("object", "list", "--type", "dashboard", "--limit", "1", "--offset", "1"); len(r.items()) != 1 || r.items()[0]["ref"] != "dashboard/db-2" ||
		r.data()["has_more"] != false {
		t.Fatalf("second page: %s", r.out)
	}
	if r = e.run("object", "list", "--type", "dashboard", "--limit", "1"); r.data()["has_more"] != true || num(r.data()["next_offset"]) != 1 {
		t.Fatalf("first page: %s", r.out)
	}
	if r = e.run("object", "list", "--type", "index-pattern"); len(r.items()) != 3 {
		t.Fatalf("data views as saved objects: %s", r.out)
	}
	if r = e.run("object", "list", "--type", "canvas"); r.code() != "E_USAGE" {
		t.Fatalf("unknown type: %s", r.out)
	}
	if r = e.run("object", "list"); r.code() != "E_USAGE" || r.details()["parameter"] != "--type" {
		t.Fatalf("no type: %s", r.out)
	}
}

func TestObjectGet(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("object", "get", "dashboard/db-1")
	d := r.data()
	refs := listOf(d["references"])
	if r.exit != 0 || d["ref"] != "dashboard/db-1" || d["description"] != "Errors and latency" || len(refs) != 1 ||
		refs[0].(map[string]any)["ref"] != "visualization/vis-1" {
		t.Fatalf("get: %s", r.out)
	}
	if _, has := d["attributes"]; has {
		t.Fatalf("attributes are not a default field: %v", d)
	}
	r = e.run("object", "get", "search/s-1", "--fields", "+attributes")
	attrs := listOf(r.data()["attributes"])
	if len(attrs) != 2 || attrs[0].(map[string]any)["name"] != "kibanaSavedObjectMeta.searchSourceJSON" || !hasString(r.untrusted(), "data.attributes") {
		t.Fatalf("attributes: %s", r.out)
	}
	raw := e.run("object", "get", "dashboard/db-1", "--format", "raw")
	var doc map[string]any
	if raw.exit != 0 || json.Unmarshal([]byte(raw.out), &doc) != nil || doc["attributes"].(map[string]any)["panelsJSON"] == nil {
		t.Fatalf("raw: %s", raw.out)
	}
	// 1.x's --type and --id, and the show alias.
	if r = e.run("object", "show", "--type", "dashboard", "--id", "db-2"); r.exit != 0 || r.data()["title"] != "Gateway" {
		t.Fatalf("--type --id: %s", r.out)
	}
	if r = e.run("object", "get", "data-view/dv-app"); r.exit != 0 || r.data()["ref"] != "index-pattern/dv-app" {
		t.Fatalf("data view as a saved object: %s", r.out)
	}
	if r = e.run("object", "get", "dashboard/nope"); r.code() != "E_NOT_FOUND" || r.message() != "Kibana has no dashboard with that id, or this account cannot see it." {
		t.Fatalf("missing: %s", r.out)
	}
	for _, bad := range []string{"db-1", "canvas/x", "dashboard/"} {
		if r = e.run("object", "get", bad); r.code() != "E_VALIDATION" {
			t.Errorf("%q: %s", bad, r.out)
		}
	}
	if r = e.run("object", "get"); r.code() != "E_USAGE" {
		t.Fatalf("no ref: %s", r.out)
	}
	if r = e.run("object", "get", "--type", "dashboard"); r.code() != "E_USAGE" {
		t.Fatalf("--type without --id: %s", r.out)
	}
}
