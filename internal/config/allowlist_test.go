package config

import (
	"strings"
	"testing"
)

func TestParseIndexPrefixes(t *testing.T) {
	if got := ParseIndexPrefixes(""); got != nil {
		t.Fatalf("empty: %v", got)
	}
	got := ParseIndexPrefixes(" logs- , ,app- , ")
	if len(got) != 2 || got[0] != "logs-" || got[1] != "app-" {
		t.Fatalf("got %v", got)
	}
}

func TestValidateIndexTargetRefusesUnsafeIndices(t *testing.T) {
	for _, index := range []string{"  ", "_security", "-metrics", "logs/../secret", "logs/x", `logs\x`,
		"logs?pretty", "logs#x", "logs x", "logs,", "a,,b", "logs..x", `"logs"`, "logs&a=b"} {
		if err := ValidateIndexTarget(index, nil); err == nil {
			t.Errorf("%q should be refused", index)
		}
	}
	for _, index := range []string{"app-log-*", "logs-2026.10.23", "*", "a-*,b-*", "remote:logs-*", "logs+x"} {
		if err := ValidateIndexTarget(index, nil); err != nil {
			t.Errorf("%q: %v", index, err)
		}
	}
}

func TestValidateIndexTargetAllowlistChecksEveryPart(t *testing.T) {
	prefixes := []string{"logs-", "app-"}
	if err := ValidateIndexTarget("logs-*", prefixes); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"metrics-*", "*", "logs-*,metrics-*", ".ds-metrics-x-2026.10.25-000001"} {
		err := ValidateIndexTarget(index, prefixes)
		if err == nil || !strings.Contains(err.Error(), "allowed_index_prefixes") {
			t.Errorf("%q: %v", index, err)
		}
	}
	// A data stream's backing index, as a hit's _index names it, is allowed when its
	// stream is.
	if err := ValidateIndexTarget(".ds-logs-app-default-2026.10.25-000001", prefixes); err != nil {
		t.Fatalf("backing index: %v", err)
	}
}
