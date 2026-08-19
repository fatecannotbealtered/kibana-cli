package config

import (
	"strings"
	"testing"
)

func TestValidateKibanaHost(t *testing.T) {
	tests := []struct {
		host    string
		wantErr bool
	}{
		{"https://kibana.example.com", false},
		{"https://kibana.example.com/", false},
		{"https://kibana.example.com/app/discover", true},
		{"https://kibana.example.com/login", true},
		{"ftp://kibana.example.com", true},
		{"", true},
		{"   ", true},
		{"kibana.example.com", true},
		{"http://localhost:5601", false},
		{"http://127.0.0.1:5601", false},
		{"http://[::1]:5601", false},
		// Unbracketed IPv6 literal. Go 1.26.6 tightened net/url (GO-2026-6218)
		// and now rejects this as `invalid port "::1:5601" after host`, where
		// older toolchains parsed it leniently. Rejecting is correct — RFC 3986
		// requires the brackets — so the bracketed form above is the only
		// accepted spelling.
		{"http://::1:5601", true},
		{"http://kibana.example.com", true},
		{"https://user:pass@kibana.example.com", true},
		{"https://%zz", true},
	}
	for _, tc := range tests {
		err := ValidateKibanaHost(tc.host)
		if tc.wantErr && err == nil {
			t.Fatalf("expected error for %q", tc.host)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.host, err)
		}
	}
}

func TestValidateKibanaHostHTTPLoopbackMessage(t *testing.T) {
	err := ValidateKibanaHost("http://remote.internal:5601")
	if err == nil || !strings.Contains(err.Error(), "http:// is only allowed for loopback") {
		t.Fatalf("unexpected error: %v", err)
	}
}
