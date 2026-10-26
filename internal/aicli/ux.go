// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Agent UX: every usage mistake is a structured E_USAGE error that names the problem
// and the fix (CLI-012), suggestions search the whole tree (CLI-022), and unexpected
// positional arguments are errors, never ignored (CLI-023). When the library can
// build the corrected command line, next_action is that command.

// defaultVerbSynonyms are the verbs agents guess first for the contract's standard
// verbs (CLI-021, CLI-022, the fleet guess list). They are used for a word that
// App.VerbSynonyms does not list, and only to suggest: to accept a guess, list it in
// Meta.Aliases.
var defaultVerbSynonyms = map[string][]string{
	"ls": {"list"}, "dir": {"list"}, "find": {"search", "list"}, "search": {"list"}, "list": {"search"},
	"rm": {"delete"}, "del": {"delete"}, "remove": {"delete"}, "delete": {"remove", "rm"},
	"show": {"get"}, "view": {"get"}, "cat": {"get"}, "read": {"get"}, "get": {"show", "view"},
	"add": {"create"}, "new": {"create"}, "make": {"create"}, "create": {"add", "new"},
	"edit": {"update"}, "set": {"update"}, "modify": {"update"}, "update": {"edit", "set"},
}

// verbSynonyms returns the tool's synonyms for word, else the library's defaults.
func (a *App) verbSynonyms(word string) []string {
	if syn, ok := a.VerbSynonyms[word]; ok {
		return syn
	}
	return defaultVerbSynonyms[word]
}

// defaultFlagSynonyms are the flag names agents guess for the contract's standard
// flags (CLI-028). Like the verb synonyms they only suggest (did_you_mean), only
// when the command has the target flag, and only for a name App.FlagSynonyms does
// not list; to accept a guess, list it in App.FlagAliases.
var defaultFlagSynonyms = map[string][]string{
	"per-page": {"limit"}, "page-size": {"limit"}, "max-results": {"limit"}, "max": {"limit"},
	"page-token": {"cursor"}, "next-token": {"cursor"}, "next": {"cursor"},
	"search": {"query"}, "keyword": {"query"}, "keywords": {"query"}, "term": {"query"},
	"order-by": {"sort"}, "sort-by": {"sort"},
}

// flagSynonyms returns the tool's synonyms for a flag name, else the library's defaults.
func (a *App) flagSynonyms(name string) []string {
	if syn, ok := a.FlagSynonyms[name]; ok {
		return syn
	}
	return defaultFlagSynonyms[name]
}

// groupRunE answers a group (or the root) called without a known subcommand: help
// when nothing follows, E_USAGE unknown_command otherwise.
func (a *App) groupRunE(c *cobra.Command, args []string) error {
	if c == a.Root {
		if v, err := c.Flags().GetBool("version"); err == nil && v {
			if len(args) > 0 {
				return a.unexpectedArg(c, args, 0)
			}
			return a.OK(c, map[string]any{"tool": a.toolName(), "version": a.version(), "spec_version": SpecVersion,
				"schema_version": SchemaVersion})
		}
	}
	if len(args) == 0 {
		return c.Help()
	}
	return a.unknownCommand(c, args[0])
}

// siblingMatches returns the children of group whose name or alias is a synonym of
// word or a close spelling, as runnable paths (with the tool name), closest first.
func (a *App) siblingMatches(group *cobra.Command, word string) []string {
	type cand struct {
		path string
		dist int
	}
	syn := a.verbSynonyms(word)
	var cands []cand
	for _, sub := range group.Commands() {
		if isCobraInternal(sub) || sub.Hidden {
			continue
		}
		best := -1
		for _, name := range append([]string{sub.Name()}, sub.Aliases...) {
			d := editDistance(word, name)
			switch {
			case contains(syn, name):
				d = 0
			case d <= maxTypo(word):
			case len(word) >= 3 && (strings.HasPrefix(name, word) || strings.HasPrefix(word, name)):
				d = maxTypo(word)
			default:
				continue
			}
			if best < 0 || d < best {
				best = d
			}
		}
		if best >= 0 {
			cands = append(cands, cand{sub.CommandPath(), best})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].path < cands[j].path
	})
	out := []string{}
	for _, c := range cands {
		out = append(out, c.path)
	}
	return out
}

// unknownCommand builds E_USAGE unknown_command with suggestions from the group's
// children, the verb synonyms and the whole tree, and the corrected command line
// when there is a suggestion.
func (a *App) unknownCommand(group *cobra.Command, word string) *Error {
	var available []string
	for _, sub := range group.Commands() {
		if !isCobraInternal(sub) && !sub.Hidden {
			available = append(available, sub.Name())
		}
	}
	sort.Strings(available)
	suggestions := a.siblingMatches(group, word)
	var ranks []int // of tree matches; siblings share one parent
	if len(suggestions) == 0 {
		suggestions, ranks = a.rankedTreeMatches(word, false)
	}
	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}
	msg := fmt.Sprintf("`%s` has no such subcommand; details.available lists its commands.", group.CommandPath())
	if len(suggestions) > 0 {
		msg = fmt.Sprintf("`%s` has no such subcommand; details.did_you_mean lists the closest commands.", group.CommandPath())
	}
	e := Usage("unknown_command", word, msg)
	if len(suggestions) > 0 {
		e.Details["did_you_mean"] = suggestions
	}
	e.Details["available"] = nonNil(available)
	e.Details["usage"] = group.CommandPath() + " <command> [flags]"
	// correct is the next_action for one suggestion and the command it says to run.
	correct := func(best string) (next, run string) {
		bestWords := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(best, a.Root.Name()), " "))
		dropped := false
		var kept []string
		line, ok := a.commandLine(best, func(_ int, tok string, kind argKind) []string {
			if kind == argPositional && !dropped && tok == word {
				dropped = true
				return nil
			}
			kept = append(kept, tok)
			return []string{tok}
		})
		// The remaining arguments are read as the suggested command reads them (P11):
		// a value of one of its flags is not a positional argument.
		_, kinds := a.classify(append(append([]string{}, bestWords...), kept...))
		positionals := 0
		for _, k := range kinds[len(bestWords):] {
			if k == argPositional {
				positionals++
			}
		}
		target := a.commandByPath(strings.Join(bestWords, " "))
		isGroup := target != nil && (target == a.Root || hasSubcommands(target))
		takes, variadic := 0, false
		if target != nil {
			for _, p := range positionalsOf(target) {
				takes++
				variadic = variadic || p.variadic
			}
			if m := a.metaFor(target); m != nil && m.Positional != "" && takes == 0 {
				takes = 1
			}
		}
		switch {
		case ok && !isGroup && !variadic && positionals > takes:
			// The rewritten line would be refused (unexpected_argument): point to the
			// command's own help instead of a command that cannot run.
			return fmt.Sprintf("Did you mean `%s`? It does not take those positional arguments; run `%s --help` for its flags.", best, best), best + " --help"
		case ok && (!isGroup || line != best):
			return fmt.Sprintf("Did you mean `%s`? Run `%s`.", best, line), line
		default:
			return fmt.Sprintf("Did you mean `%s`? Run `%s --help` for its commands and flags.", best, best), best + " --help"
		}
	}
	next := fmt.Sprintf("Run `%s --help` to list every command under it.", group.CommandPath())
	if len(suggestions) > 0 {
		next, _ = correct(suggestions[0])
		// Equally close matches under different parents (I-1): no one of them is
		// picked; each one's corrected line is named.
		var names, runs []string
		parents := map[string]bool{}
		for i, s := range suggestions {
			if i < len(ranks) && ranks[i] == ranks[0] {
				_, run := correct(s)
				names, runs = append(names, "`"+s+"`"), append(runs, "`"+run+"`")
				parents[s[:strings.LastIndex(s, " ")+1]] = true
			}
		}
		if len(parents) > 1 {
			next = fmt.Sprintf("Did you mean %s? Run %s.", strings.Join(names, " or "), strings.Join(runs, " or "))
		}
	}
	e.Details["next_action"] = next
	return e
}

// treeMatches finds commands anywhere in the tree whose name or alias matches word
// (exact, singular/plural, a verb synonym, or a close spelling), shallowest first.
// Shortcut keys count as names of their target. Paths carry the tool name.
func (a *App) treeMatches(word string, leavesOnly bool) []string {
	paths, _ := a.rankedTreeMatches(word, leavesOnly)
	return paths
}

// rankedTreeMatches is treeMatches with the rank of each path: its depth, 0 for a
// shortcut key; equal ranks are equally close.
func (a *App) rankedTreeMatches(word string, leavesOnly bool) ([]string, []int) {
	if word == "" {
		return []string{}, nil
	}
	type hit struct {
		path  string
		depth int
	}
	var hits []hit
	singular := strings.TrimSuffix(word, "s")
	syn := a.verbSynonyms(word)
	match := func(n string) bool {
		return n == word || strings.TrimSuffix(n, "s") == singular || contains(syn, n) ||
			(len(word) >= 4 && editDistance(word, n) <= 1)
	}
	seen := map[string]bool{}
	walkAll(a.Root, func(c *cobra.Command) {
		if c == a.Root || isCobraInternal(c) || c.Hidden || (leavesOnly && hasSubcommands(c)) {
			return
		}
		for _, n := range append([]string{c.Name()}, c.Aliases...) {
			if match(n) && !seen[c.CommandPath()] {
				seen[c.CommandPath()] = true
				hits = append(hits, hit{c.CommandPath(), strings.Count(c.CommandPath(), " ")})
				return
			}
		}
	})
	for key, to := range a.Shortcuts {
		if match(key) {
			if c := a.commandByPath(strings.Join(to, " ")); c != nil && !seen[c.CommandPath()] {
				seen[c.CommandPath()] = true
				hits = append(hits, hit{c.CommandPath(), 0})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].depth != hits[j].depth {
			return hits[i].depth < hits[j].depth
		}
		return hits[i].path < hits[j].path
	})
	out := []string{}
	var ranks []int
	for i, h := range hits {
		if i == 3 {
			break
		}
		out = append(out, h.path)
		ranks = append(ranks, h.depth)
	}
	return out, ranks
}

// usageMax caps a synopsis (details.usage) at about 200 characters.
const usageMax = 200

// usageLine is the command's synopsis for details.usage (CLI-012): the path, its
// positionals and its flags, required ones bare and optional ones in brackets, a
// file flag beside the literal flag it replaces, and the gate flags of a write.
// Optional flags that do not fit in about 200 characters become "[...]".
func (a *App) usageLine(c *cobra.Command) string {
	if c == a.Root || hasSubcommands(c) {
		return c.CommandPath() + " <command> [flags]"
	}
	m := a.metaFor(c)
	if m == nil {
		m = &Meta{}
	}
	parts := []string{c.CommandPath()}
	pos := positionalsOf(c)
	for _, p := range pos {
		s := p.name
		if p.variadic {
			s += "..."
		}
		if p.required {
			parts = append(parts, "<"+s+">")
		} else {
			parts = append(parts, "["+s+"]")
		}
	}
	posFlag := m.Positional
	if posFlag != "" && len(pos) == 0 {
		if f := lookupFlag(c, posFlag); f != nil {
			if flagRequired(f) {
				parts = append(parts, "<"+posFlag+">")
			} else {
				parts = append(parts, "["+posFlag+"]")
			}
		}
	}
	fileFor := map[string]string{} // bound literal flag -> file flag
	for file, bound := range m.FileInputs {
		if lookupFlag(c, bound) != nil {
			fileFor[bound] = file
		}
	}
	var required, optional []string
	c.Flags().VisitAll(func(f *pflag.Flag) {
		name := f.Name
		if f.Hidden || a.isGlobal(name) || name == posFlag || mapHasValue(fileFor, name) {
			return
		}
		text := "--" + name + a.valueHint(c, m, f)
		if file, ok := fileFor[name]; ok {
			if ff := lookupFlag(c, file); ff != nil {
				text += " | --" + file + " <path>"
			}
		}
		if flagRequired(f) || name == m.BatchFlag {
			required = append(required, text)
		} else {
			optional = append(optional, "["+text+"]")
		}
	})
	parts = append(parts, required...)
	if m.isWrite() {
		if m.dangerous() {
			parts = append(parts, "--dangerous")
		}
		parts = append(parts, "[--dry-run | --confirm <token>]")
	}
	line := strings.Join(parts, " ")
	for i, o := range optional {
		if len(line)+1+len(o) > usageMax {
			if i < len(optional) {
				line += " [...]"
			}
			break
		}
		line += " " + o
	}
	return line
}

// valueHint is the value placeholder of a flag in a synopsis: nothing for a boolean,
// the values of a short enum, <n> for integers, <path> for files, else the flag's
// own name; a repeatable flag ends in "...".
func (a *App) valueHint(c *cobra.Command, m *Meta, f *pflag.Flag) string {
	if f.NoOptDefVal != "" {
		return ""
	}
	typ, multiple := flagType(f)
	hint := "<" + f.Name + ">"
	switch {
	case len(m.Params[f.Name].Enum) > 0 && len(strings.Join(m.Params[f.Name].Enum, "|")) <= 40:
		hint = strings.Join(m.Params[f.Name].Enum, "|")
	case m.FileInputs[f.Name] != "" || m.Params[f.Name].Type == "path":
		hint = "<path>"
	case typ == "integer":
		hint = "<n>"
	case typ == "number":
		hint = "<number>"
	}
	if multiple {
		hint += "..."
	}
	return " " + hint
}

// flagNames lists the flags a command accepts besides the global ones (details.available
// of an unknown flag that matched nothing).
func (a *App) flagNames(c *cobra.Command) []string {
	var out []string
	seen := map[string]bool{}
	visit := func(f *pflag.Flag) {
		if f.Hidden || seen[f.Name] || a.isGlobal(f.Name) {
			return
		}
		seen[f.Name] = true
		out = append(out, "--"+f.Name)
	}
	c.Flags().VisitAll(visit)
	sort.Strings(out)
	if len(out) > 25 {
		out = out[:25]
	}
	return out
}

// flagErrorFunc turns pflag parse errors into E_USAGE (CLI-012, CLI-023).
func (a *App) flagErrorFunc(c *cobra.Command, err error) error {
	// A group given an unknown word followed by flags fails on the flags first; the
	// unknown word is the real mistake.
	if c == a.Root || hasSubcommands(c) {
		if word := a.firstUnknownWord(c); word != "" {
			return a.unknownCommand(c, word)
		}
	}
	usage := a.usageLine(c)
	help := fmt.Sprintf("Run `%s --help` to see its arguments and flags.", c.CommandPath())
	var notExist *pflag.NotExistError
	var needsValue *pflag.ValueRequiredError
	var invalid *pflag.InvalidValueError
	var syntax *pflag.InvalidSyntaxError
	switch {
	case errors.As(err, &notExist):
		return a.unknownFlag(c, notExist.GetSpecifiedName(), notExist.GetSpecifiedShortnames() != "", usage)
	case errors.As(err, &needsValue):
		f := needsValue.GetFlag()
		e := Usage("missing_parameter", "--"+f.Name, fmt.Sprintf("--%s needs a value.", f.Name))
		e.Details["usage"] = usage
		e.Details["next_action"] = fmt.Sprintf("Give --%s a value, as details.usage shows, and run the command again.", f.Name)
		return e
	case errors.As(err, &invalid):
		f := invalid.GetFlag()
		typ, _ := flagType(f)
		e := Usage("invalid_value", "--"+f.Name, fmt.Sprintf("--%s needs a value of type %s.", f.Name, typ))
		e.Details["expected"] = typ
		e.Details["usage"] = usage
		e.Details["next_action"] = fmt.Sprintf("Give --%s a value of type %s and run the command again.", f.Name, typ)
		return e
	case errors.As(err, &syntax):
		e := Usage("unknown_flag", syntax.GetSpecifiedFlag(), "A flag is malformed; flags look like --name value or --name=value.")
		e.Details["usage"] = usage
		e.Details["next_action"] = help
		return e
	}
	e := Usage("invalid_value", "", "The command line could not be parsed.")
	e.Details["usage"] = usage
	e.Details["next_action"] = help
	return e
}

// unknownFlag builds E_USAGE unknown_flag: the closest flags (synonyms first) and,
// when the top suggestion can take the typed flag's place, the corrected command;
// with no close flag, details.available lists the command's flags.
func (a *App) unknownFlag(c *cobra.Command, name string, short bool, usage string) *Error {
	param := "--" + name
	if short {
		param = "-" + name
	}
	sugg := a.similarFlags(c, name)
	msg := fmt.Sprintf("`%s` has no such flag; details.usage shows its flags.", c.CommandPath())
	if len(sugg) > 0 {
		msg = fmt.Sprintf("`%s` has no such flag; details.did_you_mean lists the closest flags.", c.CommandPath())
	}
	e := Usage("unknown_flag", param, msg)
	e.Details["usage"] = usage
	if len(sugg) == 0 {
		e.Details["available"] = nonNil(a.flagNames(c))
		e.Details["next_action"] = "Use one of the flags in details.available (details.usage shows how), or drop the flag."
		return e
	}
	e.Details["did_you_mean"] = sugg
	next := fmt.Sprintf("Use %s instead, as details.usage shows.", sugg[0])
	f := lookupFlag(c, strings.TrimPrefix(sugg[0], "--"))
	// A mistyped secret flag carries a secret: rewrite only to that secret flag
	// (whose value is masked), never to a neighbour that would echo it.
	secrets := a.secretFlagNames()
	risky := false
	for _, s := range sugg {
		if secrets[strings.TrimPrefix(s, "--")] && s != sugg[0] {
			risky = true
		}
	}
	if f != nil && !risky {
		if line, ok := a.replaceFlag(c, name, short, f); ok {
			next = fmt.Sprintf("Run `%s`.", line)
		} else if f.NoOptDefVal == "" {
			hint := strings.TrimSpace(a.valueHint(c, a.metaOrEmpty(c), f))
			if f.DefValue != "" && f.DefValue != "[]" {
				next = fmt.Sprintf("Use %s %s instead (default %s), as details.usage shows.", sugg[0], hint, f.DefValue)
			} else {
				next = fmt.Sprintf("Use %s %s instead, as details.usage shows.", sugg[0], hint)
			}
		}
	}
	e.Details["next_action"] = next
	return e
}

func (a *App) metaOrEmpty(c *cobra.Command) *Meta {
	if m := a.metaFor(c); m != nil {
		return m
	}
	return &Meta{}
}

// replaceFlag rebuilds the run's command line with the unknown flag token replaced
// by f, keeping its value. It fails when f needs a value the token did not carry.
func (a *App) replaceFlag(c *cobra.Command, name string, short bool, f *pflag.Flag) (string, bool) {
	st := a.run
	if st == nil {
		return "", false
	}
	_, kinds := a.classify(st.args)
	bad := -1
	for i, tok := range st.args {
		if kinds[i] != argFlag {
			continue
		}
		if short && strings.HasPrefix(tok, "-") && !strings.HasPrefix(tok, "--") && strings.Contains(tok, name) {
			bad = i
			break
		}
		if !short && strings.HasPrefix(tok, "--") && flagName(tok) == name {
			bad = i
			break
		}
	}
	if bad < 0 {
		return "", false
	}
	_, value, inline := splitFlag(st.args[bad])
	takes := f.NoOptDefVal == ""
	if short && len(strings.TrimPrefix(st.args[bad], "-")) != 1 && !inline {
		return "", false // combined shorthands: leave them to the agent
	}
	if takes && !inline {
		next := bad + 1
		if next >= len(st.args) || kinds[next] == argFlag || kinds[next] == argWord {
			return "", false
		}
	}
	return a.commandLine(c.CommandPath(), func(i int, tok string, _ argKind) []string {
		if i != bad {
			return []string{tok}
		}
		if inline {
			return []string{"--" + f.Name + "=" + value}
		}
		return []string{"--" + f.Name}
	})
}

// firstUnknownWord returns the first non-flag word after group c in the run's
// arguments that is not one of its subcommands.
func (a *App) firstUnknownWord(c *cobra.Command) string {
	st := a.run
	if st == nil {
		return ""
	}
	target, kinds := a.classify(st.args)
	if target != c {
		return ""
	}
	for i, k := range kinds {
		if k == argPositional {
			return st.args[i]
		}
	}
	return ""
}

// similarFlags suggests flags of c close to name: synonyms first, then spelling
// mistakes by edit distance, then flags that only share a prefix.
func (a *App) similarFlags(c *cobra.Command, name string) []string {
	type cand struct {
		name string
		dist int
	}
	var cands []cand
	seen := map[string]bool{}
	visit := func(f *pflag.Flag) {
		if f.Hidden || seen[f.Name] || f.Name == "help" {
			return
		}
		seen[f.Name] = true
		d := editDistance(name, f.Name)
		switch {
		case contains(a.flagSynonyms(name), f.Name):
			d = -1
		case d <= maxTypo(name):
			d *= 2
		case len(name) >= 3 && (strings.HasPrefix(f.Name, name) || strings.HasPrefix(name, f.Name)):
			d = 2*maxTypo(name) + 1
		default:
			return
		}
		cands = append(cands, cand{"--" + f.Name, d})
	}
	c.Flags().VisitAll(visit)
	c.InheritedFlags().VisitAll(visit)
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].name < cands[j].name
	})
	out := []string{}
	for i, x := range cands {
		if i == 3 {
			break
		}
		out = append(out, x.name)
	}
	return out
}

// validateArgs checks positional arguments (CLI-023): a command accepts only the
// positionals its Use line declares (or one for Meta.Positional); more are
// E_USAGE unexpected_argument, never silently ignored. A tool's own cobra Args
// validator still runs, with its errors mapped to E_USAGE.
func (a *App) validateArgs(c *cobra.Command, args []string, orig cobra.PositionalArgs) error {
	m := a.metaFor(c)
	pos := positionalsOf(c)
	maxArgs, minArgs := len(pos), 0
	variadic := false
	for _, p := range pos {
		if p.required {
			minArgs++
		}
		if p.variadic {
			variadic = true
		}
	}
	if m != nil && m.Positional != "" {
		if maxArgs == 0 {
			maxArgs = 1
		}
		minArgs = 0 // the flag can be given instead
	}
	if orig != nil {
		if err := orig(c, args); err != nil {
			switch {
			case len(args) > maxArgs && !variadic && maxArgs >= 0:
				return a.unexpectedArg(c, args, maxArgs)
			case len(args) < minArgs:
				return a.missingArg(c, pos, len(args))
			}
			usage := a.usageLine(c)
			e := Usage("invalid_value", "args", "The arguments do not match the command's usage.")
			e.Details["usage"] = usage
			e.Details["expected"] = usage
			e.Details["next_action"] = "Give the arguments details.usage shows and run the command again."
			return e
		}
		return nil
	}
	if len(args) > maxArgs && !variadic {
		return a.unexpectedArg(c, args, maxArgs)
	}
	return nil
}

// unexpectedArg reports positional arguments a command does not take. parameter
// names the first one by position (args[i]), so a value typed in the wrong place is
// not echoed. When the stray value clearly belongs to one flag (the only required
// flag still unset, or the batch flag), next_action is the corrected command.
func (a *App) unexpectedArg(c *cobra.Command, args []string, allowed int) *Error {
	idx := allowed
	if idx >= len(args) {
		idx = len(args) - 1
	}
	if idx < 0 {
		idx = 0
	}
	takes := "no positional arguments"
	switch allowed {
	case 0:
	case 1:
		takes = "one positional argument"
	default:
		takes = fmt.Sprintf("%d positional arguments", allowed)
	}
	e := Usage("unexpected_argument", fmt.Sprintf("args[%d]", idx),
		fmt.Sprintf("`%s` takes %s; pass the other values with their flags.", c.CommandPath(), takes))
	e.Details["usage"] = a.usageLine(c)
	next := "Pass each value with its flag, as details.usage shows."
	if len(args) > idx {
		if flag, value := a.strayFlag(c, args[idx:], allowed); flag != "" {
			seen := 0
			line, ok := a.commandLine(c.CommandPath(), func(_ int, tok string, kind argKind) []string {
				if kind != argPositional {
					return []string{tok}
				}
				seen++
				if seen > allowed {
					return nil
				}
				return []string{tok}
			}, "--"+flag, value)
			if ok {
				next = fmt.Sprintf("Pass it as --%s: `%s`.", flag, line)
			}
		}
	}
	e.Details["next_action"] = next
	return e
}

// strayFlag names the flag stray positional values belong to, when that is clear:
// the batch flag (values joined with commas), or the only required flag still unset.
func (a *App) strayFlag(c *cobra.Command, stray []string, allowed int) (string, string) {
	m := a.metaFor(c)
	if m == nil || len(stray) == 0 {
		return "", ""
	}
	if m.BatchFlag != "" {
		if f := lookupFlag(c, m.BatchFlag); f != nil && !f.Changed {
			return m.BatchFlag, strings.Join(stray, ",")
		}
	}
	if len(stray) != 1 {
		return "", ""
	}
	var unset []string
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if !flagRequired(f) || f.Changed {
			return
		}
		if f.Name == m.Positional && allowed > 0 {
			return // the positional fills it
		}
		unset = append(unset, f.Name)
	})
	if len(unset) == 1 {
		return unset[0], stray[0]
	}
	return "", ""
}

func (a *App) missingArg(c *cobra.Command, pos []usePos, got int) *Error {
	name := ""
	for i, p := range pos {
		if i >= got && p.required {
			name = p.name
			break
		}
	}
	e := Usage("missing_parameter", name, fmt.Sprintf("`%s` needs the <%s> argument.", c.CommandPath(), name))
	e.Details["usage"] = a.usageLine(c)
	e.Details["next_action"] = fmt.Sprintf("Give the <%s> argument, as details.usage shows, and run the command again.", name)
	return e
}

// commandLine renders a copy-paste command line: path (with the tool name) and the
// run's arguments without their command words, each token passed through edit
// (which may drop or replace it), then extra. Values of secret flags are shown as
// ***. ok is false outside a run.
func (a *App) commandLine(path string, edit func(i int, tok string, kind argKind) []string, extra ...string) (string, bool) {
	st := a.run
	if st == nil {
		return "", false
	}
	_, kinds := a.classify(st.args)
	out := strings.Fields(path)
	for i, tok := range st.args {
		if kinds[i] == argWord {
			continue
		}
		repl := []string{tok}
		if edit != nil {
			repl = edit(i, tok, kinds[i])
		}
		out = append(out, repl...)
	}
	out = append(out, extra...)
	return a.renderCommand(out), true
}

// secretFlagNames is every flag name a command declares secret (Meta.Secrets).
func (a *App) secretFlagNames() map[string]bool {
	secret := map[string]bool{}
	for _, m := range a.reg.meta {
		for _, s := range m.Secrets {
			secret[s] = true
		}
	}
	return secret
}

// nearSecret reports a flag name that is, or is a likely misspelling of, a secret
// flag: its value may be a secret.
func nearSecret(name string, secret map[string]bool) bool {
	if secret[name] {
		return true
	}
	for s := range secret {
		if editDistance(name, s) <= 2 || (len(name) >= 3 && (strings.HasPrefix(s, name) || strings.HasPrefix(name, s))) {
			return true
		}
	}
	return false
}

// renderCommand joins tokens (the tool name, the command path and its arguments)
// into one shell-ready line, quoting where needed and masking the values of secret
// flags and of flags whose names are close to a secret flag's (D6). Which token is
// a flag's value comes from classify, so the value is masked however it was given:
// --long v, --long=v, -s v, -s=v, -sv, or a value that starts with "-".
func (a *App) renderCommand(tokens []string) string {
	secret := a.secretFlagNames()
	start := 0
	if len(tokens) > 0 && a.Root != nil && tokens[0] == a.Root.Name() {
		start = 1
	}
	target, kinds := a.classify(tokens[start:])
	out := append(make([]string, 0, len(tokens)), tokens[:start]...)
	maskNext := false
	for i, tok := range tokens[start:] {
		kind := kinds[i]
		if maskNext && (kind == argFlagValue || kind == argPositional) {
			out = append(out, "***")
			maskNext = false
			continue
		}
		maskNext = false
		if kind == argFlag && tok != "--" {
			if masked, next, ok := a.maskSecretFlag(target, tok, secret); ok {
				out = append(out, masked)
				maskNext = next
				continue
			}
		}
		out = append(out, shellWord(tok))
	}
	return strings.Join(out, " ")
}

// maskSecretFlag renders one flag token of a secret flag (or of a name close to
// one): the token with its value masked, and whether the next token is its value.
// ok is false for any other flag.
func (a *App) maskSecretFlag(c *cobra.Command, tok string, secret map[string]bool) (string, bool, bool) {
	name, _, inline := splitFlag(tok)
	if strings.HasPrefix(tok, "--") {
		if !nearSecret(name, secret) {
			return "", false, false
		}
		if inline {
			return "--" + name + "=***", false, true
		}
		return tok, true, true
	}
	// A shorthand: -s, -s=v, or -sv with the value attached.
	if name == "" {
		return "", false, false
	}
	f := shorthandFlag(c, name[:1])
	if f == nil || !secret[f.Name] {
		return "", false, false
	}
	switch {
	case inline:
		return "-" + name[:1] + "=***", false, true
	case len(name) > 1 && f.NoOptDefVal == "":
		return "-" + name[:1] + "***", false, true
	}
	return tok, f.NoOptDefVal == "", true
}

// shellWord quotes a token for a copy-paste command line when it holds spaces or
// shell metacharacters.
func shellWord(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || strings.ContainsRune("\"'`$\\|&;<>()*?[]{}!#~", r) }) {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

// prepareRun runs before a leaf's own code: it fixes the output mode, binds a lone
// positional to Meta.Positional, and checks required flags, flag groups, file
// inputs, enums and --fields, so the command never runs on a bad command line.
func (a *App) prepareRun(c *cobra.Command, args []string) error {
	st := a.run
	if st == nil {
		return nil
	}
	st.cmd = c
	m := a.metaFor(c)
	a.commandDeprecationNotice(c, m)
	if err := a.applyOutputFlags(c); err != nil {
		return err
	}
	if err := a.checkProfile(c); err != nil {
		return err
	}
	if m != nil && m.Positional != "" && len(args) == 1 {
		f := lookupFlag(c, m.Positional)
		if f == nil {
			return internalError("Meta.Positional names a flag the command does not define.")
		}
		if f.Changed {
			if f.Value.String() != args[0] {
				e := Usage("conflicting_flags", "--"+f.Name, fmt.Sprintf("The argument and --%s give different values.", f.Name))
				e.Details["next_action"] = fmt.Sprintf("Give the value once, either as the argument or as --%s.", f.Name)
				return e
			}
		} else if err := c.Flags().Set(f.Name, args[0]); err != nil {
			typ, _ := flagType(f)
			e := Usage("invalid_value", "--"+f.Name, fmt.Sprintf("The argument must be a value of type %s.", typ))
			e.Details["expected"] = typ
			return e
		}
		st.positionalConsumed = true
	}
	a.flagDeprecationNotices(c, m) // after the positional is bound to its flag
	if err := a.renamedFlagUse(st); err != nil {
		return err
	}
	if err := a.checkRequired(c); err != nil {
		return err
	}
	if m != nil {
		if err := a.checkFileInputs(c, m); err != nil {
			return err
		}
		if err := a.checkEnums(c, m); err != nil {
			return err
		}
	}
	return a.checkFields(c, m)
}

// renamedFlagUse reports a renamed flag's old name (Meta.RenamedFlags, Q10): given
// beside its new name it is E_USAGE conflicting_flags; alone it adds the deprecated
// notice.
func (a *App) renamedFlagUse(st *runState) error {
	if old, renamed := st.renamedBoth[0], st.renamedBoth[1]; old != "" {
		e := Usage("conflicting_flags", "--"+old, fmt.Sprintf("--%s is the old name of --%s; give only one of them.", old, renamed))
		e.Details["next_action"] = fmt.Sprintf("Drop --%s and keep --%s.", old, renamed)
		return e
	}
	m := a.metaFor(st.cmd)
	for _, old := range st.renamedUsed {
		if m != nil && m.RenamedFlags[old] != "" {
			a.Notice(Notice{Type: "deprecated", Severity: "warning",
				Message: fmt.Sprintf("`--%s` is deprecated: use `--%s`.", old, m.RenamedFlags[old])})
		}
	}
	return nil
}

// applyOutputFlags reads --format/--json/--pretty/--quiet/--fields for the run.
func (a *App) applyOutputFlags(c *cobra.Command) error {
	st := a.run
	flags := c.Flags()
	format, _ := flags.GetString("format")
	format = strings.ToLower(strings.TrimSpace(format))
	jsonAlias, _ := flags.GetBool("json")
	st.pretty, _ = flags.GetBool("pretty")
	st.quiet, _ = flags.GetBool("quiet")
	formatSet := flags.Changed("format")
	if jsonAlias && formatSet && format != "json" {
		st.format = "json"
		e := Usage("conflicting_flags", "--json", "--json and --format name different formats.")
		e.Details["next_action"] = "Drop --json (JSON is the default) or drop --format."
		return e
	}
	if jsonAlias || format == "" {
		format = "json"
	}
	m := a.metaFor(c)
	supported := []string{"json", "text"}
	if m != nil && m.RawOutput {
		supported = append(supported, "raw")
	}
	if !contains(supported, format) {
		st.format = "json"
		e := Usage("unsupported_format", "--format", fmt.Sprintf("`%s` does not support that format.", c.CommandPath()))
		e.Details["expected"] = strings.Join(supported, "|")
		e.Details["next_action"] = "Drop --format (JSON is the default) or use one of: " + strings.Join(supported, ", ") + "."
		return e
	}
	st.format = format
	if flags.Changed("fields") {
		raw, _ := flags.GetString("fields")
		sel, err := parseFields(raw)
		if err != nil {
			return err
		}
		st.fields = sel
		if format == "raw" {
			e := Usage("conflicting_flags", "--fields", "--fields does not apply to --format raw.")
			e.Details["next_action"] = "Drop --fields or use JSON output."
			return e
		}
	}
	return nil
}

// parseFields parses --fields: "a,b" replaces, "+a,+b" adds, "all" returns everything.
func parseFields(raw string) (fieldSelection, *Error) {
	sel := fieldSelection{set: true}
	var plus, plain int
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "all" {
			sel.all = true
			continue
		}
		if strings.HasPrefix(part, "+") {
			plus++
			part = strings.TrimSpace(part[1:])
		} else {
			plain++
		}
		if part != "" {
			sel.names = append(sel.names, part)
		}
	}
	if plus > 0 && plain > 0 {
		e := Usage("invalid_value", "--fields", "--fields mixes replacing (a,b) and adding (+a) fields.")
		e.Details["expected"] = "a,b | +a,+b | all"
		return sel, e
	}
	sel.add = plus > 0
	if !sel.all && len(sel.names) == 0 {
		e := Usage("invalid_value", "--fields", "--fields is empty.")
		e.Details["expected"] = "a,b | +a,+b | all"
		return sel, e
	}
	return sel, nil
}

// checkFields rejects field names the command's schema does not declare (CLI-040).
// context accepts every top-level key it returns (P13).
func (a *App) checkFields(c *cobra.Command, m *Meta) error {
	st := a.run
	if !st.fields.set || st.fields.all {
		return nil
	}
	fields := a.schemaOf(m).Fields
	if a.reg.builtin[c] && c.Name() == "context" && c.Parent() == a.Root {
		fields = a.contextFields()
	}
	if len(fields) == 0 {
		return nil
	}
	var unknown []string
	for _, n := range st.fields.names {
		if !contains(fields, n) && n != "ref" {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		e := Usage("invalid_value", "--fields", fmt.Sprintf("--fields names %d field(s) this command does not return.", len(unknown)))
		e.Details["expected"] = strings.Join(fields, ",")
		e.Details["did_you_mean"] = closestWords(unknown[0], fields)
		e.Details["next_action"] = "Use field names from details.expected, or --fields all."
		return e
	}
	return nil
}

// checkRequired reports missing required flags and violated flag groups before the
// command runs (cobra's own checks would return plain text errors).
func (a *App) checkRequired(c *cobra.Command) error {
	var missing []string
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if flagRequired(f) && !f.Changed {
			missing = append(missing, f.Name)
		}
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		e := Usage("missing_parameter", "--"+missing[0], fmt.Sprintf("`%s` needs --%s.", c.CommandPath(), strings.Join(missing, ", --")))
		e.Details["usage"] = a.usageLine(c)
		e.Details["next_action"] = fmt.Sprintf("Add --%s <value>; run `%s --help` for details.", missing[0], c.CommandPath())
		return e
	}
	groups := map[string]map[string][]string{}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		for _, kind := range []string{"cobra_annotation_mutually_exclusive", "cobra_annotation_required_if_others_set", "cobra_annotation_one_required"} {
			for _, g := range f.Annotations[kind] {
				if groups[kind] == nil {
					groups[kind] = map[string][]string{}
				}
				groups[kind][g] = strings.Fields(g)
			}
		}
	})
	changed := func(name string) bool {
		f := c.Flags().Lookup(name)
		return f != nil && f.Changed
	}
	for _, g := range sortedKeys(groups["cobra_annotation_mutually_exclusive"]) {
		var set []string
		for _, n := range groups["cobra_annotation_mutually_exclusive"][g] {
			if changed(n) {
				set = append(set, n)
			}
		}
		if len(set) > 1 {
			e := Usage("conflicting_flags", "--"+set[1], fmt.Sprintf("Use only one of --%s.", strings.Join(groups["cobra_annotation_mutually_exclusive"][g], ", --")))
			e.Details["usage"] = a.usageLine(c)
			return e
		}
	}
	for _, g := range sortedKeys(groups["cobra_annotation_required_if_others_set"]) {
		names := groups["cobra_annotation_required_if_others_set"][g]
		var set, unset []string
		for _, n := range names {
			if changed(n) {
				set = append(set, n)
			} else {
				unset = append(unset, n)
			}
		}
		if len(set) > 0 && len(unset) > 0 {
			e := Usage("missing_parameter", "--"+unset[0], fmt.Sprintf("--%s must be given together.", strings.Join(names, ", --")))
			e.Details["usage"] = a.usageLine(c)
			return e
		}
	}
	for _, g := range sortedKeys(groups["cobra_annotation_one_required"]) {
		names := groups["cobra_annotation_one_required"][g]
		found := false
		for _, n := range names {
			if changed(n) {
				found = true
			}
		}
		if !found {
			e := Usage("missing_parameter", "--"+names[0], fmt.Sprintf("Give at least one of --%s.", strings.Join(names, ", --")))
			e.Details["usage"] = a.usageLine(c)
			return e
		}
	}
	return nil
}

// checkFileInputs rejects a literal and a file for the same bound value, and two
// flags reading stdin.
func (a *App) checkFileInputs(c *cobra.Command, m *Meta) error {
	stdinFlags := []string{}
	for _, file := range sortedKeys(m.FileInputs) {
		bound := m.FileInputs[file]
		ff := lookupFlag(c, file)
		if ff == nil || !ff.Changed {
			continue
		}
		if lf := lookupFlag(c, bound); lf != nil && lf.Changed && lf != ff {
			e := Usage("conflicting_flags", "--"+file, fmt.Sprintf("Give --%s or --%s, not both.", bound, file))
			e.Details["next_action"] = fmt.Sprintf("Drop --%s or --%s.", bound, file)
			return e
		}
		if ff.Value.String() == "-" {
			stdinFlags = append(stdinFlags, file)
		}
	}
	if len(stdinFlags) > 1 {
		e := Usage("conflicting_flags", "--"+stdinFlags[1], "Only one flag can read stdin (-).")
		e.Details["next_action"] = "Write the other input to a file and pass its path."
		return e
	}
	return nil
}

// checkEnums validates flags that declare allowed values in Meta.Params.
func (a *App) checkEnums(c *cobra.Command, m *Meta) error {
	for _, name := range sortedKeys(m.Params) {
		doc := m.Params[name]
		f := lookupFlag(c, name)
		if f == nil || !f.Changed || len(doc.Enum) == 0 {
			continue
		}
		values := []string{f.Value.String()}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			values = sv.GetSlice()
		}
		for _, v := range values {
			if !contains(doc.Enum, v) {
				e := Usage("invalid_value", "--"+name, fmt.Sprintf("--%s accepts one of a fixed set of values.", name))
				e.Details["expected"] = strings.Join(doc.Enum, "|")
				e.Details["did_you_mean"] = closestWords(v, doc.Enum)
				e.Details["next_action"] = fmt.Sprintf("Use one of: %s.", strings.Join(doc.Enum, ", "))
				return e
			}
		}
	}
	return nil
}

// closestWords returns up to three candidates close to word.
func closestWords(word string, candidates []string) []string {
	type cand struct {
		s string
		d int
	}
	var cs []cand
	for _, c := range candidates {
		d := editDistance(word, c)
		if d <= maxTypo(word)+1 || strings.HasPrefix(c, word) || strings.HasPrefix(word, c) {
			cs = append(cs, cand{c, d})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].d < cs[j].d })
	out := []string{}
	for i, c := range cs {
		if i == 3 {
			break
		}
		out = append(out, c.s)
	}
	return out
}

// maxTypo is how many edits still count as a typo of word: one for short words,
// two otherwise.
func maxTypo(word string) int {
	if len([]rune(word)) <= 4 {
		return 1
	}
	return 2
}

// editDistance is the optimal-string-alignment distance between a and b: insertions,
// deletions, substitutions and adjacent transpositions ("veiw" -> "view") cost one.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
