package config

import (
	"strings"
	"testing"
)

func TestAllowedIndexPrefixesEmpty(t *testing.T) {
	t.Setenv(envAllowedIndexPrefixes, "")
	if got := AllowedIndexPrefixes(); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestAllowedIndexPrefixesParsing(t *testing.T) {
	t.Setenv(envAllowedIndexPrefixes, " logs- , ,app- , ")
	got := AllowedIndexPrefixes()
	if len(got) != 2 || got[0] != "logs-" || got[1] != "app-" {
		t.Fatalf("got %v", got)
	}
}

func TestValidateIndexTargetRequired(t *testing.T) {
	if err := ValidateIndexTarget("  "); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIndexTargetBlocksSystemIndices(t *testing.T) {
	if err := ValidateIndexTarget("_security"); err == nil {
		t.Fatal("expected error")
	}
	if err := ValidateIndexTarget("-metrics"); err == nil {
		t.Fatal("expected error for dash prefix")
	}
}

func TestValidateIndexTargetRejectsPathTraversal(t *testing.T) {
	for _, index := range []string{"logs/../secret", "logs/x", `logs\x`} {
		if err := ValidateIndexTarget(index); err == nil {
			t.Fatalf("expected error for %q", index)
		}
	}
}

func TestValidateIndexTargetNoAllowlist(t *testing.T) {
	t.Setenv(envAllowedIndexPrefixes, "")
	if err := ValidateIndexTarget("any-index"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateIndexTargetAllowlist(t *testing.T) {
	t.Setenv(envAllowedIndexPrefixes, "logs-,app-")
	if err := ValidateIndexTarget("metrics-*"); err == nil {
		t.Fatal("expected allowlist error")
	}
	err := ValidateIndexTarget("metrics-*")
	if err == nil || !strings.Contains(err.Error(), "KIBANA_CLI_ALLOWED_INDEX_PREFIXES") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateIndexTarget("logs-*"); err != nil {
		t.Fatal(err)
	}
}
