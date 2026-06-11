package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestWalkCommands_SkipsHiddenAndHelp(t *testing.T) {
	parent := &cobra.Command{Use: "parent", Short: "parent short"}
	parent.AddCommand(&cobra.Command{Use: "visible", Short: "visible cmd"})
	hidden := &cobra.Command{Use: "hidden", Hidden: true}
	parent.AddCommand(hidden)
	parent.AddCommand(&cobra.Command{Use: "help"})
	var lines []string
	walkCommands(parent, &lines, "root ")
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "root parent visible") {
		t.Fatalf("missing visible: %s", body)
	}
	if strings.Contains(body, "hidden") {
		t.Fatal("hidden command should be skipped")
	}
}

func TestWalkCommands_IncludesLongAndFlags(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "short only",
		Long:  "long description for agents",
	}
	cmd.Flags().String("alpha", "", "alpha flag")
	cmd.Flags().AddFlag(&pflag.Flag{
		Name:   "secret",
		Usage:  "hidden flag",
		Hidden: true,
	})
	var lines []string
	walkCommands(cmd, &lines, "")
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "long description") || !strings.Contains(body, "--alpha") {
		t.Fatalf("missing long/flags: %s", body)
	}
	if strings.Contains(body, "--secret") {
		t.Fatal("hidden flag should not appear")
	}
}

func TestReference_AgentSpecFields(t *testing.T) {
	out, code := runCLI(t, []string{"reference", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	for _, key := range []string{"tool", "version", "release_readiness", "exit_codes", "error_codes", "security", "commands"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("reference missing %s: %s", key, out)
		}
	}
	readiness, ok := data["release_readiness"].(map[string]any)
	if !ok || readiness["level"] != "beta" || readiness["live_smoke_status"] != "missing" {
		t.Fatalf("unexpected release_readiness: %v", data["release_readiness"])
	}
	if data["tool"] != toolName || data["version"] != version {
		t.Fatalf("tool/version mismatch: %v/%v", data["tool"], data["version"])
	}
	if !strings.Contains(lastJSONLine(out), `"path":"kibana-cli changelog"`) &&
		!strings.Contains(lastJSONLine(out), `"path": "kibana-cli changelog"`) {
		t.Fatalf("reference missing changelog command: %s", out)
	}
	if !strings.Contains(lastJSONLine(out), `"E_CONFLICT"`) {
		t.Fatalf("reference missing spec error codes: %s", out)
	}
	if !strings.Contains(lastJSONLine(out), `"risk_tier":"T1"`) &&
		!strings.Contains(lastJSONLine(out), `"risk_tier": "T1"`) {
		t.Fatalf("reference missing security tier: %s", out)
	}
}
