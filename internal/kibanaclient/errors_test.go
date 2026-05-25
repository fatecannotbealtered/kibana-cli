package kibanaclient

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	if (&APIError{StatusCode: 500}).Error() != "Kibana proxy error 500" {
		t.Fatal("empty message")
	}
	if (&APIError{StatusCode: 400, Message: "bad"}).Error() != "Kibana proxy error 400: bad" {
		t.Fatal("with message")
	}
}

func TestParseAPIError_elasticsearchReason(t *testing.T) {
	data := []byte(`{"error":{"reason":"index not found"},"status":404}`)
	err := parseAPIError(404, data)
	if err.Message != "index not found" || err.StatusCode != 404 {
		t.Fatalf("%+v", err)
	}
}

func TestParseAPIError_kibanaMessage(t *testing.T) {
	data := []byte(`{"message":"license expired"}`)
	err := parseAPIError(403, data)
	if err.Message != "license expired" {
		t.Fatalf("%+v", err)
	}
}

func TestParseAPIError_statusDefaults(t *testing.T) {
	if parseAPIError(http.StatusUnauthorized, nil).Message == "" {
		t.Fatal("401")
	}
	if parseAPIError(http.StatusForbidden, nil).Message == "" {
		t.Fatal("403")
	}
	e := parseAPIError(502, []byte("upstream down"))
	if e.Message != "upstream down" {
		t.Fatalf("502: %+v", e)
	}
	e = parseAPIError(500, nil)
	if e.Message != "request failed" {
		t.Fatalf("empty default: %+v", e)
	}
}

func TestSanitizeAPIErrorBody(t *testing.T) {
	if sanitizeAPIErrorBody("  x  ") != "x" {
		t.Fatal("trim")
	}
	long := strings.Repeat("a", maxAPIErrorBodyLen+10)
	out := sanitizeAPIErrorBody(long)
	if len(out) != maxAPIErrorBodyLen+len("…") {
		t.Fatalf("len=%d", len(out))
	}
}

func TestIsTextFieldAggError(t *testing.T) {
	if isTextFieldAggError(nil) || isTextFieldAggError(&APIError{StatusCode: 500, Message: "fielddata"}) {
		t.Fatal("nil or non-400")
	}
	for _, msg := range []string{"fielddata", "text fields", "not supported", "keyword"} {
		if !isTextFieldAggError(&APIError{StatusCode: 400, Message: msg}) {
			t.Fatalf("want true for %q", msg)
		}
	}
	if isTextFieldAggError(&APIError{StatusCode: 400, Message: "other"}) {
		t.Fatal("unrelated 400")
	}
}

func TestParseAPIError_asError(t *testing.T) {
	var target *APIError
	err := parseAPIError(401, nil)
	if !errors.As(err, &target) {
		t.Fatal("not APIError")
	}
}
