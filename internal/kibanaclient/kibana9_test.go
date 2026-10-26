package kibanaclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// kibana9Proxy answers /api/console/proxy the way Kibana 9.5.2 does (ops report,
// 2026-10): the outer method must be POST (GET is 404), kbn-xsrf is required, and
// the internal-API restriction refuses a call without x-elastic-internal-origin.
func kibana9Proxy(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			// Redacted, as for an account without monitoring privileges.
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}})
		case "/api/console/proxy":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"statusCode":404,"error":"Not Found","message":"Not Found"}`))
				return
			}
			if r.Header.Get("kbn-xsrf") == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"statusCode":400,"error":"Bad Request","message":"Request must contain a kbn-xsrf header."}`))
				return
			}
			if r.Header.Get("x-elastic-internal-origin") == "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"statusCode":400,"error":"Bad Request","message":"uri [/api/console/proxy] with method [post] exists but is not available with the current configuration"}`))
				return
			}
			_, _ = w.Write([]byte(`{"took":1,"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"app-log-1","_id":"a1","_source":{"@timestamp":"2026-10-23T05:00:00Z","msg":"ok"}}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestSearchWorksOnKibana9 is the regression test for the Kibana 9 outage: 1.x sent
// only kbn-xsrf, so every search failed with the internal-API restriction's 400.
func TestSearchWorksOnKibana9(t *testing.T) {
	srv := kibana9Proxy(t)
	defer srv.Close()
	c := testClient(srv.URL)
	res, err := c.Search(context.Background(), SearchOptions{Index: "app-log-*"})
	if err != nil {
		t.Fatalf("search on Kibana 9: %v", err)
	}
	if res.Total != 1 || len(res.Hits) != 1 {
		t.Fatalf("hits: %+v", res)
	}
	h := c.headers()
	if h.Get("x-elastic-internal-origin") != "kibana" || h.Get("kbn-xsrf") != "true" {
		t.Fatalf("headers: %v", h)
	}
}

// TestKibana9FakeRefusesWithoutTheHeader keeps the fake honest: without the header it
// answers the restriction's 400, as Kibana 9 does.
func TestKibana9FakeRefusesWithoutTheHeader(t *testing.T) {
	srv := kibana9Proxy(t)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/console/proxy?path=x%2F_search&method=POST", strings.NewReader(`{}`))
	req.Header.Set("kbn-xsrf", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var apiErr *APIError
	if err := parseAPIError(400, []byte(`{"statusCode":400,"error":"Bad Request","message":"uri [/api/console/proxy] with method [post] exists but is not available with the current configuration"}`)); !errors.As(error(err), &apiErr) ||
		!strings.Contains(apiErr.Message, "not available with the current configuration") {
		t.Fatalf("parse: %v", err)
	}
}
