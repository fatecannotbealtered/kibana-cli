package kibanaclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const maxAPIErrorBodyLen = 512

// APIError is an error returned through Kibana Console Proxy.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Kibana proxy error %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("Kibana proxy error %d", e.StatusCode)
}

func parseAPIError(status int, data []byte) *APIError {
	err := &APIError{StatusCode: status, Body: sanitizeAPIErrorBody(string(data))}
	var es struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
		Status int `json:"status"`
	}
	if len(data) > 0 && json.Unmarshal(data, &es) == nil && es.Error.Reason != "" {
		err.Message = es.Error.Reason
		return err
	}
	var kibana struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &kibana) == nil && kibana.Message != "" {
		err.Message = kibana.Message
		return err
	}
	switch status {
	case http.StatusUnauthorized:
		err.Message = "not authorized: check Kibana username/password"
	case http.StatusForbidden:
		err.Message = "forbidden: check index read privileges"
	default:
		err.Message = err.Body
		if err.Message == "" {
			err.Message = "request failed"
		}
	}
	return err
}

func sanitizeAPIErrorBody(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= maxAPIErrorBodyLen {
		return body
	}
	return body[:maxAPIErrorBodyLen] + "…"
}
