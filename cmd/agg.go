package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
  kibana-cli agg --profile java-app --terms level --from now-1h
  kibana-cli agg --index 'app-test-log-*' --terms level --service order-svc`,
	RunE: runAgg,
}

func init() {
	rootCmd.AddCommand(aggCmd)
	aggCmd.Flags().String("index", "", "Index pattern")
	aggCmd.Flags().String("data-view", "", "Kibana data view / index-pattern id (resolves to index title)")
	aggCmd.Flags().String("profile", "", "Profile from field-map.yaml")
	aggCmd.Flags().String("service", "", "Filter by logical service name")
	aggCmd.Flags().String("level", "", "Filter by log level")
	aggCmd.Flags().String("terms", "", "Field to aggregate (required for --agg-type terms)")
	aggCmd.Flags().String("agg-type", "terms", "Aggregation type: terms|date_histogram")
	aggCmd.Flags().String("interval", "1h", "date_histogram interval (e.g. 1h, 1d, 30m)")
	aggCmd.Flags().String("metric", "", "Per-bucket metric: avg|sum|min|max|count")
	aggCmd.Flags().String("metric-field", "", "Numeric field for --metric (not needed for count)")
	aggCmd.Flags().String("query", "", "Additional text query; searches across ALL fields by default (use --precise to narrow to message)")
	aggCmd.Flags().Bool("precise", false, "Restrict --query to message field(s) via match_phrase (opt-in)")
	aggCmd.Flags().String("from", "now-1h", "Time range start")
	aggCmd.Flags().String("to", "now", "Time range end")
	aggCmd.Flags().String("time-field", "", "Timestamp field")
	aggCmd.Flags().Int("buckets", 10, "Max buckets (1-100)")
	aggCmd.Flags().Int("limit", 10, "Max aggregation buckets to return (1-100)")
	aggCmd.Flags().String("fields", "", "Comma-separated JSON data fields to include")
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

	aggType := normalizeAggTypeFlag(cmd)
	metric, metricField, err := resolveMetricFlags(cmd)
	if err != nil {
		return err
	}
	if aggType == kibanaclient.AggTypeTerms && strings.TrimSpace(terms) == "" {
		return failValidation("--terms is required for --agg-type terms")
	}

	var client *kibanaclient.Client
	index, err := resolveAggIndex(cmd, &client)
	if err != nil {
		return err
	}

	resolved, err := fieldmap.ResolveSearchOptions(fm, profile, index, service, level)
	if err != nil {
		return failValidation(err.Error())
	}
	if err := config.ValidateIndexTarget(resolved.Index); err != nil {
		return failValidation(err.Error())
	}
	termsField := resolveTermsField(terms, resolved, fm, profile)

	buckets, err := requireBucketLimit(cmd)
	if err != nil {
		return err
	}
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")
	timeField, _ := cmd.Flags().GetString("time-field")
	if timeField != "" {
		resolved.TimeField = timeField
	}
	interval, _ := cmd.Flags().GetString("interval")
	query, _ := cmd.Flags().GetString("query")
	precise, _ := cmd.Flags().GetBool("precise")

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
		MsgOnly:       precise,
		MessageField:  resolved.PrimaryMessageField(),
		MessageFields: resolved.MessageFields,
		AggType:       aggType,
		Interval:      interval,
		Metric:        metric,
		MetricField:   metricField,
	}
	if dryRunOutput("aggregate logs", map[string]any{
		"index":      resolved.Index,
		"aggType":    aggType,
		"termsField": termsField,
		"interval":   interval,
		"metric":     metric,
		"buckets":    buckets,
	}) {
		return nil
	}
	if client == nil {
		client, _, err = newKibanaClient()
		if err != nil {
			return err
		}
	}

	result, err := client.Aggregate(apiCtx(), aggOpts)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	return printAggResult(result, getFieldsFlag(cmd), buckets)
}

func normalizeAggTypeFlag(cmd *cobra.Command) string {
	raw, _ := cmd.Flags().GetString("agg-type")
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case kibanaclient.AggTypeDateHistogram, "date-histogram", "histogram":
		return kibanaclient.AggTypeDateHistogram
	default:
		return kibanaclient.AggTypeTerms
	}
}

// resolveMetricFlags validates --metric / --metric-field. avg/sum/min/max need a
// numeric field; count needs none; empty means no metric (plain doc_count).
func resolveMetricFlags(cmd *cobra.Command) (metric, metricField string, err error) {
	metric = strings.ToLower(strings.TrimSpace(mustString(cmd, "metric")))
	metricField = strings.TrimSpace(mustString(cmd, "metric-field"))
	switch metric {
	case "":
		return "", "", nil
	case "count":
		return "count", "", nil
	case "avg", "sum", "min", "max":
		if metricField == "" {
			return "", "", failValidation("--metric " + metric + " requires --metric-field")
		}
		return metric, metricField, nil
	default:
		return "", "", failValidation("--metric must be one of avg|sum|min|max|count")
	}
}

func mustString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func resolveAggIndex(cmd *cobra.Command, clientRef **kibanaclient.Client) (string, error) {
	index, _ := cmd.Flags().GetString("index")
	dataView, _ := cmd.Flags().GetString("data-view")
	if strings.TrimSpace(dataView) == "" {
		return index, nil
	}
	if dryRun {
		if strings.TrimSpace(index) != "" {
			output.AuxWarn("--data-view overrides --index")
		}
		return dataViewDryRunIndex(strings.TrimSpace(dataView)), nil
	}
	client, _, err := newKibanaClient()
	if err != nil {
		return "", err
	}
	*clientRef = client
	resolved, err := resolveIndexFromFlags(cmd, client)
	if err != nil {
		return "", handleAPIError(err, jsonMode)
	}
	return resolved, nil
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

func printAggResult(result *kibanaclient.AggResult, fields []string, limit int) error {
	hasMetric := result.Metric != "" && result.Metric != "count"
	if jsonMode {
		buckets := make([]map[string]any, 0, len(result.Buckets))
		for _, b := range result.Buckets {
			m := map[string]any{"key": b.Key, "count": b.Count}
			if hasMetric {
				if b.Metric != nil {
					m["metric"] = *b.Metric
				} else {
					m["metric"] = nil
				}
			}
			buckets = append(buckets, m)
		}
		payload := map[string]any{
			"field":       result.Field,
			"total":       result.Total,
			"tookMs":      result.TookMs,
			"buckets":     buckets,
			"count":       len(buckets),
			"limit":       limit,
			"has_more":    false,
			"next_offset": nil,
			"_untrusted":  []string{"buckets"},
		}
		if result.AggType != "" {
			payload["aggType"] = result.AggType
		}
		if result.Metric != "" {
			payload["metric"] = result.Metric
		}
		printJSONSuccess(projectTopLevelFields(payload, fields))
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if hasMetric {
		_, _ = fmt.Fprintf(w, "KEY\tCOUNT\t%s\n", strings.ToUpper(result.Metric))
		for _, b := range result.Buckets {
			_, _ = fmt.Fprintf(w, "%s\t%d\t%s\n", b.Key, b.Count, formatMetric(b.Metric))
		}
	} else {
		_, _ = fmt.Fprintf(w, "KEY\tCOUNT\n")
		for _, b := range result.Buckets {
			_, _ = fmt.Fprintf(w, "%s\t%d\n", b.Key, b.Count)
		}
	}
	_ = w.Flush()
	output.AuxGray(fmt.Sprintf("  field=%s total=%d took=%dms", result.Field, result.Total, result.TookMs))
	return nil
}

func formatMetric(v *float64) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}
