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
