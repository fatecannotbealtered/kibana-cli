// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

// Package aicli implements ai-native-cli-spec 2.0 for Go command-line tools built on
// cobra: the JSON envelope and error contract, the command registry with reference,
// help and agent-friendly parsing, the shared dry-run/confirm write gate with its
// operation journal and scope files, the built-in commands and the conformance suite.
// Tools vendor this package into internal/aicli with scripts/sync-spec.js.
package aicli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func init() {
	// cobra's Windows "started from Explorer" check snapshots every running process
	// on each run (about 35 ms) and can block on "Press return"; agent-facing tools
	// skip it (CLI-090 start-up budget).
	cobra.MousetrapHelpText = ""
}

// App is one tool: its identity, its cobra tree and the hooks the library calls.
// Build a new App (and tree) for every Execute; an App runs once.
type App struct {
	Name, Version, RiskTier, EnvPrefix string // EnvPrefix e.g. "GITLAB_CLI"
	Root                               *cobra.Command
	Changelog                          string // embedded CHANGELOG.md (Keep a Changelog)
	// SkillDirs are the directories doctor reads the installed Skill from
	// (<dir>/<Name>/SKILL.md, its metadata.version and metadata.min_version). nil
	// means the standard agent skill directories ~/.agents/skills and
	// ~/.claude/skills; an empty, non-nil slice checks none (tests).
	SkillDirs []string
	// MinSkillVersion is only a fallback for doctor when no Skill is installed: a
	// tool older than it fails the version check.
	MinSkillVersion string
	// Identity resolves the account and host a write would act as, from local
	// configuration only (no network). nil means the tool has no account.
	Identity     func(ctx context.Context) (Identity, error)
	DoctorChecks func(ctx context.Context) []Check
	ContextExtra func(ctx context.Context) map[string]any
	// Shortcuts maps words typed at the root to a command path, so nested nouns are
	// reachable from the root ("branch" -> {"repo","branch"}). Keys may hold several
	// words ("member list" -> {"project","members"}).
	Shortcuts map[string][]string
	// ShortFlags accepts single-letter flags for long flags ("p" -> "project") on
	// commands that have the long flag and no shorthand of their own.
	ShortFlags map[string]string
	// FlagAliases accepts other long flag names ("repo" -> "project") on commands that
	// have the target flag and no flag of the alias name.
	FlagAliases map[string]string
	// FlagSynonyms are suggested when an unknown flag is used ("all" -> {"limit"}).
	FlagSynonyms map[string][]string
	// VerbSynonyms are suggested when an unknown subcommand is used ("view" -> {"get"}).
	// To accept a verb instead of suggesting it, list it in Meta.Aliases.
	VerbSynonyms map[string][]string
	// ExtCodes declares the tool's extension error codes (contract-ext.json, CLI-018).
	ExtCodes map[string]CodeSpec
	// Update turns on the built-in update command (CLI-070, CLI-071): discover the
	// GitHub release, verify and install it, and sync the installed Skills. nil
	// means the tool has no update command; a tool may not define its own.
	Update *UpdateConfig
	// Settings declares the values the tool reads with App.Setting (CLI-100). When
	// non-nil, aicli registers the root flag --profile, context reports the profile
	// and doctor checks the settings file (CLI-101).
	Settings       []Setting
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Now            func() time.Time
	Env            func(string) string // default os.Getenv
	StateDir       string              // override; default <PREFIX>_STATE_DIR or ~/.<Name>

	reg               registry
	offline           bool // set by RunConformance: doctor makes no release request
	builtinsInstalled bool
	prepared          bool
	executed          bool
	run               *runState
	defects           []string // tool bugs the library corrected at run time (RunConformance reports them)
	debugSeen         map[string]bool
	mu                sync.Mutex     // Debugf, Notice and AddSecret may run on several goroutines (Q22)
	secrets           []string       // values registered with AddSecret
	updateCmd         *cobra.Command // the built-in update, when App.Update is set
	// skillCopy replaces the Skill directory copy of update's Skill sync (tests).
	skillCopy func(src, dst string) error
}

// defect records a tool bug the library corrected so the output stays within the
// contract; the conformance suite fails on it, production output is unaffected.
func (a *App) defect(msg string) {
	if !contains(a.defects, msg) {
		a.defects = append(a.defects, msg)
	}
}

// version is the tool version the whole run reports and uses (CLI-080, P14):
// App.Version when it is a semantic version; otherwise, for a development build
// without -ldflags ("dev", empty), the next patch after the newest version in
// App.Changelog with the prerelease dev ("1.2.17" gives "1.2.18-dev"; no released
// version gives "0.0.0-dev"). version, reference, changelog, context, doctor, the
// update notice, update and the confirm token all see it.
// It is computed on each call (no cached state), so a tool may call UserAgent from
// several goroutines.
func (a *App) version() string {
	if a.devBuild() {
		return devVersion(a.Changelog)
	}
	return a.Version
}

// devBuild reports a build whose App.Version is not a semantic version (P14).
func (a *App) devBuild() bool {
	_, err := ParseVersion(a.Version)
	return err != nil
}

// devVersion is a development build's version: the next patch after the highest
// version heading of the changelog, with the prerelease dev.
func devVersion(changelog string) string {
	var newest *Version
	for _, e := range parseChangelog(changelog) {
		if v, err := ParseVersion(e.version); err == nil && (newest == nil || CompareVersions(v, *newest) > 0) {
			newest = &v
		}
	}
	if newest == nil {
		return "0.0.0-dev"
	}
	return fmt.Sprintf("%d.%d.%d-dev", newest.Major, newest.Minor, newest.Patch+1)
}

// AddSecret registers a secret the tool obtained at run time, such as a token read
// from the keyring, so that debug lines and errors show it as *** (CLI-102, D2),
// also in its URL-escaped forms. Values shorter than 4 characters are ignored.
func (a *App) AddSecret(value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if utf8.RuneCountInString(value) < 4 || contains(a.secrets, value) {
		return
	}
	a.secrets = append(a.secrets, value)
}

// Identity is the account and host a command acts as.
type Identity struct {
	Account, Host string
	Extra         map[string]string
}

// Check is one doctor check (CLI-036). Status is pass, warn or fail.
type Check struct{ Check, Status, Message, Fix string }

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

func (a *App) env(key string) string {
	if a.Env != nil {
		return a.Env(key)
	}
	return os.Getenv(key)
}

func (a *App) stdout() io.Writer {
	if a.Stdout != nil {
		return a.Stdout
	}
	return os.Stdout
}

func (a *App) stderr() io.Writer {
	if a.Stderr != nil {
		return a.Stderr
	}
	return os.Stderr
}

func (a *App) stdin() io.Reader {
	if a.Stdin != nil {
		return a.Stdin
	}
	return os.Stdin
}

func (a *App) toolName() string {
	if a.Name != "" {
		return a.Name
	}
	if a.Root != nil {
		return a.Root.Name()
	}
	return "tool"
}

// envPrefix returns the environment variable prefix (NAME in upper case by default).
func (a *App) envPrefix() string {
	if a.EnvPrefix != "" {
		return a.EnvPrefix
	}
	return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(a.toolName()))
}

func (a *App) ctx() context.Context {
	if st := a.run; st != nil && st.ctx != nil {
		return st.ctx
	}
	return context.Background()
}

// Execute runs the tool with args (without the program name), writes exactly one
// response and returns the process exit code. It installs the built-ins when the
// tool has not, normalises the arguments agents commonly guess, and turns panics into
// an E_UNKNOWN envelope.
func (a *App) Execute(args []string) int {
	st := &runState{startReal: time.Now(), format: defaultFormat}
	st.start = a.now()
	prev := a.run
	a.run = st
	defer func() { a.run = prev }()
	scanOutputFlags(st, args)
	if a.executed {
		st.panicErr = internalError("App.Execute was called twice on one App; build a new App for each run.")
		return a.finish(st)
	}
	a.executed = true
	func() {
		defer func() {
			if r := recover(); r != nil {
				e := internalError("The command failed with an internal error; details.cause has the panic text.")
				e.Details["cause"] = truncateRunes(fmt.Sprint(r), 300)
				e.Untrusted = []string{"cause"}
				st.panicErr = e
			}
		}()
		if a.Root == nil {
			st.panicErr = internalError("App.Root is nil.")
			return
		}
		// OutputFormat and WantsField find the run from a command (Q14, Q17).
		appsByRoot.Store(a.Root, a)
		defer appsByRoot.CompareAndDelete(a.Root, a)
		a.InstallBuiltins()
		a.prepareTree()
		norm := a.normalizeArgs(args)
		st.args = norm
		// The leaf the arguments name is the run's command before cobra runs any hook,
		// so Debugf and Setting in a PersistentPreRun(E) know its secrets and flags (D7).
		if target, _ := a.classify(norm); target != nil && target != a.Root && !hasSubcommands(target) && !isCobraInternal(target) {
			st.cmd = target
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		go func() {
			<-ctx.Done()
			stop() // a second interrupt then ends the process the default way
		}()
		st.ctx = ctx
		a.Root.SetArgs(norm)
		a.Root.SetOut(a.stdout())
		a.Root.SetErr(a.stderr())
		a.Root.SetIn(a.stdin())
		_, err := a.Root.ExecuteContextC(ctx)
		st.cmdErr = err
	}()
	return a.finish(st)
}

// scanOutputFlags reads the output flags from the raw arguments, so errors raised
// before flag parsing (unknown commands, bad flags) use the requested format.
func scanOutputFlags(st *runState, args []string) {
	jsonFlag := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			i = len(args)
		case arg == "--format" && i+1 < len(args):
			st.format = strings.ToLower(strings.TrimSpace(args[i+1]))
			i++
		case strings.HasPrefix(arg, "--format="):
			st.format = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--format=")))
		case arg == "--json" || arg == "--json=true":
			jsonFlag = true
		case arg == "--pretty" || arg == "--pretty=true":
			st.pretty = true
		case arg == "--quiet" || arg == "--quiet=true":
			st.quiet = true
		}
	}
	if jsonFlag || !contains(formatValues, st.format) {
		st.format = defaultFormat
	}
}

// installGlobalFlags defines the contract's global control flags on the root.
func (a *App) installGlobalFlags() {
	pf := a.Root.PersistentFlags()
	def := func(name string, fn func()) {
		if pf.Lookup(name) == nil {
			fn()
		}
	}
	def("format", func() {
		pf.String("format", defaultFormat, "Output format: json (default, for agents), text (for people), raw (commands that declare it)")
	})
	def("json", func() { pf.Bool("json", false, "Same as --format json") })
	def("pretty", func() { pf.Bool("pretty", false, "Indent JSON for people (JSON is compact by default)") })
	def("compact", func() { pf.Bool("compact", false, "Accepted for compatibility; JSON is compact by default") })
	def("fields", func() {
		pf.String("fields", "", "Fields to return: a,b replaces the defaults, +a adds to them, all returns every field")
	})
	def("quiet", func() { pf.Bool("quiet", false, "Suppress non-error output on stderr") })
	def("dry-run", func() { pf.Bool("dry-run", false, "Preview a write and get a confirm token; changes nothing") })
	def("confirm", func() { pf.String("confirm", "", "Run a previewed write with the confirm token from its preview") })
	def("dangerous", func() { pf.Bool("dangerous", false, "Required in both steps of a dangerous write") })
	if a.Settings != nil {
		def("profile", func() {
			pf.String("profile", "", "Profile from the settings file to use (default "+a.envPrefix()+"_PROFILE; `context` lists the profiles)")
		})
	}
	if a.Root.Flags().Lookup("version") == nil {
		a.Root.Flags().Bool("version", false, "Print the tool, its version and the spec version as JSON")
	}
}

// prepareTree installs the global flags, help and error hooks, makes groups answer
// unknown subcommands with E_USAGE, and wraps every leaf so the library validates
// its arguments before the tool's code runs. It runs once per App.
func (a *App) prepareTree() {
	if a.prepared {
		return
	}
	a.prepared = true
	root := a.Root
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.CompletionOptions.DisableDefaultCmd = true
	a.installGlobalFlags()
	a.normalizeSecrets()
	root.SetHelpFunc(a.helpFunc)
	root.SetFlagErrorFunc(a.flagErrorFunc)
	root.InitDefaultHelpCmd()
	walkAll(root, func(c *cobra.Command) {
		c.SilenceErrors = true
		c.SilenceUsage = true
		if isCobraInternal(c) {
			return
		}
		if c == root || hasSubcommands(c) {
			if c.Run == nil && c.RunE == nil {
				c.Args = cobra.ArbitraryArgs
				c.RunE = a.groupRunE
			}
			return
		}
		a.wrapLeaf(c)
	})
}

// wrapLeaf installs the library's argument validation and pre-run checks on a leaf.
func (a *App) wrapLeaf(c *cobra.Command) {
	m := a.metaFor(c)
	if m != nil && m.BatchFlag != "" && c.Flags().Lookup("continue-on-error") == nil {
		c.Flags().Bool("continue-on-error", !m.dangerous(),
			"Keep going after an item fails (default true; false for dangerous commands)")
	}
	origArgs := c.Args
	c.Args = func(cmd *cobra.Command, args []string) error {
		return a.validateArgs(cmd, args, origArgs)
	}
	origPreE, origPre := c.PreRunE, c.PreRun
	c.PreRun = nil
	c.PreRunE = func(cmd *cobra.Command, args []string) error {
		if err := a.prepareRun(cmd, args); err != nil {
			return err
		}
		if origPreE != nil {
			return origPreE(cmd, args)
		}
		if origPre != nil {
			origPre(cmd, args)
		}
		return nil
	}
}

// walkAll visits c and every command below it.
func walkAll(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walkAll(sub, fn)
	}
}

// isCobraInternal reports cobra's own help, completion and __complete commands.
func isCobraInternal(c *cobra.Command) bool {
	if !c.HasParent() {
		return false
	}
	name := c.Name()
	return (c.Parent() == c.Root() && (name == "help" || name == "completion")) || strings.HasPrefix(name, "__")
}

// hasSubcommands reports whether c has subcommands of its own (cobra internals aside).
func hasSubcommands(c *cobra.Command) bool {
	for _, sub := range c.Commands() {
		if !isCobraInternal(sub) {
			return true
		}
	}
	return false
}

// childNamed finds a direct subcommand by name or alias.
func childNamed(c *cobra.Command, word string) *cobra.Command {
	for _, sub := range c.Commands() {
		if isCobraInternal(sub) && sub.Name() != "help" {
			continue
		}
		if sub.Name() == word || sub.HasAlias(word) {
			return sub
		}
	}
	return nil
}

// ---------------------------------------------------------------- normalisation

// normalizeArgs rewrites the command forms agents guess first (CLI-022) before
// cobra parses them: root shortcuts for nested nouns, single-letter flags, long flag
// aliases (App-wide and the command's own, Q11) and renamed flags (Q10). Arguments
// that already parse are returned unchanged. A renamed flag's use is remembered for
// its notice, and its use beside the new name for the refusal (prepareRun).
func (a *App) normalizeArgs(args []string) []string {
	out := append([]string{}, args...)
	out = a.applyShortcut(out)
	target, kinds := a.classify(out)
	if target == nil || target == a.Root {
		return out
	}
	given := map[string]bool{}
	for i, arg := range out {
		if kinds[i] == argFlag {
			given[flagName(arg)] = true
		}
	}
	var renamed [][2]string
	for i, arg := range out {
		if kinds[i] != argFlag {
			continue
		}
		var old string
		out[i], old = a.rewriteFlag(target, arg)
		if old != "" {
			renamed = append(renamed, [2]string{old, a.metaFor(target).RenamedFlags[old]})
		}
	}
	if st := a.run; st != nil {
		for _, r := range renamed {
			if !contains(st.renamedUsed, r[0]) {
				st.renamedUsed = append(st.renamedUsed, r[0])
			}
			if f := lookupFlag(target, r[1]); given[r[1]] || (f != nil && f.Shorthand != "" && given[f.Shorthand]) {
				st.renamedBoth = r
			}
		}
	}
	return out
}

// rewriteFlag maps one flag token through Meta.RenamedFlags, App.FlagAliases,
// Meta.FlagAliases and App.ShortFlags. old is the renamed flag's old name when the
// token used it.
func (a *App) rewriteFlag(target *cobra.Command, arg string) (string, string) {
	name, value, hasValue := splitFlag(arg)
	if name == "" {
		return arg, ""
	}
	applies := func(to string) bool {
		return to != "" && lookupFlag(target, name) == nil && lookupFlag(target, to) != nil
	}
	m := a.metaFor(target)
	long, old := "", ""
	if strings.HasPrefix(arg, "--") {
		switch {
		case m != nil && applies(m.RenamedFlags[name]):
			long, old = m.RenamedFlags[name], name
		case applies(a.FlagAliases[name]):
			long = a.FlagAliases[name]
		case m != nil && applies(m.FlagAliases[name]):
			long = m.FlagAliases[name]
		}
	} else if len(name) == 1 {
		if to, ok := a.ShortFlags[name]; ok && shorthandFlag(target, name) == nil && lookupFlag(target, to) != nil {
			long = to
		}
	}
	if long == "" {
		return arg, ""
	}
	if hasValue {
		return "--" + long + "=" + value, old
	}
	return "--" + long, old
}

// splitFlag splits "--name=value" / "-n=value" / "--name" into its parts.
func splitFlag(arg string) (name, value string, hasValue bool) {
	switch {
	case strings.HasPrefix(arg, "--"):
		name = arg[2:]
	case strings.HasPrefix(arg, "-") && len(arg) > 1:
		name = arg[1:]
	default:
		return "", "", false
	}
	if i := strings.IndexByte(name, '='); i >= 0 {
		return name[:i], name[i+1:], true
	}
	return name, "", false
}

// applyShortcut replaces a root-level word that is not a root command with the path
// App.Shortcuts maps it to (the longest multi-word key wins).
func (a *App) applyShortcut(args []string) []string {
	if len(a.Shortcuts) == 0 {
		return args
	}
	i := a.firstWord(args)
	if i < 0 || childNamed(a.Root, args[i]) != nil {
		return args
	}
	maxWords := 1
	for k := range a.Shortcuts {
		if n := len(strings.Fields(k)); n > maxWords {
			maxWords = n
		}
	}
	for n := maxWords; n >= 1; n-- {
		if i+n > len(args) {
			continue
		}
		words := args[i : i+n]
		if hasFlagToken(words) {
			continue
		}
		to, ok := a.Shortcuts[strings.Join(words, " ")]
		if !ok || len(to) == 0 {
			continue
		}
		out := append([]string{}, args[:i]...)
		out = append(out, to...)
		return append(out, args[i+n:]...)
	}
	return args
}

func hasFlagToken(words []string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			return true
		}
	}
	return false
}

// firstWord returns the index of the first command word, skipping root flags.
func (a *App) firstWord(args []string) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return -1
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return i
		}
		if takesValue(lookupFlag(a.Root, flagName(arg)), arg) {
			i++
		}
	}
	return -1
}

type argKind int

const (
	argWord argKind = iota
	argFlag
	argFlagValue
	argPositional
)

// classify resolves the deepest command the arguments name and labels each token as
// a command word, a flag, a flag's value or a positional argument.
func (a *App) classify(args []string) (*cobra.Command, []argKind) {
	kinds := make([]argKind, len(args))
	c := a.Root
	inPositionals := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			kinds[i] = argFlag
			for j := i + 1; j < len(args); j++ {
				kinds[j] = argPositional
			}
			return c, kinds
		case strings.HasPrefix(arg, "-") && arg != "-":
			kinds[i] = argFlag
			if takesValue(lookupFlag(c, flagName(arg)), arg) && i+1 < len(args) {
				kinds[i+1] = argFlagValue
				i++
			}
		case !inPositionals:
			if next := childNamed(c, arg); next != nil && !isCobraInternal(next) {
				kinds[i] = argWord
				c = next
				continue
			}
			inPositionals = true
			kinds[i] = argPositional
		default:
			kinds[i] = argPositional
		}
	}
	return c, kinds
}

// flagName returns the bare name of a flag token ("--a=b" -> "a", "-p" -> "p").
func flagName(arg string) string {
	name, _, _ := splitFlag(arg)
	return name
}

// takesValue reports whether a flag token consumes the next argument as its value.
func takesValue(f *pflag.Flag, arg string) bool {
	if f == nil || strings.Contains(arg, "=") {
		return false
	}
	return f.NoOptDefVal == ""
}

// lookupFlag finds a long flag (or a one-letter shorthand) on c, including inherited flags.
func lookupFlag(c *cobra.Command, name string) *pflag.Flag {
	if c == nil || name == "" {
		return nil
	}
	if f := c.Flags().Lookup(name); f != nil {
		return f
	}
	if f := c.InheritedFlags().Lookup(name); f != nil {
		return f
	}
	if len(name) == 1 {
		return shorthandFlag(c, name)
	}
	return nil
}

func shorthandFlag(c *cobra.Command, s string) *pflag.Flag {
	if f := c.Flags().ShorthandLookup(s); f != nil {
		return f
	}
	return c.InheritedFlags().ShorthandLookup(s)
}
