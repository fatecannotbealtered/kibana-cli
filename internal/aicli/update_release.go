// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bytes"
	"context"
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
	"time"
)

// Release discovery and downloads for update (CLI-070) with the network behaviour
// of CLI-110: a 30 s timeout per request (without progress), the proxy from the
// environment, TLS always verified with <PREFIX>_CA_FILE added to the system
// roots, a User-Agent naming the tool, GITHUB_TOKEN (else GH_TOKEN) sent only to
// the API host, and size caps on everything read.

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Body       string    `json:"body"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// asset finds a release asset by name.
func (r *ghRelease) asset(name string) *ghAsset {
	if r == nil {
		return nil
	}
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// fetchError is a failed GET: an HTTP answer outside 2xx (status), a timeout, an
// answer over its size cap, or a connection failure.
type fetchError struct {
	host     string
	status   int
	header   http.Header
	timeout  bool
	tooLarge bool
	err      error
}

func (e *fetchError) Error() string {
	switch {
	case e.status > 0:
		return fmt.Sprintf("%s answered HTTP %d", e.host, e.status)
	case e.err != nil:
		return e.err.Error()
	}
	return "request to " + e.host + " failed"
}

func (e *fetchError) Unwrap() error { return e.err }

// rateLimited reports GitHub's rate limit: HTTP 429, or 403 with no requests left.
func (e *fetchError) rateLimited() bool {
	return e.status == http.StatusTooManyRequests || (e.status == http.StatusForbidden && e.header.Get("X-RateLimit-Remaining") == "0")
}

// retryAfter is the wait Retry-After or X-RateLimit-Reset asks for, in ms.
func (e *fetchError) retryAfter(now time.Time) (int64, bool) {
	if v := strings.TrimSpace(e.header.Get("Retry-After")); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil && s >= 0 {
			return s * 1000, true
		}
		if t, err := http.ParseTime(v); err == nil {
			return max(t.Sub(now).Milliseconds(), 0), true
		}
	}
	if v := strings.TrimSpace(e.header.Get("X-RateLimit-Reset")); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil {
			return max(time.Unix(s, 0).Sub(now).Milliseconds(), 0), true
		}
	}
	return 0, false
}

var errIdleTimeout = errors.New("no data for 30 s")

// errInsecureRedirect is a redirect to a URL that is not https (E13).
var errInsecureRedirect = errors.New("the release source redirected to a URL that is not https")

// downloadDeadline bounds one request in total, besides the 30 s idle timeout (E13).
const downloadDeadline = 10 * time.Minute

// httpsURL reports an https URL with a host (E13: release assets and redirects).
func httpsURL(raw string) bool {
	p, err := url.Parse(raw)
	return err == nil && strings.EqualFold(p.Scheme, "https") && p.Host != ""
}

// updateTransport adds the User-Agent and, for the API host only, the GitHub token.
type updateTransport struct {
	base               http.RoundTripper
	userAgent, token   string
	apiScheme, apiHost string
}

func (t *updateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", t.userAgent)
	r.Header.Del("Authorization")
	if t.token != "" && strings.EqualFold(r.URL.Scheme, t.apiScheme) && strings.EqualFold(r.URL.Host, t.apiHost) {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(r)
}

// httpClient builds the run's HTTP client on App.HTTPTransport and App.UserAgent
// (CLI-110, P10). An unusable <PREFIX>_CA_FILE is E_CONFIG at discover.
func (u *updateRun) httpClient() *Error {
	tr, e := u.a.HTTPTransport()
	if e != nil {
		return u.decorate(e)
	}
	api, err := url.Parse(u.cfg.APIBase)
	if err != nil || api.Host == "" {
		return internalError("App.Update.APIBase is not a URL.")
	}
	token := strings.TrimSpace(u.a.env("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(u.a.env("GH_TOKEN"))
	}
	u.client = &http.Client{Transport: &updateTransport{base: tr,
		userAgent: u.a.UserAgent(),
		token:     token, apiScheme: api.Scheme, apiHost: api.Host},
		// Redirects stay on https (E13); the GitHub token still goes to the API host only.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if !strings.EqualFold(req.URL.Scheme, "https") {
				return errInsecureRedirect
			}
			return nil
		}}
	return nil
}

// idleReader resets the request's timer whenever data arrives.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(requestTimeout)
	}
	return n, err
}

// fetch GETs rawURL: into dst when given, else into the returned bytes. It reads at
// most limit bytes and fails after 30 s without progress.
func (u *updateRun) fetch(rawURL string, limit int64, dst io.Writer, download bool) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return nil, &fetchError{host: rawURL, err: fmt.Errorf("not a URL: %q", truncateRunes(rawURL, 80))}
	}
	host := parsed.Host
	bounded, stop := context.WithTimeout(u.ctx, downloadDeadline)
	defer stop()
	ctx, cancel := context.WithCancelCause(bounded)
	defer cancel(nil)
	timer := time.AfterFunc(requestTimeout, func() { cancel(errIdleTimeout) })
	defer timer.Stop()
	failure := func(err error) error {
		fe := &fetchError{host: host, err: err}
		var ne net.Error
		fe.timeout = errors.Is(context.Cause(ctx), errIdleTimeout) || errors.Is(err, context.DeadlineExceeded) ||
			(errors.As(err, &ne) && ne.Timeout())
		return fe
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &fetchError{host: host, err: err}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if download {
		req.Header.Set("Accept", "application/octet-stream")
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, failure(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &fetchError{host: host, status: resp.StatusCode, header: resp.Header.Clone()}
	}
	var buf bytes.Buffer
	w := io.Writer(&buf)
	if dst != nil {
		w = dst
	}
	n, err := io.Copy(w, io.LimitReader(&idleReader{r: resp.Body, timer: timer}, limit+1))
	if err != nil {
		return nil, failure(err)
	}
	if n > limit {
		return nil, &fetchError{host: host, tooLarge: true, err: fmt.Errorf("the answer is larger than %d bytes", limit)}
	}
	return buf.Bytes(), nil
}

// netError maps a failed GET to the contract error of its stage (CLI-070):
// download names the version being downloaded, "" for the release check.
func (u *updateRun) netError(err error, download string) *Error {
	var fe *fetchError
	isFetch := errors.As(err, &fe)
	// A request that ran out of time is cancelled too; only the run's own
	// cancellation (SIGINT, SIGTERM) is an interrupt.
	timedOut := isFetch && fe.timeout && u.ctx.Err() == nil
	if !timedOut && u.interrupted(err) {
		return u.interruptError()
	}
	if errors.Is(err, errInsecureRedirect) {
		// Usually a network's sign-in page; nothing was fetched over plain http.
		return u.fail("E_NETWORK", "The release source redirected to a URL that is not https, so nothing was downloaded.",
			fmt.Sprintf("Check the network or HTTPS_PROXY (a network's sign-in page redirects like this), then run `%s update` again.", u.tool))
	}
	next := fmt.Sprintf("Check the network or HTTPS_PROXY, then run `%s update` again; it is safe to repeat.", u.tool)
	host := u.cfg.APIBase
	if isFetch {
		host = fe.host
	}
	switch {
	case isFetch && fe.timeout:
		msg := "The release check timed out."
		if download != "" {
			msg = fmt.Sprintf("The download of v%s timed out.", download)
		}
		return u.fail("E_TIMEOUT", msg, next)
	case isFetch && fe.rateLimited():
		e := u.fail("E_RATE_LIMITED", "GitHub's API rate limit was hit.", "")
		wait := int64(60)
		if ms, ok := fe.retryAfter(u.a.now()); ok {
			e.Details["retry_after_ms"] = ms
			wait = max((ms+999)/1000, 1)
		}
		e.Details["next_action"] = fmt.Sprintf("Wait %d s, or set GITHUB_TOKEN, then run `%s update` again.", wait, u.tool)
		return e
	case isFetch && fe.status > 0:
		return u.fail("E_NETWORK", fmt.Sprintf("The release source answered HTTP %d.", fe.status), next)
	case isFetch && fe.tooLarge:
		return u.unreadable()
	}
	if download != "" {
		return u.fail("E_NETWORK", fmt.Sprintf("Could not reach %s to download v%s.", host, download), next)
	}
	return u.fail("E_NETWORK", fmt.Sprintf("Could not reach %s to check for releases.", host), next)
}

// unreadable is a release answer that is not the JSON the GitHub API sends.
func (u *updateRun) unreadable() *Error {
	return u.fail("E_NETWORK", fmt.Sprintf("The release source sent an answer %s cannot read.", u.tool),
		fmt.Sprintf("Check the network or HTTPS_PROXY, then run `%s update` again; it is safe to repeat.", u.tool))
}

// discover finds the target release (stage discover): the release tagged
// v<version> with --target-version, else the highest release that is neither a
// draft nor a prerelease; tags that are not semantic versions are skipped (a debug
// line names them), and without any usable release there is nothing to update to
// (E_UNSUPPORTED). It also returns the notice severity.
func (u *updateRun) discover(want *Version) (*ghRelease, Version, string, *Error) {
	base := strings.TrimRight(u.cfg.APIBase, "/") + "/repos/" + u.cfg.Repo
	if want != nil {
		body, err := u.fetch(fmt.Sprintf("%s/releases/tags/v%s", base, want.String()), releaseJSONLimit, nil, false)
		var fe *fetchError
		if errors.As(err, &fe) && fe.status == http.StatusNotFound {
			e := u.fail("E_USAGE", fmt.Sprintf("No release v%s exists in %s.", want.String(), u.cfg.Repo),
				fmt.Sprintf("Run `%s update --check` to see the latest version, or give an existing version.", u.tool))
			e.Details["problem"] = "invalid_value"
			e.Details["parameter"] = "--target-version"
			return nil, Version{}, "", e
		}
		if err != nil {
			return nil, Version{}, "", u.netError(err, "")
		}
		var rel ghRelease
		if json.Unmarshal(body, &rel) != nil {
			return nil, Version{}, "", u.unreadable()
		}
		return &rel, *want, updateSeverity(u.current, *want, []ghRelease{rel}), nil
	}
	body, err := u.fetch(base+"/releases?per_page=50", releaseJSONLimit, nil, false)
	if err != nil {
		return nil, Version{}, "", u.netError(err, "")
	}
	var list []ghRelease
	if json.Unmarshal(body, &list) != nil {
		return nil, Version{}, "", u.unreadable()
	}
	var best *ghRelease
	var bestV Version
	for i := range list {
		r := &list[i]
		if r.Draft {
			continue
		}
		v, err := ParseVersion(r.TagName)
		if err != nil {
			u.a.Debugf("update: skipped release tag %s (not a semantic version)", printable(r.TagName, 40))
			continue
		}
		// A prerelease is installed only through --target-version (CLI-070, E2): by
		// its semver tag, whatever GitHub's prerelease flag says, and by the flag.
		if v.Pre != "" || r.Prerelease {
			u.a.Debugf("update: skipped release tag %s (a prerelease; --target-version installs it)", printable(r.TagName, 40))
			continue
		}
		if best == nil || CompareVersions(v, bestV) > 0 {
			best, bestV = r, v
		}
	}
	if best == nil {
		return nil, Version{}, "", u.fail("E_UNSUPPORTED",
			fmt.Sprintf("No release of %s has a semantic-version tag, so there is nothing to update to.", u.cfg.Repo),
			"Report this to the tool's maintainers; nothing was changed.")
	}
	return best, bestV, updateSeverity(u.current, bestV, list), nil
}

var securityHeading = regexp.MustCompile(`(?mi)^ {0,3}#{1,6}[ \t]+security[ \t]*#*[ \t]*$`)

// updateSeverity is the notice severity of an update to target (CLI-071): warning
// when the major version changes or a release newer than the current version and
// not newer than the target has a "Security" heading in its notes, else info.
func updateSeverity(current string, target Version, releases []ghRelease) string {
	cur, curErr := ParseVersion(current)
	if curErr == nil && cur.Major != target.Major {
		return "warning"
	}
	for _, r := range releases {
		if r.Draft {
			continue
		}
		v, err := ParseVersion(r.TagName)
		if err != nil || CompareVersions(v, target) > 0 || (curErr == nil && CompareVersions(v, cur) <= 0) {
			continue
		}
		if securityHeading.MatchString(strings.ReplaceAll(r.Body, "\r\n", "\n")) {
			return "warning"
		}
	}
	return "info"
}
