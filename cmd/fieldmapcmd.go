package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
)

// The field-map group: the local file that names, per index, the fields holding the
// service, the level, the message and the trace id (default ~/.kibana-cli/
// field-map.yaml, or the field_map_file setting). field-map get reads it; init and
// add-profile write it through the gate (artifact for a new file, persist for an
// existing one; SEC-012 checks the path against the scope's outputs and roots).

const fieldMapEvidence = "simulated:cmd/fieldmap_test.go"

func registerFieldMap(app *aicli.App) {
	group := addGroup(app, "field-map", "The local field map: which fields hold service, level, message and trace id per index", "fieldmap")
	app.Schema("field_map", aicli.SchemaDef{Shape: "object",
		Fields: []string{"path", "version", "defaults", "index_rules", "profiles", "services"}})
	app.Schema("field_map_file", aicli.SchemaDef{Shape: "object", Fields: []string{"path", "bytes", "sha256", "profiles"}})
	app.Schema("field_map_profile_added", aicli.SchemaDef{Shape: "object", Fields: []string{"path", "profile", "bytes", "sha256"}})
	addFieldMapGet(app, group)
	addFieldMapInit(app, group)
	addFieldMapAddProfile(app, group)
	// 1.x's `config init` and `config show` (CLI-022).
	app.AddShortcut("config", "field-map")
}

func addFieldMapGet(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "get", RunE: func(cmd *cobra.Command, _ []string) error {
		m, path, err := loadFieldMap(app, cmd)
		if err != nil {
			return err
		}
		if m == nil {
			e := aicli.NotFound("There is no field-map file yet.", "Run `kibana-cli field-map init` for an example to edit, or `kibana-cli pattern infer --index <pattern>` and its next_command.")
			e.Details["path"] = path
			return e
		}
		profiles := []map[string]any{}
		for _, name := range sortedKeysOf(m.Profiles) {
			profiles = append(profiles, profileView(name, m.Profiles[name]))
		}
		services := []map[string]any{}
		for _, name := range sortedKeysOf(m.Services) {
			s := m.Services[name]
			item := map[string]any{"name": name}
			if len(s.Profiles) > 0 {
				item["profiles"] = s.Profiles
			}
			if len(s.MatchFields) > 0 {
				item["match_fields"] = s.MatchFields
			}
			if s.Index != "" {
				item["index"] = s.Index
			}
			services = append(services, item)
		}
		rules := m.IndexRules
		if rules == nil {
			rules = []fieldmap.IndexRule{}
		}
		return app.OK(cmd, map[string]any{"path": path, "version": m.Version, "defaults": m.Defaults, "index_rules": rules,
			"profiles": profiles, "services": services})
	}}
	c.Flags().String("field-map-file", "", "Field-map file to read instead of the field_map_file setting")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Show the field map: its defaults, index rules, profiles and services",
		Effects: []aicli.Effect{aicli.Observe}, Output: "field_map", ErrorCodes: []string{"E_CONFIG", "E_NOT_FOUND"},
		Maturity: aicli.Verified, Evidence: []string{fieldMapEvidence, liveEvidenceSvc, liveEvidenceIoT}, Aliases: []string{"show", "view"},
		Params:   map[string]aicli.ParamDoc{"field-map-file": {Type: "path"}},
		Examples: []string{"kibana-cli field-map get"}})
}

func addFieldMapInit(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "init", RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := fieldMapPath(app, cmd)
		if err != nil {
			return err
		}
		example, _ := fieldmap.Parse([]byte(fieldmap.ExampleYAML))
		return app.WriteOutput(cmd, aicli.OutputSpec{Path: path,
			Produce: func(context.Context) ([]byte, error) { return []byte(fieldmap.ExampleYAML), nil },
			Summary: map[string]any{"profiles": sortedKeysOf(example.Profiles)}})
	}}
	c.Flags().String("field-map-file", "", "Where to write the field map instead of the field_map_file setting")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Write an example field map to edit; only creates the file, never overwrites one",
		Effects: []aicli.Effect{aicli.Artifact}, Output: "field_map_file",
		ErrorCodes: []string{"E_CONFIG", "E_IO", "E_VALIDATION"},
		Maturity:   aicli.Experimental, Evidence: []string{fieldMapEvidence},
		Params:   map[string]aicli.ParamDoc{"field-map-file": {Type: "path"}},
		Examples: []string{"kibana-cli field-map init --dry-run", "kibana-cli field-map init --confirm <confirm_token>"}})
}

func addFieldMapAddProfile(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "add-profile", RunE: func(cmd *cobra.Command, _ []string) error {
		name, profile, err := addProfileInputs(cmd)
		if err != nil {
			return err
		}
		path, err := fieldMapPath(app, cmd)
		if err != nil {
			return err
		}
		view := profileView(name, profile)
		return app.Write(cmd, aicli.WriteSpec{
			Target:   aicli.Target{Key: path, Kind: "file", Label: "field map"},
			ReadBack: "field-map get",
			Preview: func(context.Context) (aicli.Preview, error) {
				raw, exists, version, err := fileState(path)
				if err != nil {
					return aicli.Preview{}, err
				}
				var blocks []aicli.Block
				action := fmt.Sprintf("Add profile %s to %s", name, path)
				if !exists {
					action = fmt.Sprintf("Create %s with profile %s", path, name)
				} else if m, perr := fieldmap.Parse(raw); perr != nil {
					blocks = append(blocks, aicli.Block{Kind: "invalid_file", Message: "The field map is not valid YAML; fix it first (`kibana-cli field-map get` says why)."})
				} else if m != nil {
					if _, taken := m.Profiles[name]; taken {
						blocks = append(blocks, aicli.Block{Kind: "exists", Message: "The field map already has a profile of this name; choose another --name or edit the file."})
					}
				}
				return aicli.Preview{Action: action, Changes: []aicli.FieldChange{{Field: "profile", After: view}},
					BaseVersion: version, BaseSource: "file_sha256", CanApply: len(blocks) == 0, BlockedBy: blocks, Outputs: []string{path}}, nil
			},
			Apply: func(context.Context, aicli.Op) (aicli.Result, error) {
				raw, exists, _, err := fileState(path)
				if err != nil {
					return aicli.Result{}, aicli.NotDispatched(err)
				}
				edited, err := fieldmap.AddProfile(raw, name, profile)
				if err != nil {
					return aicli.Result{}, aicli.NotDispatched(aicli.Validation("--name", "a profile the field map does not have yet",
						"The profile could not be added to the field map; nothing was written."))
				}
				out, e := app.OpenOutput(path, exists)
				if e != nil {
					return aicli.Result{}, aicli.NotDispatched(e)
				}
				if _, err := out.Write(edited); err != nil {
					out.Abort()
					return aicli.Result{}, aicli.NotDispatched(aicli.NewError("E_IO", "The field map could not be written; nothing was changed.",
						map[string]any{"next_action": fmt.Sprintf("Check that %s is writable.", path)}))
				}
				size, sum := out.Size(), out.SHA256()
				if e := out.Commit(); e != nil {
					return aicli.Result{}, aicli.NotDispatched(e)
				}
				// Read the file back: the profile as the tool now reads it.
				observed := map[string]any{"profile": nil}
				if m, err := fieldmap.LoadFile(path); err == nil && m != nil {
					if p, ok := m.Profiles[name]; ok {
						observed["profile"] = profileView(name, p)
					}
				}
				return aicli.Result{Observed: observed, Data: map[string]any{"path": path, "profile": view, "bytes": size, "sha256": sum}}, nil
			},
		})
	}}
	f := c.Flags()
	f.String("name", "", "Profile name: letters, digits, _ and - (default: derived from the first --index)")
	f.StringSlice("index", nil, "Index patterns the profile applies to, like 'app-log-*' (comma-separated or repeated)")
	f.String("time-field", "", "Time field of these indices (default: the field map's, else @timestamp)")
	f.StringSlice("service-field", nil, "Fields that hold the service name, in order of preference")
	f.StringSlice("level-field", nil, "Fields that hold the log level")
	f.StringSlice("message-field", nil, "Fields that hold the message")
	f.StringSlice("trace-field", nil, "Fields that hold the trace id (with --trace-mode field)")
	f.String("trace-mode", "", "Where the trace id lives: field (a document field) or msg (inside the message)")
	f.String("field-map-file", "", "Field-map file to write instead of the field_map_file setting")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Add a profile to the field map (creating the file when there is none), as pattern infer's next_command suggests",
		Effects: []aicli.Effect{aicli.Artifact, aicli.Persist}, Output: "field_map_profile_added",
		ErrorCodes: []string{"E_CONFIG", "E_IO", "E_VALIDATION"},
		Maturity:   aicli.Experimental, Evidence: []string{fieldMapEvidence},
		Params: map[string]aicli.ParamDoc{"field-map-file": {Type: "path"},
			"trace-mode": {Type: "enum", Enum: []string{fieldmap.TraceModeField, fieldmap.TraceModeMsg}}},
		Examples: []string{
			"kibana-cli field-map add-profile --name app --index 'app-log-*' --service-field log_app --message-field msg --trace-mode msg --dry-run",
			"kibana-cli field-map add-profile --name app --index 'app-log-*' --service-field log_app --message-field msg --trace-mode msg --confirm <confirm_token>",
		}})
}

// profileNamePattern is a field-map profile name kibana-cli writes: one that is safe
// on a command line and as a YAML key.
var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func profileNameOK(name string) bool { return profileNamePattern.MatchString(name) }

// addProfileInputs reads the profile from the flags (no file, no network).
func addProfileInputs(cmd *cobra.Command) (string, fieldmap.Profile, error) {
	indexes, _ := cmd.Flags().GetStringSlice("index")
	indexes = fieldmap.UniqueStrings(indexes)
	if len(indexes) == 0 {
		e := aicli.Usage("missing_parameter", "--index", "A profile needs the index patterns it applies to: --index.")
		e.Details["next_action"] = "Add --index <pattern>, or run `kibana-cli pattern infer --index <pattern>` and use its next_command."
		return "", fieldmap.Profile{}, e
	}
	for _, idx := range indexes {
		if !fieldmapIndexPattern.MatchString(idx) {
			return "", fieldmap.Profile{}, aicli.Validation("--index", "an index pattern of letters, digits and . _ - + * : ?", "--index is not an index pattern.")
		}
	}
	name := flagString(cmd, "name")
	if name == "" {
		name = fieldmap.SuggestProfileName(indexes[0])
	}
	if !profileNameOK(name) {
		return "", fieldmap.Profile{}, aicli.Validation("--name", "letters, digits, _ and -, up to 64 characters", "--name is not a usable profile name.")
	}
	mode := strings.ToLower(flagString(cmd, "trace-mode"))
	if mode != "" && mode != fieldmap.TraceModeField && mode != fieldmap.TraceModeMsg {
		return "", fieldmap.Profile{}, aicli.Validation("--trace-mode", "field or msg", "--trace-mode must be field or msg.")
	}
	p := fieldmap.Profile{IndexPatterns: indexes, TimeField: flagString(cmd, "time-field"), TraceMode: mode}
	for flag, dst := range map[string]*[]string{"service-field": &p.ServiceFields, "level-field": &p.LevelFields,
		"message-field": &p.MessageFields, "trace-field": &p.TraceIDFields} {
		list, _ := cmd.Flags().GetStringSlice(flag)
		list = fieldmap.UniqueStrings(list)
		for _, f := range list {
			if !fieldNamePattern.MatchString(f) {
				return "", fieldmap.Profile{}, aicli.Validation("--"+flag, "field names of letters, digits and . _ - @", fmt.Sprintf("--%s holds something that is not a field name.", flag))
			}
		}
		*dst = list
	}
	return name, p, nil
}

var (
	fieldmapIndexPattern = regexp.MustCompile(`^[A-Za-z0-9._*+:?-]+$`)
	fieldNamePattern     = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)
)

// profileView is a profile as output shows it: snake_case keys, its name inside.
func profileView(name string, p fieldmap.Profile) map[string]any {
	out := map[string]any{"name": name}
	if p.Index != "" {
		out["index"] = p.Index
	}
	for k, v := range map[string][]string{"index_patterns": p.IndexPatterns, "service_fields": p.ServiceFields,
		"level_fields": p.LevelFields, "message_fields": p.MessageFields, "trace_id_fields": p.TraceIDFields,
		"trace_msg_patterns": p.TraceMsgRegexes} {
		if len(v) > 0 {
			out[k] = v
		}
	}
	if p.TimeField != "" {
		out["time_field"] = p.TimeField
	}
	if p.TraceMode != "" {
		out["trace_mode"] = p.TraceMode
	}
	return out
}

// fileState reads a local file and its version for a preview's base: "absent", or
// sha256:<hex> of its content. A directory or an unreadable file is E_IO.
func fileState(path string) ([]byte, bool, string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, "absent", nil
	}
	if err == nil && info.IsDir() {
		return nil, false, "", aicli.Validation("--field-map-file", "a file", "The field-map path is a directory; name a file.")
	}
	raw, rerr := os.ReadFile(path)
	if err != nil || rerr != nil {
		return nil, false, "", aicli.NewError("E_IO", "The field-map file cannot be read; nothing was changed.",
			map[string]any{"dispatch_state": "not_started", "next_action": fmt.Sprintf("Check that %s is readable.", path)})
	}
	sum := sha256.Sum256(raw)
	return raw, true, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
