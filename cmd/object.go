package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// The object group: Kibana saved objects (dashboards, visualizations, Lens
// visualizations, saved searches, data views) through the saved objects API, which
// Kibana 7.x, 8.x and 9.x answer (8.0 deprecated it for writes and imports; reads
// remain). A saved object's ref is <type>/<id>.

const objectEvidence = "simulated:cmd/object_test.go"

// objectTypes are the saved-object types kibana-cli reads; a fixed list keeps the
// command predictable and system objects out of reach.
var objectTypes = []string{"dashboard", "visualization", "lens", "search", "index-pattern"}

var savedObjectFields = []string{"ref", "id", "type", "title", "description", "updated_at", "references", "attributes"}

func registerObject(app *aicli.App) {
	group := addGroup(app, "object", "Kibana saved objects: dashboards, visualizations, saved searches and data views", "objects")
	app.Schema("saved_object_page", aicli.SchemaDef{Shape: "page", Fields: savedObjectFields,
		DefaultFields: []string{"ref", "title", "updated_at"}, Untrusted: []string{"title", "description", "references", "attributes"}})
	app.Schema("saved_object", aicli.SchemaDef{Shape: "object", Fields: savedObjectFields,
		DefaultFields: []string{"ref", "id", "type", "title", "description", "updated_at", "references"},
		Untrusted:     []string{"title", "description", "references", "attributes"}})
	addObjectList(app, group)
	addObjectGet(app, group)
}

func addObjectList(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, _ []string) error {
		objType, err := objectType(flagString(cmd, "type"), "--type")
		if err != nil {
			return err
		}
		limit, offset, err := listPage(cmd, 100)
		if err != nil {
			return err
		}
		client, _, err := newClient(app)
		if err != nil {
			return err
		}
		// _find pages by page number: an offset on a page boundary is one page of
		// limit; any other offset reads the first offset+limit and keeps the end.
		page, perPage := 1, offset+limit
		if offset%limit == 0 {
			page, perPage = offset/limit+1, limit
		}
		query := flagString(cmd, "query")
		objs, total, err := client.FindSavedObjects(cmd.Context(), kibanaclient.FindSavedObjectsOptions{Type: objType, Search: query, Page: page, PerPage: perPage})
		if err != nil {
			return readError(err)
		}
		if perPage != limit {
			if offset < len(objs) {
				objs = objs[offset:]
			} else {
				objs = nil
			}
		}
		if len(objs) > limit {
			objs = objs[:limit]
		}
		items := make([]map[string]any, 0, len(objs))
		for _, o := range objs {
			items = append(items, savedObjectItem(o, false))
		}
		scope := map[string]any{"type": objType, "limit": limit, "offset": offset,
			"order": "Kibana's _find order (relevance when --query is given)"}
		if query != "" {
			scope["query"] = query
		}
		p := aicli.Page{Items: items, Count: len(items), Scope: scope, Sort: "upstream"}
		if total < offset+len(items) {
			total = offset + len(items)
		}
		p.Total = &total
		if next := offset + len(items); next < total && len(items) > 0 {
			p.HasMore, p.NextOffset = true, &next
		}
		return app.List(cmd, p)
	}}
	c.Flags().String("type", "", "Saved object type: "+strings.Join(objectTypes, ", "))
	c.Flags().String("query", "", "Only objects whose title matches this text (Kibana's simple query syntax; word* for a prefix)")
	addListFlags(c, 20, 100)
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "List saved objects of one type (dashboards, visualizations, saved searches, data views), optionally matching a title",
		Effects: []aicli.Effect{aicli.Observe}, Output: "saved_object_page", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{objectEvidence, liveEvidenceSvc, liveEvidenceIoT}, Aliases: []string{"ls"},
		RenamedFlags: map[string]string{"search": "query"},
		Params:       map[string]aicli.ParamDoc{"type": {Type: "enum", Enum: objectTypes}},
		Examples:     []string{"kibana-cli object list --type dashboard", "kibana-cli object list --type search --query 'payment*'"}})
}

func addObjectGet(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "get <object>", RunE: func(cmd *cobra.Command, _ []string) error {
		objType, id, err := objectRef(cmd)
		if err != nil {
			return err
		}
		client, _, err := newClient(app)
		if err != nil {
			return err
		}
		obj, err := client.GetSavedObject(cmd.Context(), objType, id)
		if err != nil {
			return readError(err, onNotFound(fmt.Sprintf("Kibana has no %s with that id, or this account cannot see it.", objType),
				fmt.Sprintf("Run `kibana-cli object list --type %s` to find its ref.", objType)))
		}
		if aicli.OutputFormat(cmd) == "raw" {
			b, err := json.Marshal(map[string]any{"id": obj.ID, "type": obj.Type, "updated_at": obj.UpdatedAt,
				"attributes": obj.Attributes, "references": obj.References})
			if err != nil {
				return err
			}
			return app.OK(cmd, append(b, '\n'))
		}
		return app.OK(cmd, savedObjectItem(*obj, aicli.WantsField(cmd, "attributes")))
	}}
	c.Flags().String("object", "", "The saved object's ref, <type>/<id>, like dashboard/7adfa750-4c81-11e8-b3d7-01146121b73d")
	c.Flags().String("type", "", "Saved object type, with --id instead of a ref: "+strings.Join(objectTypes, ", "))
	c.Flags().String("id", "", "Saved object id, with --type instead of a ref")
	c.MarkFlagsMutuallyExclusive("object", "type")
	c.MarkFlagsMutuallyExclusive("object", "id")
	c.MarkFlagsRequiredTogether("type", "id")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Read one saved object by its ref (<type>/<id>): title, description, references and, with --fields +attributes, its attributes",
		Effects: []aicli.Effect{aicli.Observe}, Output: "saved_object", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{objectEvidence, liveEvidenceSvc, liveEvidenceIoT}, Positional: "object", RawOutput: true,
		Aliases: []string{"show", "view"},
		Params:  map[string]aicli.ParamDoc{"type": {Type: "enum", Enum: objectTypes}},
		Examples: []string{"kibana-cli object get dashboard/7adfa750-4c81-11e8-b3d7-01146121b73d",
			"kibana-cli object get search/2d1c9a40 --fields +attributes", "kibana-cli object get dashboard/7adfa750 --format raw"}})
}

// objectRef reads the saved object a command names: the ref <type>/<id> (--object
// or the positional), or --type with --id as 1.x took them.
func objectRef(cmd *cobra.Command) (string, string, error) {
	if raw := flagString(cmd, "object"); raw != "" {
		typ, id, ok := strings.Cut(raw, "/")
		if !ok || strings.TrimSpace(id) == "" {
			return "", "", aicli.Validation("--object", "<type>/<id>, like dashboard/7adfa750", "A saved object's ref is <type>/<id>.")
		}
		t, err := objectType(typ, "--object")
		return t, strings.TrimSpace(id), err
	}
	typ, id := flagString(cmd, "type"), flagString(cmd, "id")
	if typ == "" || id == "" {
		e := aicli.Usage("missing_parameter", "--object", "Name the saved object by its ref, <type>/<id>.")
		e.Details["next_action"] = "Run `kibana-cli object list --type <type>` and pass an item's ref."
		return "", "", e
	}
	t, err := objectType(typ, "--type")
	return t, id, err
}

// objectType checks a saved-object type against the types this tool reads.
func objectType(t, param string) (string, error) {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "data-view" || t == "data_view" {
		t = "index-pattern"
	}
	for _, ok := range objectTypes {
		if t == ok {
			return t, nil
		}
	}
	if t == "" {
		e := aicli.Usage("missing_parameter", param, "Name the saved object type: "+strings.Join(objectTypes, ", ")+".")
		e.Details["next_action"] = "Add --type dashboard (or another of " + strings.Join(objectTypes, ", ") + ")."
		return "", e
	}
	return "", aicli.Validation(param, strings.Join(objectTypes, ", "), "The saved object type must be one of "+strings.Join(objectTypes, ", ")+".")
}

// savedObjectItem is a saved object as output shows it. Attributes (Kibana's own
// object, camelCase keys) are listed as {name, value} fields; long values are cut.
func savedObjectItem(o kibanaclient.SavedObject, withAttributes bool) map[string]any {
	refs := make([]map[string]any, 0, len(o.References))
	for _, r := range o.References {
		refs = append(refs, map[string]any{"ref": r.Type + "/" + r.ID, "type": r.Type, "id": r.ID, "name": r.Name})
	}
	out := map[string]any{"ref": o.Type + "/" + o.ID, "id": o.ID, "type": o.Type, "title": o.Title, "references": refs,
		"updated_at": utcTime(o.UpdatedAt)}
	if o.Description != "" {
		out["description"] = o.Description
	}
	if withAttributes {
		var attrs map[string]any
		_ = json.Unmarshal(o.Attributes, &attrs)
		out["attributes"] = flattenDoc(attrs, nil, getValueLimit)
	}
	return out
}
