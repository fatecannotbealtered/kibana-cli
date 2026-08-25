package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ValidateKibanaHost checks that host is a Kibana base URL (scheme + host only).
func ValidateKibanaHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("host is required")
	}
	if !strings.HasPrefix(host, "https://") && !strings.HasPrefix(host, "http://") {
		return errors.New("host must start with https:// or http://")
	}
	u, err := url.Parse(host)
	if err != nil {
		if hint := bracketedIPv6Hint(host); hint != "" {
			return fmt.Errorf("invalid host URL: %w; an IPv6 address must be bracketed so the port is unambiguous — write %s", err, hint)
		}
		return fmt.Errorf("invalid host URL: %w", err)
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("host must be Kibana base URL only (no path, e.g. https://kibana.example.com)")
	}
	if u.User != nil {
		return errors.New("host must not embed credentials in URL; use auth login or KIBANA_CLI_USER/KIBANA_CLI_PASSWORD")
	}
	if strings.HasPrefix(host, "http://") {
		h := loopbackHost(host)
		if h != "localhost" && h != "127.0.0.1" && h != "[::1]" && h != "::1" {
			return fmt.Errorf("http:// is only allowed for loopback; got %q", host)
		}
	}
	return nil
}

// bracketedIPv6Hint returns the RFC 3986 spelling of host when it failed to
// parse because an IPv6 literal was written without brackets, or "" when that
// is not what went wrong.
//
// An IPv6 address contains colons and a URL authority uses a colon to separate
// host from port, so `http://::1:5601` is genuinely ambiguous: is that ::1 on
// port 5601, or :: on port 1:5601? RFC 3986 §3.2.2 resolves it by bracketing
// the literal, after which only a colon FOLLOWING the closing bracket starts a
// port. Go's net/url used to accept the unbracketed form and stopped in 1.26.6
// (GO-2026-6218). Its own message names the symptom ("invalid port ... after
// host") but not the fix, and the address is usually pasted from somewhere it
// is legitimately written bare -- `ping ::1`, a container inspect, a config
// field that holds an address rather than a URL -- so the missing brackets are
// not obvious.
//
// The suggestion is returned only after confirming it parses, so this never
// proposes something that also fails.
func bracketedIPv6Hint(host string) string {
	scheme, rest, found := strings.Cut(host, "://")
	if !found {
		return ""
	}
	authority, path, hadPath := strings.Cut(rest, "/")
	if strings.ContainsAny(authority, "[]") {
		return "" // already bracketed; whatever is wrong, it is not this
	}
	// Two colons is the shortest an IPv6 literal can be ("::"), and a bare
	// host:port has exactly one.
	if strings.Count(authority, ":") < 2 {
		return ""
	}

	candidates := make([]string, 0, 2)
	if addr, port, ok := lastColonSplit(authority); ok {
		candidates = append(candidates, "["+addr+"]:"+port)
	}
	candidates = append(candidates, "["+authority+"]")

	for _, candidate := range candidates {
		suggestion := scheme + "://" + candidate
		if hadPath {
			suggestion += "/" + path
		}
		if _, err := url.Parse(suggestion); err == nil {
			return suggestion
		}
	}
	return ""
}

// lastColonSplit splits authority at its final colon when the tail is a plain
// port number, which is the only reading under which the tail is not part of
// the address itself.
func lastColonSplit(authority string) (addr, port string, ok bool) {
	i := strings.LastIndex(authority, ":")
	if i < 0 || i == len(authority)-1 {
		return "", "", false
	}
	port = authority[i+1:]
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	return authority[:i], port, true
}
