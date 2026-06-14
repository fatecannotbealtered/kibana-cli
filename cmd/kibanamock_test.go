package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

type mockKibanaOptions struct {
	AuthFail           bool
	AuthStatus         int
	SearchProbeFail    bool
	SearchProbeStatus  int
	ProxyStatus        int
	IndexPatternFail   bool
	IndexPatternStatus int
	SearchNoHits       bool
	SearchFail         bool
	SearchFailStatus   int
	SearchTotalGte     bool
	SearchSource       map[string]any
	EmptyPatterns      bool
	EmptyFields        bool
	SavedObjectsFail   bool
	SavedObjectsStatus int
	FieldsAPIFail      bool
	FieldsAPIStatus    int
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
		if opts.SavedObjectsFail {
			status := opts.SavedObjectsStatus
			if status == 0 {
				status = http.StatusServiceUnavailable
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"saved objects unavailable"}`))
			return
		}
		if opts.EmptyPatterns {
			_ = json.NewEncoder(w).Encode(map[string]any{"saved_objects": []any{}, "total": 0})
			return
		}
		objType := r.URL.Query().Get("type")
		if objType != "" && objType != "index-pattern" {
			// objects list: return type-tagged objects with title + description.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 2,
				"saved_objects": []map[string]any{
					{
						"id":         "obj-1",
						"type":       objType,
						"updated_at": "2024-01-01T00:00:00Z",
						"attributes": map[string]any{"title": "Latency " + objType, "description": "p99 latency"},
					},
					{
						"id":         "obj-2",
						"type":       objType,
						"updated_at": "2024-01-02T00:00:00Z",
						"attributes": map[string]any{"title": "Errors " + objType},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 1,
			"saved_objects": []map[string]any{
				{
					"id":         "dv-1",
					"type":       "index-pattern",
					"attributes": map[string]any{"title": "logs-*"},
				},
			},
		})
	case strings.HasPrefix(r.URL.Path, "/api/saved_objects/index-pattern/"):
		if opts.IndexPatternFail {
			status := opts.IndexPatternStatus
			if status == 0 {
				status = http.StatusServiceUnavailable
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"index-pattern unavailable"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"attributes": map[string]any{"title": "logs-*"},
		})
	case strings.HasPrefix(r.URL.Path, "/api/saved_objects/"):
		if opts.SavedObjectsFail {
			status := opts.SavedObjectsStatus
			if status == 0 {
				status = http.StatusNotFound
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"saved object not found"}`))
			return
		}
		// /api/saved_objects/<type>/<id>
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/saved_objects/"), "/")
		objType, objID := "", ""
		if len(parts) >= 2 {
			objType, objID = parts[0], parts[1]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         objID,
			"type":       objType,
			"updated_at": "2024-01-01T00:00:00Z",
			"attributes": map[string]any{
				"title":       "Latency " + objType,
				"description": "p99 latency dashboard",
			},
		})
	case r.URL.Path == "/api/index_patterns/_fields_for_wildcard":
		if opts.FieldsAPIFail {
			status := opts.FieldsAPIStatus
			if status == 0 {
				status = http.StatusBadGateway
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"fields api unavailable"}`))
			return
		}
		if opts.EmptyFields {
			_ = json.NewEncoder(w).Encode(map[string]any{"fields": []any{}})
			return
		}
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

// mockAggResponse builds a terms or date_histogram bucket set that mirrors the
// requested aggregation, including a "metric" sub-agg value when one was asked for.
func mockAggResponse(req map[string]any) map[string]any {
	aggs, _ := req["aggs"].(map[string]any)
	termsAgg, _ := aggs["terms_agg"].(map[string]any)
	_, isDateHisto := termsAgg["date_histogram"]
	_, hasMetric := termsAgg["aggs"]

	var keys []string
	if isDateHisto {
		keys = []string{"2024-01-01T00:00:00.000Z", "2024-01-01T01:00:00.000Z"}
	} else {
		keys = []string{"ERROR", "INFO"}
	}
	counts := []int64{2, 8}
	metricVals := []float64{12.5, 3.0}
	buckets := make([]map[string]any, 0, len(keys))
	for i, k := range keys {
		b := map[string]any{"doc_count": counts[i]}
		if isDateHisto {
			b["key"] = (i + 1) * 1000
			b["key_as_string"] = k
		} else {
			b["key"] = k
		}
		if hasMetric {
			b["metric"] = map[string]any{"value": metricVals[i]}
		}
		buckets = append(buckets, b)
	}
	return map[string]any{"buckets": buckets}
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
		if opts.SearchFail {
			status := opts.SearchFailStatus
			if status == 0 {
				status = http.StatusBadGateway
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"reason":"search failed"}}`))
			return
		}
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
				"took":         3,
				"aggregations": map[string]any{"terms_agg": mockAggResponse(req)},
			})
			return
		}
		src := map[string]any{
			"@timestamp":   "2024-01-01T00:00:00Z",
			"level":        "ERROR",
			"service_name": "order-svc",
			"msg":          "timeout",
		}
		if opts.SearchSource != nil {
			src = opts.SearchSource
		}
		hits := []map[string]any{
			{"_index": "logs-2024", "_id": "1", "_source": src, "sort": []any{1704067200000, "1"}},
		}
		totalVal := 1
		if opts.SearchNoHits {
			hits = nil
			totalVal = 0
		}
		totalObj := map[string]any{"value": totalVal}
		if opts.SearchTotalGte {
			totalObj["relation"] = "gte"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 5,
			"hits": map[string]any{
				"total": totalObj,
				"hits":  hits,
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
