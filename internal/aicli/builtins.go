// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// InstallBuiltins adds the built-in commands: reference, context, doctor, changelog,
// operation show/list/resolve (CLI-031 .. CLI-037, CLI-061) and, with App.Update,
// update (CLI-070). Execute calls it when the tool has not. A tool command already
// using a built-in name is left in place and reported by the tree check.
func (a *App) InstallBuiltins() {
	if a.builtinsInstalled || a.Root == nil {
		return
	}
	a.builtinsInstalled = true
	a.reg.init()
	tool := a.toolName()
	a.Schema("aicli_reference", SchemaDef{Shape: "object", Fields: []string{"tool", "version", "spec_version", "schema_version",
		"risk_tier", "global_flags", "commands", "schemas", "error_codes", "exit_codes", "groups", "command"}})
	contextFields := append([]string{}, contextRequiredKeys...)
	if a.Settings != nil {
		contextFields = append(contextFields, "profile")
	}
	a.Schema("aicli_context", SchemaDef{Shape: "object", Fields: contextFields})
	a.Schema("aicli_doctor", SchemaDef{Shape: "object", Fields: []string{"checks"}})
	a.Schema("aicli_changelog", SchemaDef{Shape: "object", Fields: []string{"current_version", "since", "count", "entries",
		"truncated", "truncated_reason", "omitted"}}) // a changelog cut to the budget (P3)
	a.Schema("aicli_operation", SchemaDef{Shape: "object", Fields: append(append([]string{}, operationRecordKeys...), operationOptionalKeys...),
		Untrusted: []string{"preview.changes", "preview.target.label", "receipt.observed", "resolution.evidence"}})
	a.Schema("aicli_operations", SchemaDef{Shape: "page", Fields: append(append([]string{}, operationRowKeys...), "item_count")})

	// The built-ins are tested by the library's own tests in the spec repository,
	// which run against a temporary state directory, not a recorded real upstream,
	// so they are experimental (VER-005); the evidence names those tests at the spec
	// tag, a reference that resolves from any tool repository.
	evidence := "simulated:https://github.com/fatecannotbealtered/ai-native-cli-spec/blob/v" + SpecVersion + "/template/go/aicli/builtins_test.go"
	add := func(c *cobra.Command, parent *cobra.Command, m Meta) {
		if childNamed(parent, c.Name()) != nil {
			return
		}
		parent.AddCommand(c)
		m.Maturity = Experimental
		m.Evidence = []string{evidence}
		a.Register(c, m)
		a.reg.builtin[c] = true
	}

	ref := &cobra.Command{Use: "reference [path...]", Short: "Describe the commands: --index for a compact list, --command <path> for one command",
		Long: "Agents use --index (one line per command) or --command <path> (one command with its flags, output fields and errors). " +
			"Without flags it returns the full registry for tooling such as validators and generators; it is large, so agents do not need it.",
		RunE: a.runReference}
	ref.Flags().Bool("index", false, "Return one compact entry per command (or the groups when the list is long)")
	ref.Flags().String("command", "", "Return one command's entry with its output schema and error codes")
	add(ref, a.Root, Meta{Summary: ref.Short, Effects: []Effect{Observe}, Output: "aicli_reference",
		Examples: []string{tool + " reference --index", tool + " reference --command \"<path>\""}})

	ctxCmd := &cobra.Command{Use: "context", Short: "Show the tool, account, host, credentials, state directory and granted scope (local only)",
		RunE: a.runContext}
	add(ctxCmd, a.Root, Meta{Summary: ctxCmd.Short, Effects: []Effect{Observe}, Output: "aicli_context",
		Examples: []string{tool + " context"}})

	doc := &cobra.Command{Use: "doctor", Short: "Check credentials, connectivity, the state store, the scope and the version",
		RunE: a.runDoctor}
	add(doc, a.Root, Meta{Summary: doc.Short, Effects: []Effect{Observe}, Output: "aicli_doctor",
		Examples: []string{tool + " doctor"}})

	cl := &cobra.Command{Use: "changelog", Short: "Show what changed between versions", RunE: a.runChangelog}
	cl.Flags().String("since", "", "Only versions newer than this one")
	add(cl, a.Root, Meta{Summary: cl.Short, Effects: []Effect{Observe}, Output: "aicli_changelog", ErrorCodes: []string{"E_USAGE"},
		Examples: []string{tool + " changelog --since 1.0.0"}})

	if a.Update != nil {
		a.installUpdate(add)
	}

	op := childNamed(a.Root, "operation")
	if op == nil {
		op = &cobra.Command{Use: "operation", Short: "Look up and resolve recorded write operations"}
		a.Root.AddCommand(op)
		a.Group(op, op.Short)
	}
	if a.reg.groups[op] != "" {
		show := &cobra.Command{Use: "show [id]", Short: "Show one operation: its plan, status and receipt", RunE: a.runOperationShow}
		show.Flags().String("id", "", "Operation id (op_... or opb_...), or the write's confirm token")
		add(show, op, Meta{Summary: show.Short, Effects: []Effect{Observe}, Output: "aicli_operation", Positional: "id",
			ErrorCodes: []string{"E_NOT_FOUND", "E_IO"}, Examples: []string{tool + " operation show --id <operation_id>"}})

		list := &cobra.Command{Use: "list", Short: "List dispatched operations, newest first (a batch is one row)", RunE: a.runOperationList}
		list.Flags().String("state", "", "Only operations in this state (all: every record, planned and expired too)")
		list.Flags().Int("limit", 10, "Maximum operations to return (1-200)")
		list.Flags().Int("offset", 0, "Skip this many operations (from next_offset)")
		list.Flags().Bool("items", false, "Also list the items of batches")
		list.Flags().String("batch", "", "Only the items of this batch (opb_...), in input order")
		add(list, op, Meta{Summary: list.Short, Effects: []Effect{Observe}, Output: "aicli_operations",
			ErrorCodes: []string{"E_NOT_FOUND", "E_IO"},
			Params:     map[string]ParamDoc{"state": {Enum: append(append([]string{}, operationStatusValues...), "all")}},
			Examples:   []string{tool + " operation list --state unknown"}})

		res := &cobra.Command{Use: "resolve [id]", Short: "Record what happened to an operation whose outcome is unknown",
			RunE: a.runOperationResolve}
		res.Flags().String("id", "", "Operation id (op_...), or the write's confirm token")
		res.Flags().String("outcome", "", "What the upstream shows: applied, not_applied or partial")
		res.Flags().String("evidence", "", "How the outcome was checked (what was looked at)")
		_ = res.MarkFlagRequired("outcome")
		_ = res.MarkFlagRequired("evidence")
		add(res, op, Meta{Summary: res.Short, Effects: []Effect{Observe}, Output: "aicli_operation", Positional: "id",
			ErrorCodes: []string{"E_NOT_FOUND", "E_CONFLICT", "E_FORBIDDEN", "E_VALIDATION", "E_IO"},
			Params:     map[string]ParamDoc{"outcome": {Enum: append([]string{}, resolutionOutcomes...)}},
			Examples:   []string{tool + " operation resolve --id <operation_id> --outcome applied --evidence \"<what you checked>\""}})
	}
}

// ---------------------------------------------------------------- reference

func (a *App) runReference(cmd *cobra.Command, args []string) error {
	index, _ := cmd.Flags().GetBool("index")
	command, _ := cmd.Flags().GetString("command")
	words := strings.TrimSpace(strings.Join(args, " "))
	if index && cmd.Flags().Changed("command") {
		e := Usage("conflicting_flags", "--command", "Use --index or --command, not both.")
		e.Details["next_action"] = fmt.Sprintf("Run `%s reference --index`, then `%s reference --command \"<path>\"`.", a.toolName(), a.toolName())
		return e
	}
	switch {
	case cmd.Flags().Changed("command") || (!index && words != ""):
		path := strings.TrimSpace(command + " " + words)
		data, e := a.referenceCommand(path)
		if e != nil {
			return e
		}
		return a.OK(cmd, data)
	case index:
		data, e := a.referenceIndex(words)
		if e != nil {
			return e
		}
		return a.OK(cmd, data)
	}
	return a.OK(cmd, a.referenceFull())
}

// ---------------------------------------------------------------- context

func (a *App) runContext(cmd *cobra.Command, _ []string) error {
	return a.OK(cmd, a.contextData(cmd.Context()))
}

// contextFields is every top-level key context returns, which --fields accepts
// (P13): the required keys, profile when settings are declared, and the keys of
// App.ContextExtra.
func (a *App) contextFields() []string {
	fields := append([]string{}, contextRequiredKeys...)
	if a.Settings != nil {
		fields = append(fields, "profile")
	}
	if a.ContextExtra != nil {
		for _, k := range sortedKeys(a.ContextExtra(a.ctx())) {
			if !contains(fields, k) {
				fields = append(fields, k)
			}
		}
	}
	return fields
}

// contextData is the context built-in's data (CLI-035): local reads only.
func (a *App) contextData(ctx context.Context) map[string]any {
	if ctx == nil {
		ctx = context.Background()
	}
	dir, available, problem := a.inspectStateDir()
	var account, host any
	creds := map[string]any{"configured": false, "source": "none"}
	if a.Identity != nil {
		id, err := a.Identity(ctx)
		creds["source"] = "unknown"
		if err == nil {
			if id.Account != "" {
				account = id.Account
			}
			if h := redactHost(id.Host); h != "" {
				host = h
			}
			creds["configured"] = id.Account != ""
			if src := id.Extra["credentials_source"]; src != "" {
				creds["source"] = src
			}
		}
	}
	auth := map[string]any{"status": "missing", "scope_id": nil, "path": nil, "effects": []string{}, "dangerous": false, "expires_at": nil}
	if dir != "" {
		sc := a.loadScope(dir)
		auth = a.authorizationSummary(sc)
		if a.budgetSpent(sc, dir) {
			auth["status"] = "budget_spent"
		}
	}
	// dir is null when no state directory can be resolved, and protection is null
	// whenever the store is unavailable (decision 15); reason says why.
	state := map[string]any{"dir": nil, "available": available, "protection": nil}
	if dir != "" {
		state["dir"] = dir
	}
	if available {
		state["protection"] = protection(dir)
	} else if strings.TrimSpace(problem) != "" {
		state["reason"] = problem
	}
	data := map[string]any{
		"tool": a.toolName(), "version": a.version(), "spec_version": SpecVersion, "account": account, "host": host,
		"credentials": creds, "state": state, "authorization": auth,
	}
	if a.Settings != nil {
		data["profile"] = a.profileContext()
	}
	if a.ContextExtra != nil {
		for k, v := range a.ContextExtra(ctx) {
			if _, taken := data[k]; !taken || k == "credentials" {
				data[k] = v
			}
		}
	}
	return data
}

// budgetSpent reports whether a granted scope has used up limits.max_operations,
// by the durable count in scope-usage.json (read-only).
func (a *App) budgetSpent(sc scopeState, stateDir string) bool {
	if sc.status != "granted" || sc.limit() == 0 {
		return false
	}
	usage, e := readScopeUsage(stateDir)
	return e == nil && usage.Scopes[sc.file.ScopeID] >= sc.limit()
}

// redactHost drops user info, query and fragment from a host URL, with or without
// a scheme (D11): "user:token@host" shows as "host".
func redactHost(h string) string {
	h = strings.TrimSpace(h)
	prefix, rest := "", h
	if i := strings.Index(h, "://"); i >= 0 {
		prefix, rest = h[:i+3], h[i+3:]
	}
	if at := strings.LastIndex(strings.SplitN(rest, "/", 2)[0], "@"); at >= 0 {
		rest = rest[at+1:]
	}
	h = prefix + rest
	if i := strings.IndexAny(h, "?#"); i >= 0 {
		h = h[:i]
	}
	return h
}

// ---------------------------------------------------------------- doctor

func (a *App) runDoctor(cmd *cobra.Command, _ []string) error {
	return a.OK(cmd, map[string]any{"checks": a.doctorChecks(cmd.Context())})
}

// doctorChecks returns the required checks (CLI-036); the tool's DoctorChecks
// replace library checks of the same name and add their own.
func (a *App) doctorChecks(ctx context.Context) []map[string]any {
	if ctx == nil {
		ctx = context.Background()
	}
	checks := []Check{}
	// credentials
	if a.Identity == nil {
		checks = append(checks, Check{"credentials", "warn", "This tool reports no account.", ""})
	} else if id, err := a.Identity(ctx); err != nil {
		checks = append(checks, Check{"credentials", "fail", "The account cannot be resolved: " + truncateRunes(asError(err).Message, 160),
			"Configure the credentials as the tool's README describes."})
	} else if id.Account == "" {
		checks = append(checks, Check{"credentials", "warn", "No account is configured.", "Configure the credentials as the tool's README describes."})
	} else {
		checks = append(checks, Check{"credentials", "pass", "Credentials are configured.", ""})
	}
	checks = append(checks, Check{"connectivity", "warn", "This tool has no connectivity check.", ""})
	// state store: the same protection value context reports (decision 30)
	dir, available, problem := a.inspectStateDir()
	switch {
	case !available:
		fix := fmt.Sprintf("Set %s_STATE_DIR to a private directory.", a.envPrefix())
		if dir != "" {
			fix = fmt.Sprintf("Fix %s or set %s_STATE_DIR to a private directory.", dir, a.envPrefix())
		}
		checks = append(checks, Check{"state_store", "fail", "The state store is unusable: " + strings.TrimSuffix(problem, ".") + ". Writes are refused.", fix})
	case protection(dir) == "windows-unverified":
		checks = append(checks, Check{"state_store", "warn", "The state directory is outside your user profile, so its protection depends on that directory's ACL (windows-unverified).",
			fmt.Sprintf("Keep %s_STATE_DIR inside your profile, or give the directory an owner-only ACL.", a.envPrefix())})
	default:
		checks = append(checks, Check{"state_store", "pass", "The state store is usable (" + protection(dir) + ").", ""})
	}
	// authorization
	if dir != "" {
		sc := a.loadScope(dir)
		switch sc.status {
		case "granted":
			exp := "no expiry"
			if sc.file.ExpiresAt != nil {
				exp = "expires " + *sc.file.ExpiresAt
			}
			checks = append(checks, Check{"authorization", "pass",
				fmt.Sprintf("Scope %s grants %s (%s).", sc.file.ScopeID, strings.Join(sc.file.Effects, ", "), exp), ""})
		case "missing":
			checks = append(checks, Check{"authorization", "warn", "No scope is granted, so every write is refused.",
				fmt.Sprintf("A person grants writes by creating %s; see the tool's SECURITY.md.", sc.path)})
		case "invalid":
			checks = append(checks, Check{"authorization", "fail", "The scope file is invalid: " + sc.reason + ".",
				fmt.Sprintf("Ask the user to fix %s.", sc.path)})
		default:
			checks = append(checks, Check{"authorization", "warn", "The scope is " + sc.status + ", so every write is refused.",
				fmt.Sprintf("Ask the user to renew %s.", sc.path)})
		}
	} else {
		checks = append(checks, Check{"authorization", "fail", "The state directory is unknown, so no scope can be read.",
			fmt.Sprintf("Set %s_STATE_DIR to a private directory.", a.envPrefix())})
	}
	if a.Settings != nil {
		checks = append(checks, a.settingsCheck())
	}
	// doctor may contact the release source, briefly, to refresh the notice cache (CLI-071).
	a.refreshUpdateNotice(ctx)
	checks = append(checks, a.versionCheck())
	if a.DoctorChecks != nil {
		for _, tc := range a.DoctorChecks(ctx) {
			replaced := false
			for i := range checks {
				if checks[i].Check == tc.Check {
					checks[i] = tc
					replaced = true
				}
			}
			if !replaced {
				checks = append(checks, tc)
			}
		}
	}
	out := make([]map[string]any, 0, len(checks))
	for _, c := range checks {
		status := c.Status
		if !contains(doctorStatusValues, status) {
			status = "fail"
		}
		// A passing check has no fix; a failing one always has one (decision 15).
		var fix any
		switch {
		case status == "pass":
		case strings.TrimSpace(c.Fix) != "":
			fix = c.Fix
		case status == "fail":
			fix = "Fix what the message names, then run doctor again."
		}
		message := c.Message
		if strings.TrimSpace(message) == "" {
			message = "The " + c.Check + " check reported " + status + "."
		}
		out = append(out, map[string]any{"check": c.Check, "status": status, "message": message, "fix": fix})
	}
	return out
}

// defaultSkillDirs are the standard agent skill directories (decision 22).
func defaultSkillDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")}
}

var (
	skillFrontmatter = regexp.MustCompile(`^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)`)
	skillVersion     = regexp.MustCompile(`(?m)^[ \t]+version:[ \t]*"?([^"\s#]+)"?`)
	skillMinVersion  = regexp.MustCompile(`(?m)^[ \t]+min_version:[ \t]*"?([^"\s#]+)"?`)
)

// skillVersions reads metadata.version and metadata.min_version from a SKILL.md
// frontmatter (SKILL-SPEC SKL-001: tool fields live under metadata).
func skillVersions(text string) (version, minVersion string) {
	m := skillFrontmatter.FindStringSubmatch(text)
	if m == nil {
		return "", ""
	}
	if v := skillVersion.FindStringSubmatch(m[1]); v != nil {
		version = v[1]
	}
	if v := skillMinVersion.FindStringSubmatch(m[1]); v != nil {
		minVersion = v[1]
	}
	return version, minVersion
}

// versionCheck is doctor's version check (CLI-036, CLI-080, decision 22): the tool
// against the Skill installed for it, read from <dir>/<Name>/SKILL.md in each of
// App.SkillDirs. No Skill is a warning; a Skill whose metadata.min_version is newer
// than the tool fails (update the tool); a Skill older than the tool is a warning
// (run update to sync it). With several installed copies the worst result wins.
// App.MinSkillVersion is only a fallback when no Skill is found.
func (a *App) versionCheck() Check {
	tool := a.toolName()
	dirs := a.SkillDirs
	if dirs == nil {
		dirs = defaultSkillDirs()
	}
	cur, curErr := ParseVersion(a.version())
	// A development build (P14) reports its derived version but is not compared:
	// update, the usual fix, refuses such a build.
	curOK := curErr == nil && !a.devBuild()
	worst := Check{}
	rank := map[string]int{"pass": 0, "warn": 1, "fail": 2}
	for _, d := range dirs {
		path := filepath.Join(d, tool, "SKILL.md")
		b, err := readSmallFile(path, 256<<10)
		if err != nil {
			continue
		}
		v, mv := skillVersions(string(b))
		c := a.skillCheck(tool, cur, curOK, path, v, mv)
		if worst.Check == "" || rank[c.Status] > rank[worst.Status] {
			worst = c
		}
	}
	if worst.Check != "" {
		return worst
	}
	if a.MinSkillVersion != "" && curOK {
		if need, err := ParseVersion(a.MinSkillVersion); err == nil && CompareVersions(cur, need) < 0 {
			return Check{"version", "fail",
				fmt.Sprintf("The Skill needs %s %s or newer; %s is installed.", tool, a.MinSkillVersion, a.version()),
				fmt.Sprintf("Run `%s update`.", tool)}
		}
	}
	// update syncs installed Skills only, so it is not the fix for a missing one (F10).
	return Check{"version", "warn", fmt.Sprintf("The %s Skill is not installed, so agents work without it.", tool),
		"Install the Skill with the install command in the tool's README; update does not install a missing Skill."}
}

func (a *App) skillCheck(tool string, cur Version, curOK bool, path, v, mv string) Check {
	sync := fmt.Sprintf("Run `%s update` to sync the Skill from the installed package.", tool)
	if !curOK {
		return Check{"version", "warn", fmt.Sprintf("%s %s is a development build; the Skill at %s cannot be compared.", tool, a.version(), path), ""}
	}
	var minV, skillV *Version
	if mv != "" {
		p, err := ParseVersion(mv)
		if err != nil {
			return Check{"version", "warn", fmt.Sprintf("The Skill at %s has a metadata.min_version that is not a semantic version.", path), sync}
		}
		minV = &p
	}
	if v != "" {
		p, err := ParseVersion(v)
		if err != nil {
			return Check{"version", "warn", fmt.Sprintf("The Skill at %s has a metadata.version that is not a semantic version.", path), sync}
		}
		skillV = &p
	}
	switch {
	case minV != nil && CompareVersions(cur, *minV) < 0:
		return Check{"version", "fail", fmt.Sprintf("The Skill at %s needs %s %s or newer; %s is installed.", path, tool, mv, a.version()),
			fmt.Sprintf("Run `%s update`.", tool)}
	case skillV == nil || CompareVersions(*skillV, cur) < 0:
		return Check{"version", "warn", fmt.Sprintf("The Skill at %s is older than %s %s.", path, tool, a.version()), sync}
	}
	minText := mv
	if minText == "" {
		minText = "unset"
	}
	return Check{"version", "pass", fmt.Sprintf("%s %s matches the Skill at %s (min_version %s).", tool, a.version(), path, minText), ""}
}

// readSmallFile reads a regular file of at most limit bytes.
func readSmallFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// ---------------------------------------------------------------- changelog

func (a *App) runChangelog(cmd *cobra.Command, _ []string) error {
	since, _ := cmd.Flags().GetString("since")
	var sinceV *Version
	if cmd.Flags().Changed("since") {
		v, err := ParseVersion(since)
		if err != nil {
			e := Usage("invalid_value", "--since", "--since needs a version like 1.2.3.")
			e.Details["expected"] = "semantic version, e.g. 1.2.3"
			return e
		}
		sinceV = &v
	}
	entries := []map[string]any{}
	for _, e := range parseChangelog(a.Changelog) {
		if sinceV != nil {
			v, err := ParseVersion(e.version)
			if err != nil || CompareVersions(v, *sinceV) <= 0 {
				continue
			}
		}
		changes := map[string]any{}
		for _, cat := range changelogCategories {
			if items := e.changes[cat]; len(items) > 0 {
				changes[cat] = items
			}
		}
		var date any
		if e.date != "" {
			date = e.date
		}
		entries = append(entries, map[string]any{"version": e.version, "date": date, "changes": changes})
	}
	data := map[string]any{"current_version": a.version(), "entries": entries, "count": len(entries)}
	if sinceV != nil {
		data["since"] = since
	} else {
		data = fitChangelog(data, entries)
	}
	return a.OK(cmd, data)
}

// changelogTruncated is the truncated_reason of a changelog cut to the budget (P3).
const changelogTruncated = "Only the newest entries fit the size budget; run changelog --since <version> for the changes after a version."

// fitChangelog keeps changelog data without --since within the success budget
// (CLI-042, P3): the newest entries that fit (at least one), with truncated,
// truncated_reason and omitted (the number left out) when some are left out. The
// envelope is measured compact with empty meta, keeping 500 characters for meta.
func fitChangelog(data map[string]any, entries []map[string]any) map[string]any {
	cut := func(n int) map[string]any {
		out := cloneMap(data)
		out["entries"], out["count"] = entries[:n], n
		if n < len(entries) {
			out["truncated"], out["truncated_reason"], out["omitted"] = true, changelogTruncated, len(entries)-n
		}
		return out
	}
	fits := func(n int) bool { return successSize(cut(n), metaObject{}) <= successBudgetChars-500 }
	if len(entries) <= 1 || fits(len(entries)) {
		return data
	}
	keep, over := 1, len(entries)
	for keep+1 < over {
		if mid := (keep + over) / 2; fits(mid) {
			keep = mid
		} else {
			over = mid
		}
	}
	return cut(keep)
}

type changelogEntry struct {
	version, date string
	changes       map[string][]string
}

var (
	changelogVersion  = regexp.MustCompile(`^##\s+\[?v?([0-9][0-9A-Za-z.+-]*)\]?(?:\s*[-–—]\s*([0-9]{4}-[0-9]{2}-[0-9]{2}))?`)
	changelogCategory = regexp.MustCompile(`^###\s+(\w+)`)
	changelogBullet   = regexp.MustCompile(`^\s*[-*+]\s+(.*\S)`)
	changelogLinkRef  = regexp.MustCompile(`^[ \t]*\[[^\]]+\]:[ \t]*\S`)
)

// parseChangelog reads a Keep a Changelog document (decision 26): [Unreleased] is
// skipped, unknown categories count as "changed", HTML comments are skipped, and a
// continuation line joins the bullet above it only until a blank line or a
// link-reference definition ("[1.0.0]: https://...").
func parseChangelog(text string) []changelogEntry {
	var out []changelogEntry
	var cur *changelogEntry
	category := ""
	continuing, inComment := false, false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		hadText := strings.TrimSpace(line) != ""
		if inComment {
			end := strings.Index(line, "-->")
			if end < 0 {
				continue
			}
			line = line[end+3:]
			inComment = false
		}
		for {
			start := strings.Index(line, "<!--")
			if start < 0 {
				break
			}
			end := strings.Index(line[start+4:], "-->")
			if end < 0 {
				line = line[:start]
				inComment = true
				break
			}
			line = line[:start] + line[start+4+end+3:]
		}
		if hadText && strings.TrimSpace(line) == "" {
			continue // a comment-only line is not a blank line
		}
		if strings.HasPrefix(line, "## ") {
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			if m := changelogVersion.FindStringSubmatch(line); m != nil {
				cur = &changelogEntry{version: m[1], date: m[2], changes: map[string][]string{}}
			}
			category, continuing = "", false
			continue
		}
		if cur == nil {
			continue
		}
		if m := changelogCategory.FindStringSubmatch(line); m != nil {
			category = strings.ToLower(m[1])
			if !contains(changelogCategories, category) {
				category = "changed"
			}
			continuing = false
			continue
		}
		if category == "" {
			continue
		}
		if m := changelogBullet.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, "    ") {
			cur.changes[category] = append(cur.changes[category], m[1])
			continuing = true
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || changelogLinkRef.MatchString(line) {
			continuing = false
			continue
		}
		if items := cur.changes[category]; continuing && len(items) > 0 {
			items[len(items)-1] += " " + strings.TrimLeft(t, "-*+ ")
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// ---------------------------------------------------------------- operation

// operationIDArg reads --id (or the positional) of operation show and resolve and
// returns the ids to look up. Besides an operation id it accepts the same id in
// upper case and the write's confirm token, whose id part names the operation: the
// agent often holds the token it just sent. This only finds a record; show is
// read-only and resolve still checks the scope, so it grants nothing.
func (a *App) operationIDArg(cmd *cobra.Command) ([]string, *Error) {
	raw, _ := cmd.Flags().GetString("id")
	id := strings.ToLower(strings.Trim(strings.TrimSpace(raw), "<>\"'`"))
	find := fmt.Sprintf("Run `%s operation list --limit 5` to find it.", a.toolName())
	if id == "" {
		e := Usage("missing_parameter", "--id", "Give the operation id.")
		e.Details["usage"] = a.usageLine(cmd)
		e.Details["next_action"] = "Give the operation_id from the write's output. " + find
		return nil, e
	}
	if validOperationID(id) {
		return []string{id}, nil
	}
	if part, _, ok := parseToken(id); ok {
		return []string{"op_" + part, "opb_" + part}, nil
	}
	e := Usage("invalid_value", "--id", "The operation id has the wrong form.")
	e.Details["expected"] = "op_ or opb_ followed by 26 characters a-z and 2-7, or the write's confirm token"
	e.Details["next_action"] = "Copy operation_id from the write's output. " + find
	return nil, e
}

// readOperation finds the first of ids in the journal.
func readOperation(j *journal, ids []string) (string, *record, bool, error) {
	for _, id := range ids {
		r, exists, err := j.read(id)
		if exists {
			return id, r, true, err
		}
	}
	return ids[0], nil, false, nil
}

// readJournal opens the journal without creating anything (read-only built-ins).
func (a *App) readJournal() (*journal, string, *Error) {
	dir, e := a.stateDirPath()
	if e != nil {
		return nil, "", e
	}
	j, e := a.openJournal(dir, false)
	return j, dir, e
}

func (a *App) runOperationShow(cmd *cobra.Command, _ []string) error {
	ids, e := a.operationIDArg(cmd)
	if e != nil {
		return e
	}
	j, _, e := a.readJournal()
	if e != nil {
		return e
	}
	id, r, exists, err := readOperation(j, ids)
	if !exists {
		return NotFound("No operation with that id is recorded by this tool.",
			fmt.Sprintf("Run `%s operation list --limit 5` to see recorded operations.", a.toolName()))
	}
	if err != nil {
		if errors.Is(err, errCorruptRecord) {
			return a.OK(cmd, a.corruptView(id, j.modTime(id)))
		}
		return ioError("The operation record cannot be read.", fmt.Sprintf("Check %s.", j.dir), err)
	}
	view, notice := fitOperationBudget(a.operationView(j, r, true), a.toolName())
	if notice != nil {
		a.Notice(*notice)
	}
	return a.OK(cmd, view)
}

// fitOperationBudget keeps a batch operation's view within the success budget
// (CLI-042) by the contract's operation.shortening rule. The preview first keeps its
// first targets and items, one to one, with omitted (preview.shortening). If the view
// is still too large, items lists the items that need attention first (status not
// in done_status), then the done ones, each group in input order, cut to fit, and
// items_omitted counts the rest; the returned notice then names operation list
// --batch, which pages through every item. The fit test keeps room for meta, the
// notice included. An item's receipt is in its own operation show.
func fitOperationBudget(v map[string]any, tool string) (map[string]any, *Notice) {
	items, isBatch := v["items"].([]any)
	if !isBatch {
		return v, nil
	}
	// meta is added later: keep room for it.
	fits := func(x map[string]any, n *Notice) bool {
		meta := metaObject{}
		if n != nil {
			meta.Notices = []Notice{*n}
		}
		return successSize(x, meta) <= successBudgetChars-500
	}
	if fits(v, nil) {
		return v, nil
	}
	out := cloneMap(v)
	if p, ok := v["preview"].(map[string]any); ok && hasKey(p, "items") {
		for _, n := range []int{200, 100, 50, 20, 10, 5, 1} {
			out["preview"] = shortenBatchPreview(p, n)
			if fits(out, nil) {
				return out, nil
			}
		}
	}
	ordered := attentionFirst(items)
	batchID, _ := v["operation_id"].(string)
	var notice *Notice
	for _, n := range []int{200, 100, 50, 20, 10, 5, 1} {
		if n >= len(ordered) {
			continue
		}
		out["items"] = append([]any{}, ordered[:n]...)
		out["items_omitted"] = len(ordered) - n
		nt := itemsOmittedNotice(tool, batchID, n, len(ordered))
		notice = &nt
		if fits(out, notice) {
			break
		}
	}
	return out, notice
}

// attentionFirst orders batch items for a shortened view (operation.shortening): the
// items whose status is not in done_status first, then the done ones, each group in
// input order.
func attentionFirst(items []any) []any {
	out := make([]any, 0, len(items))
	var done []any
	for _, it := range items {
		m, _ := it.(map[string]any)
		if s, _ := m["status"].(string); contains(operationDoneStatusValues, s) {
			done = append(done, it)
		} else {
			out = append(out, it)
		}
	}
	return append(out, done...)
}

// itemsOmittedNotice is the meta notice of a shortened batch view: how many items it
// lists and the command that pages through all of them.
func itemsOmittedNotice(tool, batchID string, listed, total int) Notice {
	command := fmt.Sprintf("%s operation list --batch %s", tool, batchID)
	return Notice{Type: "items_omitted", Severity: "info", Command: command,
		Message: fmt.Sprintf("operation show lists %d of the batch's %d items, those that need attention first; run `%s` to page through all of them.",
			listed, total, command)}
}

// corruptView is the view of a record file that cannot be trusted (CLI-058): its
// outcome is unknown and it blocks every target until it is resolved. Its own fields
// are unreadable, so its times are the file's last write, its effects are every
// write effect of the tool (it could be any of its writes) and the rest is null.
func (a *App) corruptView(id string, mod time.Time) map[string]any {
	v := map[string]any{}
	for _, k := range operationRecordKeys {
		v[k] = nil
	}
	at := timeString(mod)
	v["operation_id"] = id
	v["command"] = "unknown"
	v["status"] = "unknown"
	v["effects"] = a.anyWriteEffects()
	v["created_at"] = at
	v["dispatched_at"] = at
	return v
}

// anyWriteEffects is every write effect the tool declares (change when it declares
// none), for records whose own effects are unreadable.
func (a *App) anyWriteEffects() []string {
	effects := a.toolWriteNeed().effects
	if len(effects) == 0 {
		effects = []string{string(Change)}
	}
	return effects
}

// operationView is the operation show view (BLUEPRINT 4.7).
func (a *App) operationView(j *journal, r *record, full bool) map[string]any {
	now := a.now()
	status := effectiveStatus(r, now)
	if r.Resolution != nil {
		status = "resolved"
	}
	v := map[string]any{
		"operation_id": r.OperationID, "command": r.Command, "status": status, "target": nil, "effects": nonNil(r.Effects),
		"scope_id": nil, "created_at": r.CreatedAt, "expires_at": nil, "dispatched_at": nil, "finished_at": nil,
		"preview": nil, "receipt": nil, "resolution": nil, "batch_id": nil, "items": nil,
	}
	if r.ExpiresAt != "" {
		v["expires_at"] = r.ExpiresAt
	}
	if r.Target != nil {
		v["target"] = targetObject(Target{Key: r.Target.Key, Kind: r.Target.Kind, Label: r.Target.Label})
	}
	if r.ScopeID != nil {
		v["scope_id"] = *r.ScopeID
	}
	if r.DispatchedAt != nil {
		v["dispatched_at"] = *r.DispatchedAt
	}
	if r.FinishedAt != nil {
		v["finished_at"] = *r.FinishedAt
	}
	if r.BatchID != nil {
		v["batch_id"] = *r.BatchID
	}
	if r.Resolution != nil {
		v["resolution"] = map[string]any{"outcome": r.Resolution.Outcome, "evidence": r.Resolution.Evidence, "resolved_at": r.Resolution.ResolvedAt}
	}
	if full {
		if rawPresent(r.Preview) {
			v["preview"] = decodeGeneric(r.Preview)
		}
		if rawPresent(r.Receipt) {
			v["receipt"] = decodeGeneric(r.Receipt)
		}
		// A batch record without item ids replaced one that could not be read
		// (operation resolve): its items are still unknown, so items stays null.
		if r.Kind == "batch" && len(r.Items) > 0 {
			items := []any{}
			for _, id := range r.Items {
				it, exists, err := j.read(id)
				entry := map[string]any{"operation_id": id, "target": nil, "status": "unknown"}
				if exists && err == nil {
					st := effectiveStatus(it, now)
					if it.Resolution != nil {
						st = "resolved"
					}
					entry["status"] = st
					if it.Target != nil {
						entry["target"] = targetObject(Target{Key: it.Target.Key, Kind: it.Target.Kind, Label: it.Target.Label})
					}
				}
				items = append(items, entry)
			}
			v["items"] = items
		}
	}
	return v
}

// operationRowKeys are the keys of an operation list row (a batch row adds item_count).
var operationRowKeys = []string{"operation_id", "command", "status", "target", "effects", "created_at", "finished_at", "batch_id"}

// runOperationList lists recorded operations, newest first (CLI-061). By default it
// shows what an agent looks for after a write: dispatched operations, a batch as one
// row with its item count. --state <status> (or all) also shows planned and expired
// records and the items of batches; --items lists batch items without a filter;
// --batch lists the items of one batch (listBatchItems).
func (a *App) runOperationList(cmd *cobra.Command, _ []string) error {
	status, _ := cmd.Flags().GetString("state")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	withItems, _ := cmd.Flags().GetBool("items")
	if limit < 1 || limit > 200 {
		e := Usage("invalid_value", "--limit", "--limit must be between 1 and 200.")
		e.Details["expected"] = "1-200"
		e.Details["next_action"] = "Give --limit a value from 1 to 200 and run the command again."
		return e
	}
	if offset < 0 {
		e := Usage("invalid_value", "--offset", "--offset must not be negative.")
		e.Details["expected"] = "0 or more"
		e.Details["next_action"] = "Give --offset the next_offset of the previous page, or leave it out."
		return e
	}
	if cmd.Flags().Changed("batch") {
		batch, _ := cmd.Flags().GetString("batch")
		return a.listBatchItems(cmd, batch, status, limit, offset)
	}
	j, dir, e := a.readJournal()
	if e != nil {
		return e
	}
	all, e := j.list()
	if e != nil {
		return e
	}
	if status != "" {
		withItems = true
	}
	type row struct {
		created string
		view    map[string]any
	}
	var rows []row
	for _, l := range all {
		var v map[string]any
		created := ""
		itemCount := -1
		if l.corrupt {
			v = a.corruptView(l.id, l.modTime)
			created, _ = v["created_at"].(string)
		} else {
			if l.rec.Kind == "item" && !withItems {
				continue
			}
			v = a.operationView(j, l.rec, false)
			created = l.rec.CreatedAt
			// A batch whose item list was lost with its unreadable record has no count.
			if l.rec.Kind == "batch" && len(l.rec.Items) > 0 {
				itemCount = len(l.rec.Items)
			}
		}
		switch st := v["status"]; {
		case status == "all":
		case status != "":
			if st != status {
				continue
			}
		case st == "planned" || st == "expired":
			continue
		}
		summary := map[string]any{}
		for _, k := range operationRowKeys {
			summary[k] = v[k]
		}
		if itemCount >= 0 {
			summary["item_count"] = itemCount
		}
		rows = append(rows, row{created, summary})
	}
	sort.SliceStable(rows, func(i, k int) bool {
		if rows[i].created != rows[k].created {
			return rows[i].created > rows[k].created
		}
		return fmt.Sprint(rows[i].view["operation_id"]) < fmt.Sprint(rows[k].view["operation_id"])
	})
	views := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		views = append(views, r.view)
	}
	scope := map[string]any{"journal": filepath.Join(dir, "operations"),
		"state":       "dispatched operations (planned and expired are left out; --state all shows them)",
		"batch_items": "counted in their batch's row (--items lists them)"}
	if status != "" {
		scope["state"] = status
	}
	if withItems {
		scope["batch_items"] = "listed"
	}
	return a.operationPage(cmd, views, offset, limit, scope, "created_at_desc")
}

// operationPage reports rows[offset:offset+limit] as a page of operation list
// (CLI-041) within the success budget (CLI-042): when the rows --limit asks for do
// not fit one response, the page keeps the first rows that fit (at least one), is
// truncated with the reason, and next_offset continues after them. total counts
// every row, everything scope covers.
func (a *App) operationPage(cmd *cobra.Command, rows []map[string]any, offset, limit int, scope map[string]any, order string) error {
	total := len(rows)
	start := min(offset, total)
	end := min(start+limit, total)
	reason := fmt.Sprintf("The page holds fewer rows than --limit %d, as many as fit one response (%d characters, CLI-042); continue from next_offset.",
		limit, successBudgetChars)
	// meta is added later: keep room for it.
	fits := func(n int) bool {
		data := map[string]any{"items": rows[start : start+n], "count": n, "has_more": true, "next_offset": start + n,
			"truncated": true, "truncated_reason": reason, "scope": scope, "sort": order, "total": total}
		return successSize(data, metaObject{}) <= successBudgetChars-500
	}
	truncated := false
	if end-start > 1 && !fits(end-start) {
		keep, over := 1, end-start
		for keep+1 < over {
			if mid := (keep + over) / 2; fits(mid) {
				keep = mid
			} else {
				over = mid
			}
		}
		end, truncated = start+keep, true
	}
	items := make([]map[string]any, 0, end-start)
	items = append(items, rows[start:end]...)
	p := Page{Items: items, Count: len(items), HasMore: end < total, Scope: scope, Sort: order, Total: &total}
	if p.HasMore {
		next := end
		p.NextOffset = &next
	}
	if truncated {
		p.Truncated, p.TruncatedReason = true, reason
	}
	return a.List(cmd, p)
}

// listBatchItems is operation list --batch (operation.shortening): one row per item of
// the batch record, in input order, with the keys of a list row. An item whose record
// is missing or cannot be read is a row with status unknown and a null target, as in
// operation show's items; its other keys are the batch's. --state filters the rows;
// without it (or with all) every item is listed, planned and expired too. A batch
// record that could not be read, or the record operation resolve put in its place,
// names no items: that is E_IO, not an empty list.
func (a *App) listBatchItems(cmd *cobra.Command, raw, status string, limit, offset int) error {
	id := strings.ToLower(strings.Trim(strings.TrimSpace(raw), "<>\"'`"))
	if !strings.HasPrefix(id, "opb_") || !validOperationID(id) {
		e := Usage("invalid_value", "--batch", "The batch id has the wrong form.")
		e.Details["expected"] = "a batch id: opb_ followed by 26 characters a-z and 2-7"
		e.Details["next_action"] = fmt.Sprintf("Copy the batch's operation_id (opb_...) from the write's output. Run `%s operation list --limit 5` to find it.", a.toolName())
		return e
	}
	j, dir, e := a.readJournal()
	if e != nil {
		return e
	}
	r, exists, err := j.read(id)
	if !exists {
		return NotFound("No batch with that id is recorded by this tool.",
			fmt.Sprintf("Run `%s operation list --limit 5` to see recorded operations.", a.toolName()))
	}
	if errors.Is(err, errCorruptRecord) || (err == nil && (r.Kind != "batch" || len(r.Items) == 0)) {
		return ioError("The batch's items are unknown: its record could not be read.",
			fmt.Sprintf("Run `%s operation show --id %s` to see what is recorded about it.", a.toolName(), id), err)
	}
	if err != nil {
		return ioError("The operation record cannot be read.", fmt.Sprintf("Check %s.", j.dir), err)
	}
	var rows []map[string]any
	for _, itemID := range r.Items {
		row := map[string]any{"operation_id": itemID, "command": r.Command, "status": "unknown", "target": nil,
			"effects": nonNil(r.Effects), "created_at": r.CreatedAt, "finished_at": nil, "batch_id": id}
		if it, exists, err := j.read(itemID); exists && err == nil {
			v := a.operationView(j, it, false)
			for _, k := range operationRowKeys {
				row[k] = v[k]
			}
		}
		if status != "" && status != "all" && row["status"] != status {
			continue
		}
		rows = append(rows, row)
	}
	scope := map[string]any{"journal": filepath.Join(dir, "operations"), "batch": id, "state": "every item of the batch"}
	if status != "" {
		scope["state"] = status
	}
	return a.operationPage(cmd, rows, offset, limit, scope, "input_order")
}

func (a *App) runOperationResolve(cmd *cobra.Command, _ []string) error {
	ids, e := a.operationIDArg(cmd)
	if e != nil {
		return e
	}
	outcome, _ := cmd.Flags().GetString("outcome")
	evidence, _ := cmd.Flags().GetString("evidence")
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return Usage("missing_parameter", "--evidence", "Say how the outcome was checked.")
	}
	if utf8.RuneCountInString(evidence) > 2000 {
		return Usage("input_too_large", "--evidence", "--evidence is longer than 2000 characters.")
	}
	state, e := a.ensureStateDir()
	if e != nil {
		return e
	}
	j, e := a.openJournal(state, false)
	if e != nil {
		return e
	}
	id, r, exists, err := readOperation(j, ids)
	if !exists {
		return NotFound("No operation with that id is recorded by this tool.", fmt.Sprintf("Run `%s operation list --state unknown`.", a.toolName()))
	}
	corrupt := errors.Is(err, errCorruptRecord)
	mod := j.modTime(id)
	if err != nil && !corrupt {
		return ioError("The operation record cannot be read.", fmt.Sprintf("Check %s.", j.dir), err)
	}
	now := a.now()
	if !corrupt {
		if r.Kind == "batch" {
			e := Validation("--id", "op_...", "A batch is resolved through its items; resolve each item whose outcome is unknown.")
			e.Details["next_action"] = fmt.Sprintf("Run `%s operation list --batch %s --state unknown` to see the items to resolve.", a.toolName(), id)
			return e
		}
		st := effectiveStatus(r, now)
		if st == "dispatching" {
			ce := conflictError("lock_held", "The operation is still running under its lease; it cannot be resolved yet.",
				fmt.Sprintf("Wait until its lease ends (%s), then run the command again.", deref(r.LeaseUntil)))
			ce.Details["active_operation"] = id
			return ce
		}
		if !unresolved(st) || r.Resolution != nil {
			e := Validation("--id", "an operation whose status is unknown", "Only operations whose outcome is unknown can be resolved.")
			e.Details["next_action"] = fmt.Sprintf("Run `%s operation show --id %s` to see its status.", a.toolName(), id)
			return e
		}
	}
	// Resolving needs a scope covering the operation's effects (CLI-061). An
	// unreadable record could be any of this tool's writes, so it needs them all.
	need := a.toolWriteNeed()
	if !corrupt {
		need = scopeNeed{effects: r.Effects, dangerous: r.Dangerous}
		if r.Target != nil {
			need.targets = []string{r.Target.Key}
		}
	}
	if e := a.checkScope(a.loadScope(state), need, ""); e != nil {
		return e
	}
	unlock, e := j.lock(id)
	if e != nil {
		return e
	}
	defer unlock()
	cur, _, err := j.read(id)
	if err == nil && cur != nil {
		if !unresolved(effectiveStatus(cur, a.now())) || cur.Resolution != nil {
			return Validation("--id", "an operation whose status is unknown", "The operation finished or was resolved meanwhile; nothing was changed.")
		}
		r = cur
	} else {
		// The unreadable record is replaced by one that carries the resolution; its
		// own fields stay unknown (see corruptView).
		at := timeString(mod)
		r = &record{V: 2, OperationID: id, Kind: "single", Command: "unknown", Target: &targetJSON{Key: "unknown"},
			Effects: a.anyWriteEffects(), CreatedAt: at, DispatchedAt: &at}
		if strings.HasPrefix(id, "opb_") {
			r.Kind = "batch"
			r.Target = nil
		}
	}
	r.Status = "resolved"
	r.LeaseUntil = nil
	r.Resolution = &resolution{Outcome: outcome, Evidence: evidence, ResolvedAt: timeString(now)}
	if e := j.write(r); e != nil {
		return e
	}
	return a.OK(cmd, a.operationView(j, r, true))
}

// toolWriteNeed is the union of every registered command's write effects, and
// dangerous when any command is.
func (a *App) toolWriteNeed() scopeNeed {
	set := map[string]bool{}
	dangerous := false
	for _, m := range a.reg.meta {
		if !m.isWrite() {
			continue
		}
		for _, e := range m.effectStrings() {
			if e != string(Observe) {
				set[e] = true
			}
		}
		dangerous = dangerous || m.dangerous()
	}
	return scopeNeed{effects: sortedKeys(set), dangerous: dangerous}
}

// rawPresent reports whether a stored JSON value is present and not null.
func rawPresent(raw []byte) bool {
	t := strings.TrimSpace(string(raw))
	return t != "" && t != "null"
}

func deref(s *string) string {
	if s == nil {
		return "unknown"
	}
	return *s
}
