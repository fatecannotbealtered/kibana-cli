package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

var patternsInferCmd = &cobra.Command{
	Use:   "infer",
	Short: "Infer a field-map profile for an index (msg/message, service/log_app, traceId)",
	Long: `Probe an index's fields and produce a paste-ready field-map profile: it maps
heterogeneous field names (msg vs message, log_app vs service_name, ...) onto
logical groups and detects whether traceId is a field or embedded in the message.

  kibana-cli patterns infer --index 'sysA-app-*'
  kibana-cli patterns infer --index 'sysA-app-*' --write --dry-run
  kibana-cli patterns infer --index 'sysA-app-*' --write --confirm <confirm_token>`,
	RunE: runPatternsInfer,
}

func init() {
	rootCmd.AddCommand(patternsCmd)
	patternsCmd.AddCommand(patternsListCmd)
	patternsCmd.AddCommand(patternsFieldsCmd)
	patternsCmd.AddCommand(patternsInferCmd)
	addLimitOffsetFlags(patternsListCmd, 100, "Max index patterns to return")
	patternsListCmd.Flags().String("fields", "", "Comma-separated JSON data fields to include")
	patternsFieldsCmd.Flags().String("index", "", "Index pattern title (required)")
	addLimitOffsetFlags(patternsFieldsCmd, 100, "Max fields to return")
	patternsFieldsCmd.Flags().String("fields", "", "Comma-separated JSON data fields to include")
	_ = patternsFieldsCmd.MarkFlagRequired("index")
	patternsInferCmd.Flags().String("index", "", "Index pattern title to infer from (required)")
	patternsInferCmd.Flags().String("name", "", "Profile name (default: derived from --index)")
	patternsInferCmd.Flags().Int("sample", 20, "How many recent docs to sample when probing for a traceId in messages")
	patternsInferCmd.Flags().Bool("write", false, "Append the inferred profile to the active field-map (requires --dry-run then --confirm)")
	_ = patternsInferCmd.MarkFlagRequired("index")
}

func runPatternsInfer(cmd *cobra.Command, _ []string) error {
	index, _ := cmd.Flags().GetString("index")
	index = strings.TrimSpace(index)
	if index == "" {
		return failValidation("--index is required")
	}
	profileName, _ := cmd.Flags().GetString("name")
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = fieldmap.SuggestProfileName(index)
	}
	sample, _ := cmd.Flags().GetInt("sample")
	write, _ := cmd.Flags().GetBool("write")

	client, _, err := newKibanaClient()
	if err != nil {
		return err
	}
	fields, err := client.ListFieldsForIndexPattern(apiCtx(), index)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	fieldNames := make([]string, 0, len(fields))
	for _, f := range fields {
		fieldNames = append(fieldNames, f.Name)
	}
	// Only sample message bodies when no trace field is obviously present — that is
	// the case where trace_mode=msg detection matters.
	samples := sampleMessagesForTrace(client, index, fieldNames, sample)
	result := fieldmap.Infer(index, fieldNames, samples)

	snippet, err := result.YAMLSnippet(profileName)
	if err != nil {
		return failValidation("rendering profile yaml: " + err.Error())
	}

	if write {
		return writeInferredProfile(profileName, result, snippet)
	}
	if jsonMode {
		printJSONSuccess(map[string]any{
			"index":           index,
			"profileName":     profileName,
			"inferredProfile": result,
			"yamlSnippet":     snippet,
			"notes":           result.Notes,
		})
		return nil
	}
	fmt.Println()
	output.AuxBold("  Inferred field-map profile: " + profileName)
	output.AuxGray("  " + index)
	fmt.Println()
	fmt.Print(snippet)
	for _, n := range result.Notes {
		output.AuxGray("  note: " + n)
	}
	return nil
}

// sampleMessagesForTrace pulls a few recent message bodies so Infer can detect a
// traceId embedded in the log line. Skipped (returns nil) when a trace field is
// already present or sampling is disabled.
func sampleMessagesForTrace(client *kibanaclient.Client, index string, fieldNames []string, sample int) []string {
	if sample <= 0 {
		return nil
	}
	for _, n := range fieldNames {
		switch n {
		case "traceId", "trace_id", "trace.id", "log_traceId":
			return nil // a trace field exists; no need to probe messages
		}
	}
	res, err := client.Search(apiCtx(), kibanaclient.SearchOptions{
		Index: index, From: "now-24h", To: "now", Size: sample, SortDesc: true,
	})
	if err != nil || res == nil {
		return nil
	}
	msgs := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		for _, key := range []string{"msg", "message", "log.message"} {
			if v, ok := h.Source[key].(string); ok && v != "" {
				msgs = append(msgs, v)
				break
			}
		}
	}
	return msgs
}

func writeInferredProfile(profileName string, result fieldmap.InferResult, snippet string) error {
	_, fmFile := config.ActiveMeta(contextName)
	path := fieldmap.FilePath()
	if strings.TrimSpace(fmFile) != "" {
		path = filepath.Join(config.Dir(), fmFile)
	}
	skipped, err := writePlan(
		"write field-map profile",
		map[string]any{"path": path, "profile": profileName, "yaml": snippet},
		map[string]any{"path": path, "profile": profileName},
	)
	if err != nil || skipped {
		return err
	}
	m, err := fieldmap.LoadFile(path)
	if err != nil {
		return failValidation(err.Error())
	}
	if m == nil {
		m = &fieldmap.Map{Version: 1}
	}
	if m.Profiles == nil {
		m.Profiles = map[string]fieldmap.Profile{}
	}
	m.Profiles[profileName] = result.Profile()
	data, err := yaml.Marshal(m)
	if err != nil {
		return failConfig("encoding field-map: " + err.Error())
	}
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return failConfig(err.Error())
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return failConfig(err.Error())
	}
	if jsonMode {
		printJSONSuccess(map[string]any{"status": "ok", "path": path, "profile": profileName})
		return nil
	}
	output.Success("Wrote profile " + profileName + " to " + path)
	return nil
}

func runPatternsList(cmd *cobra.Command, _ []string) error {
	client, _, err := newKibanaClient()
	if err != nil {
		return err
	}
	patterns, err := client.ListIndexPatterns(apiCtx())
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	limit, _ := cmd.Flags().GetInt("limit")
	offset, err := requireOffset(cmd)
	if err != nil {
		return err
	}
	page, meta, pageErr := paginateSlice(patterns, limit, offset)
	if pageErr != nil {
		return failValidation(pageErr.Error())
	}
	if jsonMode {
		payload := map[string]any{"patterns": page, "_untrusted": []string{"patterns"}}
		for k, v := range meta {
			payload[k] = v
		}
		printJSONSuccess(projectTopLevelFields(payload, getFieldsFlag(cmd)))
		return nil
	}
	if len(page) == 0 {
		output.Info("No index patterns found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tTITLE")
	for _, p := range page {
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
	limit, _ := cmd.Flags().GetInt("limit")
	offset, err := requireOffset(cmd)
	if err != nil {
		return err
	}
	page, meta, pageErr := paginateSlice(fields, limit, offset)
	if pageErr != nil {
		return failValidation(pageErr.Error())
	}
	if jsonMode {
		payload := map[string]any{
			"index":      index,
			"fields":     page,
			"_untrusted": []string{"fields"},
		}
		for k, v := range meta {
			payload[k] = v
		}
		printJSONSuccess(projectTopLevelFields(payload, getFieldsFlag(cmd)))
		return nil
	}
	if len(page) == 0 {
		output.Info("No fields returned for " + index)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tTYPE\tSEARCHABLE\tAGGREGATABLE")
	for _, f := range page {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%v\t%v\n", f.Name, f.Type, f.Searchable, f.Aggregatable)
	}
	_ = w.Flush()
	output.AuxGray(fmt.Sprintf("  %d of %d fields on %s", len(page), len(fields), index))
	return nil
}
