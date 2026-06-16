package cmd

import (
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

func dataViewDryRunIndex(id string) string {
	return "<data-view:" + id + ">"
}

func resolveIndexFromFlags(cmd *cobra.Command, client *kibanaclient.Client) (string, error) {
	index, _ := cmd.Flags().GetString("index")
	dataView, _ := cmd.Flags().GetString("data-view")
	if strings.TrimSpace(dataView) != "" && strings.TrimSpace(index) != "" {
		output.AuxWarn("--data-view overrides --index")
	}
	if strings.TrimSpace(dataView) != "" {
		id := strings.TrimSpace(dataView)
		if dryRun {
			return dataViewDryRunIndex(id), nil
		}
		title, err := client.ResolveIndexPattern(apiCtx(), id)
		if err != nil {
			return "", err
		}
		return title, nil
	}
	return index, nil
}
