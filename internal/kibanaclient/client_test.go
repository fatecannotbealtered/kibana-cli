package kibanaclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

func TestProxy_Search(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			if r.URL.Path == "/api/console/proxy" && r.URL.Query().Get("path") == "logs-*/_search" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"took": 1,
					"hits": map[string]any{
						"total": map[string]any{"value": 0},
						"hits":  []any{},
					},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "7.10.0"}})
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	res, err := c.Search(context.Background(), SearchOptions{Index: "logs-*", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Fatalf("total=%d", res.Total)
	}
}

func TestValidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "7.7.1"}})
		case "/api/console/proxy":
			path := r.URL.Query().Get("path")
			switch path {
			case "_security/_authenticate":
				_ = json.NewEncoder(w).Encode(map[string]any{"username": "dev_ro"})
			case "*/_search":
				_, _ = io.ReadAll(r.Body)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"hits": map[string]any{"total": 0, "hits": []any{}},
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	vr, err := c.Validate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !vr.Valid || vr.Username != "dev_ro" || !strings.HasPrefix(vr.KibanaVersion, "7.") {
		t.Fatalf("%+v", vr)
	}
	if !vr.SearchReachable {
		t.Fatal("expected search reachable")
	}
}
