package cmd

import (
	"errors"
	"fmt"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// Kibana failures become contract errors here, by HTTP status (aicli.FromHTTP) and
// by the structured fields of kibanaclient.APIError, never by guessing from text the
// model reads (CLI-016). Messages are written for the model (CLI-017); Kibana's and
// Elasticsearch's own text goes to details.upstream_message, which aicli marks
// untrusted. Every Kibana request kibana-cli makes is a read, so every error here is
// dispatch_state not_started.

// The error codes a Kibana read can return besides the ones aicli adds itself
// (Meta.ErrorCodes, CLI-026).
var kibanaReadCodes = []string{"E_AUTH", "E_CONFIG", "E_FORBIDDEN", "E_INTERRUPTED", "E_NETWORK", "E_NOT_FOUND",
	"E_RATE_LIMITED", "E_SERVER", "E_TIMEOUT", "E_UNSUPPORTED", "E_VALIDATION"}

const doctorHint = "`kibana-cli doctor` checks the host, the credentials and the Console Proxy."

// errOpts adjusts how a failure is reported.
type errOpts struct {
	notFoundMessage, notFoundNext string
}

type errOpt func(*errOpts)

// onNotFound replaces the generic 404 message and next step.
func onNotFound(message, next string) errOpt {
	return func(o *errOpts) { o.notFoundMessage, o.notFoundNext = message, next }
}

// readError maps a failed Kibana read to a contract error.
func readError(err error, opts ...errOpt) error {
	if err == nil {
		return nil
	}
	var o errOpts
	for _, fn := range opts {
		fn(&o)
	}
	var ae *aicli.Error
	if errors.As(err, &ae) {
		return ae
	}
	var apiErr *kibanaclient.APIError
	if errors.As(err, &apiErr) {
		return httpError(apiErr, o)
	}
	var ne *kibanaclient.NetworkError
	if errors.As(err, &ne) {
		return networkError(ne)
	}
	if errors.Is(err, kibanaclient.ErrNotARead) {
		return aicli.NewError("E_UNKNOWN", "kibana-cli refused to send a request that is not a search; this is a bug in the tool.", map[string]any{
			"next_action": "Report this bug with the command you ran."})
	}
	// A 200 answer that is not the JSON Kibana sends: often a single sign-on proxy's
	// login page in front of Kibana.
	e := aicli.NewError("E_UNSUPPORTED", "Kibana's answer is not the JSON this tool expects; a login page of a proxy in front of Kibana is a common cause.", map[string]any{
		"dispatch_state": "not_started", "reason": err.Error(),
		"next_action": "Check that the host setting is Kibana's own base URL and reachable without a browser sign-in. " + doctorHint})
	e.Untrusted = []string{"reason"}
	return e
}

// networkError is a request that got no answer.
func networkError(ne *kibanaclient.NetworkError) *aicli.Error {
	switch {
	case ne.Canceled:
		return aicli.NewError("E_INTERRUPTED", "The command was interrupted; this read changed nothing.", map[string]any{
			"dispatch_state": "not_started", "next_action": "Run the command again if it is still needed."})
	case ne.Timeout:
		return aicli.NewError("E_TIMEOUT", "Kibana did not answer in time; this read changed nothing.", map[string]any{
			"dispatch_state": "not_started",
			"next_action":    "Retry with a narrower request (a shorter --since, a smaller --limit); if it keeps timing out, check the network. " + doctorHint})
	case ne.TLS:
		e := aicli.FromHTTP(0, "Kibana's TLS certificate is not trusted, so nothing was sent.", ne.Err.Error(), 0)
		e.Details["next_action"] = "If Kibana uses a company CA, ask the user to point KIBANA_CLI_CA_FILE at its PEM file; never turn verification off."
		return e
	}
	e := aicli.FromHTTP(0, "Kibana could not be reached; this read changed nothing.", ne.Err.Error(), 0)
	e.Details["next_action"] = "Check the host setting, the VPN, HTTPS_PROXY and NO_PROXY, then retry. " + doctorHint
	return e
}

// httpError maps an HTTP error answer by its status and structured fields.
func httpError(ae *kibanaclient.APIError, o errOpts) *aicli.Error {
	status := ae.StatusCode
	switch {
	case ae.InternalOriginRefused:
		// Kibana 9 restricts internal APIs (the Console Proxy is one) to requests that
		// carry x-elastic-internal-origin. kibana-cli always sends it, so it was lost on
		// the way: a reverse proxy that drops unknown headers. Users read the 400 as
		// "Console Proxy disabled", which it is not.
		e := aicli.NewError("E_UNSUPPORTED", "Kibana's internal-API restriction refused the request because the x-elastic-internal-origin header did not arrive; the Console Proxy itself is not disabled.", map[string]any{
			"dispatch_state": "not_started", "http_status": status, "upstream_message": truncate(ae.Upstream(), 500),
			"endpoint":    ae.Endpoint,
			"next_action": "kibana-cli sends `x-elastic-internal-origin: kibana`; ask the user to make the reverse proxy or gateway in front of Kibana pass that header through, then run `kibana-cli doctor`."})
		e.Untrusted = []string{"upstream_message"}
		return e
	case ae.XSRFRefused:
		e := aicli.NewError("E_UNSUPPORTED", "Kibana refused the request because the kbn-xsrf header did not arrive.", map[string]any{
			"dispatch_state": "not_started", "http_status": status, "upstream_message": truncate(ae.Upstream(), 500), "endpoint": ae.Endpoint,
			"next_action": "kibana-cli sends `kbn-xsrf: true`; ask the user to make the reverse proxy or gateway in front of Kibana pass that header through."})
		e.Untrusted = []string{"upstream_message"}
		return e
	case ae.RouteMissing:
		message := fmt.Sprintf("This Kibana has no %s API at that address.", endpointName(ae.Endpoint))
		next := "Check that the host setting is Kibana's base URL (no /app/... path). " + doctorHint
		if ae.Endpoint == kibanaclient.EndpointConsoleProxy {
			message = "Kibana has no Console Proxy at this address: the host is not Kibana's base URL, or the Console plugin is disabled (console.enabled: false)."
		}
		e := aicli.NewError("E_UNSUPPORTED", message, map[string]any{"dispatch_state": "not_started", "http_status": status,
			"endpoint": ae.Endpoint, "next_action": next})
		return e
	}
	e := aicli.FromHTTP(status, statusMessage(ae), ae.Upstream(), ae.RetryAfter)
	e.Details["endpoint"] = ae.Endpoint
	switch {
	case status == 401:
		e.Details["next_action"] = "Ask the user to check the password (`kibana-cli auth login` in their own terminal) or KIBANA_CLI_USER and KIBANA_CLI_PASSWORD; `kibana-cli auth status` checks them."
	case status == 403:
		if ae.Endpoint == kibanaclient.EndpointConsoleProxy {
			e.Details["required"] = "the read privilege on the index pattern (and view_index_metadata for field lists)"
		} else {
			e.Details["required"] = "Kibana's read privilege for this feature (Discover, Dashboard or Data View Management) in the space"
		}
		e.Details["next_action"] = "Tell the user this account lacks the privilege in details.required; do not retry with other credentials."
	case status == 404:
		switch {
		case ae.ESType == "index_not_found_exception":
			e.Message = "No index matches that name."
			e.Details["next_action"] = "Use an index pattern (logs-*) or `kibana-cli pattern list` to find one."
		case o.notFoundMessage != "":
			e.Message = o.notFoundMessage
			e.Details["next_action"] = o.notFoundNext
		default:
			e.Details["next_action"] = "Check the identifiers; list the resources to find the right one."
		}
	case status == 429:
		e.Details["next_action"] = "Wait details.retry_after_ms (or a few seconds when absent), then run the command again."
	case status >= 500:
		e.Details["next_action"] = "Retry after a few seconds; if it persists, Kibana or Elasticsearch is failing. " + doctorHint
	default:
		e.Details["next_action"] = "Fix what details.upstream_message names (the query, a field name, the index), then run the command again."
	}
	return e
}

func statusMessage(ae *kibanaclient.APIError) string {
	status := ae.StatusCode
	switch {
	case status == 401:
		return "Kibana rejected the username or password."
	case status == 403:
		return "Kibana or Elasticsearch refused this request for this account."
	case status == 404 || status == 410:
		return "Kibana has no such resource, or this account cannot see it."
	case status == 429:
		return "Kibana's rate limit was hit; this read changed nothing."
	case status >= 500:
		return "Kibana or Elasticsearch failed while answering; this read changed nothing."
	case ae.Endpoint == kibanaclient.EndpointConsoleProxy:
		return "Elasticsearch rejected the request; details.upstream_message says why."
	}
	return "Kibana rejected the request; details.upstream_message says why."
}

func endpointName(endpoint string) string {
	switch endpoint {
	case kibanaclient.EndpointStatus:
		return "status"
	case kibanaclient.EndpointDataViews:
		return "data views"
	case kibanaclient.EndpointFields:
		return "fields"
	case kibanaclient.EndpointSavedObjects:
		return "saved objects"
	}
	return "Console Proxy"
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
