package kibanaclient

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

const emptyHits = `{"took":1,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`

// TestSearchSendsBuildSearchBody: the body Search sends is BuildSearchBody's (the
// dry-run-free preview a command shows), newest first, exact totals, ISO bounds.
func TestSearchSendsBuildSearchBody(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) { _, _ = w.Write([]byte(emptyHits)) })
	opts := SearchOptions{Index: "logs-*", QueryClause: map[string]any{"term": map[string]any{"msg.keyword": "exact"}},
		From: "2026-10-23T04:45:00Z", To: "2026-10-23T05:00:00Z", SortDesc: true, Size: 5, Offset: 10}
	if _, err := testClient(srv.URL).Search(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(BuildSearchBody(opts))
	var a, b map[string]any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal([]byte(rc.body[0]), &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got  %s\nwant %s", rc.body[0], want)
	}
	if b["from"] != float64(10) || b["size"] != float64(5) || b["track_total_hits"] != true {
		t.Fatalf("page: %s", rc.body[0])
	}
	sort := b["sort"].([]any)[0].(map[string]any)["@timestamp"].(map[string]any)
	if sort["order"] != "desc" || sort["unmapped_type"] != "boolean" {
		t.Fatalf("sort: %v", sort)
	}
	rng := b["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)[0].(map[string]any)["range"].(map[string]any)["@timestamp"].(map[string]any)
	if rng["gte"] != "2026-10-23T04:45:00Z" || rng["format"] != "strict_date_optional_time" {
		t.Fatalf("range: %v", rng)
	}
}

func TestSearchRawAndCount(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		_, _ = w.Write([]byte(`{"took":7,"hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"logs-2026","_id":"1","_source":{"msg":"x"}}]}}`))
	})
	c := testClient(srv.URL)
	res, err := c.SearchRaw(context.Background(), "logs-*", map[string]any{"query": map[string]any{"match_all": map[string]any{}}, "size": 5})
	if err != nil || res.Total != 3 || len(res.Hits) != 1 || res.TookMs != 7 {
		t.Fatalf("%+v %v", res, err)
	}
	if p := rc.reqs[0].URL.Query().Get("path"); p != "logs-*/_search" {
		t.Fatalf("path %q", p)
	}
	if _, err := c.SearchRaw(context.Background(), "", map[string]any{}); err != nil || rc.reqs[1].URL.Query().Get("path") != "*/_search" {
		t.Fatalf("default index: %v %s", err, rc.reqs[1].URL)
	}
	n, err := c.Count(context.Background(), SearchOptions{Index: "logs-*"})
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rc.body[2]), &body)
	if body["size"] != float64(0) {
		t.Fatalf("count body: %s", rc.body[2])
	}
}

func TestGetDoc(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			_, _ = w.Write([]byte(`{"hits":{"total":1,"hits":[{"_index":"logs-1","_id":"a/b","_source":{"msg":"x"}}]}}`))
			return
		}
		_, _ = w.Write([]byte(emptyHits))
	})
	c := testClient(srv.URL)
	hit, err := c.GetDoc(context.Background(), "logs-1", "a/b")
	if err != nil || hit == nil || hit.ID != "a/b" || hit.Source["msg"] != "x" {
		t.Fatalf("%+v %v", hit, err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(rc.body[0]), &body)
	if ids := body["query"].(map[string]any)["ids"].(map[string]any)["values"].([]any); ids[0] != "a/b" {
		t.Fatalf("ids query: %s", rc.body[0])
	}
	if hit, err := c.GetDoc(context.Background(), "logs-1", "zz"); hit != nil || err != nil {
		t.Fatalf("missing doc: %+v %v", hit, err)
	}
}
