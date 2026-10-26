// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Effect is what a command does to the world (CLI-025).
type Effect string

// The contract's effects. Any effect other than Observe makes a command a write.
const (
	Observe   Effect = "observe"
	Change    Effect = "change"
	Deliver   Effect = "deliver"
	Configure Effect = "configure"
	Artifact  Effect = "artifact"
	Persist   Effect = "persist"
)

// Maturity is a command's evidence level (CLI-027): draft, experimental, verified, stable.
type Maturity string

// Maturity levels, lowest first (VER-005).
const (
	Draft        Maturity = "draft"
	Experimental Maturity = "experimental"
	Verified     Maturity = "verified"
	Stable       Maturity = "stable"
)

// Meta is a command's registry entry (CLI-020). Register it for every leaf command.
type Meta struct {
	Summary    string
	Effects    []Effect
	Dangerous  bool
	Maturity   Maturity
	Evidence   []string
	Output     string // name of a schema registered with App.Schema
	ErrorCodes []string
	Examples   []string
	Positional string            // flag filled by a single positional argument
	FileInputs map[string]string // file flag -> bound value name ("body-file" -> "body"); value "-" reads stdin
	Secrets    []string          // flags (or bound names) whose values are secret
	Aliases    []string
	BatchFlag  string // plural target flag for WriteBatch
	// DefaultFields replaces the schema's default fields for this command.
	DefaultFields []string
	RawOutput     bool // the command supports --format raw
	// Params adds reference metadata to flags: Type overrides the derived type
	// (path, json, enum), Enum lists allowed values (checked before the command
	// runs) and Unit names the unit of a quantity.
	Params map[string]ParamDoc
	// Deprecated marks the command deprecated (CLI-081) with the replacement text,
	// e.g. "use `mr update --label`": help and reference prefix its summary with
	// "Deprecated: <text>.", every run adds a deprecated notice, and CommandMap
	// leaves it out. It keeps working until the next major version.
	Deprecated string
	// DeprecatedFlags marks flags deprecated: flag name -> replacement text. Help
	// and reference prefix the flag's description; using it adds a notice.
	DeprecatedFlags map[string]string
	// RenamedFlags keeps old flag names working (old -> new, Q10): --old is read as
	// --new (and binds as --new), using it adds the notice "`--old` is deprecated:
	// use `--new`.", giving both is E_USAGE, and help leaves it out while reference
	// lists it as a deprecated parameter. The command defines only --new.
	RenamedFlags map[string]string
	// FlagAliases accepts other long flag names on this command only (alias ->
	// canonical, Q11), after App.FlagAliases: an alias App.FlagAliases maps on this
	// command wins. The canonical flag binds; reference does not list the alias.
	FlagAliases map[string]string
	// StreamItem names the schema of the items an NDJSON stream writes (App.Stream,
	// Q23) when they are not the output schema's objects (a log's lines): items are
	// projected with it and take its untrusted fields.
	StreamItem string
	// AllowUnknownProfile lets the command run under a profile (--profile or
	// <PREFIX>_PROFILE) that the settings file does not have (CLI-101, P6, Q4): `auth
	// login --profile lab` creates it, `auth logout --profile gone` cleans up after a
	// deleted one. The unknown-profile refusal is skipped, App.Setting reads the
	// flag, the environment and the default only, App.Profile returns the requested
	// name, and that name is still bound into the confirm token. Apply creates the
	// profile with App.SetProfileSettings.
	AllowUnknownProfile bool
}

// deprecatedText is the help and reference text of a deprecated command or flag
// (CLI-081): "Deprecated: <text>. <original>".
func deprecatedText(text, original string) string {
	return strings.TrimSpace("Deprecated: " + strings.TrimSuffix(strings.TrimSpace(text), ".") + ". " + original)
}

// summaryOf is a command's summary as help and reference show it.
func (a *App) summaryOf(c *cobra.Command) string {
	m := a.metaFor(c)
	summary := c.Short
	if m != nil && m.Summary != "" {
		summary = m.Summary
	}
	if m != nil && strings.TrimSpace(m.Deprecated) != "" {
		summary = deprecatedText(m.Deprecated, summary)
	}
	return summary
}

// commandDeprecationNotice adds the notice of a run of a deprecated command (CLI-081).
func (a *App) commandDeprecationNotice(c *cobra.Command, m *Meta) {
	if m == nil {
		return
	}
	if text := strings.TrimSuffix(strings.TrimSpace(m.Deprecated), "."); text != "" {
		removal := "the next major version of " + a.toolName()
		if v, err := ParseVersion(a.version()); err == nil {
			removal = fmt.Sprintf("%s %d.0.0", a.toolName(), v.Major+1)
		}
		a.Notice(Notice{Type: "deprecated", Severity: "warning",
			Message: fmt.Sprintf("`%s` is deprecated: %s. It will be removed in %s.", cmdPath(c), text, removal)})
	}
}

// flagDeprecationNotices adds a notice per deprecated flag the run sets (CLI-081).
func (a *App) flagDeprecationNotices(c *cobra.Command, m *Meta) {
	if m == nil {
		return
	}
	for _, name := range sortedKeys(m.DeprecatedFlags) {
		text := strings.TrimSuffix(strings.TrimSpace(m.DeprecatedFlags[name]), ".")
		if f := lookupFlag(c, name); f != nil && f.Changed && text != "" {
			a.Notice(Notice{Type: "deprecated", Severity: "warning", Message: fmt.Sprintf("`--%s` is deprecated: %s.", name, text)})
		}
	}
}

// ParamDoc is extra reference metadata for one flag (see Meta.Params).
type ParamDoc struct {
	Type string
	Enum []string
	Unit string
}

// SchemaDef is an output schema in the reference catalog (CLI-020). Shape is object,
// array or page; Fields lists the fields of the object (or of each item);
// DefaultFields are returned when --fields is not given; Untrusted lists the fields
// that carry external content.
type SchemaDef struct {
	Shape                            string
	Fields, DefaultFields, Untrusted []string
}

type registry struct {
	meta    map[*cobra.Command]*Meta
	groups  map[*cobra.Command]string
	schemas map[string]SchemaDef
	builtin map[*cobra.Command]bool
	order   []*cobra.Command // leaves in the order they were registered
	// problems are registration defects (a schema registered twice, Q18; a shortcut
	// word mapped to two paths, Q6), which the tree check reports.
	problems []string
}

func (r *registry) init() {
	if r.meta == nil {
		r.meta = map[*cobra.Command]*Meta{}
		r.groups = map[*cobra.Command]string{}
		r.schemas = map[string]SchemaDef{}
		r.builtin = map[*cobra.Command]bool{}
	}
}

// Register attaches metadata to a leaf command. Call it for every leaf.
func (a *App) Register(cmd *cobra.Command, m Meta) {
	a.reg.init()
	cp := m
	cp.Effects = append([]Effect(nil), m.Effects...)
	if cp.Maturity == "" {
		cp.Maturity = Draft
	}
	if cp.Summary == "" {
		cp.Summary = cmd.Short
	}
	if cmd.Short == "" {
		cmd.Short = cp.Summary
	}
	for _, al := range m.Aliases {
		if !cmd.HasAlias(al) && al != cmd.Name() {
			cmd.Aliases = append(cmd.Aliases, al)
		}
	}
	if _, again := a.reg.meta[cmd]; !again {
		a.reg.order = append(a.reg.order, cmd)
	}
	a.reg.meta[cmd] = &cp
}

// Group registers a command group (a noun) and its summary.
func (a *App) Group(cmd *cobra.Command, summary string) {
	a.reg.init()
	if summary == "" {
		summary = cmd.Short
	}
	if cmd.Short == "" {
		cmd.Short = summary
	}
	a.reg.groups[cmd] = summary
}

// Schema registers an output schema under name. A name is registered once: a
// second call is a defect the tree check reports (Q18), and the first definition
// stays.
func (a *App) Schema(name string, s SchemaDef) {
	a.reg.init()
	if _, taken := a.reg.schemas[name]; taken {
		a.reg.problems = append(a.reg.problems, fmt.Sprintf("App.Schema(%q) was called twice; register each schema name once (the first definition is kept)", name))
		return
	}
	a.reg.schemas[name] = s
}

// AddShortcut maps a word typed at the root to a command path (CLI-022, Q6), as
// App.Shortcuts does: "branch" -> "repo", "branch". A word already mapped to a
// different path is a defect the tree check reports, and the first mapping stays;
// the same mapping again is harmless.
func (a *App) AddShortcut(word string, path ...string) {
	a.reg.init()
	if a.Shortcuts == nil {
		a.Shortcuts = map[string][]string{}
	}
	if have, taken := a.Shortcuts[word]; taken {
		if !slices.Equal(have, path) {
			a.reg.problems = append(a.reg.problems, fmt.Sprintf("App.AddShortcut(%q) maps it to %q, but it already maps to %q; a shortcut word names one path (the first is kept)",
				word, strings.Join(path, " "), strings.Join(have, " ")))
		}
		return
	}
	a.Shortcuts[word] = append([]string(nil), path...)
}

func (a *App) metaFor(cmd *cobra.Command) *Meta {
	if a.reg.meta == nil || cmd == nil {
		return nil
	}
	return a.reg.meta[cmd]
}

func (a *App) schemaOf(m *Meta) SchemaDef {
	if m == nil || a.reg.schemas == nil {
		return SchemaDef{}
	}
	return a.reg.schemas[m.Output]
}

// isWrite reports whether any declared effect is not observe.
func (m *Meta) isWrite() bool {
	for _, e := range m.Effects {
		if e != Observe {
			return true
		}
	}
	return false
}

// dangerous reports whether the command needs --dangerous (declared, or deliver/configure).
func (m *Meta) dangerous() bool {
	if m.Dangerous {
		return true
	}
	for _, e := range m.Effects {
		if contains(dangerousEffects, string(e)) {
			return true
		}
	}
	return false
}

func (m *Meta) hasEffect(names ...Effect) bool {
	for _, e := range m.Effects {
		for _, n := range names {
			if e == n {
				return true
			}
		}
	}
	return false
}

func (m *Meta) effectStrings() []string {
	out := make([]string, 0, len(m.Effects))
	for _, e := range m.Effects {
		out = append(out, string(e))
	}
	return out
}

func (m *Meta) isSecret(name string) bool {
	return contains(m.Secrets, name)
}

// normalizeSecrets completes every registered command's Meta.Secrets so there is
// one notion of a secret flag (B2, SEC-021): a flag is secret when Meta.Secrets
// names it, when it is the Flag of a Secret setting, or when it is the literal twin
// of a secret file flag or the file flag of a secret literal (Meta.FileInputs).
// Binding, previews, records, reference, suggestions, conformance and debug lines
// all read the completed list. It runs before any command and in the tree check.
func (a *App) normalizeSecrets() {
	for c, m := range a.reg.meta {
		add := func(name string) {
			if name != "" && !contains(m.Secrets, name) {
				m.Secrets = append(m.Secrets, name)
			}
		}
		for _, s := range a.Settings {
			if s.Secret && s.Flag != "" && lookupFlag(c, s.Flag) != nil {
				add(s.Flag)
			}
		}
		for _, file := range sortedKeys(m.FileInputs) {
			bound := m.FileInputs[file]
			if m.isSecret(file) || m.isSecret(bound) {
				add(file)
				add(bound)
			}
		}
	}
}

// cmdPath is the command path without the tool name ("note create").
func cmdPath(c *cobra.Command) string {
	p := c.CommandPath()
	if root := c.Root(); root != nil {
		p = strings.TrimPrefix(strings.TrimPrefix(p, root.Name()), " ")
	}
	return p
}

// commands returns every visible leaf command in path order.
func (a *App) commands() []*cobra.Command {
	var out []*cobra.Command
	walkAll(a.Root, func(c *cobra.Command) {
		if c == a.Root || isCobraInternal(c) || c.Hidden || hasSubcommands(c) {
			return
		}
		for p := c.Parent(); p != nil; p = p.Parent() {
			if p.Hidden {
				return
			}
		}
		out = append(out, c)
	})
	sort.SliceStable(out, func(i, j int) bool { return cmdPath(out[i]) < cmdPath(out[j]) })
	return out
}

// commandByPath finds a leaf or group by its path without the tool name.
func (a *App) commandByPath(path string) *cobra.Command {
	words := strings.Fields(path)
	c := a.Root
	for _, w := range words {
		next := childNamed(c, w)
		if next == nil || isCobraInternal(next) {
			return nil
		}
		c = next
	}
	return c
}

// ---------------------------------------------------------------- params

type paramEntry struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Multiple    bool     `json:"multiple,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
	FileInput   bool     `json:"file_input,omitempty"`
	Description string   `json:"description,omitempty"`
}

var usePositional = regexp.MustCompile(`([<\[])([A-Za-z0-9_-]+)(\.\.\.)?[>\]](\.\.\.)?`)

type usePos struct {
	name     string
	required bool
	variadic bool
}

// positionalsOf parses <name> and [name] tokens from the command's Use line.
func positionalsOf(c *cobra.Command) []usePos {
	use := strings.TrimSpace(c.Use)
	if i := strings.IndexByte(use, ' '); i >= 0 {
		use = use[i+1:]
	} else {
		return nil
	}
	var out []usePos
	for _, m := range usePositional.FindAllStringSubmatch(use, -1) {
		if strings.EqualFold(m[2], "flags") {
			continue
		}
		out = append(out, usePos{name: m[2], required: m[1] == "<", variadic: m[3] != "" || m[4] != ""})
	}
	return out
}

// isGlobalFlag reports the contract's global flags, help and the root version flag.
func isGlobalFlag(name string) bool {
	return contains(globalFlags, name) || name == "help" || name == "version"
}

// isGlobal is isGlobalFlag plus --profile when the tool declares settings: aicli
// registers it on the root, so reference lists it once, in global_flags (CLI-101).
func (a *App) isGlobal(name string) bool {
	return isGlobalFlag(name) || (name == "profile" && a.Settings != nil)
}

// flagType maps a pflag value type to a reference param type.
func flagType(f *pflag.Flag) (typ string, multiple bool) {
	t := f.Value.Type()
	switch t {
	case "bool":
		return "boolean", false
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count":
		return "integer", false
	case "float32", "float64":
		return "number", false
	case "stringSlice", "stringArray":
		return "string", true
	case "intSlice", "int32Slice", "int64Slice", "uintSlice":
		return "integer", true
	case "float32Slice", "float64Slice":
		return "number", true
	case "boolSlice":
		return "boolean", true
	}
	return "string", false
}

func flagRequired(f *pflag.Flag) bool {
	v := f.Annotations[cobra.BashCompOneRequiredFlag]
	return len(v) > 0 && v[0] == "true"
}

func (a *App) paramFor(m *Meta, f *pflag.Flag) paramEntry {
	typ, multiple := flagType(f)
	p := paramEntry{Name: f.Name, Kind: "flag", Type: typ, Multiple: multiple, Required: flagRequired(f),
		Description: f.Usage}
	if m != nil && strings.TrimSpace(m.DeprecatedFlags[f.Name]) != "" {
		p.Description = deprecatedText(m.DeprecatedFlags[f.Name], p.Description)
	}
	if m != nil {
		if _, ok := m.FileInputs[f.Name]; ok {
			p.Type = "path"
			p.FileInput = true
		}
		if doc, ok := m.Params[f.Name]; ok {
			if doc.Type != "" {
				p.Type = doc.Type
			}
			if len(doc.Enum) > 0 {
				p.Enum = append([]string(nil), doc.Enum...)
				if doc.Type == "" && !multiple {
					p.Type = "enum"
				}
			}
			p.Unit = doc.Unit
		}
		if m.isSecret(f.Name) || (p.FileInput && m.isSecret(m.FileInputs[f.Name])) {
			p.Secret = true
		}
		// The batch flag must name at least one target (WriteBatch refuses an empty
		// batch), so it is required even though cobra does not enforce it.
		if m.BatchFlag != "" && f.Name == m.BatchFlag {
			p.Required = true
		}
	}
	if f.Value.Type() == "duration" && p.Unit == "" {
		p.Unit = "duration"
	}
	if !p.Required && !p.Secret && !p.FileInput {
		p.Default = typedDefault(f, p.Type)
	}
	return p
}

// typedDefault returns a flag's default as a JSON value, or nil when it has none.
func typedDefault(f *pflag.Flag, typ string) any {
	d := f.DefValue
	if d == "" || d == "[]" {
		return nil
	}
	switch typ {
	case "boolean":
		if b, err := strconv.ParseBool(d); err == nil && b {
			return b
		}
		return nil
	case "integer":
		if n, err := strconv.ParseInt(d, 10, 64); err == nil {
			return n
		}
	case "number":
		if n, err := strconv.ParseFloat(d, 64); err == nil {
			return n
		}
	}
	return d
}

// params builds a command's help params: positionals first, then flags by name.
func (a *App) params(c *cobra.Command) []paramEntry { return a.paramsOf(c, false) }

// paramsOf builds a command's params; withRenamed adds the old names of renamed
// flags as deprecated parameters, which reference lists and help does not (Q10).
func (a *App) paramsOf(c *cobra.Command, withRenamed bool) []paramEntry {
	m := a.metaFor(c)
	out := []paramEntry{}
	posFlag := ""
	if m != nil {
		posFlag = m.Positional
	}
	usedPosFlag := false
	for _, p := range positionalsOf(c) {
		entry := paramEntry{Name: p.name, Kind: "positional", Type: "string", Required: p.required, Multiple: p.variadic}
		if posFlag != "" && !usedPosFlag {
			if f := lookupFlag(c, posFlag); f != nil {
				fe := a.paramFor(m, f)
				entry.Name = posFlag
				entry.Type, entry.Enum, entry.Unit, entry.Secret = fe.Type, fe.Enum, fe.Unit, fe.Secret
				entry.Description = strings.TrimSpace(fe.Description + " (or --" + posFlag + ")")
				entry.Required = p.required || fe.Required
				usedPosFlag = true
			}
		}
		out = append(out, entry)
	}
	if posFlag != "" && !usedPosFlag {
		if f := lookupFlag(c, posFlag); f != nil {
			fe := a.paramFor(m, f)
			fe.Kind = "positional"
			fe.Default = nil
			fe.Description = strings.TrimSpace(fe.Description + " (or --" + posFlag + ")")
			out = append(out, fe)
			usedPosFlag = true
		}
	}
	var flags []paramEntry
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || a.isGlobal(f.Name) || (usedPosFlag && f.Name == posFlag) {
			return
		}
		flags = append(flags, a.paramFor(m, f))
	})
	c.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || a.isGlobal(f.Name) || (usedPosFlag && f.Name == posFlag) || c.Flags().Lookup(f.Name) == nil {
			return
		}
		for _, have := range flags {
			if have.Name == f.Name {
				return
			}
		}
		flags = append(flags, a.paramFor(m, f))
	})
	if withRenamed && m != nil {
		for _, old := range sortedKeys(m.RenamedFlags) {
			f := lookupFlag(c, m.RenamedFlags[old])
			if f == nil || lookupFlag(c, old) != nil {
				continue // the tree check reports it
			}
			p := a.paramFor(m, f)
			p.Name, p.Required, p.Default = old, false, nil
			p.Description = deprecatedText("use `--"+f.Name+"`", f.Usage)
			flags = append(flags, p)
		}
	}
	sort.SliceStable(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return append(out, flags...)
}

// globalParams describes the global control flags for the reference.
func (a *App) globalParams() []paramEntry {
	out := []paramEntry{}
	pf := a.Root.PersistentFlags()
	for _, name := range globalFlags {
		f := pf.Lookup(name)
		if f == nil {
			continue
		}
		p := a.paramFor(nil, f)
		if name == "format" {
			p.Type = "enum"
			p.Enum = append([]string(nil), formatValues...)
		}
		out = append(out, p)
	}
	if a.Settings != nil {
		if f := pf.Lookup("profile"); f != nil {
			out = append(out, a.paramFor(nil, f))
		}
	}
	return out
}

// ---------------------------------------------------------------- reference

type commandEntry struct {
	Path       string       `json:"path"`
	Aliases    []string     `json:"aliases"`
	Summary    string       `json:"summary"`
	Type       string       `json:"type"`
	Effects    []string     `json:"effects"`
	Dangerous  bool         `json:"dangerous"`
	Maturity   string       `json:"maturity"`
	Evidence   []string     `json:"evidence"`
	Params     []paramEntry `json:"params"`
	Output     string       `json:"output"`
	ErrorCodes []string     `json:"error_codes"`
	Examples   []string     `json:"examples"`
}

type indexEntry struct {
	Path      string   `json:"path"`
	Type      string   `json:"type"`
	Effects   []string `json:"effects"`
	Dangerous bool     `json:"dangerous"`
	Maturity  string   `json:"maturity"`
	Summary   string   `json:"summary"`
}

type groupEntry struct {
	Path         string `json:"path"`
	Summary      string `json:"summary"`
	CommandCount int    `json:"command_count"`
}

type schemaEntry struct {
	Shape           string   `json:"shape"`
	Fields          []string `json:"fields"`
	DefaultFields   []string `json:"default_fields"`
	UntrustedFields []string `json:"untrusted_fields"`
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return append([]string{}, list...)
}

func (a *App) commandType(m *Meta) string {
	if m != nil && m.isWrite() {
		return "write"
	}
	return "read"
}

// errorCodesOf lists the codes a command can return: its declared codes plus the
// codes the library itself returns for it, E_CONFIG among them for every tool
// command when settings are declared (an unknown profile stops it, D4, Q25).
func (a *App) errorCodesOf(c *cobra.Command, m *Meta) []string {
	set := map[string]bool{"E_USAGE": true, "E_UNKNOWN": true}
	if a.Settings != nil && !a.reg.builtin[c] {
		set["E_CONFIG"] = true
	}
	if m != nil {
		for _, c := range m.ErrorCodes {
			set[c] = true
		}
		if m.isWrite() {
			for _, c := range []string{"E_CONFIRMATION_REQUIRED", "E_CONFLICT", "E_FORBIDDEN", "E_IO", "E_OUTCOME_UNKNOWN"} {
				set[c] = true
			}
			if m.BatchFlag != "" {
				set["E_PARTIAL"] = true
			}
		}
		if len(m.FileInputs) > 0 {
			set["E_IO"] = true
		}
	}
	return sortedKeys(set)
}

func (a *App) entryFor(c *cobra.Command) commandEntry {
	m := a.metaFor(c)
	e := commandEntry{Path: cmdPath(c), Aliases: nonNil(c.Aliases), Summary: c.Short, Type: a.commandType(m),
		Effects: []string{}, Maturity: string(Draft), Evidence: []string{}, Params: a.paramsOf(c, true), ErrorCodes: a.errorCodesOf(c, m),
		Examples: []string{}}
	if m != nil {
		e.Summary = a.summaryOf(c)
		e.Effects = m.effectStrings()
		e.Dangerous = m.dangerous()
		e.Maturity = string(m.Maturity)
		e.Evidence = nonNil(m.Evidence)
		e.Output, _ = a.outputOf(c, m)
		e.Examples = nonNil(m.Examples)
	}
	sort.Strings(e.Aliases)
	return e
}

func (a *App) indexFor(c *cobra.Command) indexEntry {
	e := a.entryFor(c)
	return indexEntry{Path: e.Path, Type: e.Type, Effects: e.Effects, Dangerous: e.Dangerous, Maturity: e.Maturity, Summary: e.Summary}
}

func schemaEntryOf(s SchemaDef) schemaEntry {
	return schemaEntry{Shape: s.Shape, Fields: nonNil(s.Fields), DefaultFields: nonNil(s.DefaultFields), UntrustedFields: nonNil(s.Untrusted)}
}

// schemaEntries is the reference's schema catalog: every registered schema, and the
// command-specific copies outputOf names (Q5).
func (a *App) schemaEntries() map[string]schemaEntry {
	out := map[string]schemaEntry{}
	for name, s := range a.reg.schemas {
		out[name] = schemaEntryOf(s)
	}
	for _, c := range a.commands() {
		if name, own := a.outputOf(c, a.metaFor(c)); own != nil {
			out[name] = *own
		}
	}
	return out
}

// outputOf is the schema name a command's reference entry gives as its output
// (Q5): its output schema, or, when Meta.DefaultFields differ from the schema's
// default fields, a copy of the schema with the command's default fields under the
// name <output>_for_<path> (path words joined by _, - read as _), returned as own,
// so reference shows the defaults the command really uses, as help does.
func (a *App) outputOf(c *cobra.Command, m *Meta) (string, *schemaEntry) {
	if m == nil {
		return "", nil
	}
	s, ok := a.reg.schemas[m.Output]
	if !ok || len(m.DefaultFields) == 0 || slices.Equal(m.DefaultFields, s.DefaultFields) {
		return m.Output, nil
	}
	base := m.Output + "_for_" + strings.ReplaceAll(strings.Join(strings.Fields(cmdPath(c)), "_"), "-", "_")
	name := base
	for n := 2; ; n++ {
		if _, taken := a.reg.schemas[name]; !taken {
			break
		}
		name = fmt.Sprintf("%s_%d", base, n)
	}
	own := schemaEntryOf(s)
	own.DefaultFields = nonNil(m.DefaultFields)
	return name, &own
}

func (a *App) codeTable(codes []string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, code := range codes {
		if spec, ok := a.lookupCode(code); ok {
			out[code] = map[string]any{"exit": spec.Exit, "retryable": spec.Retryable, "meaning": spec.Meaning}
		}
	}
	return out
}

// referenceFull is the full reference (CLI-033): for tooling, not for agents.
func (a *App) referenceFull() map[string]any {
	cmds := []commandEntry{}
	for _, c := range a.commands() {
		cmds = append(cmds, a.entryFor(c))
	}
	codes := sortedKeys(coreCodes)
	codes = append(codes, sortedKeys(a.ExtCodes)...)
	exits := map[string]string{}
	for code, meaning := range exitCodeMeanings {
		exits[strconv.Itoa(code)] = meaning
	}
	return map[string]any{
		"tool": a.toolName(), "version": a.version(), "spec_version": SpecVersion, "schema_version": SchemaVersion,
		"risk_tier": a.RiskTier, "global_flags": a.globalParams(), "commands": cmds, "schemas": a.schemaEntries(),
		"error_codes": a.codeTable(codes), "exit_codes": exits,
	}
}

// referenceIndex is the compact index (CLI-031): one entry per command, or groups
// when the list would exceed the index budget. group narrows it to one subtree.
func (a *App) referenceIndex(group string) (map[string]any, *Error) {
	base := a.Root
	if group != "" {
		base = a.commandByPath(group)
		if base == nil {
			e := Usage("unknown_command", group, "No command group has that path; details.available lists the groups.")
			e.Details["available"] = a.topLevelNames()
			e.Details["next_action"] = fmt.Sprintf("Run `%s reference --index` to see the groups.", a.toolName())
			return nil, e
		}
	}
	var cmds []*cobra.Command
	for _, c := range a.commands() {
		if base == a.Root || c == base || isUnder(c, base) {
			cmds = append(cmds, c)
		}
	}
	entries := make([]indexEntry, 0, len(cmds))
	for _, c := range cmds {
		entries = append(entries, a.indexFor(c))
	}
	data := map[string]any{"tool": a.toolName(), "version": a.version(), "commands": entries}
	if fitsIndexBudget(data) {
		return data, nil
	}
	// Too large: list the groups one level below base.
	groups := []groupEntry{}
	seen := map[*cobra.Command]bool{}
	for _, c := range cmds {
		top := c
		for top.Parent() != nil && top.Parent() != base {
			top = top.Parent()
		}
		if seen[top] {
			continue
		}
		seen[top] = true
		count := 0
		for _, x := range cmds {
			if x == top || isUnder(x, top) {
				count++
			}
		}
		summary := top.Short
		if s, ok := a.reg.groups[top]; ok && s != "" {
			summary = s
		}
		groups = append(groups, groupEntry{Path: cmdPath(top), Summary: summary, CommandCount: count})
	}
	return map[string]any{"tool": a.toolName(), "version": a.version(), "groups": groups}, nil
}

func fitsIndexBudget(data map[string]any) bool {
	env := successEnvelope{OK: true, SchemaVersion: SchemaVersion, Data: data, Meta: metaObject{DurationMS: 100000}}
	b, err := encodeJSON(env, false)
	return err == nil && len([]rune(string(b))) <= indexBudgetChars
}

func isUnder(c, ancestor *cobra.Command) bool {
	for p := c.Parent(); p != nil; p = p.Parent() {
		if p == ancestor {
			return true
		}
	}
	return false
}

func (a *App) topLevelNames() []string {
	var out []string
	for _, c := range a.Root.Commands() {
		if !isCobraInternal(c) && !c.Hidden {
			out = append(out, c.Name())
		}
	}
	sort.Strings(out)
	return out
}

// referenceCommand is one command's entry with its output schema and error codes (CLI-032).
func (a *App) referenceCommand(path string) (map[string]any, *Error) {
	c := a.commandByPath(path)
	if c == nil || c == a.Root || hasSubcommands(c) {
		return nil, a.unknownReferencePath(path, c)
	}
	entry := a.entryFor(c)
	schemas := map[string]schemaEntry{}
	if s, ok := a.schemaEntries()[entry.Output]; ok {
		schemas[entry.Output] = s
	}
	codes := map[string]map[string]any{}
	for code, v := range a.codeTable(entry.ErrorCodes) {
		codes[code] = map[string]any{"exit": v["exit"], "retryable": v["retryable"]}
	}
	return map[string]any{"tool": a.toolName(), "version": a.version(), "command": entry, "schemas": schemas, "error_codes": codes}, nil
}

// unknownReferencePath answers reference --command with a path that names no
// command (CLI-032, CLI-012): E_USAGE unknown_command. A group lists its commands;
// otherwise the closest commands in the deepest group the path reaches are suggested
// (verb synonyms and short typos included) and next_action is the exact reference
// call for the best one.
func (a *App) unknownReferencePath(path string, c *cobra.Command) *Error {
	tool := a.toolName()
	if c != nil && (c == a.Root || hasSubcommands(c)) {
		var cmds []string
		for _, x := range a.commands() {
			if c == a.Root || isUnder(x, c) {
				cmds = append(cmds, cmdPath(x))
			}
		}
		if len(cmds) > 20 {
			cmds = cmds[:20]
		}
		e := Usage("unknown_command", path, "That path is a group, not a command; details.available lists its commands.")
		e.Details["available"] = nonNil(cmds)
		e.Details["next_action"] = fmt.Sprintf("Run `%s reference --index %s` to see its commands, or `%s reference --command \"<one of details.available>\"`.",
			tool, shellWord(path), tool)
		return e
	}
	words := strings.Fields(path)
	group := a.Root
	word := lastWord(path)
	for _, w := range words {
		next := childNamed(group, w)
		if next == nil || isCobraInternal(next) {
			word = w
			break
		}
		if !hasSubcommands(next) {
			word = next.Name() // a command followed by extra words
			break
		}
		group = next
	}
	// available and did_you_mean are --command values: registry paths without the
	// tool name.
	var available []string
	for _, sub := range group.Commands() {
		if !isCobraInternal(sub) && !sub.Hidden {
			available = append(available, cmdPath(sub))
		}
	}
	sort.Strings(available)
	withoutTool := func(s string) string { return strings.TrimPrefix(strings.TrimPrefix(s, a.Root.Name()), " ") }
	var suggestions []string
	for _, s := range a.siblingMatches(group, word) {
		if x := a.commandByPath(withoutTool(s)); x != nil && !hasSubcommands(x) {
			suggestions = append(suggestions, withoutTool(s))
		}
	}
	if len(suggestions) == 0 {
		for _, s := range a.treeMatches(word, true) {
			suggestions = append(suggestions, withoutTool(s))
		}
	}
	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}
	e := Usage("unknown_command", path, "No command has that path; details.did_you_mean lists the closest commands.")
	e.Details["available"] = nonNil(available)
	next := fmt.Sprintf("Run `%s reference --index` to list the commands.", tool)
	if len(suggestions) > 0 {
		e.Details["did_you_mean"] = suggestions
		next = fmt.Sprintf("Run `%s reference --command \"%s\"`.", tool, suggestions[0])
	} else {
		e.Message = "No command has that path; details.available lists the commands of the nearest group."
	}
	e.Details["next_action"] = next
	return e
}

func lastWord(path string) string {
	f := strings.Fields(path)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// CommandMap renders the Skill command map from the registry (SKL-002, decision 4):
// one bullet per top-level noun, “- `<noun>`: <verb>, <verb>; `<noun> <sub>`
// <verb>|<verb>“, with the verbs in registration order (register reads first),
// " (dangerous)" after each verb that needs --dangerous, and the built-ins and
// deprecated commands left out (the Skill teaches only current commands, CLI-081).
// A command at the root is a bullet of its own (“- `<command>` “). SKILL.md and
// README carry this text between the aicli:command-map markers, kept current by a
// test; for the example tool it is
//
//   - `note`: list, get, create, update, delete (dangerous), share (dangerous), bulk-tag
func (a *App) CommandMap() string {
	a.InstallBuiltins()
	cmds := a.commands()
	rank := map[*cobra.Command]int{}
	for i, c := range a.reg.order {
		rank[c] = i
	}
	sort.SliceStable(cmds, func(i, j int) bool {
		ri, oki := rank[cmds[i]]
		rj, okj := rank[cmds[j]]
		if oki != okj {
			return oki
		}
		return oki && ri < rj
	})
	type noun struct {
		name       string
		root       string // the mark of a root-level command, when the noun is one
		isRoot     bool
		verbs      []string
		groups     []string
		groupVerbs map[string][]string
	}
	var order []*noun
	byName := map[string]*noun{}
	for _, c := range cmds {
		if a.reg.builtin[c] {
			continue
		}
		if m := a.metaFor(c); m != nil && strings.TrimSpace(m.Deprecated) != "" {
			continue
		}
		mark := ""
		if m := a.metaFor(c); m != nil && m.dangerous() {
			mark = " (dangerous)"
		}
		words := strings.Fields(cmdPath(c))
		n, ok := byName[words[0]]
		if !ok {
			n = &noun{name: words[0], groupVerbs: map[string][]string{}}
			byName[words[0]] = n
			order = append(order, n)
		}
		if len(words) == 1 {
			n.isRoot, n.root = true, mark
			continue
		}
		verb := words[len(words)-1] + mark
		group := strings.Join(words[:len(words)-1], " ")
		if group == n.name {
			n.verbs = append(n.verbs, verb)
			continue
		}
		if _, seen := n.groupVerbs[group]; !seen {
			n.groups = append(n.groups, group)
		}
		n.groupVerbs[group] = append(n.groupVerbs[group], verb)
	}
	var b strings.Builder
	for _, n := range order {
		if n.isRoot {
			fmt.Fprintf(&b, "- `%s`%s\n", n.name, n.root)
			continue
		}
		var parts []string
		if len(n.verbs) > 0 {
			parts = append(parts, strings.Join(n.verbs, ", "))
		}
		for _, g := range n.groups {
			parts = append(parts, "`"+g+"` "+strings.Join(n.groupVerbs[g], "|"))
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", n.name, strings.Join(parts, "; "))
	}
	return b.String()
}

// ---------------------------------------------------------------- help

// helpFunc renders --help from the registry (CLI-030): groups list every command
// below them one line each; commands show usage, effects, the write recipe, flags
// and examples.
func (a *App) helpFunc(c *cobra.Command, _ []string) {
	if st := a.run; st != nil {
		st.helpShown = true
	}
	w := c.OutOrStdout()
	if c == a.Root || hasSubcommands(c) {
		a.groupHelp(w, c)
		return
	}
	a.commandHelp(w, c)
}

func (a *App) groupHelp(w io.Writer, c *cobra.Command) {
	var b strings.Builder
	summary := c.Short
	if s, ok := a.reg.groups[c]; ok && s != "" {
		summary = s
	}
	if summary != "" {
		b.WriteString(summary + "\n\n")
	}
	fmt.Fprintf(&b, "Usage: %s <command> [flags]\n\n", c.CommandPath())
	rows := [][2]string{}
	width := 0
	for _, x := range a.commands() {
		if c != a.Root && !isUnder(x, c) {
			continue
		}
		use := strings.TrimPrefix(x.CommandPath(), a.Root.Name()+" ")
		for _, p := range positionalsOf(x) {
			if p.required {
				use += " <" + p.name + ">"
			} else {
				use += " [" + p.name + "]"
			}
		}
		tag := ""
		if m := a.metaFor(x); m != nil && m.isWrite() {
			tag = " (write)"
			if m.dangerous() {
				tag = " (write, dangerous)"
			}
		}
		rows = append(rows, [2]string{use, a.summaryOf(x) + tag})
		if len(use) > width {
			width = len(use)
		}
	}
	scope := "Commands"
	if c != a.Root {
		scope = "Commands under " + cmdPath(c)
	}
	fmt.Fprintf(&b, "%s:\n", scope)
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, r[0], r[1])
	}
	fmt.Fprintf(&b, "\nOutput is one JSON envelope on stdout (check ok first). Writes: run the command (add --dry-run to be explicit) to get a preview and a confirm token; nothing changes. Then run it again without --dry-run, adding --confirm <token>. Commands marked dangerous need --dangerous in both steps.\n")
	fmt.Fprintf(&b, "Run `%s <command> --help` for a command's flags.\n", a.Root.Name())
	_, _ = io.WriteString(w, b.String())
}

func (a *App) commandHelp(w io.Writer, c *cobra.Command) {
	var b strings.Builder
	m := a.metaFor(c)
	b.WriteString(a.summaryOf(c) + "\n")
	if c.Long != "" {
		b.WriteString("\n" + strings.TrimSpace(c.Long) + "\n")
	}
	fmt.Fprintf(&b, "\nUsage: %s\n", a.usageLine(c))
	if len(c.Aliases) > 0 {
		fmt.Fprintf(&b, "Aliases: %s\n", strings.Join(c.Aliases, ", "))
	}
	write, dangerous := false, false
	if m != nil {
		write, dangerous = m.isWrite(), m.dangerous()
		fmt.Fprintf(&b, "Effects: %s\n", strings.Join(m.effectStrings(), ", "))
		switch {
		case dangerous:
			b.WriteString("Write (dangerous): run it with --dangerous (add --dry-run to be explicit) to get a preview and a confirm token; nothing changes. " +
				"Then run it again without --dry-run, with --dangerous --confirm <token>.\n")
			b.WriteString("Dangerous: --dangerous is required in both steps.\n")
		case write:
			b.WriteString("Write: run it (add --dry-run to be explicit) to get a preview and a confirm token; nothing changes. " +
				"Then run it again without --dry-run, adding --confirm <token>.\n")
		}
		if line := a.fieldsLine(m); line != "" {
			b.WriteString(line + "\n")
		}
	}
	params := a.params(c)
	if len(params) > 0 {
		b.WriteString("\nParameters:\n")
		for _, p := range params {
			name := p.Name
			if p.Kind == "flag" {
				name = "--" + name
			} else {
				name = "<" + name + ">"
			}
			var notes []string
			notes = append(notes, p.Type)
			if p.Required {
				notes = append(notes, "required")
			}
			if p.Multiple {
				notes = append(notes, "repeatable or comma-separated")
			}
			if len(p.Enum) > 0 {
				notes = append(notes, "one of "+strings.Join(p.Enum, "|"))
			}
			if p.Default != nil {
				notes = append(notes, fmt.Sprintf("default %v", p.Default))
			}
			if p.Secret {
				notes = append(notes, "secret")
			}
			fmt.Fprintf(&b, "  %s (%s)  %s\n", name, strings.Join(notes, ", "), p.Description)
		}
	}
	global := "--format json|text|raw, --fields, --pretty, --quiet"
	if write {
		global += ", --dry-run, --confirm <token>"
	}
	if dangerous {
		global += ", --dangerous"
	}
	b.WriteString("\nGlobal flags: " + global + "\n")
	if m != nil && len(m.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, ex := range m.Examples {
			b.WriteString("  " + ex + "\n")
		}
	}
	_, _ = io.WriteString(w, b.String())
}

// fieldsLine names the fields --fields accepts (CLI-040), so help answers it without
// a reference call: the default fields first, then the others.
func (a *App) fieldsLine(m *Meta) string {
	s := a.schemaOf(m)
	if len(s.Fields) == 0 {
		return ""
	}
	defaults := s.DefaultFields
	if len(m.DefaultFields) > 0 {
		defaults = m.DefaultFields
	}
	if len(defaults) == 0 {
		return fmt.Sprintf("Fields: %s. --fields a,b returns only those.", strings.Join(s.Fields, ", "))
	}
	var others []string
	for _, f := range s.Fields {
		if !contains(defaults, f) {
			others = append(others, f)
		}
	}
	line := "Fields: " + strings.Join(defaults, ", ") + " (default)"
	if len(others) > 0 {
		line += "; also " + strings.Join(others, ", ") + ". --fields +" + others[0] + " adds one"
	} else {
		line += "."
	}
	return line + "; --fields all returns every field."
}

// ---------------------------------------------------------------- tree checks

// checkTree reports registry problems (CLI-021, CLI-022, CLI-025, CLI-027, R02).
// The conformance suite fails on any of them.
func (a *App) checkTree() []string {
	a.normalizeSecrets()
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	tool := a.toolName()
	if !contains(riskTierValues, a.RiskTier) {
		add("App.RiskTier %q is not one of %s", a.RiskTier, strings.Join(riskTierValues, ", "))
	}
	// An empty or "dev" App.Version is a development build, which reports a
	// version derived from the changelog (P14); it is not a tree problem.
	problems = append(problems, a.checkExtCodes()...)
	problems = append(problems, a.checkSettings()...)
	problems = append(problems, a.checkUpdateConfig()...)
	// update is a built-in (App.Update); a tool's own root update is refused.
	for _, c := range a.Root.Commands() {
		if !isCobraInternal(c) && !a.reg.builtin[c] && (c.Name() == "update" || c.HasAlias("update")) {
			add("`update` is aicli's built-in; set App.Update (CLI-070)")
		}
	}
	reserved := []string{"reference", "context", "doctor", "changelog", "operation", "help", "version"}
	maxTier := "T0"
	walkAll(a.Root, func(c *cobra.Command) {
		if isCobraInternal(c) {
			return
		}
		path := cmdPath(c)
		// sibling names and aliases must be unique
		seen := map[string]string{}
		for _, sub := range c.Commands() {
			if isCobraInternal(sub) {
				continue
			}
			for _, n := range append([]string{sub.Name()}, sub.Aliases...) {
				if other, dup := seen[n]; dup && other != sub.Name() {
					add("%q: the name or alias %q is used by both %q and %q", path, n, other, sub.Name())
				}
				seen[n] = sub.Name()
			}
		}
		if c != a.Root && hasSubcommands(c) {
			if _, ok := a.reg.meta[c]; ok {
				add("%q is a group and also registered as a command; a path is one or the other", path)
			}
			return
		}
		if c == a.Root {
			return
		}
		if c.Parent() == a.Root && contains(reserved, c.Name()) && !a.reg.builtin[c] {
			add("%q uses a reserved built-in name", path)
		}
		if c.Hidden {
			return
		}
		m := a.metaFor(c)
		if m == nil {
			add("%q has no registered Meta (call App.Register)", path)
			return
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if isGlobalFlag(f.Name) && c.Parent() != nil {
				add("%q defines --%s, which shadows a global flag", path, f.Name)
			}
		})
		if strings.TrimSpace(m.Summary) == "" {
			add("%q has no summary", path)
		}
		if len(m.Effects) == 0 {
			add("%q declares no effects", path)
		}
		for _, e := range m.Effects {
			if !contains(effectValues, string(e)) {
				add("%q declares unknown effect %q", path, e)
				continue
			}
			if tierRank(minimumRiskTier[string(e)]) > tierRank(maxTier) {
				maxTier = minimumRiskTier[string(e)]
			}
		}
		if m.Dangerous && tierRank(maxTier) < tierRank("T2") && m.isWrite() {
			maxTier = "T2"
		}
		if !contains(maturityValues, string(m.Maturity)) {
			add("%q has unknown maturity %q", path, m.Maturity)
		}
		if m.Output == "" {
			add("%q names no output schema", path)
		} else if _, ok := a.reg.schemas[m.Output]; !ok {
			add("%q names output schema %q, which is not registered", path, m.Output)
		} else {
			s := a.reg.schemas[m.Output]
			if !contains(schemaShapes, s.Shape) {
				add("schema %q has unknown shape %q", m.Output, s.Shape)
			}
			for _, f := range append(append([]string{}, s.DefaultFields...), m.DefaultFields...) {
				if len(s.Fields) > 0 && !contains(s.Fields, f) {
					add("%q: default field %q is not a field of schema %q", path, f, m.Output)
				}
			}
		}
		if len(m.Examples) == 0 {
			add("%q has no example", path)
		}
		for _, ex := range m.Examples {
			if !strings.HasPrefix(ex, tool+" ") {
				add("%q: example %q does not start with %q", path, ex, tool)
			} else if target, _ := a.exampleCommand(ex); target != c {
				// Resolved as the parser resolves it (shortcuts, aliases), as R01 does (Q21).
				add("%q: example %q does not run this command; start it with %q and the command's path, a shortcut or an alias", path, ex, tool)
			}
		}
		if m.StreamItem != "" {
			if _, ok := a.reg.schemas[m.StreamItem]; !ok {
				add("%q names stream item schema %q, which is not registered", path, m.StreamItem)
			}
		}
		for _, old := range sortedKeys(m.RenamedFlags) {
			switch renamed := m.RenamedFlags[old]; {
			case lookupFlag(c, renamed) == nil:
				add("%q: Meta.RenamedFlags maps --%s to --%s, which the command does not define", path, old, renamed)
			case lookupFlag(c, old) != nil:
				add("%q: Meta.RenamedFlags names --%s, which the command still defines; keep only --%s", path, old, renamed)
			}
		}
		for _, alias := range sortedKeys(m.FlagAliases) {
			switch canonical := m.FlagAliases[alias]; {
			case lookupFlag(c, canonical) == nil:
				add("%q: Meta.FlagAliases maps --%s to --%s, which the command does not define", path, alias, canonical)
			case lookupFlag(c, alias) != nil:
				add("%q: Meta.FlagAliases names --%s, which the command already defines", path, alias)
			}
		}
		if m.isWrite() {
			if !anyContains(m.Examples, "--dry-run") || !anyContains(m.Examples, "--confirm") {
				add("%q is a write; its examples need a --dry-run and a --confirm example", path)
			}
			if m.dangerous() {
				for _, ex := range m.Examples {
					if (strings.Contains(ex, "--dry-run") || strings.Contains(ex, "--confirm")) && !strings.Contains(ex, "--dangerous") {
						add("%q is dangerous; example %q needs --dangerous", path, ex)
					}
				}
			}
		}
		for _, code := range m.ErrorCodes {
			if _, ok := a.lookupCode(code); !ok {
				add("%q lists error code %q, which is neither core nor declared in App.ExtCodes", path, code)
			}
		}
		if m.Positional != "" && lookupFlag(c, m.Positional) == nil {
			add("%q: Meta.Positional names --%s, which the command does not define", path, m.Positional)
		}
		boundBy := map[string]string{}
		for _, file := range sortedKeys(m.FileInputs) {
			bound := m.FileInputs[file]
			if lookupFlag(c, file) == nil {
				add("%q: Meta.FileInputs names --%s, which the command does not define", path, file)
			}
			if strings.TrimSpace(bound) == "" {
				add("%q: Meta.FileInputs maps --%s to an empty name", path, file)
				continue
			}
			// One bound name, one value (B4): two file flags for it, or a file flag
			// mapped onto a flag that cannot be its literal twin, would bind two
			// different things under one name.
			if other, dup := boundBy[bound]; dup {
				add("%q: Meta.FileInputs maps both --%s and --%s to %q; a bound name has one file flag", path, other, file, bound)
			}
			boundBy[bound] = file
			if _, isFile := m.FileInputs[bound]; isFile {
				add("%q: Meta.FileInputs maps --%s onto --%s, which is itself a file input", path, file, bound)
			} else if isGlobalFlag(bound) || (bound == "profile" && a.Settings != nil) {
				add("%q: Meta.FileInputs maps --%s onto --%s, a global flag", path, file, bound)
			} else if lf := lookupFlag(c, bound); lf != nil {
				if typ, multiple := flagType(lf); typ != "string" || multiple || lf.NoOptDefVal != "" {
					add("%q: Meta.FileInputs maps --%s onto --%s, which is not a string flag and so cannot be its literal twin", path, file, bound)
				}
			}
		}
		for _, s := range m.Secrets {
			if lookupFlag(c, s) == nil && !mapHasValue(m.FileInputs, s) {
				add("%q: Meta.Secrets names %q, which is neither a flag nor a bound file input", path, s)
			}
		}
		if m.BatchFlag != "" && lookupFlag(c, m.BatchFlag) == nil {
			add("%q: Meta.BatchFlag names --%s, which the command does not define", path, m.BatchFlag)
		}
		for _, name := range sortedKeys(m.DeprecatedFlags) {
			if lookupFlag(c, name) == nil {
				add("%q: Meta.DeprecatedFlags names --%s, which the command does not define", path, name)
			}
		}
		for name, doc := range m.Params {
			if lookupFlag(c, name) == nil {
				add("%q: Meta.Params names --%s, which the command does not define", path, name)
			}
			if doc.Type != "" && !contains(paramTypes, doc.Type) {
				add("%q: Meta.Params --%s has unknown type %q", path, name, doc.Type)
			}
		}
	})
	if contains(riskTierValues, a.RiskTier) && tierRank(a.RiskTier) < tierRank(maxTier) {
		add("App.RiskTier %s is below %s, which the declared effects require (SEC-001)", a.RiskTier, maxTier)
	}
	problems = append(problems, a.reg.problems...)
	for _, key := range sortedKeys(a.Shortcuts) {
		to := strings.Join(a.Shortcuts[key], " ")
		if c := a.commandByPath(to); c == nil || c == a.Root {
			add("App.Shortcuts[%q] points to %q, which is not a command or group", key, to)
		}
		if words := strings.Fields(key); len(words) > 0 && childNamed(a.Root, words[0]) != nil {
			add("App.Shortcuts[%q] never applies: %q is already a root command", key, words[0])
		}
	}
	// A nested group needs a root shortcut (CLI-022) when its noun occurs under one
	// parent only; a noun under several parents (comment under issue and mr) needs
	// none, since a root word could name only one of them (Q6).
	nested := map[string]int{}
	walkAll(a.Root, func(c *cobra.Command) {
		if c != a.Root && !isCobraInternal(c) && !c.Hidden && hasSubcommands(c) && c.Parent() != a.Root {
			nested[c.Name()]++
		}
	})
	walkAll(a.Root, func(c *cobra.Command) {
		if c == a.Root || isCobraInternal(c) || c.Hidden || !hasSubcommands(c) || c.Parent() == a.Root || nested[c.Name()] > 1 {
			return
		}
		words := strings.Fields(cmdPath(c))
		reachable := false
		for key, to := range a.Shortcuts {
			kw := strings.Fields(key)
			effective := len(kw) > 0 && childNamed(a.Root, kw[0]) == nil
			if effective && strings.Join(to, " ") == strings.Join(words, " ") {
				reachable = true
			}
		}
		if !reachable {
			add("nested group %q is not reachable from the root; add App.Shortcuts[%q] (CLI-022)", cmdPath(c), words[len(words)-1])
		}
	})
	return problems
}

// exampleCommand resolves an example the way the parser resolves a command line
// (Q21): the tool name, then root shortcuts and aliases. It returns the command the
// example runs (nil when it names none) and the example rewritten with the command's
// path in place of the words that named it.
func (a *App) exampleCommand(ex string) (*cobra.Command, string) {
	tokens := strings.Fields(ex)
	if len(tokens) == 0 || tokens[0] != a.toolName() {
		return nil, ex
	}
	args := a.applyShortcut(tokens[1:])
	target, kinds := a.classify(args)
	if target == nil || target == a.Root || hasSubcommands(target) {
		return nil, ex
	}
	out := []string{tokens[0], cmdPath(target)}
	for i, k := range kinds {
		if k != argWord {
			out = append(out, args[i])
		}
	}
	return target, strings.Join(out, " ")
}

func tierRank(t string) int {
	for i, v := range riskTierValues {
		if v == t {
			return i
		}
	}
	return -1
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func mapHasValue(m map[string]string, v string) bool {
	for _, x := range m {
		if x == v {
			return true
		}
	}
	return false
}
