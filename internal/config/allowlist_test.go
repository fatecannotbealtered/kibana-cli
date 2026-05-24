package config

import "testing"

func TestValidateIndexTargetBlocksSystemIndices(t *testing.T) {
	if err := ValidateIndexTarget("_security"); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIndexTargetAllowlist(t *testing.T) {
	t.Setenv(envAllowedIndexPrefixes, "logs-,app-")
	if err := ValidateIndexTarget("metrics-*"); err == nil {
		t.Fatal("expected allowlist error")
	}
	if err := ValidateIndexTarget("logs-*"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateKibanaHostRejectsUserinfo(t *testing.T) {
	if err := ValidateKibanaHost("https://user:pass@kibana.example.com"); err == nil {
		t.Fatal("expected userinfo rejection")
	}
}
