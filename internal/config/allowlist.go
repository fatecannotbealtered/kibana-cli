package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const envAllowedIndexPrefixes = "KIBANA_CLI_ALLOWED_INDEX_PREFIXES"

// AllowedIndexPrefixes returns optional index allowlist from KIBANA_CLI_ALLOWED_INDEX_PREFIXES (comma-separated).
func AllowedIndexPrefixes() []string {
	raw := strings.TrimSpace(os.Getenv(envAllowedIndexPrefixes))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ValidateIndexTarget checks index pattern safety and optional allowlist.
func ValidateIndexTarget(index string) error {
	index = strings.TrimSpace(index)
	if index == "" {
		return errors.New("index is required")
	}
	if strings.Contains(index, "..") || strings.Contains(index, "/") || strings.Contains(index, `\`) {
		return fmt.Errorf("invalid index pattern %q", index)
	}
	if strings.HasPrefix(index, "_") || strings.HasPrefix(index, "-") {
		return fmt.Errorf("index pattern %q is not allowed", index)
	}
	prefixes := AllowedIndexPrefixes()
	if len(prefixes) == 0 {
		return nil
	}
	for _, p := range prefixes {
		if strings.HasPrefix(index, p) {
			return nil
		}
	}
	return fmt.Errorf("index %q is outside KIBANA_CLI_ALLOWED_INDEX_PREFIXES", index)
}
