package cmd

import (
	"testing"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

func resetTimeoutFlag(t *testing.T) {
	t.Helper()
	timeoutSeconds = defaultTimeoutSeconds
	if f := rootCmd.PersistentFlags().Lookup("timeout"); f != nil {
		f.Changed = false
		_ = f.Value.Set("60")
	}
}

func TestApplyTimeoutFromEnv_UsesEnvWhenFlagNotSet(t *testing.T) {
	resetTimeoutFlag(t)
	t.Setenv("KIBANA_CLI_TIMEOUT", "90")
	got := applyTimeoutFromEnv(rootCmd)
	if got != 90 {
		t.Fatalf("got %d want 90", got)
	}
}

func TestApplyTimeoutFromEnv_ExplicitFlagOverridesEnv(t *testing.T) {
	resetTimeoutFlag(t)
	t.Setenv("KIBANA_CLI_TIMEOUT", "90")
	if err := rootCmd.ParseFlags([]string{"--timeout", "30"}); err != nil {
		t.Fatal(err)
	}
	got := applyTimeoutFromEnv(rootCmd)
	if got != 30 {
		t.Fatalf("got %d want 30", got)
	}
}

func TestApplyTimeoutFromEnv_DefaultWhenUnset(t *testing.T) {
	resetTimeoutFlag(t)
	t.Setenv("KIBANA_CLI_TIMEOUT", "")
	got := applyTimeoutFromEnv(rootCmd)
	if got != defaultTimeoutSeconds {
		t.Fatalf("got %d want %d", got, defaultTimeoutSeconds)
	}
}

func TestInitClientOptionsFromEnv_AppliesTimeoutFromEnv(t *testing.T) {
	resetTimeoutFlag(t)
	t.Setenv("KIBANA_CLI_TIMEOUT", "120")
	activeCmd = rootCmd
	initClientOptionsFromEnv()
	opts := kibanaclient.CurrentClientOptions()
	if opts.Timeout != 120*time.Second {
		t.Fatalf("timeout %v want 120s", opts.Timeout)
	}
}
