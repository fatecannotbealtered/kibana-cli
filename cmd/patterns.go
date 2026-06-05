package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var patternsCmd = &cobra.Command{
	Use:   "patterns",
	Short: "List Kibana index patterns and their fields",
}

var patternsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved index-pattern objects",
	RunE:  runPatternsList,
}

var patternsFieldsCmd = &cobra.Command{
	Use:   "fields",
	Short: "List fields available on an index pattern",
	Long: `List field names and types for a Kibana index pattern title (same value as search --index).

Examples:
  kibana-cli patterns fields --index 'app-v3-test-log-*'
  kibana-cli patterns fields --index 'platform-b-test-log-*'`,
	RunE: runPatternsFields,
}

func init() {
	rootCmd.AddCommand(patternsCmd)
	patternsCmd.AddCommand(patternsListCmd)
	patternsCmd.AddCommand(patternsFieldsCmd)
	patternsFieldsCmd.Flags().String("index", "", "Index pattern title (required)")
	_ = patternsFieldsCmd.MarkFlagRequired("index")
}

func runPatternsList(_ *cobra.Command, _ []string) error {
	client, _, err := newKibanaClient()
	if err != nil {
		return err
	}
	patterns, err := client.ListIndexPatterns(apiCtx())
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	if jsonMode {
		output.PrintJSON(map[string]any{"ok": true, "patterns": patterns, "count": len(patterns)})
		return nil
	}
	if len(patterns) == 0 {
		output.Info("No index patterns found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tTITLE")
	for _, p := range patterns {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", p.ID, p.Title)
	}
	_ = w.Flush()
	return nil
}

func runPatternsFields(cmd *cobra.Command, _ []string) error {
	index, _ := cmd.Flags().GetString("index")
	index = strings.TrimSpace(index)
	if index == "" {
		return failValidation("--index is required")
	}
	client, _, err := newKibanaClient()
	if err != nil {
		return err
	}
	fields, err := client.ListFieldsForIndexPattern(apiCtx(), index)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	if jsonMode {
		output.PrintJSON(map[string]any{
			"ok":     true,
			"index":  index,
			"count":  len(fields),
			"fields": fields,
		})
		return nil
	}
	if len(fields) == 0 {
		output.Info("No fields returned for " + index)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tTYPE\tSEARCHABLE\tAGGREGATABLE")
	for _, f := range fields {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%v\t%v\n", f.Name, f.Type, f.Searchable, f.Aggregatable)
	}
	_ = w.Flush()
	output.AuxGray(fmt.Sprintf("  %d fields on %s", len(fields), index))
	return nil
}
