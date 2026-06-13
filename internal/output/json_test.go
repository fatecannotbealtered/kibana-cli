package output

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(fn func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		panic(err)
	}
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func captureStderr(fn func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	if err := w.Close(); err != nil {
		panic(err)
	}
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestPrintJSON_success(t *testing.T) {
	out := captureStdout(func() { PrintJSON(map[string]string{"a": "b"}) })
	out = strings.TrimSpace(out)
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["a"] != "b" {
		t.Fatalf("stdout=%q err=%v", out, err)
	}
}

func TestPrintJSON_marshalError(t *testing.T) {
	errOut := captureStderr(func() { PrintJSON(make(chan int)) })
	if !strings.Contains(errOut, "json marshal error") {
		t.Fatalf("stderr=%q", errOut)
	}
}

func TestErrorCodeFromStatus(t *testing.T) {
	tests := []struct {
		code int
		want ErrorCode
	}{
		{401, ErrAuth},
		{403, ErrForbidden},
		{404, ErrNotFound},
		{429, ErrRateLimit},
		{500, ErrServer},
		{502, ErrServer},
		{400, ErrValidation},
		{422, ErrValidation},
		{200, ErrUnknown},
		{0, ErrUnknown},
	}
	for _, tt := range tests {
		if got := ErrorCodeFromStatus(tt.code); got != tt.want {
			t.Fatalf("status %d: got %q want %q", tt.code, got, tt.want)
		}
	}
}

func TestHintForErrorCode(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want string
	}{
		{ErrConfig, "auth login"},
		{ErrAuth, "KIBANA_CLI_USER"},
		{ErrForbidden, "roles"},
		{ErrNotFound, "index"},
		{ErrRateLimit, "retry"},
		{ErrServer, "try again"},
		{ErrValidation, "arguments"},
		{ErrNetwork, "KIBANA_CLI_HOST"},
		{ErrUnknown, ""},
		{"OTHER", ""},
	}
	for _, tt := range tests {
		got := HintForErrorCode(tt.code)
		if tt.want == "" {
			if got != "" {
				t.Fatalf("code %q: got hint %q", tt.code, got)
			}
			continue
		}
		if !strings.Contains(got, tt.want) {
			t.Fatalf("code %q: hint %q missing %q", tt.code, got, tt.want)
		}
	}
}

func TestExitCodeForHTTP(t *testing.T) {
	tests := []struct {
		status int
		want   int
	}{
		{401, 4},
		{403, 4},
		{404, 3},
		{429, 7},
		{500, 7},
		{400, 2},
		{422, 2},
		{0, 7},
		{200, 7},
	}
	for _, tt := range tests {
		if got := ExitCodeForHTTP(tt.status); got != tt.want {
			t.Fatalf("status %d: got %d want %d", tt.status, got, tt.want)
		}
	}
}

func TestPrintErrorJSONWithCode(t *testing.T) {
	tests := []struct {
		name       string
		msg        string
		statusCode int
		code       ErrorCode
		wantKeys   map[string]any
	}{
		{
			name:       "auth with hint",
			msg:        "unauthorized",
			statusCode: 401,
			code:       ErrAuth,
			wantKeys: map[string]any{
				"ok": false,
			},
		},
		{
			name:       "no status code field",
			msg:        "network",
			statusCode: 0,
			code:       ErrNetwork,
			wantKeys: map[string]any{
				"ok": false,
			},
		},
		{
			name:       "unknown no hint",
			msg:        "oops",
			statusCode: 0,
			code:       ErrUnknown,
			wantKeys: map[string]any{
				"ok": false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(func() {
				PrintErrorJSONWithCode(tt.msg, tt.statusCode, tt.code)
			})
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
				t.Fatalf("unmarshal: %v out=%q", err, out)
			}
			for k, want := range tt.wantKeys {
				if payload[k] != want {
					t.Fatalf("%s: got %v want %v full=%v", k, payload[k], want, payload)
				}
			}
			if payload["schema_version"] != SchemaVersion {
				t.Fatalf("schema_version: %v", payload)
			}
			errObj, ok := payload["error"].(map[string]any)
			if !ok {
				t.Fatalf("missing error object: %v", payload)
			}
			if errObj["message"] != tt.msg || errObj["code"] != string(tt.code) {
				t.Fatalf("error fields: %v", payload)
			}
			if gotRetryable, _ := errObj["retryable"].(bool); gotRetryable != RetryableForErrorCode(tt.code) {
				t.Fatalf("retryable=%v want %v", gotRetryable, RetryableForErrorCode(tt.code))
			}
			details, ok := errObj["details"].(map[string]any)
			if !ok {
				t.Fatalf("missing details: %v", payload)
			}
			if details["status"] != "api_error" || details["errorCode"] != string(tt.code) {
				t.Fatalf("details: %v", details)
			}
			hint := HintForErrorCode(tt.code)
			if hint != "" {
				if details["hint"] != hint {
					t.Fatalf("hint=%v want %q", details["hint"], hint)
				}
			} else if _, ok := details["hint"]; ok {
				t.Fatalf("unexpected hint: %v", payload["hint"])
			}
			if tt.statusCode <= 0 {
				if _, ok := details["statusCode"]; ok {
					t.Fatal("statusCode should be omitted")
				}
			} else if details["statusCode"] != float64(tt.statusCode) {
				t.Fatalf("statusCode=%v", details["statusCode"])
			}
		})
	}
}
