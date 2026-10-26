package kibanaclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClient is a client for a test server, with no waiting between retries.
func testClient(url string) *Client {
	return New(Config{Host: url, User: "u", Password: "p", Timeout: 5 * time.Second,
		Sleep: func(context.Context, time.Duration) {}})
}

// recorder is a test server that records each request and answers with handler.
type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
}

func (rc *recorder) server(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, n int)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.reqs = append(rc.reqs, r.Clone(context.Background()))
		rc.body = append(rc.body, string(b))
		n := len(rc.reqs)
		rc.mu.Unlock()
		handler(w, r, n)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEveryRequestCarriesTheKibanaHeaders(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"name":"k","version":{"number":"9.5.2"},"status":{"overall":{"level":"available"}}}`))
		case "/api/data_views":
			_, _ = w.Write([]byte(`{"data_view":[{"id":"dv1","title":"logs-*","name":"Logs"}]}`))
		default:
			_, _ = w.Write([]byte(`{"took":1,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`))
		}
	})
	c := New(Config{Host: srv.URL + "/", User: "agent", Password: "s3cret", UserAgent: "kibana-cli/2.0.0 aicli/2.0.1"})
	ctx := context.Background()
	if st, err := c.Status(ctx); err != nil || st.Version != "9.5.2" || st.Overall != "available" {
		t.Fatalf("status: %+v %v", st, err)
	}
	if _, err := c.ListDataViews(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(ctx, SearchOptions{Index: "logs-*"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range rc.reqs {
		if r.Header.Get("x-elastic-internal-origin") != "kibana" || r.Header.Get("kbn-xsrf") != "true" ||
			r.Header.Get("User-Agent") != "kibana-cli/2.0.0 aicli/2.0.1" || r.Header.Get("kbn-version") != "" {
			t.Fatalf("%s %s headers: %v", r.Method, r.URL.Path, r.Header)
		}
		if u, p, ok := r.BasicAuth(); !ok || u != "agent" || p != "s3cret" {
			t.Fatalf("basic auth on %s", r.URL.Path)
		}
	}
	proxy := rc.reqs[2]
	if proxy.Method != http.MethodPost || proxy.URL.Query().Get("path") != "logs-*/_search" || proxy.URL.Query().Get("method") != "POST" {
		t.Fatalf("console proxy request: %s %s", proxy.Method, proxy.URL)
	}
}

// TestProxyOnlySearches: the read-only guard refuses every Elasticsearch request but
// _security/_authenticate and <index>/_search, before anything is sent.
func TestProxyOnlySearches(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) { _, _ = w.Write([]byte(`{}`)) })
	c := testClient(srv.URL)
	for _, req := range [][2]string{
		{"DELETE", "logs-1"}, {"PUT", "logs-1/_doc/1"}, {"POST", "logs-1/_delete_by_query"}, {"POST", "logs-1/_update_by_query"},
		{"POST", "_bulk"}, {"GET", "logs-1/_search"}, {"POST", "logs-1/_search?scroll=1m"}, {"POST", "a/b/_search"},
		{"POST", "_search/scroll"}, {"GET", "_cat/indices"}, {"POST", "logs#x/_search"},
	} {
		if _, err := c.Proxy(context.Background(), req[0], req[1], nil); !errors.Is(err, ErrNotARead) {
			t.Errorf("%s %s: %v", req[0], req[1], err)
		}
	}
	if len(rc.reqs) != 0 {
		t.Fatalf("%d requests were sent", len(rc.reqs))
	}
	for _, req := range [][2]string{{"GET", "_security/_authenticate"}, {"POST", "logs-*/_search"}, {"POST", "a-*,b-*/_search"}} {
		if _, err := c.Proxy(context.Background(), req[0], req[1], map[string]any{}); err != nil {
			t.Errorf("%s %s: %v", req[0], req[1], err)
		}
	}
}

func TestReadsRetryOnlyTransientFailures(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"took":1,"hits":{"total":0,"hits":[]}}`))
	})
	c := testClient(srv.URL)
	if _, err := c.Search(context.Background(), SearchOptions{Index: "x"}); err != nil || len(rc.reqs) != 2 {
		t.Fatalf("503 then 200: %v, %d requests", err, len(rc.reqs))
	}

	var rc2 recorder
	srv2 := rc2.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Retry-After", "11")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := testClient(srv2.URL).Search(context.Background(), SearchOptions{Index: "x"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 429 || ae.RetryAfter != 11*time.Second || len(rc2.reqs) != 1 {
		t.Fatalf("a Retry-After over 10 s is not waited for: %v, %d requests", err, len(rc2.reqs))
	}

	var rc3 recorder
	srv3 := rc3.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(http.StatusBadGateway) })
	if _, err := testClient(srv3.URL).Search(context.Background(), SearchOptions{Index: "x"}); err == nil || len(rc3.reqs) != 3 {
		t.Fatalf("at most two retries: %v, %d requests", err, len(rc3.reqs))
	}

	var rc4 recorder
	srv4 := rc4.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(http.StatusBadRequest) })
	if _, err := testClient(srv4.URL).Search(context.Background(), SearchOptions{Index: "x"}); err == nil || len(rc4.reqs) != 1 {
		t.Fatalf("a 400 is not retried: %v, %d requests", err, len(rc4.reqs))
	}

	// A refused connection is retried (nothing was sent), and reported as one.
	c5 := testClient("http://127.0.0.1:1")
	_, err = c5.Search(context.Background(), SearchOptions{Index: "x"})
	var ne *NetworkError
	if !errors.As(err, &ne) || !ne.Refused || ne.Timeout || ne.TLS {
		t.Fatalf("refused: %#v", err)
	}
}

func TestNetworkErrorsAreClassified(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer slow.Close()
	c := New(Config{Host: slow.URL, Timeout: 50 * time.Millisecond})
	_, err := c.Status(context.Background())
	var ne *NetworkError
	if !errors.As(err, &ne) || !ne.Timeout {
		t.Fatalf("timeout: %#v", err)
	}

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer tlsSrv.Close()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	_, err = New(Config{Host: tlsSrv.URL, Transport: tr}).Status(context.Background())
	if !errors.As(err, &ne) || !ne.TLS {
		t.Fatalf("an untrusted certificate: %#v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = testClient(slow.URL).Status(ctx)
	if !errors.As(err, &ne) || !ne.Canceled {
		t.Fatalf("canceled: %#v", err)
	}
}

func TestAPIErrorsAreRecognised(t *testing.T) {
	cases := []struct {
		status                 int
		body                   string
		route, internal, xsrf  bool
		esType, upstreamPrefix string
	}{
		{404, `{"statusCode":404,"error":"Not Found","message":"Not Found"}`, true, false, false, "", "Not Found"},
		{404, `{"statusCode":404,"error":"Not Found","message":"Saved object [index-pattern/x] not found"}`, false, false, false, "", "Saved object"},
		{404, `{"error":{"root_cause":[{"type":"index_not_found_exception","reason":"no such index [x]"}],"type":"index_not_found_exception","reason":"no such index [x]"},"status":404}`, false, false, false, "index_not_found_exception", "no such index"},
		{400, `{"statusCode":400,"error":"Bad Request","message":"uri [/api/console/proxy] with method [post] exists but is not available with the current configuration"}`, false, true, false, "", "uri ["},
		{400, `{"statusCode":400,"error":"Bad Request","message":"Request must contain a kbn-xsrf header."}`, false, false, true, "", "Request must"},
		{502, `<html>bad gateway</html>`, false, false, false, "", "<html>"},
	}
	for _, tc := range cases {
		e := parseAPIError(tc.status, []byte(tc.body))
		if e.RouteMissing != tc.route || e.InternalOriginRefused != tc.internal || e.XSRFRefused != tc.xsrf || e.ESType != tc.esType ||
			!strings.HasPrefix(e.Upstream(), tc.upstreamPrefix) {
			t.Errorf("%d %s: %+v", tc.status, tc.body, e)
		}
	}
	long := parseAPIError(500, []byte(strings.Repeat("x", 2000)))
	if len(long.Body) > 520 || !strings.HasSuffix(long.Body, "…") {
		t.Fatalf("a long body is cut: %d", len(long.Body))
	}
}

func TestStatusIsReadOnceAndConfiguredVersionSkipsIt(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		_, _ = w.Write([]byte(`{"version":{"number":"7.10.2"},"status":{"overall":{"state":"green"}}}`))
	})
	c := testClient(srv.URL)
	for i := 0; i < 3; i++ {
		if v, src := c.VersionInfo(context.Background()); v != "7.10.2" || src != VersionFromKibanaStatus {
			t.Fatal(v, src)
		}
	}
	if len(rc.reqs) != 1 || c.major() != 7 {
		t.Fatalf("%d status reads, major %d", len(rc.reqs), c.major())
	}
	c2 := New(Config{Host: srv.URL, KibanaVersion: "8.15.0"})
	if v, src := c2.VersionInfo(context.Background()); v != "8.15.0" || src != VersionFromSetting || len(rc.reqs) != 1 || c2.major() != 8 {
		t.Fatalf("configured version: %s from %s, %d reads", v, src, len(rc.reqs))
	}
}

// TestVersionWhenStatusIsRedacted: Kibana 8 and 9 answer GET /api/status with only
// the overall level for an account without monitoring privileges (live check on
// 9.5.2). The version then comes from Elasticsearch's GET / through the Console
// Proxy, and when that is refused too it is unknown, never an error.
func TestVersionWhenStatusIsRedacted(t *testing.T) {
	for _, tc := range []struct {
		name          string
		esRoot        func(w http.ResponseWriter)
		version, from string
	}{
		{"elasticsearch answers", func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"name":"es01","version":{"number":"9.5.2"},"tagline":"You Know, for Search"}`))
		}, "9.5.2", VersionFromElasticsearch},
		{"elasticsearch refuses", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"type":"security_exception","reason":"action [cluster:monitor/main] is unauthorized"},"status":403}`))
		}, "", VersionUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rc recorder
			srv := rc.server(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				switch r.URL.Path {
				case "/api/status":
					_, _ = w.Write([]byte(`{"status":{"overall":{"level":"available"}}}`))
				case "/api/console/proxy":
					if r.URL.Query().Get("path") != "/" || r.URL.Query().Get("method") != "GET" {
						t.Errorf("proxy query: %s", r.URL.RawQuery)
					}
					tc.esRoot(w)
				}
			})
			c := testClient(srv.URL)
			v, src := c.VersionInfo(context.Background())
			if v != tc.version || src != tc.from {
				t.Fatalf("version %q from %s", v, src)
			}
			if again, _ := c.VersionInfo(context.Background()); again != v || len(rc.reqs) != 2 {
				t.Fatalf("not cached: %d requests", len(rc.reqs))
			}
			for _, r := range rc.reqs {
				if r.Header.Get("kbn-version") != "" {
					t.Fatalf("kbn-version sent: %v", r.Header)
				}
			}
		})
	}
}

func TestAuthenticate(t *testing.T) {
	var rc recorder
	srv := rc.server(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		_ = json.NewEncoder(w).Encode(map[string]any{"username": "agent", "roles": []string{"viewer"}})
	})
	u, err := testClient(srv.URL).Authenticate(context.Background())
	if err != nil || u.Username != "agent" || u.Roles[0] != "viewer" {
		t.Fatal(u, err)
	}
	if q := rc.reqs[0].URL.Query(); q.Get("path") != "_security/_authenticate" || q.Get("method") != "GET" || rc.reqs[0].Method != "POST" {
		t.Fatalf("request: %s", rc.reqs[0].URL)
	}
}
