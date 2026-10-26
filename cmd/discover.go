package cmd

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/discover"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// --discover-url (#11): a Kibana Discover link that shows the documents a log search
// or aggregation matched, for a person to open (a Feishu card, a Jira comment). The
// link carries the CLI's own Elasticsearch query, minus the time range, as one custom
// filter, and the range in Discover's time picker, so Discover shows the CLI's result
// set whatever the query language was. It is opt-in: a link is 1 to 3 KB. Format,
// versions and limits: docs/DISCOVER-LINKS.md.

// dslLinkSince is where the link of a --dsl search starts: the body carries its own
// range, if any, so the time picker must exclude nothing before the search.
const dslLinkSince = "1970-01-01T00:00:00Z"

// maxLinkCandidates is how many data views a reason names at most.
const maxLinkCandidates = 3

// discoverFlag adds --discover-url.
func discoverFlag(c *cobra.Command) {
	c.Flags().Bool("discover-url", false,
		"Add scope.discover_url: a Kibana Discover link that shows the same documents (with --index, reads the data view list)")
}

// discoverRequest is what a link is to show.
type discoverRequest struct {
	// query is the Elasticsearch query the CLI sent, without the time range.
	query any
	// since and until are the time range of the link, ISO 8601 in UTC.
	since, until string
	// label lists what the filter pill names (discover.Label).
	label []string
	// timeChecked: the CLI searched a time range on q.TimeField, so the data view
	// must filter time on the same field (false for --dsl, whose link covers all
	// time before the search).
	timeChecked bool
}

// addDiscoverURL sets scope.discover_url when --discover-url asks for it: the link,
// or null with scope.discover_url_reason saying why no link could be built and what
// to do. It reads Kibana's data view list when --index (not --data-view) named the
// index, and never fails the command. It returns the scope paths it added, which
// are untrusted: the link and the reason carry query text and data view titles.
func addDiscoverURL(app *aicli.App, cmd *cobra.Command, client *kibanaclient.Client, q *logQuery, req discoverRequest, scope map[string]any) []string {
	if on, _ := cmd.Flags().GetBool("discover-url"); !on {
		return nil
	}
	link, reason := discoverURL(cmd.Context(), app, client, q, req)
	if reason != "" {
		scope["discover_url"], scope["discover_url_reason"] = nil, reason
		return []string{"scope.discover_url_reason"}
	}
	scope["discover_url"] = link
	return []string{"scope.discover_url"}
}

// discoverURL builds the link, or says why it cannot.
func discoverURL(ctx context.Context, app *aicli.App, client *kibanaclient.Client, q *logQuery, req discoverRequest) (string, string) {
	view, reason := discoverDataView(ctx, app, client, q)
	if reason != "" {
		return "", reason
	}
	if req.timeChecked && view.timeKnown && view.timeField != q.TimeField {
		if view.timeField == "" {
			return "", fmt.Sprintf("The data view %s has no time field, so Discover would show its documents of all time instead of the searched range; "+
				"search through a data view whose time field is %s (--data-view <ref>; `kibana-cli pattern list` lists them).", view.ref, q.TimeField)
		}
		return "", fmt.Sprintf("The data view %s filters time on %s but this search used %s, so Discover would show another time window; "+
			"search again with --time-field %s, or through a data view on %s (--data-view <ref>).", view.ref, view.timeField, q.TimeField, view.timeField, q.TimeField)
	}
	appPath := discover.AppPath(client.KnownVersion(), client.DataViewAPI() == kibanaclient.EndpointSavedObjects)
	link, err := discover.Link{Host: client.Host(), AppPath: appPath, DataView: view.id, Since: req.since, Until: req.until,
		Query: req.query, Label: discover.Label(req.label)}.URL()
	if err != nil {
		app.Debugf("discover link: %v", err)
		return "", "The link could not be encoded; the search itself is complete. Report this bug in kibana-cli."
	}
	return link, ""
}

// linkView is the data view a link opens.
type linkView struct {
	id, ref, timeField string
	// timeKnown: timeField is the data view's (Kibana's data views API lists data
	// views without their time field).
	timeKnown bool
}

// discoverDataView finds the data view of the search: the --data-view one, else the
// one data view whose index pattern is exactly the index searched.
func discoverDataView(ctx context.Context, app *aicli.App, client *kibanaclient.Client, q *logQuery) (linkView, string) {
	if q.DataView != "" {
		return linkView{id: strings.TrimPrefix(q.DataView, "index-pattern/"), ref: q.DataView, timeField: q.dataViewTime, timeKnown: true}, ""
	}
	views, err := client.ListDataViews(ctx)
	if err != nil {
		app.Debugf("discover link: listing data views: %v", err)
		return linkView{}, listFailureReason(err)
	}
	var exact []kibanaclient.DataView
	for _, v := range views {
		if strings.TrimSpace(v.Title) == q.Index {
			exact = append(exact, v)
		}
	}
	switch len(exact) {
	case 0:
		return linkView{}, noDataViewReason(q.Index, views)
	case 1:
		v := exact[0]
		return linkView{id: v.ID, ref: "index-pattern/" + v.ID, timeField: strings.TrimSpace(v.TimeFieldName),
			timeKnown: client.DataViewAPI() == kibanaclient.EndpointSavedObjects}, ""
	}
	refs := make([]string, 0, len(exact))
	for _, v := range exact {
		refs = append(refs, viewLabel(v))
	}
	sort.Strings(refs)
	return linkView{}, fmt.Sprintf("%d data views have the index pattern %s (%s); pass --data-view <ref> with the one to link to.",
		len(exact), q.Index, strings.Join(refs, ", "))
}

// noDataViewReason says that no data view has the index pattern, naming the closest
// ones that cover it.
func noDataViewReason(index string, views []kibanaclient.DataView) string {
	type cand struct {
		label   string
		literal int
	}
	var cands []cand
	for _, v := range views {
		if patternCovers(v.Title, index) {
			cands = append(cands, cand{viewLabel(v), len(strings.ReplaceAll(v.Title, "*", ""))})
		}
	}
	if len(cands) == 0 {
		return fmt.Sprintf("No Kibana data view has the index pattern %s, so Discover cannot show this search; "+
			"search through an existing data view with --data-view <ref> (`kibana-cli pattern list` lists them), or ask the user to create one for %s.", index, index)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].literal != cands[j].literal {
			return cands[i].literal > cands[j].literal
		}
		return cands[i].label < cands[j].label
	})
	names := make([]string, 0, maxLinkCandidates)
	for i := 0; i < len(cands) && i < maxLinkCandidates; i++ {
		names = append(names, cands[i].label)
	}
	return fmt.Sprintf("No Kibana data view has exactly the index pattern %s; the closest that cover it are %s: "+
		"search again with --data-view <ref> (its pattern may match more indices) to get a link.", index, strings.Join(names, ", "))
}

// viewLabel names a data view in a reason: its ref and index pattern.
func viewLabel(v kibanaclient.DataView) string {
	return fmt.Sprintf("index-pattern/%s (%s)", v.ID, strings.TrimSpace(v.Title))
}

// patternCovers reports whether a data view's index pattern (a comma-separated list
// of patterns, exclusions ignored) matches every part of the index searched; a '*'
// in the index is matched as text, so app-* covers app-log-*.
func patternCovers(title, index string) bool {
	var include []string
	for _, t := range strings.Split(title, ",") {
		if t = strings.TrimSpace(t); t != "" && !strings.HasPrefix(t, "-") {
			include = append(include, t)
		}
	}
	parts := strings.Split(index, ",")
	if len(include) == 0 {
		return false
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		matched := false
		for _, t := range include {
			if ok, err := path.Match(t, p); err == nil && ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// listFailureReason says why the data view list could not be read.
func listFailureReason(err error) string {
	var ae *kibanaclient.APIError
	var ne *kibanaclient.NetworkError
	switch {
	case errors.As(err, &ae) && (ae.StatusCode == 401 || ae.StatusCode == 403):
		return fmt.Sprintf("This account may not list Kibana's data views (HTTP %d), so no link was built; pass --data-view <ref> if the user knows the data view.", ae.StatusCode)
	case errors.As(err, &ae):
		return fmt.Sprintf("Kibana's data view list could not be read (HTTP %d), so no link was built; try again, or pass --data-view <ref>.", ae.StatusCode)
	case errors.As(err, &ne):
		return "Kibana did not answer the data view list, so no link was built; try again, or pass --data-view <ref>."
	}
	return "Kibana's data view list could not be read, so no link was built; try again, or pass --data-view <ref>."
}

// labelParts is what the link's filter pill names: the filters of the query.
func (q *logQuery) labelParts() []string {
	var parts []string
	if q.Service != "" {
		parts = append(parts, "service "+q.Service)
	}
	if q.Level != "" {
		parts = append(parts, "level "+q.Level)
	}
	if q.TraceID != "" {
		parts = append(parts, "trace "+q.TraceID)
	}
	for _, k := range q.fieldOrder {
		parts = append(parts, k+"="+q.Fields[k])
	}
	if text := strings.TrimSpace(q.Query); text != "" {
		kind := "query"
		switch {
		case q.Precise:
			kind = "phrase"
		case q.Language == queryLanguageKQL:
			kind = "kql"
		}
		parts = append(parts, kind+` "`+text+`"`)
	}
	if p := q.Resolved.Profile; p != "" {
		parts = append(parts, "profile "+p)
	}
	if len(parts) == 0 {
		parts = append(parts, "all documents")
	}
	return parts
}

// searchLink is the link request of a flag-mode search or aggregation.
func searchLink(q *logQuery, opts kibanaclient.SearchOptions) discoverRequest {
	return discoverRequest{query: kibanaclient.QueryWithoutTimeRange(opts), since: isoTime(q.Since), until: isoTime(q.Until),
		label: q.labelParts(), timeChecked: true}
}
