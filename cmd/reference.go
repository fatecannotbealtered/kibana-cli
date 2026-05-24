package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var referenceCmd = &cobra.Command{
	Use:   "reference",
	Short: "Print all commands and flags (for AI Agents)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		var lines []string
		lines = append(lines, "# kibana-cli Command Reference", "")
		lines = append(lines, fmt.Sprintf("Version: %s", rootCmd.Version), "")
		walkCommands(rootCmd, &lines, "")
		for _, line := range lines {
			cmd.Println(line)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(referenceCmd)
}

func walkCommands(cmd *cobra.Command, lines *[]string, prefix string) {
	if cmd.Hidden {
		return
	}
	name := prefix + cmd.Use
	*lines = append(*lines, "## "+name, "")
	if cmd.Short != "" {
		*lines = append(*lines, cmd.Short, "")
	}
	if cmd.Long != "" && cmd.Long != cmd.Short {
		*lines = append(*lines, cmd.Long, "")
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		*lines = append(*lines, fmt.Sprintf("  --%s (%s) %s", f.Name, f.Value.Type(), f.Usage))
	})
	*lines = append(*lines, "")
	children := cmd.Commands()
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		if child.Name() == "help" || child.Name() == "completion" {
			continue
		}
		walkCommands(child, lines, name+" ")
	}
}
