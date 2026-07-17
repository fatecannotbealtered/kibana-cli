package kibanaclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

func TestListIndexPatterns(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
		case "/api/saved_objects/_find":
			page++
			if page == 1 {
				objs := make([]map[string]any, 100)
				for i := range objs {
					objs[i] = map[string]any{
						"id":         "id",
						"attributes": map[string]string{"title": "logs-*"},
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"saved_objects": objs})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"saved_objects": []map[string]any{
					{"id": "b", "attributes": map[string]string{"title": "app-*"}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	list, err := c.ListIndexPatterns(context.Background())
	if err != nil || len(list) != 101 {
		t.Fatalf("err=%v len=%d", err, len(list))
	}
}

func TestListIndexPatterns_emptyAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/saved_objects/_find" {
			_ = json.NewEncoder(w).Encode(map[string]any{"saved_objects": []any{}})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	list, err := c.ListIndexPatterns(context.Background())
	if err != nil || len(list) != 0 {
		t.Fatalf("empty: err=%v len=%d", err, len(list))
	}

	c2 := NewClient(&config.Config{Host: srv.URL + "/bad", Username: "u", Password: "p"})
	c2.baseURL = srv.URL
	c2.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/api/saved_objects/_find" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errReadCloser{err: errors.New("read fail")},
					Header:     make(http.Header),
				}, nil
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	_, err = c2.ListIndexPatterns(context.Background())
	if err == nil {
		t.Fatal("read error")
	}
}

func TestFindSavedObjects(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
		case "/api/saved_objects/_find":
			gotQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 2,
				"saved_objects": []map[string]any{
					{"id": "a", "type": "dashboard", "updated_at": "t1", "attributes": map[string]any{"title": "Latency", "description": "p99"}},
					{"id": "b", "type": "dashboard", "attributes": map[string]any{"title": "Errors"}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	objs, total, err := c.FindSavedObjects(context.Background(), FindSavedObjectsOptions{
		Type: "dashboard", Search: "lat", PerPage: 50,
	})
	if err != nil || total != 2 || len(objs) != 2 {
		t.Fatalf("err=%v total=%d objs=%d", err, total, len(objs))
	}
	if objs[0].Title != "Latency" || objs[0].Description != "p99" || objs[0].Type != "dashboard" {
		t.Fatalf("obj0=%+v", objs[0])
	}
	if !strings.Contains(gotQuery, "type=dashboard") || !strings.Contains(gotQuery, "search=lat") {
		t.Fatalf("query=%q", gotQuery)
	}
}

func TestFindSavedObjects_apiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, _, err := c.FindSavedObjects(context.Background(), FindSavedObjectsOptions{Type: "search"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetSavedObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/saved_objects/dashboard/abc" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "abc", "type": "dashboard", "updated_at": "t1",
				"attributes": map[string]any{"title": "Latency", "description": "p99"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	obj, full, err := c.GetSavedObject(context.Background(), "dashboard", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if obj.ID != "abc" || obj.Title != "Latency" || obj.Description != "p99" {
		t.Fatalf("obj=%+v", obj)
	}
	if full["id"] != "abc" {
		t.Fatalf("full=%v", full)
	}
}

func TestGetSavedObject_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, _, err := c.GetSavedObject(context.Background(), "dashboard", "missing"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveIndexPattern(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
		case "/api/saved_objects/index-pattern/abc":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "abc",
				"attributes": map[string]string{
					"title":         "logs-*",
					"timeFieldName": "event.created",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	view, err := c.ResolveDataView(context.Background(), "abc")
	if err != nil || view.ID != "abc" || view.Title != "logs-*" || view.TimeFieldName != "event.created" {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	title, err := c.ResolveDataViewTitle(context.Background(), "abc")
	if err != nil || title != "logs-*" {
		t.Fatalf("compat title=%q err=%v", title, err)
	}
	title, err = c.ResolveIndexPattern(context.Background(), "abc")
	if err != nil || title != "logs-*" {
		t.Fatalf("title=%q err=%v", title, err)
	}
}

func TestListIndexPatterns_ensureVersionAndDoErrors(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://127.0.0.1:1", Username: "u", Password: "p"})
	if _, err := c.ListIndexPatterns(context.Background()); err == nil {
		t.Fatal("ensure version")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/saved_objects/_find" {
			objs := make([]map[string]any, 100)
			for i := range objs {
				objs[i] = map[string]any{"id": "a", "attributes": map[string]string{"title": "t"}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"saved_objects": objs})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c2 := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	c2.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/api/saved_objects/_find" && req.URL.Query().Get("page") == "2" {
				return nil, errors.New("page 2 fail")
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	if _, err := c2.ListIndexPatterns(context.Background()); err == nil {
		t.Fatal("do error on page 2")
	}
}

func TestListIndexPatterns_apiAndUnmarshalErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/saved_objects/_find" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, err := c.ListIndexPatterns(context.Background()); err == nil {
		t.Fatal("403")
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv2.Close()
	c2 := NewClient(&config.Config{Host: srv2.URL, Username: "u", Password: "p"})
	if _, err := c2.ListIndexPatterns(context.Background()); err == nil {
		t.Fatal("unmarshal")
	}
}

func TestListIndexPatterns_invalidBaseURL(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.baseURL = "http://\n"
	if _, err := c.ListIndexPatterns(context.Background()); err == nil {
		t.Fatal("invalid URL")
	}
}

func TestResolveIndexPattern_invalidBaseURL(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.baseURL = "http://\n"
	if _, err := c.ResolveIndexPattern(context.Background(), "id"); err == nil {
		t.Fatal("invalid URL")
	}
}

func TestResolveIndexPattern_doError(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network")
		}),
	}
	if _, err := c.ResolveIndexPattern(context.Background(), "id"); err == nil {
		t.Fatal("do error")
	}
}

func TestResolveIndexPattern_ensureVersionError(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://127.0.0.1:1", Username: "u", Password: "p"})
	if _, err := c.ResolveIndexPattern(context.Background(), "id"); err == nil {
		t.Fatal("ensure version")
	}
}

func TestResolveIndexPattern_errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, err := c.ResolveIndexPattern(context.Background(), "missing"); err == nil {
		t.Fatal("404")
	}

	c2 := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	c2.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "index-pattern") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errReadCloser{err: errors.New("read fail")},
					Header:     make(http.Header),
				}, nil
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	if _, err := c2.ResolveIndexPattern(context.Background(), "x"); err == nil {
		t.Fatal("read error")
	}

	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		_, _ = w.Write([]byte(`{`))
	}))
	defer srv3.Close()
	c3 := NewClient(&config.Config{Host: srv3.URL, Username: "u", Password: "p"})
	if _, err := c3.ResolveIndexPattern(context.Background(), "x"); err == nil {
		t.Fatal("unmarshal")
	}
}

func TestResolveIndexPattern_noTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"attributes": map[string]string{}})
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	_, err := c.ResolveIndexPattern(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}
