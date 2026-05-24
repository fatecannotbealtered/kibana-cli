package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

type mockKibanaOptions struct {
	AuthFail         bool
	AuthStatus       int
	SearchProbeFail  bool
	SearchProbeStatus int
	ProxyStatus      int
}

// newMockKibanaServer returns a minimal Kibana API stub (Console Proxy + saved objects).
func newMockKibanaServer() *httptest.Server {
	return newMockKibanaServerWith(mockKibanaOptions{})
}

func newMockKibanaServerWith(opts mockKibanaOptions) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockKibanaHandlerWith(w, r, opts)
	}))
}

func mockKibanaHandler(w http.ResponseWriter, r *http.Request) {
	mockKibanaHandlerWith(w, r, mockKibanaOptions{})
}

func mockKibanaHandlerWith(w http.ResponseWriter, r *http.Request, opts mockKibanaOptions) {
	switch {
	case r.URL.Path == "/api/status":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": map[string]any{"number": "7.10.0"},
		})
	case r.URL.Path == "/api/console/proxy":
		path := r.URL.Query().Get("path")
		method := strings.ToUpper(r.URL.Query().Get("method"))
		mockKibanaProxyWith(w, r, method, path, opts)
	case r.URL.Path == "/api/saved_objects/_find":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"saved_objects": []map[string]any{
				{
					"id": "dv-1",
					"attributes": map[string]any{
						"title": "logs-*",
					},
				},
			},
		})
	case strings.HasPrefix(r.URL.Path, "/api/saved_objects/index-pattern/"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attributes": map[string]any{"title": "logs-*"},
		})
	case r.URL.Path == "/api/index_patterns/_fields_for_wildcard":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"fields": []map[string]any{
				{"name": "@timestamp", "type": "date", "searchable": true, "aggregatable": true},
				{"name": "level", "type": "string", "searchable": true, "aggregatable": true},
				{"name": "msg", "type": "string", "searchable": true, "aggregatable": false},
			},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func mockKibanaProxy(w http.ResponseWriter, r *http.Request, method, path string) {
	mockKibanaProxyWith(w, r, method, path, mockKibanaOptions{})
}

func mockKibanaProxyWith(w http.ResponseWriter, r *http.Request, method, path string, opts mockKibanaOptions) {
	if opts.ProxyStatus >= 400 {
		w.WriteHeader(opts.ProxyStatus)
		_, _ = w.Write([]byte(`{"message":"proxy error"}`))
		return
	}
	switch {
	case path == "_security/_authenticate" && method == http.MethodGet:
		if opts.AuthFail {
			status := opts.AuthStatus
			if status == 0 {
				status = http.StatusUnauthorized
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"username": "agent",
			"roles":    []string{"viewer"},
		})
	case strings.HasSuffix(path, "/_search") && method == http.MethodPost:
		if path == "*/_search" && opts.SearchProbeFail {
			status := opts.SearchProbeStatus
			if status == 0 {
				status = http.StatusForbidden
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"reason":"forbidden"}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if _, hasAgg := req["aggs"]; hasAgg {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 3,
				"aggregations": map[string]any{
					"terms_agg": map[string]any{
						"buckets": []map[string]any{
							{"key": "ERROR", "doc_count": 2},
							{"key": "INFO", "doc_count": 8},
						},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 5,
			"hits": map[string]any{
				"total": map[string]any{"value": 1},
				"hits": []map[string]any{
					{
						"_index": "logs-2024",
						"_id":    "1",
						"_source": map[string]any{
							"@timestamp":   "2024-01-01T00:00:00Z",
							"level":        "ERROR",
							"service_name": "order-svc",
							"msg":          "timeout",
						},
					},
				},
			},
		})
	case path == "*/_search" && method == http.MethodPost:
		if opts.SearchProbeFail {
			status := opts.SearchProbeStatus
			if status == 0 {
				status = http.StatusForbidden
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"reason":"forbidden"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 1,
			"hits": map[string]any{"total": map[string]any{"value": 0}, "hits": []any{}},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
