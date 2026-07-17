package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

// loadFieldMapOrExit loads the active field-map: a per-context override file when
// the selected context sets fieldMapFile, otherwise the global field-map.yaml.
func loadFieldMapOrExit() (*fieldmap.Map, error) {
	_, fieldMapFile := config.ActiveMeta(contextName)
	var (
		fm  *fieldmap.Map
		err error
	)
	if strings.TrimSpace(fieldMapFile) != "" {
		fm, err = fieldmap.LoadFile(filepath.Join(config.Dir(), fieldMapFile))
	} else {
		fm, err = fieldmap.Load()
	}
	if err != nil {
		return nil, failValidation(err.Error())
	}
	return fm, nil
}

type queryTarget struct {
	Index             string
	DataViewID        string
	DataViewTimeField string
	Client            *kibanaclient.Client
}

func resolveQueryTarget(cmd *cobra.Command) (queryTarget, error) {
	index, _ := cmd.Flags().GetString("index")
	dataViewID, _ := cmd.Flags().GetString("data-view")
	dataViewID = strings.TrimSpace(dataViewID)
	if dataViewID == "" {
		return queryTarget{Index: index}, nil
	}
	if strings.TrimSpace(index) != "" {
		output.AuxWarn("--data-view overrides --index")
	}
	client, _, err := newKibanaClient()
	if err != nil {
		return queryTarget{}, err
	}
	view, err := client.ResolveDataView(apiCtx(), dataViewID)
	if err != nil {
		return queryTarget{}, handleAPIError(err, jsonMode)
	}
	return queryTarget{
		Index:             view.Title,
		DataViewID:        view.ID,
		DataViewTimeField: view.TimeFieldName,
		Client:            client,
	}, nil
}

func resolveQueryTimeField(cmd *cobra.Command, target queryTarget, fallback string) (string, error) {
	explicit, _ := cmd.Flags().GetString("time-field")
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit, nil
	}
	if target.DataViewID != "" {
		if field := strings.TrimSpace(target.DataViewTimeField); field != "" {
			return field, nil
		}
		return "", failValidation("data view " + target.DataViewID + " has no timeFieldName; pass --time-field explicitly")
	}
	return fallback, nil
}

type queryOutputMeta struct {
	Context       string
	ContextSource string
	Host          string
	Index         string
	DataViewID    string
	TimeField     string
	From          string
	To            string
	QueryLanguage string
}

func loadQueryConnectionMeta() (context, source, host string, err error) {
	cfg, loadErr := config.LoadConnectionMetaFor(contextName)
	if loadErr != nil {
		return "", "", "", failConfig(loadErr.Error())
	}
	return cfg.ContextName, queryContextSource(cfg.ContextName), strings.TrimRight(cfg.Host, "/"), nil
}

func queryContextSource(resolvedContext string) string {
	if resolvedContext == "env" {
		return "environment_auth"
	}
	if strings.TrimSpace(contextName) != "" {
		return "flag"
	}
	if strings.TrimSpace(os.Getenv("KIBANA_CLI_CONTEXT")) != "" {
		return "environment"
	}
	if resolvedContext != "" {
		return "current"
	}
	return "none"
}

func addQueryOutputMeta(detail map[string]any, meta queryOutputMeta) {
	detail["context"] = meta.Context
	detail["contextSource"] = meta.ContextSource
	detail["host"] = meta.Host
	detail["index"] = meta.Index
	detail["dataViewId"] = meta.DataViewID
	detail["timeField"] = meta.TimeField
	detail["from"] = meta.From
	detail["to"] = meta.To
	detail["queryLanguage"] = meta.QueryLanguage
}

func queryOutputSummary(meta queryOutputMeta) string {
	return fmt.Sprintf(
		"context=%s contextSource=%s host=%s index=%s dataViewId=%s timeField=%s from=%s to=%s queryLanguage=%s",
		emptyLabel(meta.Context), emptyLabel(meta.ContextSource), emptyLabel(meta.Host),
		emptyLabel(meta.Index), emptyLabel(meta.DataViewID), emptyLabel(meta.TimeField),
		emptyLabel(meta.From), emptyLabel(meta.To), emptyLabel(meta.QueryLanguage),
	)
}

func emptyLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
