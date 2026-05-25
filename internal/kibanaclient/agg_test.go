package kibanaclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
