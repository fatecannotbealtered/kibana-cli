package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	updateNoticeCacheTTL       = 24 * time.Hour
	updateNoticeRefreshTimeout = 2 * time.Second
	updateNoticeEnvOptOut      = "KIBANA_CLI_NO_UPDATE_CHECK"
)

type updateNotice struct {
	Type               string   `json:"type"`
	Severity           string   `json:"severity"`
	Message            string   `json:"message"`
	CurrentVersion     string   `json:"current_version"`
	LatestVersion      string   `json:"latest_version"`
	UpdateAvailable    bool     `json:"update_available"`
	InstallMethod      string   `json:"install_method,omitempty"`
	RecommendedCommand string   `json:"recommended_command"`
	ReleaseURL         string   `json:"release_url,omitempty"`
	CheckedAt          string   `json:"checked_at"`
	Source             string   `json:"source"`
	NextSteps          []string `json:"next_steps"`
}

type updateNoticeCache struct {
	CheckedAt string         `json:"checked_at"`
	Notices   []updateNotice `json:"notices,omitempty"`
}

func installUpdateNoticeHelp(root *cobra.Command) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if cmd.Long != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), cmd.Long)
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
		} else if cmd.Short != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), cmd.Short)
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), cmd.UsageString())
		printUpdateNoticeHint(cmd.OutOrStdout(), readCachedUpdateNotices())
	})
}

func refreshUpdateNotices(ctx context.Context, source string) []updateNotice {
	if updateNoticeAutoDisabled() {
		return nil
	}
	refreshCtx, cancel := context.WithTimeout(ctx, updateNoticeRefreshTimeout)
	defer cancel()

	release, err := fetchUpdateRelease(refreshCtx, "")
	if err != nil {
		return readCachedUpdateNotices()
	}
	target := normalizeReleaseVersion(release.TagName)
	installPath, _ := updateExecutablePath()
	method := detectInstallMethod(installPath)
	notices := updateNoticesFromValues(version, target, method, release.HTMLURL, source)
	writeUpdateNoticeCache(notices)
	return notices
}

func updateNoticesFromResult(result updateResult, source string) []updateNotice {
	notices := updateNoticesFromValues(result.CurrentVersion, result.TargetVersion, result.InstallMethod, result.URL, source)
	writeUpdateNoticeCache(notices)
	return notices
}

func updateNoticesFromValues(current, latest, installMethod, releaseURL, source string) []updateNotice {
	current = normalizeReleaseVersion(current)
	latest = normalizeReleaseVersion(latest)
	if !updateAvailable(current, latest, false) {
		return nil
	}
	command := updateNoticeRecommendedCommand(installMethod, latest)
	notice := updateNotice{
		Type:               "update_available",
		Severity:           updateNoticeSeverity(current, latest),
		CurrentVersion:     current,
		LatestVersion:      latest,
		UpdateAvailable:    true,
		InstallMethod:      installMethod,
		RecommendedCommand: command,
		ReleaseURL:         releaseURL,
		CheckedAt:          time.Now().UTC().Format(time.RFC3339),
		Source:             source,
		NextSteps: []string{
			"run the recommended command",
			"ask the user before confirming the local self-update",
			"after update, run kibana-cli changelog --since " + current + " --compact",
			"refresh kibana-cli reference --compact before using new behavior",
		},
	}
	notice.Message = fmt.Sprintf("kibana-cli %s is available (current %s)", latest, current)
	return []updateNotice{notice}
}

// updateNoticeSeverity grades an available-update notice from the embedded
// CHANGELOG delta between the running version (current) and the latest. It
// returns "warning" when the delta contains a security entry OR the latest
// crosses a major version; otherwise "info" (CLI-SPEC §14).
func updateNoticeSeverity(current, latest string) string {
	if majorVersion(latest) > majorVersion(current) {
		return "warning"
	}
	for _, entry := range filterChangelogEntries(parseChangelog(changelogSource), current) {
		if len(entry.Changes["security"]) > 0 {
			return "warning"
		}
	}
	return "info"
}

// majorVersion returns the first semver component of v, or -1 when it cannot be
// parsed (so an unparseable version never spuriously triggers a major bump).
func majorVersion(v string) int {
	parts, ok := parseVersionParts(v)
	if !ok {
		return -1
	}
	return parts[0]
}

func updateNoticeRecommendedCommand(installMethod, latest string) string {
	switch strings.ToLower(strings.TrimSpace(installMethod)) {
	case "npm", "go":
		return updateInstallCommand(installMethod, latest)
	default:
		return "kibana-cli update --dry-run --compact"
	}
}

// cachedUpdateNoticesAsAny adapts the cached update notices to the generic
// []any the output layer expects, so the envelope builder can attach them to
// meta.notices without importing package cmd. Reads ONLY the local cache.
func cachedUpdateNoticesAsAny() []any {
	notices := readCachedUpdateNotices()
	if len(notices) == 0 {
		return nil
	}
	out := make([]any, 0, len(notices))
	for _, n := range notices {
		out = append(out, n)
	}
	return out
}

func readCachedUpdateNotices() []updateNotice {
	if updateNoticeAutoDisabled() {
		return nil
	}
	path, err := updateNoticeCachePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache updateNoticeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	checkedAt, err := time.Parse(time.RFC3339, cache.CheckedAt)
	if err != nil || time.Since(checkedAt) > updateNoticeCacheTTL {
		return nil
	}
	notices := make([]updateNotice, 0, len(cache.Notices))
	for _, notice := range cache.Notices {
		if notice.Type != "update_available" || !notice.UpdateAvailable {
			continue
		}
		// Re-validate against the version running now, not the version cached at
		// write time: within the cache TTL after an upgrade the entry would
		// otherwise keep advertising an update to a version we are already on.
		if !updateAvailable(version, notice.LatestVersion, false) {
			continue
		}
		notice.CurrentVersion = normalizeReleaseVersion(version)
		notice.Message = fmt.Sprintf("kibana-cli %s is available (current %s)", notice.LatestVersion, notice.CurrentVersion)
		notice.Source = "cache"
		notices = append(notices, notice)
	}
	return notices
}

func writeUpdateNoticeCache(notices []updateNotice) {
	if updateNoticeAutoDisabled() {
		return
	}
	path, err := updateNoticeCachePath()
	if err != nil {
		return
	}
	if len(notices) == 0 {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	cache := updateNoticeCache{CheckedAt: checkedAt, Notices: notices}
	for i := range cache.Notices {
		cache.Notices[i].CheckedAt = checkedAt
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

func updateNoticeCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	return filepath.Join(home, "."+toolName, "update-check.json"), nil
}

func updateNoticeDisabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(updateNoticeEnvOptOut)))
	return value == "1" || value == "true" || value == "yes"
}

// updateNoticeTestCacheEnabled lets tests opt back into real cache I/O, which is
// otherwise auto-disabled under the `.test` binary so unrelated tests never read
// or write the user's cache. Production code never sets this.
var updateNoticeTestCacheEnabled bool

func updateNoticeAutoDisabled() bool {
	if updateNoticeDisabled() {
		return true
	}
	if updateNoticeTestCacheEnabled {
		return false
	}
	exe := strings.ToLower(os.Args[0])
	return strings.HasSuffix(exe, ".test") || strings.HasSuffix(exe, ".test.exe")
}

func printUpdateNoticeHint(w io.Writer, notices []updateNotice) {
	if len(notices) == 0 {
		return
	}
	notice := notices[0]
	_, _ = fmt.Fprintf(w, "\nUpdate available: kibana-cli %s -> %s. Run: %s\n", notice.CurrentVersion, notice.LatestVersion, notice.RecommendedCommand)
}
