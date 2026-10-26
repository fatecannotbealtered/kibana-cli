package cmd

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

// The profile group (CLI-101): the profiles of settings.json, one per Kibana host
// and user, which replace 1.x's contexts. `auth login --profile <name>` creates one;
// --profile or KIBANA_CLI_PROFILE picks one per call; settings.json's
// default_profile picks the default. Profiles hold no secret, and this group never
// reads the keyring.

const profileEvidence = "simulated:cmd/profile_test.go"

func registerProfile(app *aicli.App) {
	group := addGroup(app, "profile", "The profiles of settings.json: one per Kibana host and user", "profiles")
	app.Schema("profile_page", aicli.SchemaDef{Shape: "page", Fields: []string{"ref", "name", "host", "user", "default_index", "default", "active"}})
	addProfileList(app, group)
}

func addProfileList(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, _ []string) error {
		// App.Setting reports an invalid settings file as E_CONFIG (CLI-100).
		if _, _, e := app.Setting(nil, "host"); e != nil {
			return e
		}
		file := readSettingsFile(app)
		active, _ := app.Profile()
		items := []map[string]any{}
		for _, name := range app.Profiles() {
			p := file.Profiles[name]
			item := map[string]any{"ref": profileTarget(name), "name": name, "default": file.DefaultProfile == name, "active": name == active}
			if h := strings.TrimSpace(p["host"]); h != "" {
				item["host"] = config.NormalizeHost(h)
			}
			for _, k := range []string{"user", "default_index"} {
				if v := strings.TrimSpace(p[k]); v != "" {
					item[k] = v
				}
			}
			items = append(items, item)
		}
		total := len(items)
		var settingsFile any
		if path := app.SettingsPath(); path != "" {
			settingsFile = path
		}
		return app.List(cmd, aicli.Page{Items: items, Count: total, Total: &total, Sort: "file_order",
			Scope: map[string]any{"settings_file": settingsFile}})
	}}
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "List the profiles of settings.json: name, host, user, and which is the default and which is active",
		Effects: []aicli.Effect{aicli.Observe}, Output: "profile_page", ErrorCodes: []string{"E_CONFIG"},
		Maturity: aicli.Verified, Evidence: []string{profileEvidence, liveEvidenceSvc, liveEvidenceIoT}, Aliases: []string{"ls"},
		Examples: []string{"kibana-cli profile list"}})
}

// settingsDoc is the part of settings.json the profile list and auth login's
// read-back show. aicli has already checked the file's strict shape (CLI-100).
type settingsDoc struct {
	DefaultProfile string                       `json:"default_profile"`
	Profiles       map[string]map[string]string `json:"profiles"`
}

// readSettingsFile reads settings.json; a missing or unreadable file has no profiles.
func readSettingsFile(app *aicli.App) settingsDoc {
	var doc settingsDoc
	path := app.SettingsPath()
	if path == "" {
		return doc
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return doc
	}
	var loose struct {
		DefaultProfile *string                   `json:"default_profile"`
		Profiles       map[string]map[string]any `json:"profiles"`
	}
	if json.Unmarshal(raw, &loose) != nil {
		return doc
	}
	if loose.DefaultProfile != nil {
		doc.DefaultProfile = *loose.DefaultProfile
	}
	doc.Profiles = map[string]map[string]string{}
	for name, values := range loose.Profiles {
		m := map[string]string{}
		for k, v := range values {
			if s, ok := v.(string); ok {
				m[k] = s
			}
		}
		doc.Profiles[name] = m
	}
	return doc
}

// profileHostUser is the host and user a profile has in settings.json.
func profileHostUser(app *aicli.App, profile string) (string, string, bool) {
	p, ok := readSettingsFile(app).Profiles[profile]
	if !ok {
		return "", "", false
	}
	return config.NormalizeHost(p["host"]), strings.TrimSpace(p["user"]), true
}
