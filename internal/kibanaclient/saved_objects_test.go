package kibanaclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const routeMissingBody = `{"statusCode":404,"error":"Not Found","message":"Not Found"}`

// kibanaAPIs answers the Kibana APIs of a 7.x (legacy) or 9.x Kibana: each has only
// its own routes, so the client must pick or fall back.
func kibanaAPIs(t *testing.T, rc *recorder, major int) string {
	return rc.server(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		legacy := major < 8
		switch p := r.URL.Path; {
		case p == "/api/status":
			v := "7.10.2"
			if !legacy {
				v = "9.5.2"
			}
			_, _ = w.Write([]byte(`{"version":{"number":"` + v + `"}}`))
		case p == "/api/data_views" && !legacy:
			_, _ = w.Write([]byte(`{"data_view":[{"id":"dv1","title":"logs-*","name":"Logs"}]}`))
		case p == "/api/data_views/data_view/dv1" && !legacy:
			_, _ = w.Write([]byte(`{"data_view":{"id":"dv1","title":"logs-*","name":"Logs","timeFieldName":"event.time","fields":{}}}`))
		case strings.HasPrefix(p, "/api/data_views/data_view/") && !legacy:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"statusCode":404,"error":"Not Found","message":"Saved object [index-pattern/nope] not found"}`))
		case p == "/api/saved_objects/_find" && legacy:
			if r.URL.Query().Get("type") == "index-pattern" {
				_, _ = w.Write([]byte(`{"total":1,"saved_objects":[{"id":"dv1","attributes":{"title":"logs-*","timeFieldName":"@timestamp"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"total":7,"saved_objects":[{"id":"d1","type":"dashboard","updated_at":"2026-10-01T00:00:00.000Z","attributes":{"title":"Errors","description":"p99"},"references":[{"type":"visualization","id":"v1","name":"panel_0"}]}]}`))
		case p == "/api/saved_objects/index-pattern/dv1" && legacy:
			_, _ = w.Write([]byte(`{"id":"dv1","attributes":{"title":"logs-*","timeFieldName":"@timestamp"}}`))
		case p == "/api/saved_objects/dashboard/d1":
			_, _ = w.Write([]byte(`{"id":"d1","type":"dashboard","attributes":{"title":"Errors","panelsJSON":"[]"},"references":[]}`))
		case p == "/api/index_patterns/_fields_for_wildcard" && legacy:
			_, _ = w.Write([]byte(`{"fields":[{"name":"msg","type":"string","searchable":true,"aggregatable":false},{"name":"@timestamp","type":"date","searchable":true,"aggregatable":true}]}`))
		case p == "/internal/data_views/_fields_for_wildcard" && !legacy:
			if r.Header.Get("Elastic-Api-Version") != "1" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"statusCode":400,"error":"Bad Request","message":"Please specify a version via elastic-api-version header. Available versions: 1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"fields":[{"name":"level","type":"string","esTypes":["keyword"],"searchable":true,"aggregatable":true}]}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(routeMissingBody))
		}
	}).URL
}

func TestDataViewsOnKibana7And9(t *testing.T) {
	for _, major := range []int{7, 9} {
		var rc recorder
		c := testClient(kibanaAPIs(t, &rc, major))
		views, err := c.ListDataViews(context.Background())
		if err != nil || len(views) != 1 || views[0].ID != "dv1" || views[0].Title != "logs-*" {
			t.Fatalf("Kibana %d: %+v %v", major, views, err)
		}
		v, err := c.ResolveDataView(context.Background(), "dv1")
		if err != nil || v.Title != "logs-*" || v.TimeFieldName == "" {
			t.Fatalf("Kibana %d resolve: %+v %v", major, v, err)
		}
		fields, err := c.ListFields(context.Background(), "logs-*")
		if err != nil || len(fields) == 0 {
			t.Fatalf("Kibana %d fields: %+v %v", major, fields, err)
		}
		if major == 7 && fields[0].Name != "@timestamp" {
			t.Fatalf("fields are sorted: %+v", fields)
		}
	}
}

// TestKnownVersionPicksTheRouteFirst: once the version is known, the client asks
// the route of that version first, without a 404 round trip.
func TestKnownVersionPicksTheRouteFirst(t *testing.T) {
	var rc recorder
	url := kibanaAPIs(t, &rc, 9)
	c := New(Config{Host: url, KibanaVersion: "9.5.2"})
	if _, err := c.ListFields(context.Background(), "logs-*"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveDataView(context.Background(), "dv1"); err != nil {
		t.Fatal(err)
	}
	if len(rc.reqs) != 2 || rc.reqs[0].URL.Path != "/internal/data_views/_fields_for_wildcard" {
		t.Fatalf("requests: %d %s", len(rc.reqs), rc.reqs[0].URL.Path)
	}
	var rc7 recorder
	c7 := New(Config{Host: kibanaAPIs(t, &rc7, 7), KibanaVersion: "7.10.2"})
	if _, err := c7.ListDataViews(context.Background()); err != nil || len(rc7.reqs) != 1 || rc7.reqs[0].URL.Path != "/api/saved_objects/_find" {
		t.Fatalf("7.x list: %v %d", err, len(rc7.reqs))
	}
}

func TestUnknownDataView(t *testing.T) {
	var rc recorder
	c := testClient(kibanaAPIs(t, &rc, 9))
	if _, err := c.ResolveDataView(context.Background(), "nope"); !errors.Is(err, ErrNoDataView) {
		t.Fatalf("%v", err)
	}
}

func TestSavedObjects(t *testing.T) {
	var rc recorder
	c := testClient(kibanaAPIs(t, &rc, 7))
	objs, total, err := c.FindSavedObjects(context.Background(), FindSavedObjectsOptions{Type: "dashboard", Search: "err", Page: 2, PerPage: 5})
	if err != nil || total != 7 || len(objs) != 1 || objs[0].Title != "Errors" || objs[0].Description != "p99" || objs[0].References[0].ID != "v1" {
		t.Fatalf("%+v %d %v", objs, total, err)
	}
	q := rc.reqs[0].URL.Query()
	if q.Get("search") != "err" || q.Get("search_fields") != "title" || q.Get("page") != "2" || q.Get("per_page") != "5" {
		t.Fatalf("query: %s", rc.reqs[0].URL.RawQuery)
	}
	o, err := c.GetSavedObject(context.Background(), "dashboard", "d1")
	if err != nil || o.Title != "Errors" || !strings.Contains(string(o.Attributes), "panelsJSON") {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := c.GetSavedObject(context.Background(), "dashboard", "missing"); err == nil {
		t.Fatal("missing object")
	}
}

func TestParseFieldsForWildcard(t *testing.T) {
	list, err := parseFieldsForWildcard([]byte(`{"fields":{"b":{"type":"string"},"a":{"name":"a","type":"date"}}}`))
	if err != nil || len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Fatalf("%+v %v", list, err)
	}
	if list, err := parseFieldsForWildcard([]byte(`{"fields":null}`)); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("null: %+v %v", list, err)
	}
	for _, bad := range []string{`{`, `{"fields":7}`} {
		if _, err := parseFieldsForWildcard([]byte(bad)); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
}
