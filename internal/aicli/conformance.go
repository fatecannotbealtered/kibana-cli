// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TB is the subset of testing.TB the conformance suite uses, so this package does
// not import testing.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// Harness connects the conformance suite to a tool.
type Harness struct {
	// NewApp returns a fresh App and command tree for every call; env holds the
	// variables the suite sets (state dir, scope file), and the suite also overlays
	// them on App.Env.
	NewApp func(env map[string]string) *App
	// Writes returns the number of write requests the fake upstream has seen so far.
	Writes func() int
	// Reset restores the fake upstream's seed state between checks.
	Reset func()
	// Samples maps each write command path ("note create") to the arguments of a
	// valid write, without the command path and without gate flags.
	Samples map[string][]string
	// Scope is the scope granted for positive checks (written by the suite to a
	// temporary file); nil grants every effect, dangerous included, for an hour.
	Scope map[string]any
	// CoverageFile enables R06: the file the tool's own tests recorded executed
	// commands into (set <PREFIX>_COVERAGE_FILE to it while those tests run).
	CoverageFile string
}

// RunConformance runs the conformance checks (BLUEPRINT 6) against a tool:
// G00-G10 for every write command and R01-R05 (R06 with Harness.CoverageFile).
// Every failure is reported with its check ID.
func RunConformance(t TB, h Harness) {
	t.Helper()
	if h.NewApp == nil || h.Writes == nil {
		t.Fatalf("aicli conformance: Harness.NewApp and Harness.Writes are required")
		return
	}
	s, err := newSuite(t, h)
	if err != nil {
		t.Fatalf("aicli conformance: %v", err)
		return
	}
	defer s.cleanup()
	s.runAll()
}

type suite struct {
	t       TB
	h       Harness
	root    string
	tool    string
	prefix  string
	probe   *App
	counter int
	seen    []seenEnvelope
	outputs [][]byte // every stdout/stderr, for the secret check
}

type seenEnvelope struct {
	label  string
	raw    []byte
	stream bool
	// byDefault marks a default invocation (no --fields or --limit), whose success
	// response R04 holds to the success budget (CLI-042, Q24).
	byDefault bool
}

// defaultInvocation reports arguments that leave the output's size as the command
// sets it: no --fields and no --limit (CLI-042).
func defaultInvocation(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if name := flagName(arg); strings.HasPrefix(arg, "--") && (name == "fields" || name == "limit") {
			return false
		}
	}
	return true
}

// stateDir is one isolated state directory with its scope file.
type stateDir struct {
	dir       string
	scopeFile string
	scopeOn   bool
}

type runResult struct {
	label   string
	exit    int
	stdout  []byte
	stderr  []byte
	env     map[string]any
	writes  int
	defects []string // tool bugs the library corrected during the run
}

func newSuite(t TB, h Harness) (*suite, error) {
	if h.Scope != nil {
		b, err := json.Marshal(h.Scope)
		if err != nil {
			return nil, fmt.Errorf("Harness.Scope cannot be encoded: %v", err)
		}
		if err := validateScopeFile(b); err != nil {
			return nil, fmt.Errorf("Harness.Scope is not a valid scope file: %v", err)
		}
	}
	root, err := os.MkdirTemp("", "aicli-conformance-")
	if err != nil {
		return nil, err
	}
	s := &suite{t: t, h: h, root: root}
	probe := h.NewApp(map[string]string{})
	if probe == nil || probe.Root == nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("Harness.NewApp returned no App or no Root")
	}
	probe.InstallBuiltins()
	probe.prepareTree()
	s.probe = probe
	s.tool = probe.toolName()
	s.prefix = probe.envPrefix()
	return s, nil
}

func (s *suite) cleanup() { _ = os.RemoveAll(s.root) }

func (s *suite) newState(scopeOn bool) *stateDir {
	s.counter++
	base := filepath.Join(s.root, fmt.Sprintf("s%03d", s.counter))
	_ = os.MkdirAll(base, 0o700)
	st := &stateDir{dir: filepath.Join(base, "state"), scopeFile: filepath.Join(base, "scope.json"), scopeOn: scopeOn}
	s.writeScope(st, nil)
	return st
}

func (s *suite) scopeObject() map[string]any {
	if s.h.Scope != nil {
		out := map[string]any{}
		for k, v := range s.h.Scope {
			out[k] = v
		}
		return out
	}
	now := time.Now().UTC()
	return map[string]any{
		"scope_id": "scp_aicli_conformance", "tool": s.tool, "granted_by": "aicli conformance suite",
		"granted_at": timeString(now.Add(-time.Minute)), "expires_at": timeString(now.Add(time.Hour)),
		"effects": writeEffects, "dangerous": true,
	}
}

func (s *suite) writeScope(st *stateDir, change map[string]any) {
	obj := s.scopeObject()
	for k, v := range change {
		obj[k] = v
	}
	b, _ := json.MarshalIndent(obj, "", "  ")
	_ = os.WriteFile(st.scopeFile, b, 0o600)
}

func (s *suite) writes() int { return s.h.Writes() }

func (s *suite) reset() {
	if s.h.Reset != nil {
		s.h.Reset()
	}
}

// run executes one command line in a fresh App bound to st.
func (s *suite) run(st *stateDir, label string, args []string, stdin string) runResult {
	scope := ""
	if st.scopeOn {
		scope = st.scopeFile
	}
	env := map[string]string{
		s.prefix + "_STATE_DIR":     st.dir,
		s.prefix + "_SCOPE_FILE":    scope,
		s.prefix + "_COVERAGE_FILE": "",
	}
	app := s.h.NewApp(env)
	if app == nil {
		s.t.Fatalf("aicli conformance: Harness.NewApp returned nil")
		return runResult{}
	}
	app.StateDir = st.dir
	app.offline = true // doctor skips its release refresh: conformance never touches the network
	orig := app.Env
	app.Env = func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		if orig != nil {
			return orig(k)
		}
		return os.Getenv(k)
	}
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr = &out, &errb
	if stdin == "" {
		stdin = "aicli conformance input\n"
	}
	app.Stdin = strings.NewReader(stdin)
	before := s.writes()
	exit := app.Execute(args)
	r := runResult{label: label, exit: exit, stdout: out.Bytes(), stderr: errb.Bytes(), writes: s.writes() - before,
		defects: app.defects}
	s.outputs = append(s.outputs, r.stdout, r.stderr)
	if t := bytes.TrimSpace(r.stdout); len(t) > 0 && t[0] == '{' {
		dec := json.NewDecoder(bytes.NewReader(r.stdout))
		dec.UseNumber()
		var m map[string]any
		if dec.Decode(&m) == nil {
			r.env = m
		}
		s.seen = append(s.seen, seenEnvelope{label: label + ": " + strings.Join(args, " "), raw: r.stdout, stream: isStreamOutput(r.stdout),
			byDefault: defaultInvocation(args)})
	}
	return r
}

func (r runResult) code() string {
	e, _ := r.env["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func (r runResult) details() map[string]any {
	e, _ := r.env["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	if d == nil {
		d = map[string]any{}
	}
	return d
}

func (r runResult) data() map[string]any {
	d, _ := r.env["data"].(map[string]any)
	if d == nil {
		d = map[string]any{}
	}
	return d
}

func (r runResult) describe() string {
	out := strings.TrimSpace(string(r.stdout))
	if out == "" {
		out = strings.TrimSpace(string(r.stderr))
	}
	return fmt.Sprintf("exit %d, %s", r.exit, truncateRunes(out, 600))
}

func (s *suite) errorf(id string, format string, args ...any) {
	s.t.Helper()
	s.t.Errorf("%s: "+format, append([]any{id}, args...)...)
}

// ---------------------------------------------------------------- driver

func (s *suite) runAll() {
	for _, p := range s.probe.checkTree() {
		s.errorf("R02", "%s", p)
	}
	s.checkReference()
	s.checkUX()
	s.checkSelfDescription()
	var writes []*cobra.Command
	for _, c := range s.probe.commands() {
		if m := s.probe.metaFor(c); m != nil && m.isWrite() {
			writes = append(writes, c)
		}
	}
	for path := range s.h.Samples {
		if c := s.probe.commandByPath(path); c == nil || !contains(pathsOf(writes), path) {
			s.errorf("G00", "Harness.Samples names %q, which is not a write command", path)
		}
	}
	for _, c := range writes {
		path := cmdPath(c)
		sample, ok := s.h.Samples[path]
		if !ok {
			s.errorf("G00", "write command %q has no sample in Harness.Samples", path)
			continue
		}
		s.checkWrite(c, stripPath(sample, path))
	}
	s.checkEnvelopes()
	s.checkCoverage()
}

func pathsOf(cmds []*cobra.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = cmdPath(c)
	}
	return out
}

// stripPath tolerates samples that repeat the command path.
func stripPath(sample []string, path string) []string {
	words := strings.Fields(path)
	if len(sample) >= len(words) {
		match := true
		for i, w := range words {
			if sample[i] != w {
				match = false
			}
		}
		if match {
			return append([]string{}, sample[len(words):]...)
		}
	}
	return append([]string{}, sample...)
}

// ---------------------------------------------------------------- R01 reference

func (s *suite) checkReference() {
	st := s.newState(false)
	r := s.run(st, "R01", []string{"reference"}, "")
	if r.exit != 0 {
		s.errorf("R01", "reference failed: %s", r.describe())
	} else if err := validateReferenceFull(s.resolvedExamples(r.data())); err != nil {
		s.errorf("R01", "reference: %v", err)
	}
	r = s.run(st, "R01", []string{"reference", "--index"}, "")
	if r.exit != 0 {
		s.errorf("R01", "reference --index failed: %s", r.describe())
		return
	}
	if n := runeLen(r.stdout); n > indexBudgetChars {
		s.errorf("R01", "reference --index is %d characters, over %d", n, indexBudgetChars)
	}
	if err := validateReferenceIndex(r.data()); err != nil {
		s.errorf("R01", "reference --index: %v", err)
	}
	if groups, ok := r.data()["groups"].([]any); ok {
		for _, g := range groups {
			path, _ := g.(map[string]any)["path"].(string)
			gr := s.run(st, "R01", []string{"reference", "--index", path}, "")
			if gr.exit != 0 {
				s.errorf("R01", "reference --index %s failed: %s", path, gr.describe())
				continue
			}
			if n := runeLen(gr.stdout); n > indexBudgetChars {
				s.errorf("R01", "reference --index %s is %d characters, over %d", path, n, indexBudgetChars)
			}
		}
	}
	for _, c := range s.probe.commands() {
		path := cmdPath(c)
		cr := s.run(st, "R01", []string{"reference", "--command", path}, "")
		if cr.exit != 0 {
			s.errorf("R01", "reference --command %q failed: %s", path, cr.describe())
			continue
		}
		if n := runeLen(cr.stdout); n > commandBudgetChars {
			s.errorf("R01", "reference --command %q is %d characters, over %d", path, n, commandBudgetChars)
		}
		if err := validateReferenceCommand(s.resolvedExamples(cr.data())); err != nil {
			s.errorf("R01", "reference --command %q: %v", path, err)
		}
	}
}

// resolvedExamples returns reference data whose examples are resolved as the parser
// resolves them (Q21): an example written with a root shortcut or an alias is
// checked as the command line it runs, as the tree check (R02) checks it. Examples
// that resolve to no command are left as they are, for the validator to report.
func (s *suite) resolvedExamples(data map[string]any) map[string]any {
	fix := func(entry any) any {
		c, ok := entry.(map[string]any)
		if !ok {
			return entry
		}
		list, ok := c["examples"].([]any)
		if !ok {
			return entry
		}
		out := cloneMap(c)
		examples := make([]any, len(list))
		for i, x := range list {
			examples[i] = x
			if ex, isText := x.(string); isText {
				if target, resolved := s.probe.exampleCommand(ex); target != nil && cmdPath(target) == c["path"] {
					examples[i] = resolved
				}
			}
		}
		out["examples"] = examples
		return out
	}
	out := cloneMap(data)
	if cmds, ok := data["commands"].([]any); ok {
		fixed := make([]any, len(cmds))
		for i, c := range cmds {
			fixed[i] = fix(c)
		}
		out["commands"] = fixed
	}
	if c, ok := data["command"]; ok {
		out["command"] = fix(c)
	}
	return out
}

func runeLen(b []byte) int { return utf8.RuneCount(bytes.TrimRight(b, "\n")) }

// ---------------------------------------------------------------- R03 agent UX

func (s *suite) checkUX() {
	st := s.newState(false)
	var leaf *cobra.Command
	for _, c := range s.probe.commands() {
		if !s.probe.reg.builtin[c] {
			leaf = c
			break
		}
	}
	if leaf == nil {
		leaf = s.probe.commandByPath("context")
	}
	words := strings.Fields(cmdPath(leaf))
	last := words[len(words)-1]
	typo := last[:len(last)-1]
	if len(last) <= 3 {
		typo = last + "x"
	}
	args := append(append([]string{}, words[:len(words)-1]...), typo)
	r := s.run(st, "R03", args, "")
	full := s.tool + " " + cmdPath(leaf)
	switch {
	case r.exit != 2 || r.code() != "E_USAGE" || r.details()["problem"] != "unknown_command":
		s.errorf("R03", "an unknown command (%q) must be E_USAGE unknown_command: %s", strings.Join(args, " "), r.describe())
	case !listHasString(r.details()["did_you_mean"], full):
		s.errorf("R03", "unknown command %q should suggest %q, got %v", strings.Join(args, " "), full, r.details()["did_you_mean"])
	}
	r = s.run(st, "R03", append(append([]string{}, words...), "--aicli-no-such-flag"), "")
	if r.exit != 2 || r.code() != "E_USAGE" || r.details()["problem"] != "unknown_flag" || r.details()["parameter"] != "--aicli-no-such-flag" {
		s.errorf("R03", "an unknown flag must be E_USAGE unknown_flag naming it: %s", r.describe())
	}
	r = s.run(st, "R03", []string{"context", "aicli-unexpected-argument"}, "")
	if r.exit != 2 || r.code() != "E_USAGE" || r.details()["problem"] != "unexpected_argument" {
		s.errorf("R03", "an unexpected positional argument must be E_USAGE unexpected_argument: %s", r.describe())
	}
	r = s.run(st, "R03", []string{"context", "--format", "raw"}, "")
	if r.exit != 2 || r.code() != "E_USAGE" || r.details()["problem"] != "unsupported_format" {
		s.errorf("R03", "an unsupported --format must be E_USAGE unsupported_format in JSON: %s", r.describe())
	}
}

func listHasString(x any, want string) bool {
	list, _ := x.([]any)
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- R05 self-description

func (s *suite) checkSelfDescription() {
	st := s.newState(false)
	r := s.run(st, "R05", []string{"context"}, "")
	if r.exit != 0 {
		s.errorf("R05", "context failed: %s", r.describe())
	} else if err := validateContext(r.data()); err != nil {
		s.errorf("R05", "context: %v", err)
	}
	r = s.run(st, "R05", []string{"doctor"}, "")
	if r.exit != 0 {
		s.errorf("R05", "doctor failed: %s", r.describe())
	} else if err := validateDoctor(r.data()); err != nil {
		s.errorf("R05", "doctor: %v", err)
	}
	r = s.run(st, "R05", []string{"operation", "list"}, "")
	if r.exit != 0 {
		s.errorf("R05", "operation list on an empty journal failed: %s", r.describe())
	} else if items, ok := r.data()["items"].([]any); !ok || len(items) != 0 {
		s.errorf("R05", "operation list on an empty journal must return no items: %s", r.describe())
	}
	r = s.run(st, "R05", []string{"changelog"}, "")
	if r.exit != 0 {
		s.errorf("R05", "changelog failed: %s", r.describe())
	} else if err := validateChangelog(r.data()); err != nil {
		s.errorf("R05", "changelog: %v", err)
	}
}

// ---------------------------------------------------------------- write checks

// sampleArg is one flag or positional of a sample.
type sampleArg struct {
	positional bool
	name       string // canonical flag name
	value      string
	hasValue   bool
	tokens     []string
}

func parseSample(c *cobra.Command, sample []string) []sampleArg {
	var out []sampleArg
	for i := 0; i < len(sample); i++ {
		tok := sample[i]
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			out = append(out, sampleArg{positional: true, value: tok, hasValue: true, tokens: []string{tok}})
			continue
		}
		name, value, inline := splitFlag(tok)
		f := lookupFlag(c, name)
		arg := sampleArg{name: name, tokens: []string{tok}}
		if f != nil {
			arg.name = f.Name
		}
		if inline {
			arg.value, arg.hasValue = value, true
		} else if f != nil && f.NoOptDefVal == "" && i+1 < len(sample) {
			arg.value, arg.hasValue = sample[i+1], true
			arg.tokens = append(arg.tokens, sample[i+1])
			i++
		}
		out = append(out, arg)
	}
	return out
}

func joinArgs(args []sampleArg) []string {
	var out []string
	for _, a := range args {
		out = append(out, a.tokens...)
	}
	return out
}

func withFlagValue(a sampleArg, value string) sampleArg {
	b := a
	if len(a.tokens) == 2 {
		b.tokens = []string{a.tokens[0], value}
	} else {
		b.tokens = []string{"--" + a.name + "=" + value}
	}
	b.value = value
	return b
}

func (s *suite) checkWrite(c *cobra.Command, sample []string) {
	path := cmdPath(c)
	m := s.probe.metaFor(c)
	words := strings.Fields(path)
	base := append(append([]string{}, words...), sample...)
	gate := func(extra ...string) []string {
		out := append(append([]string{}, base...), extra...)
		if m.dangerous() {
			out = append(out, "--dangerous")
		}
		return out
	}
	s.reset()
	st := s.newState(true)

	// G01: a bare call is refused with the preview and a token.
	r := s.run(st, "G01", gate(), "")
	d := r.details()
	switch {
	case r.writes != 0:
		s.errorf("G01", "%s: a bare call sent %d write(s)", path, r.writes)
	case r.exit != 5 || r.code() != "E_CONFIRMATION_REQUIRED" || d["problem"] != "confirmation_required":
		s.errorf("G01", "%s: a bare call must be exit 5 E_CONFIRMATION_REQUIRED confirmation_required: %s", path, r.describe())
	case d["preview"] == nil || d["operation_id"] == nil || !isToken(d["confirm_token"]):
		if p, _ := d["preview"].(map[string]any); p != nil && p["can_apply"] == false {
			s.errorf("G01", "%s: the sample's preview cannot apply (blocked_by %v); fix the sample or the fake upstream", path, p["blocked_by"])
		} else {
			s.errorf("G01", "%s: the refusal must carry preview, confirm_token and operation_id: %s", path, r.describe())
		}
	}

	// G02: dry-run returns the preview and a canonical token; nothing is sent.
	r = s.run(st, "G02", gate("--dry-run"), "")
	token, _ := r.data()["confirm_token"].(string)
	switch {
	case r.writes != 0:
		s.errorf("G02", "%s: --dry-run sent %d write(s)", path, r.writes)
	case r.exit != 0 || !isToken(token) || r.data()["preview"] == nil:
		s.errorf("G02", "%s: --dry-run must return exit 0 with a preview and a canonical token: %s", path, r.describe())
		return
	}

	// G06: no output mode bypasses the gate.
	for _, mode := range [][]string{{"--format", "text"}, {"--pretty"}, {"--quiet"}, {"--compact"}, {"--json"}} {
		r = s.run(st, "G06", gate(mode...), "")
		if r.writes != 0 || r.exit != 5 {
			s.errorf("G06", "%s %s without a token must be refused with exit 5 and send nothing: %s (writes %d)",
				path, strings.Join(mode, " "), r.describe(), r.writes)
		}
	}
	r = s.run(st, "G06", gate("--format", "raw"), "")
	if r.writes != 0 || (r.exit != 5 && r.exit != 2) {
		s.errorf("G06", "%s --format raw without a token must be refused: %s (writes %d)", path, r.describe(), r.writes)
	}
	r = s.run(st, "G06", gate("--format", "text", "--dry-run"), "")
	if r.writes != 0 || r.exit != 0 {
		s.errorf("G06", "%s --format text --dry-run must preview without sending: %s", path, r.describe())
	}

	// G03: every mutated input is token_mismatch naming the parameter.
	s.checkMutations(st, c, m, sample, token)

	// G04: the exact confirm dispatches; a repeat is token_used. A command whose only
	// effects are configure, persist or artifact may change only local things (the
	// tool's keyring entry, its settings file, a local file) and send nothing
	// upstream: a receipt with change applied or persistence saved counts as its one
	// write (P7).
	r = s.run(st, "G04", gate("--confirm", token), "")
	reported := r.data()["receipt"] != nil || r.details()["receipt"] != nil
	if m.BatchFlag != "" {
		reported = (r.data()["items"] != nil && r.data()["summary"] != nil) || (r.details()["items"] != nil && r.details()["summary"] != nil)
	}
	localOnly := !m.hasEffect(Change, Deliver)
	switch {
	case localOnly && r.writes < 1 && !appliedLocally(r):
		s.errorf("G04", "%s: the exact confirm sent no write, and its receipt shows no applied change or saved file: %s", path, r.describe())
	case !localOnly && r.writes < 1:
		s.errorf("G04", "%s: the exact confirm sent no write: %s", path, r.describe())
	case !reported:
		s.errorf("G04", "%s: the confirm must report a receipt (per item for a batch): %s", path, r.describe())
	case r.exit != 0:
		s.t.Logf("G04: %s: the confirm dispatched and reported %s (exit %d) per its receipt", path, r.code(), r.exit)
	}
	for _, d := range r.defects {
		s.errorf("G04", "%s: %s", path, d)
	}
	if id, _ := r.data()["operation_id"].(string); id != "" {
		show := s.run(st, "G04", []string{"operation", "show", "--id", id}, "")
		if show.exit != 0 {
			s.errorf("G04", "%s: operation show cannot find the confirmed write: %s", path, show.describe())
		} else if err := validateOperationView(show.data()); err != nil {
			s.errorf("G04", "%s: operation show: %v", path, err)
		} else if st, _ := show.data()["status"].(string); unresolved(st) || st == "planned" || st == "expired" {
			s.errorf("G04", "%s: after a reported confirm the operation is %s", path, st)
		}
	}
	r = s.run(st, "G04", gate("--confirm", token), "")
	if r.writes != 0 || r.exit != 6 || r.code() != "E_CONFLICT" || r.details()["conflict"] != "token_used" {
		s.errorf("G04", "%s: repeating the confirm must be E_CONFLICT token_used and send nothing: %s (writes %d)", path, r.describe(), r.writes)
	}
	s.checkSecrets(c, m, sample, st)

	// G05: re-encoded tokens are rejected.
	s.reset()
	st2 := s.newState(true)
	r = s.run(st2, "G05", gate("--dry-run"), "")
	t2, _ := r.data()["confirm_token"].(string)
	r = s.run(st2, "G05", gate("--dry-run"), "")
	t3, _ := r.data()["confirm_token"].(string)
	if !isToken(t2) || !isToken(t3) {
		s.errorf("G05", "%s: --dry-run did not return tokens: %s", path, r.describe())
	} else {
		variants := map[string]string{
			"upper case":    strings.ToUpper(t2),
			"upper-case id": t2[:4] + strings.ToUpper(t2[4:30]) + t2[30:],
			"padded":        t2 + "=",
			"truncated":     t2[:len(t2)-1],
			"extra char":    t2 + "0",
			"leading space": " " + t2,
			"other op id":   t3[:31] + t2[31:],
			"other mac":     t2[:31] + t3[31:],
			"no prefix":     t2[4:],
		}
		for _, name := range sortedKeys(variants) {
			r = s.run(st2, "G05", gate("--confirm", variants[name]), "")
			if r.writes != 0 || r.exit == 0 {
				s.errorf("G05", "%s: a re-encoded token (%s) must be rejected and send nothing: %s (writes %d)", path, name, r.describe(), r.writes)
			} else if r.code() != "E_CONFLICT" || r.details()["conflict"] != "token_mismatch" {
				s.errorf("G05", "%s: a re-encoded token (%s) must be E_CONFLICT token_mismatch: %s", path, name, r.describe())
			}
		}
	}

	// G07: without a scope, dry-run and confirm are E_FORBIDDEN with details.scope.
	st2.scopeOn = false
	r = s.run(st2, "G07", gate("--dry-run"), "")
	s.expectForbidden(path, "dry-run", r)
	if isToken(t2) {
		r = s.run(st2, "G07", gate("--confirm", t2), "")
		s.expectForbidden(path, "confirm", r)
	}
	st2.scopeOn = true

	// G10: a scope changed between dry-run and confirm is scope_changed.
	r = s.run(st2, "G10", gate("--dry-run"), "")
	t4, _ := r.data()["confirm_token"].(string)
	s.writeScope(st2, map[string]any{"notes": "changed by the aicli conformance suite"})
	r = s.run(st2, "G10", gate("--confirm", t4), "")
	if r.writes != 0 || r.exit != 6 || r.code() != "E_CONFLICT" || r.details()["conflict"] != "scope_changed" {
		s.errorf("G10", "%s: a changed scope must be E_CONFLICT scope_changed and send nothing: %s (writes %d)", path, r.describe(), r.writes)
	}
	s.writeScope(st2, nil)

	// G09: dangerous commands need --dangerous in both steps.
	if m.dangerous() {
		r = s.run(st2, "G09", append(append([]string{}, base...), "--dry-run"), "")
		if r.writes != 0 || r.exit != 5 || r.details()["problem"] != "dangerous_flag_required" {
			s.errorf("G09", "%s: --dry-run without --dangerous must be exit 5 dangerous_flag_required: %s", path, r.describe())
		}
		r = s.run(st2, "G09", gate("--dry-run"), "")
		t5, _ := r.data()["confirm_token"].(string)
		r = s.run(st2, "G09", append(append([]string{}, base...), "--confirm", t5), "")
		if r.writes != 0 || r.exit != 5 || r.details()["problem"] != "dangerous_flag_required" {
			s.errorf("G09", "%s: --confirm without --dangerous must be exit 5 dangerous_flag_required: %s", path, r.describe())
		}
	}

	// G08: a corrupt key or a blocked journal refuses with E_IO.
	st3 := s.newState(true)
	r = s.run(st3, "G08", gate("--dry-run"), "")
	t6, _ := r.data()["confirm_token"].(string)
	keyPath := filepath.Join(st3.dir, "confirm.key")
	for _, bad := range [][]byte{[]byte("corrupt"), bytes.Repeat([]byte{7}, keySize+1)} {
		_ = os.WriteFile(keyPath, bad, 0o600)
		for _, step := range [][]string{gate("--confirm", t6), gate(), gate("--dry-run")} {
			r = s.run(st3, "G08", step, "")
			if r.writes != 0 || r.exit != 1 || r.code() != "E_IO" {
				s.errorf("G08", "%s: with a corrupt key, %q must be E_IO and send nothing: %s (writes %d)",
					path, strings.Join(step[len(base):], " "), r.describe(), r.writes)
			}
		}
	}
	st4 := s.newState(true)
	_ = os.MkdirAll(st4.dir, 0o700)
	_ = os.WriteFile(filepath.Join(st4.dir, "operations"), []byte("not a directory"), 0o600)
	for _, step := range [][]string{gate(), gate("--dry-run")} {
		r = s.run(st4, "G08", step, "")
		if r.writes != 0 || r.exit != 1 || r.code() != "E_IO" {
			s.errorf("G08", "%s: with the journal path blocked, %q must be E_IO and send nothing: %s (writes %d)",
				path, strings.Join(step[len(base):], " "), r.describe(), r.writes)
		}
	}
}

func isToken(x any) bool {
	s, _ := x.(string)
	_, _, ok := parseToken(s)
	return ok
}

// isStreamOutput reports stdout that holds more than one JSON document, one per
// line: an NDJSON stream (CLI-006), which R04 checks with ValidateStream.
func isStreamOutput(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var first json.RawMessage
	if dec.Decode(&first) != nil {
		return false
	}
	rest := bytes.TrimSpace(raw[dec.InputOffset():])
	return len(rest) > 0 && rest[0] == '{' && !bytes.Contains(first, []byte("\n"))
}

// appliedLocally reports a confirmed write whose receipt (or a batch item's) shows
// change applied or persistence saved (P7).
func appliedLocally(r runResult) bool {
	var receipts []any
	for _, part := range []map[string]any{r.data(), r.details()} {
		receipts = append(receipts, part["receipt"])
		items, _ := part["items"].([]any)
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				receipts = append(receipts, m["receipt"])
			}
		}
	}
	for _, x := range receipts {
		receipt, _ := x.(map[string]any)
		outcome, _ := receipt["outcome"].(map[string]any)
		if outcome["change"] == "applied" || outcome["persistence"] == "saved" {
			return true
		}
	}
	return false
}

func (s *suite) expectForbidden(path, step string, r runResult) {
	scope, _ := r.details()["scope"].(map[string]any)
	if r.writes != 0 || r.exit != 4 || r.code() != "E_FORBIDDEN" || scope == nil {
		s.errorf("G07", "%s: %s without a scope must be E_FORBIDDEN with details.scope and send nothing: %s (writes %d)",
			path, step, r.describe(), r.writes)
	}
}

// checkMutations runs G03: each sample input changed, dropped or added between the
// dry-run and the confirm must be token_mismatch naming it, with nothing sent.
func (s *suite) checkMutations(st *stateDir, c *cobra.Command, m *Meta, sample []string, token string) {
	path := cmdPath(c)
	words := strings.Fields(path)
	args := parseSample(c, sample)
	positionals := 0
	for _, a := range args {
		if a.positional {
			positionals++
		}
	}
	confirm := func(label string, mutated []sampleArg, stdin string, expect string, requiredDrop bool) {
		full := append(append([]string{}, words...), joinArgs(mutated)...)
		full = append(full, "--confirm", token)
		if m.dangerous() {
			full = append(full, "--dangerous")
		}
		r := s.run(st, "G03", full, stdin)
		switch {
		case r.writes != 0 || r.exit == 0:
			s.errorf("G03", "%s: %s was confirmed or sent %d write(s): %s", path, label, r.writes, r.describe())
		case r.code() == "E_CONFLICT" && r.details()["conflict"] == "token_mismatch":
			if p, _ := r.details()["parameter"].(string); p != expect {
				s.errorf("G03", "%s: %s must name %q, named %q", path, label, expect, p)
			}
		case r.code() == "E_CONFLICT":
			s.errorf("G03", "%s: %s passed the input check and failed later (%v); the token does not bind it", path, label, r.details()["conflict"])
		case requiredDrop && r.code() == "E_USAGE":
		case r.exit == 2 || r.exit == 3:
			s.t.Logf("G03: %s: %s was rejected before the gate (%s); its binding was not exercised", path, label, r.code())
		default:
			s.errorf("G03", "%s: %s must be E_CONFLICT token_mismatch naming %q: %s", path, label, expect, r.describe())
		}
	}
	tmp := filepath.Join(s.root, fmt.Sprintf("mut%03d", s.counter))
	_ = os.MkdirAll(tmp, 0o700)
	for i, a := range args {
		label := "changing " + describeArg(a)
		var expect string
		if a.positional {
			expect = "args"
			if m.Positional != "" && positionals == 1 {
				expect = "--" + m.Positional
			}
		} else {
			expect = s.labelFor(c, m, a.name)
		}
		if a.hasValue && !contains(excludedFromBinding, a.name) {
			mutated := append([]sampleArg{}, args...)
			stdin := ""
			f := lookupFlag(c, a.name)
			switch {
			case a.positional:
				mutated[i].value = mutateValue(nil, m, "", a.value)
				mutated[i].tokens = []string{mutated[i].value}
			case f != nil && m.FileInputs[f.Name] != "" && a.value == "-":
				stdin = "aicli conformance input (changed)\n"
			case f != nil && m.FileInputs[f.Name] != "":
				p := filepath.Join(tmp, fmt.Sprintf("changed-%d.txt", i))
				_ = os.WriteFile(p, []byte("aicli conformance changed file content\n"), 0o600)
				mutated[i] = withFlagValue(a, p)
			default:
				mutated[i] = withFlagValue(a, mutateValue(f, m, a.name, a.value))
			}
			confirm(label, mutated, stdin, expect, false)
		}
		if !a.positional && !contains(excludedFromBinding, a.name) {
			dropped := append(append([]sampleArg{}, args[:i]...), args[i+1:]...)
			f := lookupFlag(c, a.name)
			required := f != nil && (flagRequired(f) || a.name == m.BatchFlag)
			confirm("dropping "+describeArg(a), dropped, "", expect, required)
		}
	}
	// add one optional flag the sample does not use
	present := map[string]bool{}
	for _, a := range args {
		if !a.positional {
			present[a.name] = true
		}
	}
	var add *pflag.Flag
	c.Flags().VisitAll(func(f *pflag.Flag) {
		// --profile is aicli's, like the other global flags: it binds as the active
		// profile (CLI-101), and an unknown name stops the command first.
		if add != nil || present[f.Name] || f.Hidden || s.probe.isGlobal(f.Name) || flagRequired(f) || f.Name == m.Positional {
			return
		}
		if bound, ok := m.FileInputs[f.Name]; ok && present[bound] {
			return
		}
		for file, bound := range m.FileInputs {
			if bound == f.Name && present[file] {
				return
			}
		}
		if len(f.Annotations["cobra_annotation_mutually_exclusive"]) > 0 {
			return
		}
		add = f
	})
	if add != nil {
		value := "aicli-conformance"
		if doc, ok := m.Params[add.Name]; ok && len(doc.Enum) > 0 {
			value = doc.Enum[0]
		}
		if typ, _ := flagType(add); typ == "integer" || typ == "number" {
			value = "1"
		}
		if m.FileInputs[add.Name] != "" {
			p := filepath.Join(tmp, "added.txt")
			_ = os.WriteFile(p, []byte("aicli conformance added file\n"), 0o600)
			value = p
		}
		extra := sampleArg{name: add.Name, tokens: []string{"--" + add.Name + "=" + value}}
		if add.NoOptDefVal != "" {
			extra.tokens = []string{"--" + add.Name}
			if add.Value.Type() == "bool" && add.DefValue == "true" {
				extra.tokens = []string{"--" + add.Name + "=false"}
			}
		}
		confirm("adding --"+add.Name, append(append([]sampleArg{}, args...), extra), "", s.labelFor(c, m, add.Name), false)
	}
}

// labelFor is the parameter a token_mismatch names for a flag (binding.go's rule).
func (s *suite) labelFor(c *cobra.Command, m *Meta, flag string) string {
	if bound, ok := m.FileInputs[flag]; ok {
		return labelFor(c, bound, flag)
	}
	return "--" + flag
}

func describeArg(a sampleArg) string {
	if a.positional {
		return "the positional argument " + strconv.Quote(a.value)
	}
	return "--" + a.name
}

var (
	numberPattern = regexp.MustCompile(`^-?[0-9]+$`)
	floatPattern  = regexp.MustCompile(`^-?[0-9]+\.[0-9]+$`)
	datePattern   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	colourPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// mutateValue returns a different value of the same type, so G03 reaches the gate
// instead of failing the tool's own validation (P9): an integer or a float plus 1,
// a boolean flipped, another allowed value of an enum, a duration doubled (a zero
// duration becomes 1s), an ISO date or date-time one day later, a #rrggbb colour
// inverted; anything else with -mutated appended. A list gets one more element.
func mutateValue(f *pflag.Flag, m *Meta, name, value string) string {
	if f != nil {
		if doc, ok := m.Params[name]; ok && len(doc.Enum) > 1 {
			for _, e := range doc.Enum {
				if e != value {
					return e
				}
			}
		}
		if _, ok := f.Value.(pflag.SliceValue); ok {
			// The sample's own last element decides: a list of integers gets another
			// integer.
			parts := strings.Split(value, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); numberPattern.MatchString(last) {
				if n, err := strconv.Atoi(last); err == nil {
					return value + "," + strconv.Itoa(n+1000)
				}
			}
			return value + ",aicli-mutated"
		}
		switch typ, _ := flagType(f); typ {
		case "boolean":
			if b, err := strconv.ParseBool(value); err == nil {
				return strconv.FormatBool(!b)
			}
		case "number":
			if n, err := strconv.ParseFloat(value, 64); err == nil {
				return strconv.FormatFloat(n+1, 'f', -1, 64)
			}
		}
		if f.Value.Type() == "duration" {
			if d, err := time.ParseDuration(value); err == nil {
				if d == 0 {
					return "1s"
				}
				return (2 * d).String()
			}
		}
	}
	switch {
	case numberPattern.MatchString(value):
		if n, err := strconv.Atoi(value); err == nil {
			return strconv.Itoa(n + 1)
		}
	case floatPattern.MatchString(value):
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			return strconv.FormatFloat(n+1, 'f', -1, 64)
		}
	case datePattern.MatchString(value):
		if d, err := time.Parse(time.DateOnly, value); err == nil {
			return d.AddDate(0, 0, 1).Format(time.DateOnly)
		}
	case colourPattern.MatchString(value):
		if rgb, err := strconv.ParseUint(value[1:], 16, 32); err == nil {
			return fmt.Sprintf("#%06x", 0xffffff^rgb)
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Add(24 * time.Hour).Format(time.RFC3339Nano)
	}
	return value + "-mutated"
}

// checkSecrets verifies SEC-021: secret sample values never appear in any output or
// in the state directory.
func (s *suite) checkSecrets(c *cobra.Command, m *Meta, sample []string, st *stateDir) {
	var secrets []string
	for _, a := range parseSample(c, sample) {
		if !a.positional && a.hasValue && m.isSecret(a.name) && len(a.value) >= 4 {
			secrets = append(secrets, a.value)
		}
	}
	if len(secrets) == 0 {
		return
	}
	for _, out := range s.outputs {
		for _, sec := range secrets {
			if bytes.Contains(out, []byte(sec)) {
				s.errorf("G04", "%s: a secret value appeared in the output (SEC-021)", cmdPath(c))
				return
			}
		}
	}
	_ = filepath.Walk(st.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, sec := range secrets {
			if bytes.Contains(b, []byte(sec)) {
				s.errorf("G04", "%s: a secret value was stored in %s (SEC-021)", cmdPath(c), filepath.Base(p))
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------- R04 envelopes

func (s *suite) checkEnvelopes() {
	for _, e := range s.seen {
		// ValidateEnvelope covers the envelope, details, budgets, snake_case keys, ids
		// and UTC times (CLI-007); ValidateStream does the same for each line of a
		// stream and checks that the summary is last.
		validate := ValidateEnvelope
		if e.stream {
			validate = ValidateStream
		}
		if err := validate(e.raw); err != nil {
			s.errorf("R04", "%s: %v", e.label, err)
			continue
		}
		// A default invocation's success stays within the budget (CLI-042, Q24);
		// the full reference is exempt (decision 17).
		if e.stream || !e.byDefault {
			continue
		}
		var env map[string]any
		dec := json.NewDecoder(bytes.NewReader(e.raw))
		dec.UseNumber()
		if dec.Decode(&env) != nil {
			continue
		}
		if size, over := overSuccessBudget(env); over {
			s.errorf("R04", "%s: the response is %d characters, over the %d budget of a default invocation (CLI-042)", e.label, size, successBudgetChars)
		}
	}
}

// overSuccessBudget reports a success envelope over the success budget of a default
// invocation (CLI-042, Q24), measured compact; the full reference is exempt.
func overSuccessBudget(env map[string]any) (int, bool) {
	if env["ok"] != true || isFullReference(env["data"]) {
		return 0, false
	}
	size := compactSize(env)
	return size, size > successBudgetChars
}

// ---------------------------------------------------------------- R06 coverage

// recordCoverage appends the executed command to <PREFIX>_COVERAGE_FILE when set
// (the recorder hook for VER-003 / R06).
func (a *App) recordCoverage(st *runState, exit int) {
	path := strings.TrimSpace(a.env(a.envPrefix() + "_COVERAGE_FILE"))
	if path == "" || st == nil || st.cmd == nil {
		return
	}
	line, err := json.Marshal(map[string]any{"command": cmdPath(st.cmd), "exit": exit})
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// CheckCoverage runs only R06 (VER-003): every registry command must appear in
// Harness.CoverageFile. Call it after all of the tool's own tests ran, for example
// from TestMain, so the result does not depend on test order.
func CheckCoverage(t TB, h Harness) {
	t.Helper()
	if h.NewApp == nil || h.CoverageFile == "" {
		t.Fatalf("aicli coverage: Harness.NewApp and Harness.CoverageFile are required")
		return
	}
	if h.Writes == nil {
		h.Writes = func() int { return 0 }
	}
	s, err := newSuite(t, h)
	if err != nil {
		t.Fatalf("aicli coverage: %v", err)
		return
	}
	defer s.cleanup()
	s.checkCoverage()
}

func (s *suite) checkCoverage() {
	if s.h.CoverageFile == "" {
		return
	}
	f, err := os.Open(s.h.CoverageFile)
	if err != nil {
		s.errorf("R06", "the coverage file cannot be read: %v", err)
		return
	}
	defer func() { _ = f.Close() }()
	seen := map[string]bool{}
	sc := bufio.NewScanner(io.LimitReader(f, 64<<20))
	for sc.Scan() {
		var rec struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Command != "" {
			seen[rec.Command] = true
		}
	}
	for _, c := range s.probe.commands() {
		if s.probe.reg.builtin[c] {
			continue
		}
		if !seen[cmdPath(c)] {
			s.errorf("R06", "command %q was never executed by the tool's own tests (VER-003)", cmdPath(c))
		}
	}
}
