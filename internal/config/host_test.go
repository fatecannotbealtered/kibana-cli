package config

import (
	"net/url"
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

// TestValidateKibanaHostIPv6BracketHint checks that the unbracketed-IPv6
// rejection tells the caller what to write instead. Go's own message names the
// symptom (`invalid port "::1:5601" after host`) but not the fix, and the
// address is usually pasted from a context where writing it bare is correct.
func TestValidateKibanaHostIPv6BracketHint(t *testing.T) {
	err := ValidateKibanaHost("http://::1:5601")
	if err == nil {
		t.Fatal("an unbracketed IPv6 host must be rejected")
	}
	if !strings.Contains(err.Error(), "http://[::1]:5601") {
		t.Errorf("error should suggest the bracketed form, got: %v", err)
	}
}

func TestBracketedIPv6Hint(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		// Loopback with a port: the trailing :5601 is the port, the rest is the
		// address.
		{"http://::1:5601", "http://[::1]:5601"},
		// No port at all -- the whole authority is the address.
		{"http://::1", "http://[::1]"},
		// A routable address, and one whose last group is not numeric so it
		// cannot be mistaken for a port.
		{"https://2001:db8::1:9200", "https://[2001:db8::1]:9200"},
		{"https://2001:db8::abc", "https://[2001:db8::abc]"},
		// A path is preserved so the suggestion stays copy-pasteable, even
		// though ValidateKibanaHost rejects paths separately.
		{"http://::1:5601/app", "http://[::1]:5601/app"},
		// Already bracketed: any failure is something else, so no hint.
		{"http://[::1]:5601", ""},
		{"http://[::1]:notaport", ""},
		// Not IPv6: one colon is host:port, none is a bare host.
		{"https://kibana.example.com:5601", ""},
		{"https://kibana.example.com", ""},
		// No scheme separator to split on.
		{"::1:5601", ""},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			if got := bracketedIPv6Hint(tc.host); got != tc.want {
				t.Errorf("bracketedIPv6Hint(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

// TestBracketedIPv6HintSuggestionsAreValid is the property the hint must never
// violate: whatever it proposes has to parse, or it just replaces one confusing
// error with another.
func TestBracketedIPv6HintSuggestionsAreValid(t *testing.T) {
	for _, host := range []string{
		"http://::1:5601", "http://::1", "https://2001:db8::1:9200",
		"https://2001:db8::abc", "http://fe80::1%25eth0:5601", "http://:::::",
	} {
		hint := bracketedIPv6Hint(host)
		if hint == "" {
			continue
		}
		if _, err := url.Parse(hint); err != nil {
			t.Errorf("hint for %q is itself unparseable: %q (%v)", host, hint, err)
		}
	}
}
