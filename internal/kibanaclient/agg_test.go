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

func aggTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(handler))
}

func TestClient_Terms_success(t *testing.T) {
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 2,
				"hits": map[string]any{"total": 5},
				"aggregations": map[string]any{
					"terms_agg": map[string]any{
						"buckets": []map[string]any{
							{"key": "ERROR", "doc_count": 3},
							{"key": 42, "doc_count": 1},
							{"key": float64(3.5), "doc_count": 1},
						},
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	res, err := c.Terms(context.Background(), AggOptions{Index: "logs-*", TermsField: "level", BucketSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buckets) != 3 || res.Field != "level" || res.Total != 5 {
		t.Fatalf("%+v", res)
	}
}

func TestClient_Terms_keywordRetry(t *testing.T) {
	var calls int
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"reason":"text fields aggregation"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 1,
				"hits": map[string]any{"total": 0},
				"aggregations": map[string]any{
					"terms_agg": map[string]any{"buckets": []any{}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	res, err := c.Terms(context.Background(), AggOptions{Index: "logs-*", TermsField: "level"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Field != "level (.keyword)" || calls != 2 {
		t.Fatalf("field=%q calls=%d", res.Field, calls)
	}
}

func TestClient_Terms_proxyError(t *testing.T) {
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	_, err := c.Terms(context.Background(), AggOptions{Index: "logs-*", TermsField: "level"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClient_termsOnce_defaults(t *testing.T) {
	var body map[string]any
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			payload, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(payload, &body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 0,
				"hits": map[string]any{"total": 0},
				"aggregations": map[string]any{
					"terms_agg": map[string]any{"buckets": []any{}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	_, err := c.Terms(context.Background(), AggOptions{Index: "/logs-*/", BucketSize: 0})
	if err != nil {
		t.Fatal(err)
	}
	aggs := body["aggs"].(map[string]any)["terms_agg"].(map[string]any)["terms"].(map[string]any)
	if aggs["size"].(float64) != 10 {
		t.Fatalf("bucket size: %v", aggs["size"])
	}
}

func TestBuildAggBody_matchesInitialRequest(t *testing.T) {
	var bodies []map[string]any
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			var body map[string]any
			payload, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(payload, &body)
			bodies = append(bodies, body)
			if len(bodies) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"reason":"text fields aggregation"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"hits": map[string]any{"total": 0},
				"aggregations": map[string]any{
					"terms_agg": map[string]any{"buckets": []any{}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	opts := AggOptions{
		Index:       "logs-*",
		TermsField:  "service",
		QueryClause: map[string]any{"term": map[string]any{"msg.keyword": "exact"}},
		From:        "now-1h",
		To:          "now",
	}
	want := BuildAggBody(opts)
	if want["track_total_hits"] != true {
		t.Fatalf("aggregation total must be exact: %#v", want)
	}
	wantJSON, _ := json.Marshal(want)
	var normalizedWant map[string]any
	_ = json.Unmarshal(wantJSON, &normalizedWant)
	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, err := c.Aggregate(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected initial request plus .keyword retry, got %d", len(bodies))
	}
	if !reflect.DeepEqual(bodies[0], normalizedWant) {
		t.Fatalf("initial request body differs from preview\ngot:  %#v\nwant: %#v", bodies[0], normalizedWant)
	}
	retryTerms := bodies[1]["aggs"].(map[string]any)["terms_agg"].(map[string]any)["terms"].(map[string]any)
	if retryTerms["field"] != "service.keyword" {
		t.Fatalf("retry field=%v", retryTerms["field"])
	}
}

func TestClient_Aggregate_dateHistogramWithMetric(t *testing.T) {
	var body map[string]any
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			payload, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(payload, &body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 4,
				"hits": map[string]any{"total": 10},
				"aggregations": map[string]any{
					"terms_agg": map[string]any{
						"buckets": []map[string]any{
							{"key": 1000, "key_as_string": "2024-01-01T00:00:00.000Z", "doc_count": 6, "metric": map[string]any{"value": 12.5}},
							{"key": 2000, "key_as_string": "2024-01-01T01:00:00.000Z", "doc_count": 4, "metric": map[string]any{"value": 3.0}},
						},
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	res, err := c.Aggregate(context.Background(), AggOptions{
		Index: "logs-*", AggType: AggTypeDateHistogram, Interval: " 1h ",
		Metric: "avg", MetricField: "took_ms",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.AggType != AggTypeDateHistogram || res.Metric != "avg" || len(res.Buckets) != 2 {
		t.Fatalf("%+v", res)
	}
	if res.Buckets[0].Key != "2024-01-01T00:00:00.000Z" || res.Buckets[0].Metric == nil || *res.Buckets[0].Metric != 12.5 {
		t.Fatalf("bucket0=%+v", res.Buckets[0])
	}
	// Request must carry a date_histogram with the interval and a metric sub-agg.
	agg := body["aggs"].(map[string]any)["terms_agg"].(map[string]any)
	dh, ok := agg["date_histogram"].(map[string]any)
	if !ok || dh["fixed_interval"] != "1h" {
		t.Fatalf("date_histogram body: %v", agg)
	}
	if _, ok := agg["aggs"].(map[string]any)["metric"]; !ok {
		t.Fatalf("missing metric sub-agg: %v", agg)
	}
}

func TestClient_Aggregate_termsWithMetricKeywordRetry(t *testing.T) {
	var calls int
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"reason":"text fields aggregation"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 1,
				"hits": map[string]any{"total": 2},
				"aggregations": map[string]any{
					"terms_agg": map[string]any{
						"buckets": []map[string]any{
							{"key": "order-svc", "doc_count": 2, "metric": map[string]any{"value": 7.0}},
						},
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	res, err := c.Aggregate(context.Background(), AggOptions{
		Index: "logs-*", AggType: AggTypeTerms, TermsField: "service",
		Metric: "sum", MetricField: "took_ms",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Field != "service (.keyword)" || calls != 2 || res.Buckets[0].Metric == nil {
		t.Fatalf("field=%q calls=%d buckets=%+v", res.Field, calls, res.Buckets)
	}
}

func TestClient_Terms_keywordRetryFails(t *testing.T) {
	srv := aggTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"reason":"text fields aggregation"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	_, err := c.Terms(context.Background(), AggOptions{Index: "logs-*", TermsField: "level.keyword"})
	if err == nil {
		t.Fatal("expected error without retry")
	}
}
