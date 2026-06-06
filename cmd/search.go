package cmd

import (
	"fmt"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/msgtrace"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search service logs via Kibana",
	Long: `Search logs through Kibana Console Proxy (_search).

Use field-map.yaml (--profile / --service) when indices use different field names.
Use --data-view with a Kibana data view id to resolve the index pattern title.

Examples:
  kibana-cli search --profile java-app --service order-svc --level ERROR --from now-30m
  kibana-cli search --index 'app-test-log-*' --query 'timeout' --from now-15m
  kibana-cli search --data-view 17f4cc60-eafc-11ec-8e68-f14beaf972d1 --level ERROR`,
	RunE: runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().String("index", "", "Index or index pattern (overrides profile)")
	searchCmd.Flags().String("data-view", "", "Kibana data view / index-pattern id (resolves to index title)")
	searchCmd.Flags().String("profile", "", "Profile name from field-map.yaml")
	searchCmd.Flags().String("service", "", "Logical service name (maps to multiple fields)")
	searchCmd.Flags().String("level", "", "Log level (maps across level_fields)")
	searchCmd.Flags().String("trace-id", "", "Trace ID (uses trace_mode / trace_field from field-map or flags)")
	searchCmd.Flags().String("trace-mode", "", "Override trace lookup: field|msg (for heterogeneous indices)")
	searchCmd.Flags().StringArray("trace-field", nil, "Override trace id fields (repeatable, e.g. log_traceId)")
	searchCmd.Flags().String("query", "", "Keyword/Lucene query; searches across ALL fields by default (use --precise to narrow to message)")
	searchCmd.Flags().Bool("precise", false, "Restrict --query to message field(s) via match_phrase (opt-in; default searches all fields)")
	searchCmd.Flags().String("from", "now-15m", "Range start (date math, e.g. now-15m)")
	searchCmd.Flags().String("to", "now", "Range end")
	searchCmd.Flags().String("time-field", "", "Timestamp field (default from profile or @timestamp)")
	searchCmd.Flags().Int("size", 50, "Max hits (1-1000)")
	searchCmd.Flags().String("fields", "", "Comma-separated fields in JSON output")
	searchCmd.Flags().StringArray("field", nil, "Exact term filter key=value (repeatable)")
}

func runSearch(cmd *cobra.Command, _ []string) error {
	if !jsonMode && cmd.Flags().Changed("fields") {
		return failValidation("--fields is only supported with --format json")
	}
	fm, err := loadFieldMapOrExit()
	if err != nil {
		return err
	}
	profile, _ := cmd.Flags().GetString("profile")
	service, _ := cmd.Flags().GetString("service")
	level, _ := cmd.Flags().GetString("level")
	traceID, _ := cmd.Flags().GetString("trace-id")

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
	timeField, _ := cmd.Flags().GetString("time-field")
	if timeField != "" {
		resolved.TimeField = timeField
	}
	if tm, _ := cmd.Flags().GetString("trace-mode"); strings.TrimSpace(tm) != "" {
		resolved.TraceMode = fieldmap.NormalizeTraceMode(tm)
	}
	if tf := mustStringArrayFlag(cmd, "trace-field"); len(tf) > 0 {
		resolved.TraceIDFields = fieldmap.UniqueStrings(tf)
	}

	size, sizeCapped, err := requireSize(cmd)
	if err != nil {
		return err
	}
	fields := map[string]string{}
	for _, pair := range mustStringArrayFlag(cmd, "field") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k == "" {
			return failValidation("invalid --field, expected key=value: " + pair)
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	query, _ := cmd.Flags().GetString("query")
	precise, _ := cmd.Flags().GetBool("precise")
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")

	opts := kibanaclient.SearchOptions{
		Index:         resolved.Index,
		Query:         query,
		MsgOnly:       precise,
		Fields:        fields,
		From:          from,
		To:            to,
		TimeField:     resolved.TimeField,
		Size:          size,
		SortDesc:      true,
		ServiceValue:  service,
		ServiceFields: resolved.ServiceFields,
		LevelValue:    level,
		LevelFields:   resolved.LevelFields,
		TraceID:       traceID,
		TraceFields:   resolved.TraceIDFields,
		TraceMode:     resolved.TraceMode,
		MessageField:  resolved.PrimaryMessageField(),
		MessageFields: resolved.MessageFields,
	}
	if dryRunOutput("search logs", map[string]any{
		"index":   resolved.Index,
		"profile": resolved.Profile,
		"query":   kibanaclient.BuildQuery(opts),
		"size":    size,
	}) {
		return nil
	}
	result, err := client.Search(apiCtx(), opts)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	if jsonMode {
		project := getFieldsFlag(cmd)
		if len(project) == 0 && len(resolved.MessageFields) > 0 {
			project = append(project, "@timestamp")
			project = append(project, resolved.MessageFields...)
			project = append(project, resolved.ServiceFields...)
			project = append(project, resolved.LevelFields...)
		}
		hits := output.FlattenSearchHits(result.Hits, project)
		payload := map[string]any{
			"ok":      true,
			"tookMs":  result.TookMs,
			"total":   result.Total,
			"index":   resolved.Index,
			"profile": resolved.Profile,
			"hits":    hits,
			"size":    size,
		}
		if result.TotalRelation != "" {
			payload["totalRelation"] = result.TotalRelation
		}
		if sizeCapped {
			payload["sizeCapped"] = true
			payload["sizeMax"] = sizeMax
		}
		if resolved.TraceMode != "" {
			payload["traceMode"] = resolved.TraceMode
		}
		if result.Total == 0 {
			if reason, hint, probes := explainZeroHits(client, opts, precise); reason != "" {
				payload["zeroReason"] = reason
				payload["hint"] = hint
				payload["diagnostics"] = probes
			}
		}
		printJSONSuccess(payload)
		return nil
	}
	if len(result.Hits) == 0 {
		output.Info("No hits.")
		if _, hint, _ := explainZeroHits(client, opts, precise); hint != "" {
			output.AuxGray("  " + hint)
		}
		return nil
	}
	for _, h := range result.Hits {
		ts := h.Timestamp
		if ts == "" {
			ts = "-"
		}
		msg := firstMessage(h.Source, resolved.MessageFields)
		svc := firstFieldValue(h.Source, resolved.ServiceFields)
		lvl := firstFieldValue(h.Source, resolved.LevelFields)
		traceHint := ""
		if tid, _, ok := msgtrace.ParseBracketIDs(msg); ok {
			traceHint = tid[:8] + "… "
		}
		fmt.Printf("%s  %-8s  %-16s  %s%s\n", ts, lvl, svc, traceHint, msg)
	}
	output.AuxGray(fmt.Sprintf("  %d of %d hits on %s (took %dms)", len(result.Hits), result.Total, resolved.Index, result.TookMs))
	return nil
}

func mustStringArrayFlag(cmd *cobra.Command, name string) []string {
	v, _ := cmd.Flags().GetStringArray(name)
	return v
}

func firstMessage(src map[string]any, fields []string) string {
	for _, f := range fields {
		if v, ok := src[f].(string); ok && v != "" {
			return v
		}
	}
	for _, key := range []string{"message", "msg", "log.message"} {
		if v, ok := src[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func firstFieldValue(src map[string]any, fields []string) string {
	for _, f := range fields {
		if v, ok := src[f].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// explainZeroHits probes why a search returned nothing, distinguishing
// "no data in window" vs "filters excluded all" vs "term lives in other fields".
// Best-effort: returns empty reason if probes fail.
func explainZeroHits(client *kibanaclient.Client, opts kibanaclient.SearchOptions, precise bool) (reason, hint string, probes map[string]any) {
	base := kibanaclient.SearchOptions{Index: opts.Index, From: opts.From, To: opts.To, TimeField: opts.TimeField}
	windowTotal, err := client.Count(apiCtx(), base)
	if err != nil {
		return "", "", nil
	}
	probes = map[string]any{"windowTotal": windowTotal}
	if windowTotal == 0 {
		return "no_data_in_window",
			"No documents in this index for the time window; widen --from/--to or verify the index pattern.",
			probes
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		broad := base
		broad.Query = q // MsgOnly stays false: search all fields
		if broadTotal, err := client.Count(apiCtx(), broad); err == nil {
			probes["broadMatchTotal"] = broadTotal
			if broadTotal > 0 {
				h := fmt.Sprintf("%d docs match %q across all fields but current filters/precision excluded them.", broadTotal, q)
				if precise {
					h += " Drop --precise to search all fields."
				}
				return "matched_in_other_fields", h, probes
			}
		}
	}
	return "filters_excluded_all",
		fmt.Sprintf("%d docs exist in the window but none matched; relax --level/--service/--field or the query.", windowTotal),
		probes
}
