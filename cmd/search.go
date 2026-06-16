package cmd

import (
	"encoding/base64"
	"encoding/json"
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
	addLimitOffsetFlags(searchCmd, 50, "Max hits to return (1-1000)")
	searchCmd.Flags().String("fields", "", "Comma-separated fields in JSON output")
	searchCmd.Flags().StringArray("field", nil, "Exact term filter key=value (repeatable)")
	searchCmd.Flags().String("dsl", "", "Raw Elasticsearch _search query DSL (JSON); bypasses the flag query builder")
	searchCmd.Flags().String("search-after", "", "Stable cursor token (next_search_after from a prior page); alternative to --offset")
}

func runSearch(cmd *cobra.Command, _ []string) error {
	if !jsonMode && cmd.Flags().Changed("fields") {
		return failValidation("--fields is only supported with --format json")
	}
	if dsl, _ := cmd.Flags().GetString("dsl"); strings.TrimSpace(dsl) != "" {
		return runSearchDSL(cmd, dsl)
	}
	fm, err := loadFieldMapOrExit()
	if err != nil {
		return err
	}
	profile, _ := cmd.Flags().GetString("profile")
	service, _ := cmd.Flags().GetString("service")
	level, _ := cmd.Flags().GetString("level")
	traceID, _ := cmd.Flags().GetString("trace-id")

	var client *kibanaclient.Client
	index, err := resolveSearchIndex(cmd, &client)
	if err != nil {
		return err
	}
	// Fall back to the active context's default index when the caller named
	// neither an index nor a profile.
	if strings.TrimSpace(index) == "" && strings.TrimSpace(profile) == "" {
		if defIdx, _ := config.ActiveMeta(contextName); defIdx != "" {
			index = defIdx
		}
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

	limit, offset, limitCapped, err := requireSearchPage(cmd)
	if err != nil {
		return err
	}
	cursorToken, _ := cmd.Flags().GetString("search-after")
	cursorToken = strings.TrimSpace(cursorToken)
	if cursorToken != "" && cmd.Flags().Changed("offset") {
		return failValidation("--search-after and --offset are mutually exclusive; pick one pagination mode")
	}
	searchAfter, err := decodeSearchAfter(cursorToken)
	if err != nil {
		return failValidation(err.Error())
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
		Size:          limit,
		Offset:        offset,
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
		SearchAfter:   searchAfter,
	}
	if dryRunOutput("search logs", map[string]any{
		"index":        resolved.Index,
		"profile":      resolved.Profile,
		"query":        kibanaclient.BuildQuery(opts),
		"limit":        limit,
		"offset":       offset,
		"search_after": searchAfter,
	}) {
		return nil
	}
	if client == nil {
		client, _, err = newKibanaClient()
		if err != nil {
			return err
		}
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
			project = append(project, "_service", "_message", "_level", "traceId", "spanId")
		}
		hits := output.FlattenSearchHits(result.Hits, project, normalizeSpecFor(resolved))
		payload := map[string]any{
			"tookMs":  result.TookMs,
			"total":   result.Total,
			"index":   resolved.Index,
			"profile": resolved.Profile,
			"hits":    hits,
			"count":   len(hits),
			"limit":   limit,
			"offset":  offset,
		}
		usingCursor := len(searchAfter) > 0
		hasMore := int64(offset+len(hits)) < result.Total
		if usingCursor {
			// With a cursor, total-vs-offset is meaningless; a full page implies more.
			hasMore = len(hits) == limit
		}
		if result.TotalRelation == "gte" && len(hits) == limit {
			hasMore = true
		}
		payload["has_more"] = hasMore
		if hasMore && !usingCursor {
			payload["next_offset"] = offset + len(hits)
		} else {
			payload["next_offset"] = nil
		}
		// next_search_after is the stable cursor for the following page; null when
		// this page is the last one or yielded no hits.
		if hasMore && len(result.LastSort) > 0 {
			payload["next_search_after"] = encodeSearchAfter(result.LastSort)
		} else {
			payload["next_search_after"] = nil
		}
		if result.TotalRelation != "" {
			payload["totalRelation"] = result.TotalRelation
		}
		if limitCapped {
			payload["limit_capped"] = true
			payload["limit_max"] = sizeMax
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

// runSearchDSL handles `search --dsl <json>`: it sends the caller's raw _search
// body straight through the Console Proxy, for queries the flag surface can't
// express. The index still comes from --index/--data-view (default *).
func runSearchDSL(cmd *cobra.Command, dsl string) error {
	var body map[string]any
	if err := json.Unmarshal([]byte(dsl), &body); err != nil {
		return failValidation("invalid --dsl JSON: " + err.Error())
	}
	var client *kibanaclient.Client
	index, err := resolveSearchIndex(cmd, &client)
	if err != nil {
		return err
	}
	index = strings.TrimSpace(index)
	if index != "" && index != "*" {
		if err := config.ValidateIndexTarget(index); err != nil {
			return failValidation(err.Error())
		}
	}
	if dryRunOutput("search logs (raw dsl)", map[string]any{
		"index": index,
		"dsl":   body,
	}) {
		return nil
	}
	if client == nil {
		client, _, err = newKibanaClient()
		if err != nil {
			return err
		}
	}
	result, err := client.SearchRaw(apiCtx(), index, body)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	hits := output.FlattenSearchHits(result.Hits, getFieldsFlag(cmd), output.NormalizeSpec{})
	if jsonMode {
		payload := map[string]any{
			"index":      index,
			"tookMs":     result.TookMs,
			"total":      result.Total,
			"hits":       hits,
			"count":      len(hits),
			"_untrusted": []string{"hits"},
		}
		if result.TotalRelation != "" {
			payload["totalRelation"] = result.TotalRelation
		}
		printJSONSuccess(projectTopLevelFields(payload, getFieldsFlag(cmd)))
		return nil
	}
	if len(result.Hits) == 0 {
		output.Info("No hits.")
		return nil
	}
	for _, h := range result.Hits {
		ts := h.Timestamp
		if ts == "" {
			ts = "-"
		}
		fmt.Printf("%s  %s\n", ts, firstMessage(h.Source, nil))
	}
	indexLabel := index
	if indexLabel == "" {
		indexLabel = "*"
	}
	output.AuxGray(fmt.Sprintf("  %d of %d hits on %s (took %dms)", len(result.Hits), result.Total, indexLabel, result.TookMs))
	return nil
}

// encodeSearchAfter packs ES sort values into an opaque, URL-safe cursor token.
func encodeSearchAfter(sort []any) string {
	raw, err := json.Marshal(sort)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeSearchAfter unpacks a cursor token back into ES sort values. An empty
// token yields a nil cursor (first page).
func decodeSearchAfter(token string) ([]any, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("invalid --search-after token: %v", err)
	}
	var sort []any
	if err := json.Unmarshal(raw, &sort); err != nil {
		return nil, fmt.Errorf("invalid --search-after token: %v", err)
	}
	if len(sort) == 0 {
		return nil, fmt.Errorf("invalid --search-after token: empty cursor")
	}
	return sort, nil
}

func resolveSearchIndex(cmd *cobra.Command, clientRef **kibanaclient.Client) (string, error) {
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

func mustStringArrayFlag(cmd *cobra.Command, name string) []string {
	v, _ := cmd.Flags().GetStringArray(name)
	return v
}

// normalizeSpecFor builds the output normalization spec from a resolved field-map,
// compiling any user trace patterns (bad patterns are dropped, not fatal).
func normalizeSpecFor(resolved fieldmap.ResolvedSearch) output.NormalizeSpec {
	spec := output.NormalizeSpec{
		ServiceFields: resolved.ServiceFields,
		MessageFields: resolved.MessageFields,
		LevelFields:   resolved.LevelFields,
		TraceIDFields: resolved.TraceIDFields,
	}
	if pats, _ := msgtrace.CompilePatterns(resolved.TraceMsgRegexes); len(pats) > 0 {
		spec.TraceMsgPatterns = pats
	}
	return spec
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
