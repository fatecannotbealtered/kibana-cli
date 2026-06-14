package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

// validObjectTypes are the saved-object types this CLI reads. Restricting the
// surface keeps the command predictable and avoids exposing system objects.
var validObjectTypes = map[string]bool{
	"dashboard":     true,
	"visualization": true,
	"search":        true,
	"index-pattern": true,
}

var objectsCmd = &cobra.Command{
	Use:   "objects",
	Short: "Read Kibana saved objects (dashboards, visualizations, searches, index-patterns)",
}

var objectsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved objects of a type via /api/saved_objects/_find",
	Long: `List Kibana saved objects of a given type, optionally filtered by title.

Examples:
  kibana-cli objects list --type dashboard --limit 50
  kibana-cli objects list --type visualization --search latency --json`,
	RunE: runObjectsList,
}

var objectsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get one saved object via /api/saved_objects/<type>/<id>",
	Long: `Fetch a single Kibana saved object by type and id.

Examples:
  kibana-cli objects get --type dashboard --id 7adfa750-4c81-11e8-b3d7-01146121b73d --json`,
	RunE: runObjectsGet,
}

func init() {
	rootCmd.AddCommand(objectsCmd)
	objectsCmd.AddCommand(objectsListCmd)
	objectsCmd.AddCommand(objectsGetCmd)

	objectsListCmd.Flags().String("type", "", "Saved object type: dashboard|visualization|search|index-pattern (required)")
	objectsListCmd.Flags().String("search", "", "Free-text filter on object title")
	addLimitOffsetFlags(objectsListCmd, 100, "Max saved objects to return")
	objectsListCmd.Flags().String("fields", "", "Comma-separated JSON data fields to include")
	_ = objectsListCmd.MarkFlagRequired("type")

	objectsGetCmd.Flags().String("type", "", "Saved object type: dashboard|visualization|search|index-pattern (required)")
	objectsGetCmd.Flags().String("id", "", "Saved object id (required)")
	objectsGetCmd.Flags().String("fields", "", "Comma-separated JSON data fields to include")
	_ = objectsGetCmd.MarkFlagRequired("type")
	_ = objectsGetCmd.MarkFlagRequired("id")
}

func validateObjectType(t string) (string, error) {
	t = strings.TrimSpace(t)
	if !validObjectTypes[t] {
		return "", failValidation("--type must be one of dashboard|visualization|search|index-pattern")
	}
	return t, nil
}

func runObjectsList(cmd *cobra.Command, _ []string) error {
	objType, err := validateObjectType(mustString(cmd, "type"))
	if err != nil {
		return err
	}
	search := mustString(cmd, "search")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, err := requireOffset(cmd)
	if err != nil {
		return err
	}
	if limit < 1 || limit > 1000 {
		return failValidation("--limit must be 1-1000")
	}
	if dryRunOutput("list saved objects", map[string]any{
		"type":   objType,
		"search": search,
		"limit":  limit,
		"offset": offset,
	}) {
		return nil
	}
	client, _, err := newKibanaClient()
	if err != nil {
		return err
	}
	// Kibana _find paginates server-side; fetch the page covering offset+limit.
	perPage := offset + limit
	objs, total, err := client.FindSavedObjects(apiCtx(), kibanaclient.FindSavedObjectsOptions{
		Type:    objType,
		Search:  search,
		Page:    1,
		PerPage: perPage,
	})
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	var page []kibanaclient.SavedObject
	if offset < len(objs) {
		end := offset + limit
		if end > len(objs) {
			end = len(objs)
		}
		page = objs[offset:end]
	}
	if jsonMode {
		payload := map[string]any{
			"type":        objType,
			"objects":     page,
			"count":       len(page),
			"total":       total,
			"limit":       limit,
			"offset":      offset,
			"has_more":    offset+len(page) < total,
			"next_offset": nextOffsetOrNil(offset, len(page), total),
			"_untrusted":  []string{"objects"},
		}
		printJSONSuccess(projectTopLevelFields(payload, getFieldsFlag(cmd)))
		return nil
	}
	if len(page) == 0 {
		output.Info("No saved objects found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tTITLE")
	for _, o := range page {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", o.ID, o.Title)
	}
	_ = w.Flush()
	output.AuxGray(fmt.Sprintf("  %d of %d %s objects", len(page), total, objType))
	return nil
}

func runObjectsGet(cmd *cobra.Command, _ []string) error {
	objType, err := validateObjectType(mustString(cmd, "type"))
	if err != nil {
		return err
	}
	id := strings.TrimSpace(mustString(cmd, "id"))
	if id == "" {
		return failValidation("--id is required")
	}
	if dryRunOutput("get saved object", map[string]any{
		"type": objType,
		"id":   id,
	}) {
		return nil
	}
	client, _, err := newKibanaClient()
	if err != nil {
		return err
	}
	obj, full, err := client.GetSavedObject(apiCtx(), objType, id)
	if err != nil {
		return handleAPIError(err, jsonMode)
	}
	if jsonMode {
		payload := map[string]any{
			"id":          obj.ID,
			"type":        obj.Type,
			"title":       obj.Title,
			"description": obj.Description,
			"updated_at":  obj.UpdatedAt,
			"object":      full,
			"_untrusted":  []string{"title", "description", "object"},
		}
		printJSONSuccess(projectTopLevelFields(payload, getFieldsFlag(cmd)))
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "ID\t%s\n", obj.ID)
	_, _ = fmt.Fprintf(w, "TYPE\t%s\n", obj.Type)
	_, _ = fmt.Fprintf(w, "TITLE\t%s\n", obj.Title)
	if obj.Description != "" {
		_, _ = fmt.Fprintf(w, "DESCRIPTION\t%s\n", obj.Description)
	}
	_ = w.Flush()
	return nil
}

func nextOffsetOrNil(offset, count, total int) any {
	if offset+count < total {
		return offset + count
	}
	return nil
}
