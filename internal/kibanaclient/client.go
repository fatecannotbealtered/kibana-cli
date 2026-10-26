// Package kibanaclient talks to Kibana for kibana-cli: Elasticsearch searches through
// the Console Proxy, and the Kibana APIs for status, data views, fields and saved
// objects. Every request is a read. The client sends nothing but GET requests to
// Kibana's own APIs and POST /api/console/proxy calls whose Elasticsearch request is
// either GET _security/_authenticate or POST <index>/_search (proxyAllowed); nothing
// else can be sent through it.
package kibanaclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxTimeout is the longest per-request timeout (CLI-110).
const MaxTimeout = 30 * time.Second

// maxRetries is how often a failed read is retried (CLI-110): at most twice.
const maxRetries = 2

// maxRetryAfter is the longest Retry-After the client waits for; a longer one is
// returned to the caller instead.
const maxRetryAfter = 10 * time.Second

// maxBody caps a response body (a search page with large documents fits easily).
const maxBody = 64 << 20

// Config is what a client needs. Transport carries the proxy, TLS and CA settings
// (aicli's App.HTTPTransport); Timeout bounds each request.
type Config struct {
	Host, User, Password string
	// KibanaVersion skips GET /api/status when the caller knows the version.
	KibanaVersion string
	Transport     http.RoundTripper
	Timeout       time.Duration
	UserAgent     string
	// Debugf receives one line per request (method, path, status, duration), never
	// a header or a body.
	Debugf func(format string, args ...any)
	// Sleep waits between retries; nil waits on a timer that honours ctx.
	Sleep func(ctx context.Context, d time.Duration)
}

// Client is one run's connection to Kibana.
type Client struct {
	cfg  Config
	http *http.Client

	mu         sync.Mutex
	status     *Status
	statusErr  error
	statusDone bool
	// version is the Kibana version once VersionInfo found it, and versionSource
	// where from; versionDone records that it looked.
	version, versionSource string
	versionDone            bool
}

// New builds a client.
func New(cfg Config) *Client {
	cfg.Host = strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if cfg.Timeout <= 0 || cfg.Timeout > MaxTimeout {
		cfg.Timeout = MaxTimeout
	}
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "kibana-cli"
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout, Transport: cfg.Transport}}
}

// Host is the Kibana base URL the client talks to.
func (c *Client) Host() string { return c.cfg.Host }

// headers are sent with every request. kbn-xsrf satisfies Kibana's XSRF check for
// POST; x-elastic-internal-origin opens Kibana 9's internal APIs, the Console Proxy
// among them, which otherwise answer 400 "... exists but is not available with the
// current configuration"; Kibana 7.x and 8.x ignore it. No kbn-version header: it is
// optional, and a stale one makes Kibana answer 400 "Browser client is out of date".
func (c *Client) headers() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.cfg.User+":"+c.cfg.Password)))
	h.Set("kbn-xsrf", "true")
	h.Set("x-elastic-internal-origin", "kibana")
	h.Set("Accept", "application/json")
	h.Set("User-Agent", c.cfg.UserAgent)
	return h
}

func (c *Client) debugf(format string, args ...any) {
	if c.cfg.Debugf != nil {
		c.cfg.Debugf(format, args...)
	}
}

// get sends GET <host><path>?<query> to a Kibana API.
func (c *Client) get(ctx context.Context, endpoint, path string, query url.Values, extra http.Header) ([]byte, error) {
	return c.do(ctx, endpoint, http.MethodGet, path, query, nil, extra)
}

// do sends one request, retrying reads at most twice after a connection failure or
// HTTP 429, 502, 503 or 504 (CLI-110). Every request this client makes is a read,
// the Console Proxy's POST included (proxyAllowed admits only searches).
func (c *Client) do(ctx context.Context, endpoint, method, path string, query url.Values, body []byte, extra http.Header) ([]byte, error) {
	target := c.cfg.Host + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		data, err := c.once(ctx, endpoint, method, target, path, query, body, extra)
		if err == nil {
			return data, nil
		}
		lastErr = err
		wait, retry := retryAfter(err, attempt)
		if !retry {
			return nil, lastErr
		}
		c.debugf("retrying %s %s in %d ms", method, path, wait.Milliseconds())
		if c.cfg.Sleep != nil {
			c.cfg.Sleep(ctx, wait)
		} else {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, &NetworkError{Err: ctx.Err(), Canceled: errors.Is(ctx.Err(), context.Canceled), Timeout: errors.Is(ctx.Err(), context.DeadlineExceeded)}
			case <-t.C:
			}
		}
	}
}

// retryAfter decides whether a failed attempt is retried and after how long.
func retryAfter(err error, attempt int) (time.Duration, bool) {
	if attempt >= maxRetries {
		return 0, false
	}
	backoff := time.Duration(250*(attempt+1)) * time.Millisecond
	var ne *NetworkError
	if errors.As(err, &ne) {
		return backoff, ne.Refused
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch ae.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			if ae.RetryAfter > maxRetryAfter {
				return 0, false
			}
			if ae.RetryAfter > 0 {
				return ae.RetryAfter, true
			}
			return backoff, true
		}
	}
	return 0, false
}

func (c *Client) once(ctx context.Context, endpoint, method, target, path string, query url.Values, body []byte, extra http.Header) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		return nil, &NetworkError{Err: err}
	}
	req.Header = c.headers()
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.debugf("%s %s -> no answer (%d ms)", method, debugPath(path, query), time.Since(start).Milliseconds())
		return nil, classifyNetError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	c.debugf("%s %s -> %d (%d ms)", method, debugPath(path, query), resp.StatusCode, time.Since(start).Milliseconds())
	if err != nil {
		return nil, classifyNetError(ctx, err)
	}
	if resp.StatusCode >= 400 {
		ae := parseAPIError(resp.StatusCode, data)
		ae.Endpoint = endpoint
		ae.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, ae
	}
	return data, nil
}

// debugPath is the request line a debug message shows: the path and the query
// (an Elasticsearch path and method for the Console Proxy), no headers.
func debugPath(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// NetworkError is a request that got no HTTP answer. Refused marks a connection
// that was never made (DNS, refused, reset before the request), the one kind a read
// retries; TLS a certificate Kibana presented that is not trusted.
type NetworkError struct {
	Err                             error
	Timeout, TLS, Canceled, Refused bool
}

func (e *NetworkError) Error() string { return e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }

func classifyNetError(ctx context.Context, err error) *NetworkError {
	ne := &NetworkError{Err: err}
	var netErr net.Error
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	var verifyErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		ne.Canceled = true
	case errors.As(err, &unknownCA), errors.As(err, &hostErr), errors.As(err, &certInvalid), errors.As(err, &verifyErr):
		ne.TLS = true
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		ne.Timeout = true
	default:
		var opErr *net.OpError
		var dnsErr *net.DNSError
		ne.Refused = errors.As(err, &dnsErr) || (errors.As(err, &opErr) && opErr.Op == "dial")
	}
	return ne
}

// ---------------------------------------------------------------- Console Proxy

// searchPath is the only Elasticsearch path the client searches: an index list (the
// characters config.ValidateIndexTarget allows) followed by /_search.
var searchPath = regexp.MustCompile(`^[A-Za-z0-9._*+:,-]+/_search$`)

// proxyAllowed is the read-only guard of the Console Proxy: GET / (Elasticsearch's
// version), GET _security/_authenticate (who the credentials belong to) and POST
// <index>/_search. Anything else is refused before a request is built.
func proxyAllowed(esMethod, esPath string) bool {
	switch esMethod {
	case http.MethodGet:
		return esPath == "" || esPath == "_security/_authenticate"
	case http.MethodPost:
		return searchPath.MatchString(esPath)
	}
	return false
}

// ErrNotARead is a Console Proxy request the read-only guard refused.
var ErrNotARead = errors.New("kibana-cli sends only searches through the Console Proxy")

// Proxy sends an Elasticsearch request through Kibana's Console Proxy: always POST
// /api/console/proxy (Kibana 9 answers 404 to GET) with the Elasticsearch path and
// method as query parameters.
func (c *Client) Proxy(ctx context.Context, esMethod, esPath string, body any) ([]byte, error) {
	esMethod = strings.ToUpper(strings.TrimSpace(esMethod))
	esPath = strings.TrimPrefix(strings.TrimSpace(esPath), "/")
	if !proxyAllowed(esMethod, esPath) {
		return nil, ErrNotARead
	}
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	// The path keeps 1.x's form (no leading slash), which Kibana 7 to 9 accept;
	// Elasticsearch's root is "/".
	path := esPath
	if path == "" {
		path = "/"
	}
	q := url.Values{}
	q.Set("path", path)
	q.Set("method", esMethod)
	data, err := c.do(ctx, EndpointConsoleProxy, http.MethodPost, "/api/console/proxy", q, raw, nil)
	if err != nil {
		return nil, err
	}
	// Kibana 9's Console Proxy answers 200 when Elasticsearch refused the request and
	// carries Elasticsearch's error and status in the body (live check on 9.5.2,
	// 2026-10); Kibana 7 passes the status through.
	if status, ok := wrappedESError(data); ok {
		ae := parseAPIError(status, data)
		ae.Endpoint = EndpointConsoleProxy
		return nil, ae
	}
	return data, nil
}

// wrappedESError reports the status of an Elasticsearch error carried in a 2xx proxy
// answer. A successful Elasticsearch answer never has a top-level error object.
func wrappedESError(data []byte) (int, bool) {
	if !bytes.Contains(data, []byte(`"error"`)) {
		return 0, false
	}
	var v struct {
		Error  json.RawMessage `json:"error"`
		Status int             `json:"status"`
	}
	if json.Unmarshal(data, &v) != nil || len(v.Error) == 0 || v.Error[0] != '{' {
		return 0, false
	}
	if v.Status < 400 {
		return http.StatusInternalServerError, true
	}
	return v.Status, true
}

// AuthenticateUser is Elasticsearch's _security/_authenticate answer.
type AuthenticateUser struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	FullName string   `json:"full_name"`
}

// Authenticate asks Elasticsearch, through the Console Proxy, whom the credentials
// belong to.
func (c *Client) Authenticate(ctx context.Context) (*AuthenticateUser, error) {
	data, err := c.Proxy(ctx, http.MethodGet, "_security/_authenticate", nil)
	if err != nil {
		return nil, err
	}
	var u AuthenticateUser
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, fmt.Errorf("parsing _security/_authenticate: %w", err)
	}
	return &u, nil
}

// ---------------------------------------------------------------- status

// Status is the part of GET /api/status kibana-cli uses. Kibana 8 and 9 redact the
// answer for an account without monitoring privileges to the overall level: Version
// is then "" (live check on 9.5.2, 2026-10).
type Status struct {
	Version string // version.number
	Name    string
	Overall string // status.overall.level (8+) or .state (7.x)
}

// Status reads GET /api/status once per client.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.statusDone {
		return c.status, c.statusErr
	}
	data, err := c.get(ctx, EndpointStatus, "/api/status", nil, nil)
	if err == nil {
		var raw struct {
			Name    string `json:"name"`
			Version struct {
				Number string `json:"number"`
			} `json:"version"`
			Status struct {
				Overall struct {
					Level string `json:"level"`
					State string `json:"state"`
				} `json:"overall"`
			} `json:"status"`
		}
		if jerr := json.Unmarshal(data, &raw); jerr != nil {
			err = fmt.Errorf("parsing /api/status: %w", jerr)
		} else {
			overall := raw.Status.Overall.Level
			if overall == "" {
				overall = raw.Status.Overall.State
			}
			c.status = &Status{Version: raw.Version.Number, Name: raw.Name, Overall: overall}
		}
	}
	c.statusErr, c.statusDone = err, true
	return c.status, err
}

// Version sources (VersionInfo).
const (
	VersionFromSetting       = "setting"
	VersionFromKibanaStatus  = "kibana_status"
	VersionFromElasticsearch = "elasticsearch"
	VersionUnknown           = "unknown"
)

// VersionInfo is the Kibana version and where it came from: the configured version,
// else GET /api/status's version.number, else Elasticsearch's (GET / through the
// Console Proxy; Kibana and Elasticsearch share their version), else unknown. It
// never fails: Kibana 8 and 9 hide the version from accounts without monitoring
// privileges, and every command works without it (the APIs are picked by trying
// them). It reads at most once per client.
func (c *Client) VersionInfo(ctx context.Context) (version, source string) {
	if v := strings.TrimSpace(c.cfg.KibanaVersion); v != "" {
		return v, VersionFromSetting
	}
	c.mu.Lock()
	done, v, src := c.versionDone, c.version, c.versionSource
	c.mu.Unlock()
	if done {
		return v, src
	}
	v, src = "", VersionUnknown
	if st, err := c.Status(ctx); err == nil && strings.TrimSpace(st.Version) != "" {
		v, src = strings.TrimSpace(st.Version), VersionFromKibanaStatus
	} else if es, err := c.elasticsearchVersion(ctx); err == nil && es != "" {
		v, src = es, VersionFromElasticsearch
	} else if err != nil {
		c.debugf("version: Elasticsearch did not say (%v)", err)
	}
	c.mu.Lock()
	c.version, c.versionSource, c.versionDone = v, src, true
	c.mu.Unlock()
	return v, src
}

// elasticsearchVersion is version.number of Elasticsearch's GET /, through the
// Console Proxy. It needs the monitor cluster privilege, which a read-only account
// may lack.
func (c *Client) elasticsearchVersion(ctx context.Context) (string, error) {
	data, err := c.Proxy(ctx, http.MethodGet, "", nil)
	if err != nil {
		return "", err
	}
	var raw struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("parsing Elasticsearch's GET /: %w", err)
	}
	return strings.TrimSpace(raw.Version.Number), nil
}

// major is the Kibana major version when the client knows it without a request
// (the configured version or an earlier lookup), else 0: the APIs are then tried
// newest first, falling back on a missing route.
func (c *Client) major() int {
	v := strings.TrimSpace(c.cfg.KibanaVersion)
	if v == "" {
		c.mu.Lock()
		switch {
		case c.version != "":
			v = c.version
		case c.status != nil:
			v = c.status.Version
		}
		c.mu.Unlock()
	}
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}
