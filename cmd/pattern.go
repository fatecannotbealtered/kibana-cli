package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// The pattern group: Kibana's data views (index patterns before 8.0), the fields of
// an index pattern, and a field-map profile inferred from those fields. Data views
// come from the data views API on 8.x and 9.x and from the index-pattern saved
// objects on 7.x (kibanaclient picks by version).

const patternEvidence = "simulated:cmd/pattern_test.go"

func registerPattern(app *aicli.App) {
	group := addGroup(app, "pattern", "Data views (index patterns), their fields, and field-map profiles inferred from them", "patterns", "data-view", "data-views")
	app.Schema("data_view_page", aicli.SchemaDef{Shape: "page", Fields: []string{"ref", "id", "title", "name", "time_field"},
		Untrusted: []string{"title", "name"}})
	app.Schema("index_field_page", aicli.SchemaDef{Shape: "page",
		Fields: []string{"name", "type", "es_types", "searchable", "aggregatable"}, Untrusted: []string{"name", "es_types"}})
	app.Schema("field_profile_inference", aicli.SchemaDef{Shape: "object",
		Fields: []string{"index", "profile_name", "profile", "notes", "yaml", "next_command"}})
	addPatternList(app, group)
	addPatternFields(app, group)
	addPatternInfer(app, group)
}

func addPatternList(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, _ []string) error {
		limit, offset, err := listPage(cmd, 500)
		if err != nil {
			return err
		}
		client, _, err := newClient(app)
		if err != nil {
			return err
		}
		views, err := client.ListDataViews(cmd.Context())
		if err != nil {
			return readError(err)
		}
		query := strings.ToLower(flagString(cmd, "query"))
		items := []map[string]any{}
		sort.SliceStable(views, func(i, j int) bool { return strings.ToLower(views[i].Title) < strings.ToLower(views[j].Title) })
		for _, v := range views {
			if query != "" && !strings.Contains(strings.ToLower(v.Title), query) && !strings.Contains(strings.ToLower(v.Name), query) {
				continue
			}
			item := map[string]any{"ref": "index-pattern/" + v.ID, "id": v.ID, "title": v.Title}
			if v.Name != "" && v.Name != v.Title {
				item["name"] = v.Name
			}
			if v.TimeFieldName != "" {
				item["time_field"] = v.TimeFieldName
			}
			items = append(items, item)
		}
		scope := map[string]any{"limit": limit, "offset": offset}
		if query != "" {
			scope["query"] = flagString(cmd, "query")
		}
		return app.List(cmd, slicePage(items, limit, offset, scope, "title_asc"))
	}}
	c.Flags().String("query", "", "Only data views whose index pattern or name contains this text (case-insensitive)")
	addListFlags(c, 50, 500)
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "List Kibana's data views (index patterns) with their ref, index pattern and time field",
		Effects: []aicli.Effect{aicli.Observe}, Output: "data_view_page", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{patternEvidence, liveEvidenceSvc, liveEvidenceIoT}, Aliases: []string{"ls"},
		Examples: []string{"kibana-cli pattern list", "kibana-cli pattern list --query app-log"}})
}

func addPatternFields(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "fields", RunE: func(cmd *cobra.Command, _ []string) error {
		limit, offset, err := listPage(cmd, 1000)
		if err != nil {
			return err
		}
		index, viewRef, client, err := patternIndex(app, cmd)
		if err != nil {
			return err
		}
		fields, err := client.ListFields(cmd.Context(), index)
		if err != nil {
			return readError(err, onNotFound("No index matches that pattern.", "Use an index pattern from `kibana-cli pattern list`."))
		}
		query, typ := strings.ToLower(flagString(cmd, "query")), strings.ToLower(flagString(cmd, "type"))
		items := []map[string]any{}
		for _, f := range fields {
			if query != "" && !strings.Contains(strings.ToLower(f.Name), query) {
				continue
			}
			if typ != "" && strings.ToLower(f.Type) != typ {
				continue
			}
			if aggOnly, _ := cmd.Flags().GetBool("aggregatable"); aggOnly && !f.Aggregatable {
				continue
			}
			item := map[string]any{"name": f.Name, "type": f.Type, "searchable": f.Searchable, "aggregatable": f.Aggregatable}
			if len(f.ESTypes) > 0 {
				item["es_types"] = f.ESTypes
			}
			items = append(items, item)
		}
		scope := map[string]any{"index": index, "limit": limit, "offset": offset}
		if viewRef != "" {
			scope["data_view"] = viewRef
		}
		for k, v := range map[string]string{"query": flagString(cmd, "query"), "type": flagString(cmd, "type")} {
			if v != "" {
				scope[k] = v
			}
		}
		if aggOnly, _ := cmd.Flags().GetBool("aggregatable"); aggOnly {
			scope["aggregatable"] = true
		}
		return app.List(cmd, slicePage(items, limit, offset, scope, "name_asc"))
	}}
	c.Flags().String("index", "", "Index pattern, like 'app-log-*' (the value log search takes)")
	c.Flags().String("data-view", "", "Data view ref (index-pattern/<id>) or id instead of --index")
	c.Flags().String("query", "", "Only fields whose name contains this text (case-insensitive), like trace")
	c.Flags().String("type", "", "Only fields of this Kibana type: string, number, date, boolean, ip, geo_point, ...")
	c.Flags().Bool("aggregatable", false, "Only fields log aggregate --terms can count by")
	c.MarkFlagsMutuallyExclusive("index", "data-view")
	addListFlags(c, 50, 1000)
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "List the fields of an index pattern with their type and whether they are searchable and aggregatable",
		Effects: []aicli.Effect{aicli.Observe}, Output: "index_field_page", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{patternEvidence, liveEvidenceSvc, liveEvidenceIoT},
		Examples: []string{"kibana-cli pattern fields --index 'app-log-*' --query trace", "kibana-cli pattern fields --data-view index-pattern/17f4cc60 --aggregatable"}})
}

// patternIndex is the index pattern --index or --data-view names, checked against
// the allowlist, and the client it was resolved with.
func patternIndex(app *aicli.App, cmd *cobra.Command) (index, viewRef string, client *kibanaclient.Client, err error) {
	index = flagString(cmd, "index")
	raw := flagString(cmd, "data-view")
	if index == "" && raw == "" {
		e := aicli.Usage("missing_parameter", "--index", "Name the index pattern with --index, or a data view with --data-view.")
		e.Details["next_action"] = "Add --index <pattern>; `kibana-cli pattern list` lists the data views and their index patterns."
		return "", "", nil, e
	}
	if client, _, err = newClient(app); err != nil {
		return "", "", nil, err
	}
	if raw != "" {
		id := dataViewID(raw)
		view, err := client.ResolveDataView(cmd.Context(), id)
		if errors.Is(err, kibanaclient.ErrNoDataView) {
			return "", "", nil, aicli.NotFound("Kibana has no data view with that id, or this account cannot see it.",
				"Run `kibana-cli pattern list` to find the data view's ref.")
		}
		if err != nil {
			return "", "", nil, readError(err)
		}
		index, viewRef = view.Title, "index-pattern/"+view.ID
	}
	if err := checkIndex(app, index, "--index"); err != nil {
		return "", "", nil, err
	}
	return index, viewRef, client, nil
}

func addPatternInfer(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "infer", RunE: func(cmd *cobra.Command, _ []string) error {
		name := flagString(cmd, "name")
		if name != "" && !profileNameOK(name) {
			return aicli.Validation("--name", "letters, digits, _ and -, up to 64 characters", "--name is not a usable profile name.")
		}
		sample, _ := cmd.Flags().GetInt("sample")
		if sample < 0 || sample > 100 {
			return aicli.Validation("--sample", "0 to 100", "--sample must be between 0 and 100 documents.")
		}
		index, _, client, err := patternIndex(app, cmd)
		if err != nil {
			return err
		}
		if name == "" {
			name = fieldmap.SuggestProfileName(index)
		}
		fields, err := client.ListFields(cmd.Context(), index)
		if err != nil {
			return readError(err, onNotFound("No index matches that pattern.", "Use an index pattern from `kibana-cli pattern list`."))
		}
		names := make([]string, 0, len(fields))
		for _, f := range fields {
			names = append(names, f.Name)
		}
		result := fieldmap.Infer(index, names, sampleMessages(app, cmd, client, index, names, sample))
		snippet, err := result.YAMLSnippet(name)
		if err != nil {
			return err
		}
		profile := result.Profile()
		return app.OK(cmd, map[string]any{"index": index, "profile_name": name, "profile": profile,
			"notes": nonNilStrings(result.Notes), "yaml": snippet, "next_command": addProfileCommand(name, profile)})
	}}
	c.Flags().String("index", "", "Index pattern to infer the profile of, like 'app-log-*'")
	c.Flags().String("data-view", "", "Data view ref (index-pattern/<id>) or id instead of --index")
	c.Flags().String("name", "", "Profile name (default: derived from the index pattern)")
	c.Flags().Int("sample", 20, "Recent documents to read when looking for a trace id inside messages, 0 to 100")
	c.MarkFlagsMutuallyExclusive("index", "data-view")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Infer a field-map profile for an index pattern: its service, level, message and trace id fields, and whether the trace id sits in the message",
		Effects: []aicli.Effect{aicli.Observe}, Output: "field_profile_inference", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{patternEvidence, liveEvidenceSvc, liveEvidenceIoT},
		Examples: []string{"kibana-cli pattern infer --index 'app-log-*'", "kibana-cli pattern infer --index 'app-log-*' --name app"}})
}

// sampleMessages reads a few recent messages (one search of the last 24 hours) so
// Infer can find a trace id inside them; it reads none when a trace field exists.
func sampleMessages(app *aicli.App, cmd *cobra.Command, client *kibanaclient.Client, index string, names []string, sample int) []string {
	if sample == 0 {
		return nil
	}
	for _, n := range names {
		switch n {
		case "traceId", "trace_id", "trace.id", "log_traceId":
			return nil
		}
	}
	until := timeNow(app)
	res, err := client.Search(cmd.Context(), kibanaclient.SearchOptions{Index: index, From: isoTime(until.AddDate(0, 0, -1)),
		To: isoTime(until), Size: sample, SortDesc: true})
	if err != nil {
		app.Debugf("pattern infer sample: %v", err)
		return nil
	}
	var msgs []string
	for _, h := range res.Hits {
		if m := firstText(h.Source, defaultMessageFields); m != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

// addProfileCommand is the field-map add-profile line that stores an inferred
// profile. Every part is safe to run: the index passed config.ValidateIndexTarget,
// the name the profile-name check, and the field names come from Infer's fixed
// dictionaries, never from free text.
func addProfileCommand(name string, p fieldmap.Profile) string {
	parts := []string{"kibana-cli", "field-map", "add-profile", "--name", name}
	for _, pat := range p.IndexPatterns {
		parts = append(parts, "--index", shellQuote(pat))
	}
	for _, group := range []struct {
		flag   string
		fields []string
	}{{"--service-field", p.ServiceFields}, {"--level-field", p.LevelFields}, {"--message-field", p.MessageFields}, {"--trace-field", p.TraceIDFields}} {
		if len(group.fields) > 0 {
			parts = append(parts, group.flag, strings.Join(group.fields, ","))
		}
	}
	if p.TraceMode != "" {
		parts = append(parts, "--trace-mode", p.TraceMode)
	}
	return strings.Join(parts, " ")
}

// shellQuote quotes a value with glob characters for POSIX shells and PowerShell.
func shellQuote(s string) string {
	if strings.ContainsAny(s, "*?[ ,") {
		return "'" + s + "'"
	}
	return s
}

// ---------------------------------------------------------------- list helpers

// addListFlags adds --limit and --offset to a list read from one upstream answer.
func addListFlags(c *cobra.Command, def, max int) {
	c.Flags().Int("limit", def, fmt.Sprintf("Items to return, 1 to %d", max))
	c.Flags().Int("offset", 0, "Items to skip (next_offset of the previous page)")
}

func listPage(cmd *cobra.Command, max int) (int, int, error) {
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	if limit < 1 || limit > max {
		return 0, 0, aicli.Validation("--limit", fmt.Sprintf("1 to %d", max), fmt.Sprintf("--limit must be between 1 and %d.", max))
	}
	if offset < 0 {
		return 0, 0, aicli.Validation("--offset", "0 or more", "--offset must not be negative.")
	}
	return limit, offset, nil
}

// slicePage is one page of a list the tool holds whole: total is its length.
func slicePage(all []map[string]any, limit, offset int, scope map[string]any, sortName string) aicli.Page {
	total := len(all)
	page := []map[string]any{}
	if offset < total {
		end := min(offset+limit, total)
		page = all[offset:end]
	}
	p := aicli.Page{Items: page, Count: len(page), Total: &total, Scope: scope, Sort: sortName}
	if next := offset + len(page); next < total {
		p.HasMore, p.NextOffset = true, &next
	}
	return p
}
