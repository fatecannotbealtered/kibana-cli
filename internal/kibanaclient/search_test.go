package kibanaclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

func TestClient_SearchRaw_passthrough(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		path = r.URL.Query().Get("path")
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 7,
			"hits": map[string]any{
				"total": map[string]any{"value": 1, "relation": "eq"},
				"hits": []map[string]any{
					{"_index": "logs-2024", "_id": "1", "_source": map[string]any{"msg": "x"}, "sort": []any{123, "1"}},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	res, err := c.SearchRaw(context.Background(), "logs-*", map[string]any{
		"query": map[string]any{"match_all": map[string]any{}},
		"size":  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || len(res.Hits) != 1 || res.TookMs != 7 {
		t.Fatalf("%+v", res)
	}
	if path != "logs-*/_search" {
		t.Fatalf("path=%q", path)
	}
	if len(res.LastSort) != 2 {
		t.Fatalf("expected sort cursor: %+v", res.LastSort)
	}
}

func TestClient_SearchRaw_defaultIndex(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		path = r.URL.Query().Get("path")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 1,
			"hits": map[string]any{"total": map[string]any{"value": 0}, "hits": []any{}},
		})
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, err := c.SearchRaw(context.Background(), "", map[string]any{"query": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if path != "*/_search" {
		t.Fatalf("path=%q", path)
	}
}

func TestClient_Search_searchAfterCursor(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 1,
			"hits": map[string]any{"total": map[string]any{"value": 0}, "hits": []any{}},
		})
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	_, err := c.Search(context.Background(), SearchOptions{
		Index: "logs-*", Size: 10, SortDesc: true, TimeField: "@timestamp",
		Offset: 99, SearchAfter: []any{123, "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// search_after present, from omitted, and an _id tiebreaker appended to sort.
	if _, hasFrom := body["from"]; hasFrom {
		t.Fatalf("from must be omitted with cursor: %v", body)
	}
	if _, ok := body["search_after"]; !ok {
		t.Fatalf("missing search_after: %v", body)
	}
	sort, _ := body["sort"].([]any)
	if len(sort) != 2 {
		t.Fatalf("expected 2 sort keys (time + _id): %v", sort)
	}
}

func TestBuildSearchBody_matchesRequest(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &got)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits": map[string]any{"total": map[string]any{"value": 0}, "hits": []any{}},
		})
	}))
	defer srv.Close()

	opts := SearchOptions{
		Index:       "logs-*",
		QueryClause: map[string]any{"term": map[string]any{"msg.keyword": "exact"}},
		From:        "now-1h",
		To:          "now",
		SortDesc:    true,
	}
	want := BuildSearchBody(opts)
	wantJSON, _ := json.Marshal(want)
	var normalizedWant map[string]any
	_ = json.Unmarshal(wantJSON, &normalizedWant)
	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, err := c.Search(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, normalizedWant) {
		t.Fatalf("request body differs from preview\ngot:  %#v\nwant: %#v", got, normalizedWant)
	}
}
