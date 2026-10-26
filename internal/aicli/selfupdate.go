// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Self-update helpers (CLI-070, CLI-071), used by the built-in update command
// (update.go) and exported for tools: targets are strict semver, package-manager
// argv is built only from validated parts (never through a shell), and downgrades
// need an explicit flag.

// Version is a parsed semantic version (https://semver.org).
type Version struct {
	Major, Minor, Patch int
	Pre, Build          string
}

// String renders the version without a leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// core is the version without build metadata (what package managers install).
func (v Version) core() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
	`(?:-((?:0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
	`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// ParseVersion parses a strict semantic version with an optional leading "v".
// Anything else (spaces, shell characters, leading zeros, missing parts) is an error.
func ParseVersion(s string) (Version, error) {
	if len(s) > 128 {
		return Version{}, errors.New("version is too long")
	}
	t := strings.TrimPrefix(s, "v")
	m := semverPattern.FindStringSubmatch(t)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a semantic version like 1.2.3", truncateRunes(s, 40))
	}
	var nums [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, errors.New("version number is out of range")
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: m[4], Build: m[5]}, nil
}

// CompareVersions orders versions by semver precedence (build metadata ignored):
// -1 when a < b, 0 when equal, 1 when a > b.
func CompareVersions(a, b Version) int {
	for _, p := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	ai, bi := strings.Split(a.Pre, "."), strings.Split(b.Pre, ".")
	for i := 0; i < len(ai) && i < len(bi); i++ {
		if c := compareIdent(ai[i], bi[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(ai) < len(bi):
		return -1
	case len(ai) > len(bi):
		return 1
	}
	return 0
}

func compareIdent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

var (
	npmName  = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)
	goModule = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}(?:/[A-Za-z0-9._~-]+)+$`)
	pipName  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	brewName = regexp.MustCompile(`^[a-z0-9][a-z0-9@._+-]*(?:/[a-z0-9][a-z0-9._-]*){0,2}$`)
)

// PackageManagerArgs builds the argv that installs pkg at version v with a package
// manager: npm, pnpm, yarn, go, pip or brew. Every part is validated against a
// strict character set, so the argv is safe to run without a shell, including
// through npm.cmd on Windows. brew cannot pin a version: its argv upgrades to the
// formula's current version, which the caller must compare with v afterwards. pip
// accepts release versions only (no pre-release). Build metadata is never passed on.
func PackageManagerArgs(manager, pkg string, v Version) ([]string, error) {
	if strings.HasPrefix(pkg, "-") || len(pkg) > 214 {
		return nil, fmt.Errorf("package name %q is not allowed", truncateRunes(pkg, 40))
	}
	ver := v.core()
	if !semverPattern.MatchString(ver) {
		return nil, errors.New("version is not a semantic version")
	}
	switch manager {
	case "npm", "pnpm", "yarn":
		if !npmName.MatchString(pkg) {
			return nil, fmt.Errorf("%q is not a valid npm package name", truncateRunes(pkg, 40))
		}
		spec := pkg + "@" + ver
		switch manager {
		case "npm":
			return []string{"npm", "install", "-g", spec}, nil
		case "pnpm":
			return []string{"pnpm", "add", "-g", spec}, nil
		}
		return []string{"yarn", "global", "add", spec}, nil
	case "go":
		if !goModule.MatchString(pkg) || strings.Contains(pkg, "..") {
			return nil, fmt.Errorf("%q is not a valid Go package path", truncateRunes(pkg, 40))
		}
		return []string{"go", "install", pkg + "@v" + ver}, nil
	case "pip":
		if !pipName.MatchString(pkg) {
			return nil, fmt.Errorf("%q is not a valid Python package name", truncateRunes(pkg, 40))
		}
		if v.Pre != "" {
			return nil, errors.New("pip installs release versions only")
		}
		return []string{"pip", "install", "--upgrade", pkg + "==" + ver}, nil
	case "brew":
		if !brewName.MatchString(pkg) {
			return nil, fmt.Errorf("%q is not a valid Homebrew formula name", truncateRunes(pkg, 40))
		}
		return []string{"brew", "upgrade", pkg}, nil
	}
	return nil, fmt.Errorf("unsupported package manager %q (use npm, pnpm, yarn, go, pip or brew)", truncateRunes(manager, 20))
}

// CheckUpdateTarget validates an update target (CLI-070): it must be strict semver
// (E_USAGE invalid_value otherwise) and not older than the installed version unless
// allowDowngrade (the --allow-downgrade flag). A development build (unparseable
// current version) accepts any valid target.
func CheckUpdateTarget(current, target string, allowDowngrade bool) (Version, *Error) {
	t, err := ParseVersion(target)
	if err != nil {
		e := Usage("invalid_value", "--target-version", "The target version is not a semantic version; nothing was installed.")
		e.Details["expected"] = "semantic version, e.g. 1.2.3"
		return Version{}, e
	}
	c, err := ParseVersion(current)
	if err != nil {
		return t, nil
	}
	if CompareVersions(t, c) < 0 && !allowDowngrade {
		e := Usage("invalid_value", "--target-version", "The target is older than the installed version; nothing was installed.")
		e.Details["expected"] = "a version newer than " + c.String() + ", or " + downgradeFlag
		e.Details["next_action"] = "Ask the user before downgrading; then add " + downgradeFlag + "."
		return Version{}, e
	}
	return t, nil
}

// updateCache is <state>/update-check.json, written by the tool's update check.
type updateCache struct {
	CheckedAt      string `json:"checked_at"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Severity       string `json:"severity"`
	Command        string `json:"command"`
	NotifiedAt     string `json:"notified_at,omitempty"`
}

// RecordUpdateCheck stores the latest released version found by update (and
// doctor's refresh) (CLI-071). Commands then attach an update_available notice from
// this cache only (no network), at most once a day, while the installed version is
// older. severity is info or warning (security fix or major version).
func (a *App) RecordUpdateCheck(latest, severity string) error {
	lv, err := ParseVersion(latest)
	if err != nil {
		return Usage("invalid_value", "latest", "The latest version is not a semantic version.")
	}
	severity = noticeSeverity(severity, a.version(), lv)
	dir, e := a.ensureStateDir()
	if e != nil {
		return e
	}
	c := updateCache{CheckedAt: timeString(a.now()), CurrentVersion: a.version(), LatestVersion: latest, Severity: severity,
		Command: a.toolName() + " update"}
	b, err := json.Marshal(c)
	if err != nil {
		return internalError("The update cache cannot be encoded.")
	}
	if err := writeFileAtomic(dir, "update-check.json", append(b, '\n')); err != nil {
		return ioError("The update cache cannot be written.", fmt.Sprintf("Check %s.", dir), err)
	}
	return nil
}

// noticeSeverity is an update notice's severity (CLI-071): the given one when valid
// (info otherwise), and always warning when the update crosses a major version.
func noticeSeverity(severity, current string, latest Version) string {
	if !contains(noticeSeverityValues, severity) {
		severity = "info"
	}
	if cur, err := ParseVersion(current); err == nil && latest.Major > cur.Major {
		severity = "warning"
	}
	return severity
}

// attachUpdateNotice adds the cached update notice to meta.notices (CLI-047): local
// cache only, at most once a day, never when the installed version is current.
func (a *App) attachUpdateNotice(st *runState) {
	dir, e := a.stateDirPath()
	if e != nil {
		return
	}
	path := filepath.Join(dir, "update-check.json")
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 4096 {
		return
	}
	var c updateCache
	if json.Unmarshal(b, &c) != nil {
		return
	}
	cur, err1 := ParseVersion(a.version())
	latest, err2 := ParseVersion(c.LatestVersion)
	if err1 != nil || err2 != nil || CompareVersions(latest, cur) <= 0 {
		return
	}
	now := a.now()
	if t, ok := parseTime(c.NotifiedAt); ok && now.Sub(t) < 24*time.Hour && now.After(t) {
		return
	}
	severity := noticeSeverity(c.Severity, a.version(), latest)
	command := a.toolName() + " update"
	a.Notice(Notice{Type: "update_available", Severity: severity,
		Message:        fmt.Sprintf("%s %s is available (installed %s); run `%s`.", a.toolName(), latest.String(), cur.String(), command),
		CurrentVersion: cur.String(), LatestVersion: latest.String(), Command: command, CheckedAt: c.CheckedAt})
	c.NotifiedAt = timeString(now)
	if nb, err := json.Marshal(c); err == nil {
		_ = writeFileAtomic(dir, "update-check.json", append(nb, '\n'))
	}
}
