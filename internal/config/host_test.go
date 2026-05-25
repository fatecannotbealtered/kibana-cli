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
		{"http://::1:5601", false},
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
