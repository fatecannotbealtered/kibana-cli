// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// The built-in update command (CLI-070, CLI-071). A tool turns it on with
// App.Update; it then discovers the GitHub release, and either drives the package
// manager that owns the install or, for a standalone binary, downloads the release,
// verifies the Sigstore signature of checksums.txt and the archive's checksum, runs
// the new binary once, swaps it in atomically and syncs the installed Skills from
// the release. update is outside the confirm gate: it changes the tool, not a
// target, so its registry entry declares only observe.

// UpdateConfig configures the built-in update command (App.Update).
type UpdateConfig struct {
	Repo        string // GitHub "owner/name" publishing the releases (required)
	Workflow    string // release workflow file that signs checksums.txt; default "release.yml"
	NPMPackage  string // npm wrapper package ("@scope/tool" or "tool"); "" when not on npm (also used for pnpm and yarn)
	GoPackage   string // main package for `go install <GoPackage>@v<version>`; "" when not go-installable
	PyPIPackage string // distribution name for pip; "" when not on PyPI
	BrewFormula string // Homebrew formula; "" when not on brew
	APIBase     string // default "https://api.github.com"; tests point it at a fake server
	// Test seams (nil = production behaviour):
	Verify        func(ctx context.Context, artifact, bundle []byte, identity, issuer string) error // default VerifySigstoreBundle
	Executable    func() (string, error)                                                            // default os.Executable with symlinks resolved
	InstallMethod func(exe string) string                                                           // default DetectInstallMethod
	Run           func(ctx context.Context, argv []string) ([]byte, error)                          // default exec without a shell, combined output
}

const (
	releaseJSONLimit = 4 << 20   // release JSON
	signedFileLimit  = 1 << 20   // checksums.txt and its Sigstore bundle
	archiveLimit     = 512 << 20 // the release archive
	extractedLimit   = 512 << 20 // any file extracted from it
	requestTimeout   = 30 * time.Second
	managerTimeout   = 10 * time.Minute
	versionTimeout   = 30 * time.Second
	refreshTimeout   = 3 * time.Second // doctor's refresh of the notice cache
)

var (
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	workflowPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+\.ya?ml$`)
)

// checkUpdateConfig reports App.Update problems (tree check).
func (a *App) checkUpdateConfig() []string {
	if a.Update == nil {
		return nil
	}
	var problems []string
	if !repoPattern.MatchString(a.Update.Repo) {
		problems = append(problems, fmt.Sprintf("App.Update.Repo %q is not a GitHub \"owner/name\"", a.Update.Repo))
	}
	if w := a.Update.Workflow; w != "" && !workflowPattern.MatchString(w) {
		problems = append(problems, fmt.Sprintf("App.Update.Workflow %q is not a workflow file name like release.yml", w))
	}
	return problems
}

// installUpdate registers the built-in update command (InstallBuiltins).
func (a *App) installUpdate(add func(c, parent *cobra.Command, m Meta)) {
	tool := a.toolName()
	up := &cobra.Command{Use: "update", Short: "Update the tool and its Skills to the latest release, or to --target-version",
		RunE: a.runUpdate}
	up.Flags().Bool("check", false, "Only look: report whether a newer release exists and change nothing (same as --dry-run)")
	up.Flags().String("target-version", "", "Install this version (strict semver, e.g. 2.1.0) instead of the latest release")
	up.Flags().Bool("allow-downgrade", false, "Allow --target-version to be older than the installed version")
	a.Schema("aicli_update", SchemaDef{Shape: "object",
		Fields: append(append([]string{}, updateRequiredKeys...), updateOptionalKeys...)})
	add(up, a.Root, Meta{Summary: up.Short, Effects: []Effect{Observe}, Output: "aicli_update",
		ErrorCodes: append([]string{}, updateErrorCodes...),
		Examples:   []string{tool + " update --check", tool + " update", tool + " update --target-version 2.1.0"}})
	if a.reg.builtin[up] {
		a.updateCmd = up
		a.reg.meta[up].Evidence = []string{"simulated:https://github.com/fatecannotbealtered/ai-native-cli-spec/blob/v" +
			SpecVersion + "/template/go/aicli/update_test.go"}
	}
}

// updateRun is one run of update, and the state its failure details report.
type updateRun struct {
	a       *App
	cfg     UpdateConfig
	ctx     context.Context
	client  *http.Client
	tool    string
	current string // the version running now

	stage      string
	target     string // once known
	method     string // once known
	previous   string
	dispatched bool // a package manager ran or the swap happened
	lost       bool // the swap failed and the old binary could not be moved back: the install state is unknown
	replaced   bool
	sigStatus  string
}

func (a *App) newUpdate(ctx context.Context) *updateRun {
	cfg := *a.Update
	if cfg.Workflow == "" {
		cfg.Workflow = "release.yml"
	}
	if cfg.APIBase == "" {
		cfg.APIBase = "https://api.github.com"
	}
	if cfg.Executable == nil {
		cfg.Executable = executablePath
	}
	if cfg.InstallMethod == nil {
		cfg.InstallMethod = DetectInstallMethod
	}
	if cfg.Run == nil {
		cfg.Run = runArgv
	}
	return &updateRun{a: a, cfg: cfg, ctx: ctx, tool: a.toolName(), current: a.version(), sigStatus: "not_checked"}
}

func (a *App) runUpdate(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	// SIGINT already cancels the run's context (Execute); update also stops on SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer stop()
	data, e := a.newUpdate(ctx).run(cmd)
	if e != nil {
		return e
	}
	return a.OK(cmd, data)
}

// begin enters a stage (and logs it once when <PREFIX>_DEBUG is set).
func (u *updateRun) begin(stage string) {
	if u.stage != stage {
		u.stage = stage
		u.a.Debugf("update stage: %s", stage)
	}
}

// details are the update failure details (CLI-070) for the current state.
func (u *updateRun) details() map[string]any {
	d := map[string]any{"stage": u.stage, "current_version": u.current, "binary_replaced": u.replaced,
		"skill_sync_status": "skipped", "dispatch_state": "not_started"}
	if u.replaced {
		d["current_version"] = u.target
	}
	if u.dispatched {
		d["dispatch_state"] = "dispatched"
	}
	if u.lost {
		d["dispatch_state"] = "unknown"
	}
	if u.target != "" {
		d["target_version"] = u.target
	}
	if u.method != "" {
		d["install_method"] = u.method
	}
	return d
}

func (u *updateRun) fail(code, message, next string) *Error {
	d := u.details()
	d["next_action"] = next
	return NewError(code, message, d)
}

// decorate adds the update failure details to an error built elsewhere.
func (u *updateRun) decorate(e *Error) *Error {
	for k, v := range u.details() {
		if _, has := e.Details[k]; !has {
			e.Details[k] = v
		}
	}
	return e
}

// interrupted reports SIGINT or SIGTERM (the run's context was cancelled).
func (u *updateRun) interrupted(err error) bool {
	return errors.Is(u.ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled)
}

func (u *updateRun) interruptError() *Error {
	return u.fail("E_INTERRUPTED",
		fmt.Sprintf("The update was interrupted before anything was installed; %s is still %s.", u.tool, u.current),
		fmt.Sprintf("Run `%s update` again when ready.", u.tool))
}

// partial is E_PARTIAL: the new version is installed (or was, for a sync-only
// run) but the Skill sync failed.
func (u *updateRun) partial(cause error) *Error {
	e := NewError("E_PARTIAL",
		fmt.Sprintf("%s was updated but its Skills were not synced, so the installed Skill still describes the old version.", u.tool),
		map[string]any{
			"dispatch_state":     "dispatched",
			"next_action":        fmt.Sprintf("Run `%s update` again to finish the Skill sync, then read `%s changelog --since %s`.", u.tool, u.tool, u.previous),
			"status":             "partial",
			"stage":              "skill_sync",
			"binary_replaced":    true,
			"skill_sync_status":  "failed",
			"skill_sync_command": u.tool + " update",
			"previous_version":   u.previous,
			"target_version":     u.target,
			"current_version":    u.target,
			"update_available":   false,
			"install_method":     u.method,
			"signature_status":   u.sigStatus,
		})
	e.Cause = cause
	return e
}

// withSyncCommand adds skill_sync_command only when the Skill sync could not finish
// (F10). Not for skipped, not_needed or synced: update installs no missing Skill, so
// the command would answer the same again.
func (u *updateRun) withSyncCommand(d map[string]any) map[string]any {
	if d["skill_sync_status"] == "failed" {
		d["skill_sync_command"] = u.tool + " update"
	}
	return d
}

// record refreshes the notice cache (CLI-071); a cache that cannot be written is
// not the update's failure.
func (u *updateRun) record(latest Version, severity string) {
	_ = u.a.RecordUpdateCheck(latest.String(), severity)
}

// run is the update flow: discover, then check, up to date, the package-manager
// path or the binary path, then the Skill sync.
func (u *updateRun) run(cmd *cobra.Command) (map[string]any, *Error) {
	u.begin("discover")
	flags := cmd.Flags()
	if flags.Changed("confirm") {
		e := u.fail("E_USAGE",
			fmt.Sprintf("update takes no --confirm token; run `%s update`, or `%s update --check` to only look.", u.tool, u.tool),
			fmt.Sprintf("Run `%s update`, or `%s update --check` to only look.", u.tool, u.tool))
		e.Details["problem"] = "invalid_value"
		e.Details["parameter"] = "--confirm"
		return nil, e
	}
	if !repoPattern.MatchString(u.cfg.Repo) {
		return nil, internalError("App.Update.Repo is not a GitHub owner/name.")
	}
	// A development build (App.Version not a semantic version, P14) has no release
	// to compare with or to replace: nothing is contacted or downloaded.
	if u.a.devBuild() {
		return nil, u.fail("E_UNSUPPORTED", fmt.Sprintf("%s %s is a development build; install a release to update.", u.tool, u.current),
			fmt.Sprintf("Install %s with the install command in its README.", u.tool))
	}
	check, _ := flags.GetBool("check")
	if dry, _ := flags.GetBool("dry-run"); dry {
		check = true
	}
	allowDowngrade, _ := flags.GetBool("allow-downgrade")
	var want *Version
	if flags.Changed("target-version") {
		raw, _ := flags.GetString("target-version")
		v, e := CheckUpdateTarget(u.current, raw, allowDowngrade)
		if e != nil {
			return nil, u.decorate(e)
		}
		want = &v
		u.target = v.String()
	}
	exe, err := u.cfg.Executable()
	if err != nil {
		exe = ""
	}
	method := u.cfg.InstallMethod(exe)
	if !contains(updateInstallMethods, method) {
		return nil, internalError("UpdateConfig.InstallMethod returned %q, which is not an install method.", truncateRunes(method, 20))
	}
	u.method = method
	if e := u.httpClient(); e != nil {
		return nil, e
	}
	if method == "binary" && !check {
		removeStaleBinaries(exe)
	}
	rel, target, severity, e := u.discover(want)
	if e != nil {
		return nil, e
	}
	u.target = target.String()
	cur, curErr := ParseVersion(u.current)
	newer := curErr != nil || CompareVersions(target, cur) > 0
	downgrade := want != nil && allowDowngrade && curErr == nil && CompareVersions(target, cur) < 0
	install := newer || downgrade
	// The notice cache holds the real latest release, as discovery found it; a
	// --target-version is not news about the latest (E13).
	if check {
		if want == nil {
			u.record(target, severity)
		}
		d := map[string]any{"status": "up_to_date", "current_version": u.current, "target_version": u.current,
			"update_available": false, "install_method": u.method, "signature_status": "not_checked", "skill_sync_status": "skipped"}
		if install {
			d["status"], d["target_version"], d["update_available"] = "available", target.String(), true
			if argv, err := u.managerArgv(target); err == nil {
				d["command"] = argv
			}
		}
		return d, nil
	}
	u.previous = u.current
	if !install {
		// Already current: no download, no package manager; finish a Skill sync an
		// earlier run left undone, for this tool's own Skills only (E3).
		if want == nil {
			u.record(target, severity)
		}
		u.target = u.current
		status, err := u.syncSkills(exe, u.current, u.ownSkills(exe))
		if err != nil {
			return nil, u.partial(err)
		}
		return u.withSyncCommand(map[string]any{"status": "up_to_date", "current_version": u.current,
			"target_version": u.current, "update_available": false, "install_method": u.method,
			"signature_status": "not_checked", "skill_sync_status": status, "binary_replaced": false}), nil
	}
	if u.method == "binary" {
		return u.installBinary(exe, rel, target)
	}
	return u.installWithManager(exe, target)
}

// packageName is the package the install method's manager installs.
func (u *updateRun) packageName() string {
	switch u.method {
	case "npm", "pnpm", "yarn":
		return u.cfg.NPMPackage
	case "go":
		return u.cfg.GoPackage
	case "pip":
		return u.cfg.PyPIPackage
	case "brew":
		return u.cfg.BrewFormula
	}
	return ""
}

// managerArgv is the package-manager argv that installs target.
func (u *updateRun) managerArgv(target Version) ([]string, error) {
	pkg := u.packageName()
	if pkg == "" {
		return nil, errors.New("no package name")
	}
	return PackageManagerArgs(u.method, pkg, target)
}

// installWithManager is the package-manager path (stage replace).
func (u *updateRun) installWithManager(exe string, target Version) (map[string]any, *Error) {
	u.begin("replace")
	manager := u.method
	if u.packageName() == "" {
		return nil, u.fail("E_CONFIG",
			fmt.Sprintf("%s does not know its %s package name, so it cannot update this install.", u.tool, manager),
			fmt.Sprintf("Reinstall %s with the install command in its README.", u.tool))
	}
	argv, err := u.managerArgv(target)
	if err != nil {
		return nil, u.fail("E_UNSUPPORTED", fmt.Sprintf("%s cannot install %s %s: %s.", manager, u.tool, target.String(), err),
			fmt.Sprintf("Install %s with the package-manager command in its README instead.", u.tool))
	}
	command := strings.Join(argv, " ")
	// A running package manager is not killed by an interrupt (E9): it runs under a
	// context that is not cancelled, within the 10-minute cap, and SIGINT and SIGTERM
	// are held until it and the version check finish, so the run reports what
	// happened.
	release := holdInterrupts()
	defer release()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(u.ctx), managerTimeout)
	_, runErr := u.cfg.Run(ctx, argv)
	cancel()
	// A manager that never started sent nothing (E13): exec.ErrDot is a manager found
	// only in the current directory, which update never runs.
	if runErr != nil && (errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, exec.ErrDot)) {
		return nil, u.fail("E_CONFIG", fmt.Sprintf("%s was not found on PATH.", manager),
			fmt.Sprintf("Install %s, or reinstall %s with the install command in its README.", manager, u.tool))
	}
	u.dispatched = true
	observed := u.installedVersion(exe, target)
	if !sameVersion(observed, target) {
		if runErr != nil {
			e := u.fail("E_IO", fmt.Sprintf("`%s` failed, so %s is still %s.", command, u.tool, u.current),
				fmt.Sprintf("Run `%s` yourself, then run `%s update` to sync the Skills.", command, u.tool))
			e.Details["command"] = argv
			return nil, e
		}
		shown := observed
		if shown == "" {
			shown = "an unknown version"
		}
		return nil, u.fail("E_IO", fmt.Sprintf("%s finished, but `%s --version` still reports %s.", manager, u.tool, shown),
			fmt.Sprintf("Check which %s comes first on PATH, then run `%s update` again.", u.tool, u.tool))
	}
	u.replaced = true
	status, err := u.syncSkills(exe, target.String(), nil)
	if err != nil {
		return nil, u.partial(err)
	}
	return u.withSyncCommand(map[string]any{"status": "updated", "current_version": target.String(),
		"target_version": target.String(), "previous_version": u.previous, "update_available": false,
		"install_method": u.method, "signature_status": "not_checked", "signature_verified": false,
		"skill_sync_status": status, "binary_replaced": true, "command": argv}), nil
}

// lookPath finds a program on PATH (a test seam). exec.LookPath never resolves a
// program from the current directory: it answers exec.ErrDot instead.
var lookPath = exec.LookPath

// installedVersion is the version the installed tool reports after a package
// manager ran (E6): first the tool PATH finds by its command name, since a manager
// may install it at a new path (pnpm 11's store does), then the executable that runs
// this update. The target version from either decides; otherwise the first version
// seen is returned.
func (u *updateRun) installedVersion(exe string, target Version) string {
	var candidates []string
	if p, err := lookPath(u.tool); err == nil && p != "" {
		candidates = append(candidates, p)
	}
	if exe != "" && !contains(candidates, exe) {
		candidates = append(candidates, exe)
	}
	first := ""
	for _, c := range candidates {
		v, _ := u.observeVersion(c, true)
		if sameVersion(v, target) {
			return v
		}
		if first == "" {
			first = v
		}
	}
	return first
}

// holdInterrupts keeps SIGINT and SIGTERM from ending the process until the returned
// function is called (E9): while a signal is subscribed, Go does not apply its
// default action.
func holdInterrupts() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return func() { signal.Stop(ch) }
}

// observeVersion runs `<exe> --version` and returns the version it reports
// (versionFromOutput; "" when it says nothing readable) and whether it ran and
// exited zero. After a package manager ran (afterInstall) it runs even when the
// update was interrupted.
func (u *updateRun) observeVersion(exe string, afterInstall bool) (string, bool) {
	if exe == "" {
		return "", false
	}
	parent := u.ctx
	if afterInstall {
		parent = context.WithoutCancel(u.ctx)
	}
	ctx, cancel := context.WithTimeout(parent, versionTimeout)
	defer cancel()
	out, err := u.cfg.Run(ctx, []string{exe, "--version"})
	return versionFromOutput(out), err == nil
}

// versionFromOutput reads data.version from the first JSON envelope line of a
// --version run, else a plain-text version (a 1.x tool prints "gitlab-cli version
// 1.2.17"): the last word of the first line whose last word is a semantic version.
func versionFromOutput(out []byte) string {
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var env struct {
			OK   bool `json:"ok"`
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &env) != nil || !env.OK {
			continue
		}
		if v, err := ParseVersion(env.Data.Version); err == nil {
			return v.String()
		}
		return truncateRunes(env.Data.Version, 40)
	}
	for _, line := range lines {
		if words := strings.Fields(line); len(words) > 0 {
			if v, err := ParseVersion(words[len(words)-1]); err == nil {
				return v.String()
			}
		}
	}
	return ""
}

// sameVersion reports whether observed is the target version.
func sameVersion(observed string, target Version) bool {
	v, err := ParseVersion(observed)
	return err == nil && CompareVersions(v, target) == 0
}

// refreshUpdateNotice is doctor's refresh of the notice cache (CLI-071): the
// latest release only, within 3 s, every failure ignored.
func (a *App) refreshUpdateNotice(ctx context.Context) {
	if a.Update == nil || a.offline || !repoPattern.MatchString(a.Update.Repo) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	u := a.newUpdate(ctx)
	if u.httpClient() != nil {
		return
	}
	body, err := u.fetch(strings.TrimRight(u.cfg.APIBase, "/")+"/repos/"+u.cfg.Repo+"/releases/latest", releaseJSONLimit, nil, false)
	if err != nil {
		return
	}
	var rel ghRelease
	if json.Unmarshal(body, &rel) != nil || rel.Draft || rel.Prerelease {
		return
	}
	// A prerelease tag is never the latest, whatever GitHub's flag says (E2).
	v, err := ParseVersion(rel.TagName)
	if err != nil || v.Pre != "" {
		return
	}
	_ = a.RecordUpdateCheck(v.String(), updateSeverity(a.version(), v, []ghRelease{rel}))
}

// ---------------------------------------------------------------- install method

// DetectInstallMethod names what owns the installed executable (CLI-070), first
// match wins: pnpm 11's global store (store/v<digits>/links segments) is pnpm; a
// node_modules path is pnpm (a .pnpm segment), yarn (a .yarn segment or
// /yarn/global/) or npm; a Cellar segment, /opt/homebrew/ or /home/linuxbrew/ is
// brew; a binary with Go module build info in GOBIN, else GOPATH/bin, else ~/go/bin
// is go; anything else is binary (the tool replaces its own file).
func DetectInstallMethod(exe string) string {
	if exe == "" {
		return "binary"
	}
	slashed := filepath.ToSlash(exe)
	segs := strings.FieldsFunc(exe, func(r rune) bool { return r == '/' || r == '\\' })
	switch {
	case pnpmStore(segs):
		return "pnpm"
	case contains(segs, "node_modules"):
		switch {
		case contains(segs, ".pnpm"):
			return "pnpm"
		case contains(segs, ".yarn") || strings.Contains(slashed, "/yarn/global/"):
			return "yarn"
		}
		return "npm"
	case contains(segs, "Cellar") || strings.HasPrefix(slashed, "/opt/homebrew/") || strings.HasPrefix(slashed, "/home/linuxbrew/"):
		return "brew"
	case goInstalled(exe):
		return "go"
	}
	return "binary"
}

var pnpmStoreVersion = regexp.MustCompile(`^v[0-9]+$`)

// pnpmStore reports a path inside pnpm 11's global store: the segments store,
// v<digits> and links in a row (E6).
func pnpmStore(segs []string) bool {
	for i := 0; i+2 < len(segs); i++ {
		if segs[i] == "store" && pnpmStoreVersion.MatchString(segs[i+1]) && segs[i+2] == "links" {
			return true
		}
	}
	return false
}

// goInstalled reports a Go-built executable in the directory `go install` uses.
func goInstalled(exe string) bool {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Path == "" {
		return false
	}
	bin := os.Getenv("GOBIN")
	if bin == "" {
		if gopath := os.Getenv("GOPATH"); gopath != "" {
			bin = filepath.Join(filepath.SplitList(gopath)[0], "bin")
		} else if home, err := os.UserHomeDir(); err == nil && home != "" {
			bin = filepath.Join(home, "go", "bin")
		}
	}
	if bin == "" {
		return false
	}
	dir, want := filepath.Clean(filepath.Dir(exe)), filepath.Clean(bin)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(dir, want)
	}
	return dir == want
}

// executablePath is os.Executable with symlinks resolved.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// runArgv runs argv without a shell and returns its combined output.
func runArgv(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("no command to run")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 5 * time.Second
	return cmd.CombinedOutput()
}
