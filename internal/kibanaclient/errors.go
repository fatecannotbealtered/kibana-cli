package kibanaclient

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The Kibana endpoints an APIError can come from.
const (
	EndpointConsoleProxy = "console_proxy"
	EndpointStatus       = "status"
	EndpointDataViews    = "data_views"
	EndpointFields       = "fields"
	EndpointSavedObjects = "saved_objects"
)

const maxAPIErrorBodyLen = 512

// APIError is an HTTP error answer from Kibana, or from Elasticsearch through the
// Console Proxy (which passes Elasticsearch's status and body through).
type APIError struct {
	StatusCode int
	// Message is the upstream's own text: Elasticsearch's error.reason or Kibana's
	// message. It is external text; never put it in a message for the model.
	Message string
	// ESType is Elasticsearch's error.type (index_not_found_exception, ...), "" for
	// an answer from Kibana itself.
	ESType     string
	Body       string // the body, cut to 512 characters
	RetryAfter time.Duration
	Endpoint   string
	// RouteMissing is Kibana's bare 404 for a path it has no route for (not a 404
	// about a resource, which names it): the API does not exist on this Kibana.
	RouteMissing bool
	// InternalOriginRefused is Kibana 9's restriction of internal APIs: the request
	// lacked x-elastic-internal-origin when it reached Kibana.
	InternalOriginRefused bool
	// XSRFRefused is Kibana's XSRF check: the request lacked kbn-xsrf when it
	// reached Kibana.
	XSRFRefused bool
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Kibana answered HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("Kibana answered HTTP %d", e.StatusCode)
}

// Upstream is the upstream text for details.upstream_message.
func (e *APIError) Upstream() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Body
}

func parseAPIError(status int, data []byte) *APIError {
	e := &APIError{StatusCode: status, Body: sanitizeAPIErrorBody(string(data))}
	var es struct {
		Error json.RawMessage `json:"error"`
	}
	var kibana struct {
		StatusCode int    `json:"statusCode"`
		Error      string `json:"error"`
		Message    string `json:"message"`
	}
	if len(data) > 0 && json.Unmarshal(data, &es) == nil && len(es.Error) > 0 && es.Error[0] == '{' {
		var detail struct {
			Type      string `json:"type"`
			Reason    string `json:"reason"`
			RootCause []struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"root_cause"`
		}
		if json.Unmarshal(es.Error, &detail) == nil {
			e.ESType, e.Message = detail.Type, detail.Reason
			// "all shards failed" says nothing; the root cause says why.
			if (e.Message == "" || e.Message == "all shards failed") && len(detail.RootCause) > 0 && detail.RootCause[0].Reason != "" {
				e.Message = detail.RootCause[0].Reason
			}
		}
	} else if len(data) > 0 && json.Unmarshal(data, &kibana) == nil {
		e.Message = kibana.Message
		if e.Message == "" {
			e.Message = kibana.Error
		}
		e.RouteMissing = status == 404 && kibana.StatusCode == 404 && (kibana.Message == "Not Found" || kibana.Message == "")
	}
	if e.Message == "" {
		e.Message = e.Body
	}
	lower := strings.ToLower(e.Message)
	e.InternalOriginRefused = status == 400 && strings.Contains(lower, "is not available with the current configuration")
	e.XSRFRefused = status == 400 && strings.Contains(lower, "kbn-xsrf")
	return e
}

func sanitizeAPIErrorBody(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= maxAPIErrorBodyLen {
		return body
	}
	return body[:maxAPIErrorBodyLen] + "…"
}
