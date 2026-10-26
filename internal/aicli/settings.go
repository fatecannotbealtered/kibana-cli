// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Settings and profiles (CLI-100, CLI-101). A tool declares the values it reads
// (host, project, ...) in App.Settings and reads each with App.Setting: a command
// flag, then <PREFIX>_<ENV>, then the active profile of the settings file, then the
// default. The settings file is <PREFIX>_SETTINGS_FILE, else
// <state_dir>/settings.json (not config.json, which 1.x tools keep in their own
// format):
//
//	{"default_profile": "work", "profiles": {"work": {"host": "https://gitlab.acme.test"}}}
//
// The active profile is --profile (a root flag aicli registers when App.Settings is
// set), then <PREFIX>_PROFILE, then default_profile, then the only profile. Secrets
// never go in the settings file (SEC-040).

// Setting is one value a tool reads through App.Setting. Name is snake_case; Env
// defaults to upper(Name) and is read as <PREFIX>_<Env>; Flag names a command flag
// that overrides the setting when the running command has it and it is set. A Secret
// setting never comes from the settings file and is redacted from debug lines.
type Setting struct {
	Name        string
	Env         string
	Flag        string
	Default     string
	Secret      bool
	Description string
}

// envName is the setting's environment variable without the tool prefix.
func (s Setting) envName() string {
	if s.Env != "" {
		return s.Env
	}
	return strings.ToUpper(s.Name)
}

const maxSettingsBytes = 64 << 10

var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// settingsFile is the parsed settings file. reason is set when the file is invalid.
type settingsFile struct {
	path           string
	exists         bool
	reason         string
	defaultProfile string
	profiles       map[string]map[string]string
	names          []string // profile names, sorted
	order          []string // profile names in file order (App.Profiles, Q3)
}

// profileState is the active profile and where it came from (context.profile).
// requested is a profile asked for by --profile or <PREFIX>_PROFILE (requestedFrom:
// flag or env) that the settings file does not define.
type profileState struct {
	file          settingsFile
	name          string
	source        string
	requested     string
	requestedFrom string
}

// settingsPath is <PREFIX>_SETTINGS_FILE, else <state_dir>/settings.json; "" when
// no state directory can be resolved. The variable must hold an absolute path
// (C4): loadSettings refuses a relative one.
func (a *App) settingsPath() string {
	if p := strings.TrimSpace(a.env(a.envPrefix() + "_SETTINGS_FILE")); p != "" {
		return p
	}
	dir, e := a.stateDirPath()
	if e != nil {
		return ""
	}
	return filepath.Join(dir, "settings.json")
}

// SettingsPath returns the settings file aicli reads (CLI-100): <PREFIX>_SETTINGS_FILE,
// else <state_dir>/settings.json; "" when no state directory can be resolved. A
// configure command (auth login) writes the profile's settings there, through the
// gate.
func (a *App) SettingsPath() string { return a.settingsPath() }

// Profile returns the active profile's name and where it came from: flag, env,
// config (default_profile) or single (the only profile); "" and none when no profile
// is active (CLI-101). Tools key their keyring entries by it. For a command that may
// run under an unknown profile (Meta.AllowUnknownProfile, P6, Q4), a requested
// profile the settings file does not have is the one returned, with its source
// (flag or env).
func (a *App) Profile() (name, source string) {
	ps := a.profileState()
	if ps.name == "" && ps.requested != "" && a.allowsUnknownProfile(nil) {
		return ps.requested, ps.requestedFrom
	}
	return ps.name, ps.source
}

// loadSettings reads and strictly validates the settings file at path (CLI-101),
// the way loadScope reads the scope file. A missing file is no profiles.
func (a *App) loadSettings(path string) settingsFile {
	sf := settingsFile{path: path, profiles: map[string]map[string]string{}, names: []string{}}
	if path == "" {
		return sf
	}
	if !filepath.IsAbs(path) {
		sf.exists = true
		sf.reason = a.envPrefix() + "_SETTINGS_FILE must be an absolute path"
		return sf
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return sf
	}
	sf.exists = true
	if err != nil {
		sf.reason = "it cannot be read"
		return sf
	}
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0 || mode&fs.ModeIrregular != 0:
		sf.reason = "it must be a regular file, not a link"
		return sf
	case !mode.IsRegular():
		sf.reason = "it is not a regular file"
		return sf
	}
	if reason := fileTrustProblem(path, info); reason != "" {
		sf.reason = reason
		return sf
	}
	if info.Size() > maxSettingsBytes {
		sf.reason = "it is larger than 64 KiB"
		return sf
	}
	raw, err := readLimited(path, maxSettingsBytes)
	if err != nil {
		sf.reason = "it cannot be read"
		return sf
	}
	if len(raw) > maxSettingsBytes {
		sf.reason = "it is larger than 64 KiB"
		return sf
	}
	def, profiles, reason := a.parseSettings(raw)
	if reason != "" {
		sf.reason = reason
		return sf
	}
	sf.defaultProfile, sf.profiles = def, profiles
	sf.names = sortedKeys(profiles)
	sf.order = sf.names
	if top, err := readOrderedObject(raw); err == nil {
		if v, ok := top.vals["profiles"]; ok && !isJSONNull(v) {
			if p, err := readOrderedObject(v); err == nil && len(p.keys) == len(profiles) {
				sf.order = p.keys
			}
		}
	}
	return sf
}

// parseSettings parses a settings file strictly: one UTF-8 JSON object without a
// BOM or duplicate keys, only default_profile and profiles at the top, profile names
// that match the pattern, declared settings with scalar values, no secrets.
// Problems are reported in sorted key order, the first one wins.
func (a *App) parseSettings(raw []byte) (string, map[string]map[string]string, string) {
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		return "", nil, "it starts with a byte order mark"
	}
	if !utf8.Valid(raw) {
		return "", nil, "it is not valid UTF-8"
	}
	if _, err := strictKeys(raw); err != nil {
		return "", nil, err.Error()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		return "", nil, "it is not valid JSON"
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", nil, "it holds more than one JSON value"
	}
	known := []string{"default_profile", "profiles"}
	for _, k := range sortedKeys(top) {
		if !contains(known, k) {
			reason := fmt.Sprintf("unknown key `%s`", truncateRunes(k, 40))
			if near := closestWords(k, known); len(near) > 0 {
				reason += fmt.Sprintf(" (did you mean `%s`?)", near[0])
			}
			return "", nil, reason
		}
	}
	// default_profile is a profile name or null; "" names no profile and is invalid.
	def, hasDefault := "", false
	switch v := top["default_profile"].(type) {
	case nil:
	case string:
		def, hasDefault = v, true
	default:
		return "", nil, "default_profile must be a string or null"
	}
	profiles := map[string]map[string]string{}
	rawProfiles := map[string]any{}
	if p, has := top["profiles"]; has && p != nil {
		obj, ok := p.(map[string]any)
		if !ok {
			return "", nil, "profiles must be an object"
		}
		rawProfiles = obj
	}
	for _, name := range sortedKeys(rawProfiles) {
		shown := truncateRunes(name, 64)
		if !profileNamePattern.MatchString(name) {
			return "", nil, fmt.Sprintf("the profile name `%s` is not allowed (use a-z, 0-9, _ and -, up to 64 characters)", shown)
		}
		values, ok := rawProfiles[name].(map[string]any)
		if !ok {
			return "", nil, fmt.Sprintf("profile `%s` must be an object", shown)
		}
		profile := map[string]string{}
		for _, key := range sortedKeys(values) {
			s, declared := a.settingNamed(key)
			switch {
			case !declared:
				return "", nil, fmt.Sprintf("profile `%s` sets `%s`, which is not a setting of %s", shown, truncateRunes(key, 40), a.toolName())
			case s.Secret:
				return "", nil, fmt.Sprintf("`%s` is a secret; secrets go in the OS keyring, not the settings file (SEC-040)", truncateRunes(key, 40))
			}
			switch v := values[key].(type) {
			case string:
				profile[key] = v
			case json.Number:
				profile[key] = v.String()
			case bool:
				profile[key] = fmt.Sprint(v)
			default:
				return "", nil, fmt.Sprintf("`%s` in profile `%s` must be a string, number or true/false", truncateRunes(key, 40), shown)
			}
		}
		profiles[name] = profile
	}
	if hasDefault {
		if _, ok := profiles[def]; !ok {
			return "", nil, fmt.Sprintf("default_profile `%s` is not one of its profiles", truncateRunes(def, 64))
		}
	}
	return def, profiles, ""
}

// settingNamed finds a declared setting.
func (a *App) settingNamed(name string) (Setting, bool) {
	for _, s := range a.Settings {
		if s.Name == name {
			return s, true
		}
	}
	return Setting{}, false
}

// profileState resolves the active profile: --profile, then <PREFIX>_PROFILE, then
// default_profile, then the only profile. It is computed once per run.
func (a *App) profileState() profileState {
	if st := a.run; st != nil && st.profile != nil {
		return *st.profile
	}
	ps := profileState{file: a.loadSettings(a.settingsPath()), source: "none"}
	switch {
	case !ps.file.exists:
		a.debugOnce("settings file: %s (not found)", ps.file.path)
	case ps.file.reason != "":
		a.debugOnce("settings file: %s (invalid: %s)", ps.file.path, ps.file.reason)
	default:
		a.debugOnce("settings file: %s (valid)", ps.file.path)
	}
	if ps.file.reason == "" {
		// --profile "" counts as not given (D10): <PREFIX>_PROFILE applies.
		requested, source := "", ""
		if f := a.profileFlag(); f != nil && f.Changed && f.Value.String() != "" {
			requested, source = f.Value.String(), "flag"
		} else if v := a.env(a.envPrefix() + "_PROFILE"); v != "" {
			requested, source = v, "env"
		}
		_, defined := ps.file.profiles[requested]
		switch {
		case requested != "" && defined:
			ps.name, ps.source = requested, source
		case requested != "":
			ps.requested, ps.requestedFrom = requested, source
		case ps.file.defaultProfile != "":
			ps.name, ps.source = ps.file.defaultProfile, "config"
		case len(ps.file.names) == 1:
			ps.name, ps.source = ps.file.names[0], "single"
		}
	}
	if ps.name != "" {
		a.debugOnce("active profile: %s (%s)", ps.name, ps.source)
	} else {
		a.debugOnce("active profile: none")
	}
	if st := a.run; st != nil {
		cp := ps
		st.profile = &cp
	}
	return ps
}

// profileFlag is the root's --profile flag (registered when App.Settings is set).
func (a *App) profileFlag() *pflag.Flag {
	if a.Settings == nil || a.Root == nil {
		return nil
	}
	return a.Root.PersistentFlags().Lookup("profile")
}

// settingsError reports an invalid settings file (E_CONFIG).
func (a *App) settingsError(sf settingsFile) *Error {
	return NewError("E_CONFIG", fmt.Sprintf("The settings file %s is invalid: %s.", sf.path, sf.reason), map[string]any{
		"dispatch_state": "not_started",
		"next_action":    fmt.Sprintf("Ask the user to fix %s; `%s context` shows which file is read.", sf.path, a.toolName()),
	})
}

// unknownProfileError reports a requested profile the settings file does not define
// (CLI-101, D5). The requested name is the caller's text: it goes to
// details.profile, declared untrusted, never into message or next_action (CLI-017).
// did_you_mean lists the closest profile names, else the file's first names.
func (a *App) unknownProfileError(ps profileState) *Error {
	path := ps.file.path
	if path == "" {
		path = "the settings file"
	}
	message := "The requested profile is not in the settings file."
	next := fmt.Sprintf("Use one of the profiles in details.did_you_mean, or add the profile to %s.", path)
	if !ps.file.exists {
		message = "The requested profile does not exist: there is no settings file."
		next = fmt.Sprintf("Create %s with this profile, or run without --profile.", path)
	}
	e := NewError("E_CONFIG", message, map[string]any{"dispatch_state": "not_started", "next_action": next,
		"profile": truncateRunes(ps.requested, 64), "source": ps.requestedFrom})
	e.Untrusted = []string{"profile"}
	near := closestWords(ps.requested, ps.file.names)
	if len(near) == 0 {
		near = ps.file.names[:min(len(ps.file.names), 3)]
	}
	if len(near) > 0 {
		e.Details["did_you_mean"] = append([]string{}, near...)
	}
	return e
}

// checkProfile stops a tool command whose requested profile (--profile or
// <PREFIX>_PROFILE) is not in the settings file (CLI-101, D4): E_CONFIG before the
// command's own code runs, whether or not it reads a setting. The built-ins keep
// working, so context and doctor can report the problem, and so does a command that
// may run under an unknown profile (Meta.AllowUnknownProfile, P6, Q4).
func (a *App) checkProfile(c *cobra.Command) error {
	if a.Settings == nil || a.reg.builtin[c] || a.allowsUnknownProfile(c) {
		return nil
	}
	if ps := a.profileState(); ps.file.reason == "" && ps.requested != "" {
		return a.unknownProfileError(ps)
	}
	return nil
}

// allowsUnknownProfile reports a command declared with Meta.AllowUnknownProfile (P6,
// Q4); nil means the running command.
func (a *App) allowsUnknownProfile(c *cobra.Command) bool {
	if c == nil && a.run != nil {
		c = a.run.cmd
	}
	m := a.metaFor(c)
	return m != nil && m.AllowUnknownProfile
}

// SetProfileSettings writes values into one profile of the settings file (CLI-101,
// P5), for a configure command such as auth login to call from its Apply. profile
// must match ^[a-z0-9][a-z0-9_-]{0,63}$ and every key must be a declared setting
// that is not secret (secrets go in the OS keyring, SEC-040). The current file is
// read with the same strict parser, and an invalid file is E_CONFIG and never
// overwritten. The profile is created when missing; every other key and profile is
// kept. default_profile is set only when this write creates the file's first profile
// (no file, or a file without profiles): it then names this profile (Q3, RC2-1).
// Otherwise it stays as it was, a name, null or absent; profiles without a default
// are a person's choice, never filled in. The file keeps its order: default_profile
// first (when present), then the profiles in file order (a new one last), each with
// its keys in file order (new keys last, sorted), indented by two spaces. It is
// written atomically (a temporary file in the same directory, fsync, rename) with
// mode 0600; the state directory is created 0700 when the file lives there. A
// failure to write is E_IO. On any error nothing was written, so Apply returns it
// wrapped in NotDispatched.
func (a *App) SetProfileSettings(profile string, values map[string]string) *Error {
	if !profileNamePattern.MatchString(profile) {
		e := Validation("--profile", "a profile name: a-z, 0-9, _ and -, up to 64 characters", "The profile name is not allowed; nothing was written.")
		e.Details["next_action"] = "Give a profile name of a-z, 0-9, _ and -, up to 64 characters, starting with a letter or a digit."
		return e
	}
	for _, k := range sortedKeys(values) {
		s, declared := a.settingNamed(k)
		switch {
		case !declared:
			return internalError("App.SetProfileSettings: %q is not declared in App.Settings.", k)
		case s.Secret:
			return internalError("App.SetProfileSettings: %q is a secret setting; secrets go in the OS keyring, not the settings file (SEC-040).", k)
		}
	}
	path := a.settingsPath()
	if path == "" {
		if _, e := a.stateDirPath(); e != nil {
			return e
		}
		return ioError("The settings file cannot be found; nothing was written.", fmt.Sprintf("Set %s_STATE_DIR or %s_SETTINGS_FILE.", a.envPrefix(), a.envPrefix()), nil)
	}
	sf := a.loadSettings(path)
	if sf.reason != "" {
		return a.settingsError(sf)
	}
	var raw []byte
	if sf.exists {
		var err error
		if raw, err = readLimited(path, maxSettingsBytes); err != nil {
			return ioError("The settings file cannot be read; nothing was written.", fmt.Sprintf("Check %s.", path), err)
		}
	}
	data, err := settingsWithValues(raw, profile, values)
	if err != nil {
		return ioError("The settings file cannot be read; nothing was written.", fmt.Sprintf("Check %s.", path), err)
	}
	dir := filepath.Dir(path)
	if state, e := a.stateDirPath(); e == nil && strings.EqualFold(filepath.Clean(state), filepath.Clean(dir)) {
		if _, e := a.ensureStateDir(); e != nil {
			return e
		}
	}
	if err := writeFileAtomic(dir, filepath.Base(path), data); err != nil {
		return ioError("The settings file cannot be written; nothing was changed.", fmt.Sprintf("Check that %s is writable.", dir), err)
	}
	if st := a.run; st != nil {
		st.profile = nil // read again on the next Setting
	}
	return nil
}

// settingsWithValues is the settings file raw (nil: none yet) with values set in
// profile, in the order SetProfileSettings writes (Q3).
func settingsWithValues(raw []byte, profile string, values map[string]string) ([]byte, error) {
	top, profiles, entry := newOrderedObject(), newOrderedObject(), newOrderedObject()
	var err error
	if raw != nil {
		if top, err = readOrderedObject(raw); err != nil {
			return nil, err
		}
	}
	if v, ok := top.vals["profiles"]; ok && !isJSONNull(v) {
		if profiles, err = readOrderedObject(v); err != nil {
			return nil, err
		}
	}
	if v, ok := profiles.vals[profile]; ok {
		if entry, err = readOrderedObject(v); err != nil {
			return nil, err
		}
	}
	first := len(profiles.keys) == 0 // this write creates the file's first profile
	for _, k := range sortedKeys(values) {
		entry.set(k, compactJSON(values[k]))
	}
	profiles.set(profile, compactJSON(entry))
	out := newOrderedObject()
	// The file's first profile becomes its default (Q3, RC2-1). Profiles without a
	// default are a person's choice: default_profile stays as it was, null or absent.
	def, has := top.vals["default_profile"]
	if first && (!has || isJSONNull(def)) {
		def, has = compactJSON(profile), true
	}
	if has {
		out.set("default_profile", def)
	}
	out.set("profiles", compactJSON(profiles))
	var b bytes.Buffer
	if err := json.Indent(&b, compactJSON(out), "", "  "); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// orderedObject is a JSON object whose keys keep their order (the settings file is
// rewritten in place, Q3). Values are compact JSON.
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func newOrderedObject() orderedObject { return orderedObject{vals: map[string]json.RawMessage{}} }

func (o *orderedObject) set(k string, v json.RawMessage) {
	if _, has := o.vals[k]; !has {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// MarshalJSON writes the object compactly in its key order.
func (o orderedObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(compactJSON(k))
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// readOrderedObject reads a JSON object's keys in order, each value re-encoded
// compactly (numbers kept as written).
func readOrderedObject(raw []byte) (orderedObject, error) {
	o := newOrderedObject()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	t, err := dec.Token()
	if err != nil {
		return o, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return o, errors.New("not a JSON object")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return o, err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return o, err
		}
		o.set(k, compactRaw(v))
	}
	return o, nil
}

// compactRaw re-encodes a scalar compactly (strings without HTML escaping,
// numbers as written); objects and arrays are compacted as they are.
func compactRaw(v json.RawMessage) json.RawMessage {
	t := bytes.TrimSpace(v)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		var b bytes.Buffer
		if json.Compact(&b, t) == nil {
			return b.Bytes()
		}
		return t
	}
	dec := json.NewDecoder(bytes.NewReader(t))
	dec.UseNumber()
	var x any
	if dec.Decode(&x) != nil {
		return t
	}
	return compactJSON(x)
}

// compactJSON is v's compact JSON encoding without HTML escaping or a newline.
func compactJSON(v any) json.RawMessage {
	b, err := encodeJSON(v, false)
	if err != nil {
		return json.RawMessage("null")
	}
	return bytes.TrimRight(b, "\n")
}

func isJSONNull(v json.RawMessage) bool { return string(bytes.TrimSpace(v)) == "null" }

// Profiles returns the profile names of the settings file in file order (Q3): empty
// without a file, and for an invalid one, whose E_CONFIG App.Setting reports.
func (a *App) Profiles() []string {
	return append([]string{}, a.profileState().file.order...)
}

// Setting returns the value of a declared setting and where it came from (CLI-100):
// the setting's Flag when the command has it and it is set ("flag"), then
// <PREFIX>_<Env> ("env"), then the active profile ("profile"), then the default
// ("default"). cmd may be nil (the running command is used). An invalid settings
// file or a requested profile that does not exist is E_CONFIG, whatever the source.
func (a *App) Setting(cmd *cobra.Command, name string) (value, source string, err *Error) {
	s, ok := a.settingNamed(name)
	if !ok {
		return "", "", internalError("App.Setting: %q is not declared in App.Settings.", name)
	}
	ps := a.profileState()
	if ps.file.reason != "" {
		return "", "", a.settingsError(ps.file)
	}
	if cmd == nil && a.run != nil {
		cmd = a.run.cmd
	}
	// A requested profile that does not exist stops every setting, except for a
	// command that may run under it (Meta.AllowUnknownProfile, P6, Q4): it reads the
	// flag, the environment and the default.
	if ps.requested != "" && !a.allowsUnknownProfile(cmd) {
		return "", "", a.unknownProfileError(ps)
	}
	if s.Flag != "" && cmd != nil {
		if f := lookupFlag(cmd, s.Flag); f != nil && f.Changed {
			return f.Value.String(), "flag", nil
		}
	}
	if v := a.env(a.envPrefix() + "_" + s.envName()); v != "" {
		return v, "env", nil
	}
	if ps.name != "" {
		if v, ok := ps.file.profiles[ps.name][s.Name]; ok {
			return v, "profile", nil
		}
	}
	return s.Default, "default", nil
}

// profileContext is context.profile (CLI-101). It never fails: an invalid settings
// file reports no profile and its path; a requested profile that does not exist is
// not active.
func (a *App) profileContext() map[string]any {
	ps := a.profileState()
	var file any
	if ps.file.exists {
		file = ps.file.path
	}
	out := map[string]any{"name": nil, "source": "none", "profiles": []string{}, "file": file}
	if ps.file.reason != "" {
		return out
	}
	out["profiles"] = nonNil(ps.file.names)
	if ps.name != "" {
		out["name"], out["source"] = ps.name, ps.source
	}
	return out
}

// settingsCheck is doctor's settings check (CLI-101).
func (a *App) settingsCheck() Check {
	ps := a.profileState()
	switch {
	case ps.file.reason != "":
		return Check{"settings", "fail", a.settingsError(ps.file).Message,
			fmt.Sprintf("Fix %s, or move it aside to use defaults and environment variables.", ps.file.path)}
	case ps.requested != "":
		e := a.unknownProfileError(ps)
		next, _ := e.Details["next_action"].(string)
		return Check{"settings", "fail", e.Message, next}
	case !ps.file.exists:
		return Check{"settings", "pass", "No settings file; defaults and environment variables apply.", ""}
	case ps.name == "":
		return Check{"settings", "pass", fmt.Sprintf("%s is valid; no profile is active.", ps.file.path), ""}
	}
	return Check{"settings", "pass", fmt.Sprintf("%s is valid; profile `%s` is active (%s).", ps.file.path, ps.name, ps.source), ""}
}

// checkSettings reports declaration problems of App.Settings and commands that define
// --profile while aicli owns it (tree check, CLI-100, CLI-101).
func (a *App) checkSettings() []string {
	if a.Settings == nil {
		return nil
	}
	var problems []string
	seen := map[string]bool{}
	for i, s := range a.Settings {
		if !snakeCase.MatchString(s.Name) {
			problems = append(problems, fmt.Sprintf("App.Settings[%d]: %q is not a snake_case name", i, s.Name))
		}
		if seen[s.Name] {
			problems = append(problems, fmt.Sprintf("App.Settings: %q is declared twice", s.Name))
		}
		seen[s.Name] = true
	}
	walkAll(a.Root, func(c *cobra.Command) {
		if c == a.Root || isCobraInternal(c) {
			return
		}
		if f := c.LocalFlags().Lookup("profile"); f != nil && f != a.profileFlag() {
			problems = append(problems, fmt.Sprintf("%q defines --profile, which aicli reserves for the active profile when App.Settings is set (CLI-101)", cmdPath(c)))
		}
	})
	sort.Strings(problems)
	return problems
}
