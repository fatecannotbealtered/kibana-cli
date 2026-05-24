package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var aggCmd = &cobra.Command{
	Use:   "agg",
	Short: "Aggregate log fields (e.g. count by level or service)",
	Long: `Run a terms aggregation on log indices via Kibana Console Proxy.

Examples:
  kibana-cli agg --profile java-app --terms level --from now-1h --json
  kibana-cli agg --index 'app-test-log-*' --terms level --service order-svc --json`,
	RunE: runAgg,
}

func init() {
	rootCmd.AddCommand(aggCmd)
	aggCmd.Flags().String("index", "", "Index pattern")
	aggCmd.Flags().String("data-view", "", "Kibana data view / index-pattern id (resolves to index title)")
	aggCmd.Flags().String("profile", "", "Profile from field-map.yaml")
	aggCmd.Flags().String("service", "", "Filter by logical service name")
	aggCmd.Flags().String("level", "", "Filter by log level")
	aggCmd.Flags().String("terms", "", "Field to aggregate (required)")
	aggCmd.Flags().String("query", "", "Additional text query (match_phrase on message by default)")
	aggCmd.Flags().Bool("msg-only", true, "Search --query only in message field (match_phrase); default on")
	aggCmd.Flags().Bool("all-fields", false, "Search --query across all fields (disables --msg-only)")
	aggCmd.Flags().String("from", "now-1h", "Time range start")
	aggCmd.Flags().String("to", "now", "Time range end")
	aggCmd.Flags().String("time-field", "", "Timestamp field")
	aggCmd.Flags().Int("buckets", 10, "Max buckets (1-100)")
	_ = aggCmd.MarkFlagRequired("terms")
}

func runAgg(cmd *cobra.Command, _ []string) error {
	fm, err := loadFieldMapOrExit()
	if err != nil {
		return err
	}
	profile, _ := cmd.Flags().GetString("profile")
	service, _ := cmd.Flags().GetString("service")
	level, _ := cmd.Flags().GetString("level")
	terms, _ := cmd.Flags().GetString("terms")

	client, _, err := newKibanaClient()
	if err != nil {
		return err
	}
	index, err := resolveIndexFromFlags(cmd, client)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}

	resolved, err := fieldmap.ResolveSearchOptions(fm, profile, index, service, level)
	if err != nil {
		return failValidation(err.Error())
	}
	if err := config.ValidateIndexTarget(resolved.Index); err != nil {
		return failValidation(err.Error())
	}
	termsField := resolveTermsField(terms, resolved, fm, profile)

	buckets, _ := cmd.Flags().GetInt("buckets")
	if buckets < 1 || buckets > 100 {
		return failValidation("--buckets must be 1-100")
	}
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")
	timeField, _ := cmd.Flags().GetString("time-field")
	if timeField != "" {
		resolved.TimeField = timeField
	}
	query, _ := cmd.Flags().GetString("query")
	msgOnly, _ := cmd.Flags().GetBool("msg-only")
	allFields, _ := cmd.Flags().GetBool("all-fields")
	if allFields {
		msgOnly = false
	}

	aggOpts := kibanaclient.AggOptions{
		Index:         resolved.Index,
		From:          from,
		To:            to,
		TimeField:     resolved.TimeField,
		TermsField:    termsField,
		BucketSize:    buckets,
		ServiceValue:  service,
		ServiceFields: resolved.ServiceFields,
		LevelValue:    level,
		LevelFields:   resolved.LevelFields,
		Query:         query,
		MsgOnly:       msgOnly,
		MessageField:  resolved.PrimaryMessageField(),
	}
	if dryRunOutput("aggregate logs", map[string]any{
		"index":      resolved.Index,
		"termsField": termsField,
		"buckets":    buckets,
	}) {
		return nil
	}

	result, err := client.Terms(apiCtx(), aggOpts)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	return printAggResult(result)
}

func resolveTermsField(terms string, resolved fieldmap.ResolvedSearch, fm *fieldmap.Map, profile string) string {
	switch terms {
	case "level":
		if len(resolved.LevelFields) > 0 {
			return resolved.LevelFields[0]
		}
		return "level"
	case "service":
		if len(resolved.ServiceFields) > 0 {
			return resolved.ServiceFields[0]
		}
		return "service_name"
	default:
		return terms
	}
}

func printAggResult(result *kibanaclient.AggResult) error {
	if jsonMode {
		output.PrintJSON(map[string]any{
			"ok":      true,
			"field":   result.Field,
			"total":   result.Total,
			"tookMs":  result.TookMs,
			"buckets": result.Buckets,
		})
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "KEY\tCOUNT\n")
	for _, b := range result.Buckets {
		_, _ = fmt.Fprintf(w, "%s\t%d\n", b.Key, b.Count)
	}
	_ = w.Flush()
	output.Gray(fmt.Sprintf("  field=%s total=%d took=%dms", result.Field, result.Total, result.TookMs))
	return nil
}
