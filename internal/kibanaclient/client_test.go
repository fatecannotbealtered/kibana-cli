package kibanaclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errReadCloser struct{ err error }

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (errReadCloser) Close() error               { return nil }

func TestSearch_proxyAndParseErrors(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://kibana.example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	if _, err := c.Search(context.Background(), SearchOptions{Index: "logs-*"}); err == nil {
		t.Fatal("proxy error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/console/proxy" {
			_, _ = w.Write([]byte(`{`))
		}
	}))
	defer srv.Close()
	c2 := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	if _, err := c2.Search(context.Background(), SearchOptions{Index: "logs-*"}); err == nil {
		t.Fatal("parse error")
	}
}

func TestSearch_defaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"took": 0,
			"hits": map[string]any{"total": 0, "hits": []any{}},
		})
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if _, err := c.Search(context.Background(), SearchOptions{Index: "logs-*"}); err != nil {
		t.Fatal(err)
	}
}

func TestProxy_Search(t *testing.T) {
	var searchBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			if r.URL.Path == "/api/console/proxy" && r.URL.Query().Get("path") == "logs-*/_search" {
				payload, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(payload, &searchBody)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"took": 1,
					"hits": map[string]any{
						"total": map[string]any{"value": 0, "relation": "eq"},
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
	res, err := c.Search(context.Background(), SearchOptions{Index: "logs-*", Size: 1, Offset: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 || res.TotalRelation != "eq" {
		t.Fatalf("total=%d totalRelation=%q", res.Total, res.TotalRelation)
	}
	if searchBody["track_total_hits"] != true {
		t.Fatalf("search body missing track_total_hits: %v", searchBody)
	}
	if searchBody["from"] != float64(5) {
		t.Fatalf("search body missing offset: %v", searchBody)
	}
}

func TestEnsureVersion_readBodyError(t *testing.T) {
	readErr := errors.New("read failed")
	c := NewClient(&config.Config{Host: "http://unused.example", Username: "u", Password: "p"})
	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errReadCloser{err: readErr},
				Header:     make(http.Header),
			}, nil
		}),
	}
	err := c.EnsureVersion(context.Background())
	if err == nil || !errors.Is(err, readErr) {
		t.Fatalf("got err=%v want %v", err, readErr)
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

func TestNewClient_trimsHostAndVersion(t *testing.T) {
	c := NewClient(&config.Config{
		Host:          "http://kibana.example///",
		Username:      "u",
		Password:      "p",
		KibanaVersion: " 8.2.0 ",
	})
	if c.baseURL != "http://kibana.example" || c.kibanaVersion != "8.2.0" {
		t.Fatalf("baseURL=%q version=%q", c.baseURL, c.kibanaVersion)
	}
}

func TestNewClient_insecureExistingTLSConfig(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{}}
	defer func() { http.DefaultTransport = old }()

	SetClientOptions(ClientOptions{InsecureSkipVerify: true})
	defer SetClientOptions(ClientOptions{Timeout: 60 * time.Second})

	c := NewClient(&config.Config{Host: "https://kibana.example", Username: "u", Password: "p"})
	if !c.httpClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify not set on existing TLS config")
	}
}

func TestSetClientOptions_and_NewClient(t *testing.T) {
	SetClientOptions(ClientOptions{Timeout: 0, InsecureSkipVerify: true})
	if CurrentClientOptions().Timeout != 60*time.Second || !CurrentClientOptions().InsecureSkipVerify {
		t.Fatal("zero timeout defaults to 60s and insecure")
	}
	SetClientOptions(ClientOptions{Timeout: 30 * time.Second})
	defer SetClientOptions(ClientOptions{Timeout: 60 * time.Second})

	c := NewClient(&config.Config{Host: "https://kibana.example", Username: "u", Password: "p", KibanaVersion: "8.1.0"})
	if c.kibanaVersion != "8.1.0" || c.baseURL != "https://kibana.example" {
		t.Fatalf("client: %+v", c)
	}
	h := c.proxyHeaders()
	if h.Get("kbn-version") != "8.1.0" || h.Get("Authorization") == "" {
		t.Fatalf("headers: %v", h)
	}
}

func TestProxyHeaders_customUserAgent(t *testing.T) {
	t.Setenv("KIBANA_CLI_USER_AGENT", "test-agent/1")
	c := NewClient(&config.Config{Host: "http://x", Username: "u", Password: "p"})
	if c.proxyHeaders().Get("User-Agent") != "test-agent/1" {
		t.Fatal("custom UA")
	}
}

func TestEnsureVersion_presetAndFailures(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://unused", Username: "u", Password: "p", KibanaVersion: "7.0.0"})
	if err := c.EnsureVersion(context.Background()); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c2 := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	if err := c2.EnsureVersion(context.Background()); err == nil {
		t.Fatal("expected 401 error")
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv2.Close()
	c3 := NewClient(&config.Config{Host: srv2.URL, Username: "u", Password: "p"})
	if err := c3.EnsureVersion(context.Background()); err == nil {
		t.Fatal("expected missing version")
	}
}

func TestProxy_errorsAndGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	data, err := c.Proxy(context.Background(), http.MethodGet, "/_cluster/health", nil)
	if err != nil || string(data) == "" {
		t.Fatalf("GET proxy: data=%s err=%v", data, err)
	}

	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "console/proxy") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errReadCloser{err: errors.New("proxy read")},
					Header:     make(http.Header),
				}, nil
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	_, err = c.Proxy(context.Background(), http.MethodPost, "logs-*/_search", map[string]any{"size": 0})
	if err == nil {
		t.Fatal("read error")
	}

	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "console/proxy") {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"error":{"reason":"bad query"}}`)),
					Header:     make(http.Header),
				}, nil
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	_, err = c.Proxy(context.Background(), http.MethodPost, "x/_search", map[string]any{})
	if err == nil {
		t.Fatal("400 error")
	}
}

func TestAuthenticate_unmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	_, err := c.Authenticate(context.Background())
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestValidate_searchGenericError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/console/proxy" && r.URL.Query().Get("path") == "_security/_authenticate" {
			_ = json.NewEncoder(w).Encode(map[string]any{"username": "u"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/api/console/proxy" && req.URL.Query().Get("path") == "*/_search" {
				return nil, errors.New("search down")
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	vr, err := c.Validate(context.Background())
	if err != nil || !vr.Valid || vr.SearchReachable || vr.SearchStatusCode != 0 {
		t.Fatalf("%+v err=%v", vr, err)
	}
}

func TestValidate_searchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
		case "/api/console/proxy":
			path := r.URL.Query().Get("path")
			if path == "_security/_authenticate" {
				_ = json.NewEncoder(w).Encode(map[string]any{"username": "u"})
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"no index"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	vr, err := c.Validate(context.Background())
	if err != nil || !vr.Valid || vr.SearchReachable || vr.SearchStatusCode != http.StatusForbidden {
		t.Fatalf("%+v err=%v", vr, err)
	}
}

func TestKibanaStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"overall": map[string]any{"level": "available"}}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	st, err := c.KibanaStatus(context.Background())
	if err != nil || st["status"] == nil {
		t.Fatalf("status=%v err=%v", st, err)
	}

	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("down")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	_, err = c.KibanaStatus(context.Background())
	if err == nil {
		t.Fatal("503")
	}
}

func TestSearch_sortDescAndHits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
			return
		}
		if r.URL.Path == "/api/console/proxy" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 3,
				"hits": map[string]any{
					"total": 1,
					"hits": []map[string]any{
						{
							"_index": "logs-1",
							"_id":    "1",
							"_source": map[string]any{
								"@timestamp": "2024-01-01T00:00:00Z",
								"msg":        "hi",
							},
						},
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	res, err := c.Search(context.Background(), SearchOptions{
		Index: "logs-*", Size: 0, SortDesc: true, To: "now",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Timestamp == "" || res.Total != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestCount_successAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/console/proxy" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"took": 1,
				"hits": map[string]any{
					"total": map[string]any{"value": 7, "relation": "eq"},
					"hits":  []any{},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	total, err := c.Count(context.Background(), SearchOptions{Index: "logs-*"})
	if err != nil || total != 7 {
		t.Fatalf("total=%d err=%v", total, err)
	}

	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader(`{"message":"down"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	if _, err := c.Count(context.Background(), SearchOptions{Index: "logs-*"}); err == nil {
		t.Fatal("expected proxy error")
	}

	srvBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{`))
	}))
	defer srvBadJSON.Close()
	c2 := NewClient(&config.Config{Host: srvBadJSON.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	if _, err := c2.Count(context.Background(), SearchOptions{Index: "logs-*"}); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestListFieldsForIndexPattern(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "8.0.0"}})
		case "/api/index_patterns/_fields_for_wildcard":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"fields": []map[string]any{{"name": "msg", "type": "string", "searchable": true}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p"})
	list, err := c.ListFieldsForIndexPattern(context.Background(), "logs-*")
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}

	_, err = c.ListFieldsForIndexPattern(context.Background(), "")
	if err == nil {
		t.Fatal("empty pattern")
	}
}

func TestEnsureVersion_invalidBaseURL(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://example", Username: "u", Password: "p"})
	c.baseURL = "http://\n"
	if err := c.EnsureVersion(context.Background()); err == nil {
		t.Fatal("invalid URL")
	}
}

func TestEnsureVersion_networkError(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://127.0.0.1:1", Username: "u", Password: "p"})
	if err := c.EnsureVersion(context.Background()); err == nil {
		t.Fatal("connection error")
	}
}

func TestProxyHeaders_defaultUserAgent(t *testing.T) {
	_ = os.Unsetenv("KIBANA_CLI_USER_AGENT")
	c := NewClient(&config.Config{Host: "http://x", Username: "u", Password: "p"})
	if c.proxyHeaders().Get("User-Agent") != "kibana-cli" {
		t.Fatal("default UA")
	}
}

func TestProxy_invalidBaseURL(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.baseURL = "http://\n"
	if _, err := c.Proxy(context.Background(), http.MethodPost, "x/_search", nil); err == nil {
		t.Fatal("invalid URL")
	}
}

func TestProxy_marshalAndDoErrors(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://kibana.example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	_, err := c.Proxy(context.Background(), http.MethodPost, "x/_search", map[string]any{"bad": make(chan int)})
	if err == nil {
		t.Fatal("marshal error")
	}

	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("do failed")
		}),
	}
	_, err = c.Proxy(context.Background(), http.MethodPost, "x/_search", map[string]any{"size": 0})
	if err == nil {
		t.Fatal("do error")
	}
}

func TestValidate_authenticateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/console/proxy" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	vr, err := c.Validate(context.Background())
	if err == nil || vr.Valid {
		t.Fatalf("vr=%+v err=%v", vr, err)
	}
}

func TestValidate_ensureVersionError(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://127.0.0.1:1", Username: "u", Password: "p"})
	vr, err := c.Validate(context.Background())
	if err == nil || vr.Valid {
		t.Fatalf("vr=%+v err=%v", vr, err)
	}
}

func TestAuthenticate_proxyError(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://127.0.0.1:1", Username: "u", Password: "p"})
	_, err := c.Authenticate(context.Background())
	if err == nil {
		t.Fatal("proxy error")
	}
}

func TestKibanaStatus_invalidBaseURL(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.baseURL = "http://\n"
	if _, err := c.KibanaStatus(context.Background()); err == nil {
		t.Fatal("invalid URL")
	}
}

func TestKibanaStatus_errors(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://kibana.example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network")
		}),
	}
	if _, err := c.KibanaStatus(context.Background()); err == nil {
		t.Fatal("do error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()
	c2 := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	if _, err := c2.KibanaStatus(context.Background()); err == nil {
		t.Fatal("unmarshal error")
	}
}

func TestListFieldsForIndexPattern_invalidBaseURL(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.baseURL = "http://\n"
	if _, err := c.ListFieldsForIndexPattern(context.Background(), "logs-*"); err == nil {
		t.Fatal("invalid URL")
	}
}

func TestListFieldsForIndexPattern_ensureVersionError(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://127.0.0.1:1", Username: "u", Password: "p"})
	if _, err := c.ListFieldsForIndexPattern(context.Background(), "logs-*"); err == nil {
		t.Fatal("ensure version")
	}
}

func TestListFieldsForIndexPattern_errors(t *testing.T) {
	c := NewClient(&config.Config{Host: "http://kibana.example", Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c.httpClient = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network")
		}),
	}
	if _, err := c.ListFieldsForIndexPattern(context.Background(), "logs-*"); err == nil {
		t.Fatal("do error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/index_patterns/_fields_for_wildcard" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c2 := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	if _, err := c2.ListFieldsForIndexPattern(context.Background(), "logs-*"); err == nil {
		t.Fatal("403")
	}

	c3 := NewClient(&config.Config{Host: srv.URL, Username: "u", Password: "p", KibanaVersion: "8.0.0"})
	c3.httpClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/api/index_patterns/_fields_for_wildcard" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errReadCloser{err: errors.New("read fail")},
					Header:     make(http.Header),
				}, nil
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
	if _, err := c3.ListFieldsForIndexPattern(context.Background(), "logs-*"); err == nil {
		t.Fatal("read error")
	}
}
