package config

import "testing"

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
