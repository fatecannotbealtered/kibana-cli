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
