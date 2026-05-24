package kibanaclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

var globalHTTP = ClientOptions{Timeout: 60 * time.Second}

// ClientOptions configures HTTP transport.
type ClientOptions struct {
	Timeout            time.Duration
	InsecureSkipVerify bool
}

// SetClientOptions updates global HTTP settings (from cmd flags).
func SetClientOptions(o ClientOptions) {
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	globalHTTP = o
}

// Client performs log queries via Kibana Console Proxy.
type Client struct {
	baseURL       string
	username      string
	password      string
	kibanaVersion string
	httpClient    *http.Client
}

// NewClient builds a Kibana proxy client from config.
func NewClient(cfg *config.Config) *Client {
	opts := globalHTTP
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if opts.InsecureSkipVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec
	}
	return &Client{
		baseURL:       strings.TrimRight(cfg.Host, "/"),
		username:      cfg.Username,
		password:      cfg.Password,
		kibanaVersion: strings.TrimSpace(cfg.KibanaVersion),
		httpClient: &http.Client{
			Timeout:   opts.Timeout,
			Transport: transport,
		},
	}
}

func (c *Client) proxyHeaders() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.username+":"+c.password)))
	h.Set("kbn-xsrf", "true")
	h.Set("Accept", "application/json")
	if c.kibanaVersion != "" {
		h.Set("kbn-version", c.kibanaVersion)
	}
	if ua := strings.TrimSpace(os.Getenv("KIBANA_CLI_USER_AGENT")); ua != "" {
		h.Set("User-Agent", ua)
	} else {
		h.Set("User-Agent", "kibana-cli")
	}
	return h
}

// EnsureVersion loads Kibana version from GET /api/status when unset.
func (c *Client) EnsureVersion(ctx context.Context) error {
	if c.kibanaVersion != "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/status", nil)
	if err != nil {
		return err
	}
	h := c.proxyHeaders()
	req.Header = h
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return parseAPIError(resp.StatusCode, data)
	}
	var st struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if json.Unmarshal(data, &st) == nil && st.Version.Number != "" {
		c.kibanaVersion = st.Version.Number
	}
	if c.kibanaVersion == "" {
		return fmt.Errorf("could not detect Kibana version from /api/status; set KIBANA_CLI_KIBANA_VERSION")
	}
	return nil
}

// Proxy forwards a search API path through Kibana Console Proxy.
func (c *Client) Proxy(ctx context.Context, esMethod, esPath string, body any) ([]byte, error) {
	if err := c.EnsureVersion(ctx); err != nil {
		return nil, err
	}
	esMethod = strings.ToUpper(strings.TrimSpace(esMethod))
	esPath = strings.TrimPrefix(strings.TrimSpace(esPath), "/")
	proxyURL := fmt.Sprintf("%s/api/console/proxy?path=%s&method=%s",
		c.baseURL, url.QueryEscape(esPath), url.QueryEscape(esMethod))
	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header = c.proxyHeaders()
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, data)
	}
	return data, nil
}

// AuthenticateUser is the ES security authenticate payload.
type AuthenticateUser struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	FullName string   `json:"full_name"`
}

// Authenticate verifies credentials via Console Proxy.
func (c *Client) Authenticate(ctx context.Context) (*AuthenticateUser, error) {
	data, err := c.Proxy(ctx, http.MethodGet, "_security/_authenticate", nil)
	if err != nil {
		return nil, err
	}
	var u AuthenticateUser
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ValidateResult is returned by Doctor-style checks.
type ValidateResult struct {
	Valid            bool
	Username         string
	KibanaVersion    string
	SearchReachable  bool
	SearchError      string
	SearchStatusCode int
}

// Validate checks auth and a minimal search against Kibana proxy.
func (c *Client) Validate(ctx context.Context) (*ValidateResult, error) {
	out := &ValidateResult{}
	if err := c.EnsureVersion(ctx); err != nil {
		return out, err
	}
	out.KibanaVersion = c.kibanaVersion
	user, err := c.Authenticate(ctx)
	if err != nil {
		return out, err
	}
	out.Valid = true
	out.Username = user.Username
	_, searchErr := c.Proxy(ctx, http.MethodPost, "*/_search", map[string]any{
		"size":  0,
		"query": map[string]any{"match_all": map[string]any{}},
	})
	out.SearchReachable = searchErr == nil
	if searchErr != nil {
		out.SearchError = searchErr.Error()
		var apiErr *APIError
		if errors.As(searchErr, &apiErr) {
			out.SearchStatusCode = apiErr.StatusCode
		}
	}
	return out, nil
}

// KibanaStatus is GET /api/status (no proxy).
func (c *Client) KibanaStatus(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/status", nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.proxyHeaders()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, data)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
