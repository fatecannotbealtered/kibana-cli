package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// overwriteCacheCheckedAt rewrites the cache file's checked_at so a TTL-expiry
// path can be exercised without sleeping.
func overwriteCacheCheckedAt(path string, checkedAt time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cache updateNoticeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return err
	}
	cache.CheckedAt = checkedAt.UTC().Format(time.RFC3339)
	out, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// enableNoticeCacheForTest opts the `.test` binary back into real notice-cache
// I/O and points HOME at a temp dir, so meta.notices can be exercised end to end.
func enableNoticeCacheForTest(t *testing.T) {
	t.Helper()
	setupTestHome(t)
	updateNoticeTestCacheEnabled = true
	t.Cleanup(func() { updateNoticeTestCacheEnabled = false })
}

func seedUpdateNoticeCache(t *testing.T, severity string) {
	t.Helper()
	writeUpdateNoticeCache([]updateNotice{{
		Type:               "update_available",
		Severity:           severity,
		Message:            "kibana-cli 9.9.9 is available (current 1.0.0)",
		CurrentVersion:     "1.0.0",
		LatestVersion:      "9.9.9",
		UpdateAvailable:    true,
		RecommendedCommand: "kibana-cli update --dry-run --compact",
		CheckedAt:          time.Now().UTC().Format(time.RFC3339),
		Source:             "update --check",
		NextSteps:          []string{"run the recommended command"},
	}})
}

func metaFromOutput(t *testing.T, out string) map[string]any {
	t.Helper()
	payload := envelopePayload(t, out)
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("missing meta: %s", out)
	}
	return meta
}

// TASK C: meta.notices appears on an arbitrary (non-update) command when the
// cache holds an available update.
func TestMetaNotices_PresentWhenCachePopulated(t *testing.T) {
	enableNoticeCacheForTest(t)
	seedUpdateNoticeCache(t, "info")

	out, code := runCLI(t, []string{"changelog", "--format", "json", "--compact"})
	if code != ExitOK {
		t.Fatalf("changelog exit %d: %s", code, out)
	}
	meta := metaFromOutput(t, out)
	notices, ok := meta["notices"].([]any)
	if !ok || len(notices) == 0 {
		t.Fatalf("expected meta.notices on a non-update command, got: %s", out)
	}
	notice, ok := notices[0].(map[string]any)
	if !ok {
		t.Fatalf("notice is not an object: %s", out)
	}
	if notice["type"] != "update_available" {
		t.Fatalf("unexpected notice type: %v", notice["type"])
	}
	if notice["latest_version"] != "9.9.9" {
		t.Fatalf("unexpected latest_version: %v", notice["latest_version"])
	}
}

// TASK C: meta.notices is ABSENT when the cache is empty.
func TestMetaNotices_AbsentWhenCacheEmpty(t *testing.T) {
	enableNoticeCacheForTest(t)
	// No cache seeded.

	out, code := runCLI(t, []string{"changelog", "--format", "json", "--compact"})
	if code != ExitOK {
		t.Fatalf("changelog exit %d: %s", code, out)
	}
	meta := metaFromOutput(t, out)
	if _, present := meta["notices"]; present {
		t.Fatalf("expected meta.notices to be omitted when cache empty, got: %s", out)
	}
}

// TASK C: meta.notices is ABSENT when the cache has expired.
func TestMetaNotices_AbsentWhenCacheExpired(t *testing.T) {
	enableNoticeCacheForTest(t)
	seedUpdateNoticeCache(t, "info")

	// Backdate the cache past its TTL.
	path, err := updateNoticeCachePath()
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().UTC().Add(-2 * updateNoticeCacheTTL)
	if err := overwriteCacheCheckedAt(path, stale); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, []string{"changelog", "--format", "json", "--compact"})
	if code != ExitOK {
		t.Fatalf("changelog exit %d: %s", code, out)
	}
	meta := metaFromOutput(t, out)
	if _, present := meta["notices"]; present {
		t.Fatalf("expected meta.notices to be omitted when cache expired, got: %s", out)
	}
}

// TASK C: the meta.notices piggyback path makes no network call — a business
// command must never contact the GitHub release API (the http/test seam).
func TestMetaNotices_NoNetworkOnBusinessCommand(t *testing.T) {
	enableNoticeCacheForTest(t)
	seedUpdateNoticeCache(t, "info")

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	origBase := updateGitHubAPIBase
	updateGitHubAPIBase = srv.URL
	t.Cleanup(func() { updateGitHubAPIBase = origBase })

	out, code := runCLI(t, []string{"changelog", "--format", "json", "--compact"})
	if code != ExitOK {
		t.Fatalf("changelog exit %d: %s", code, out)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("meta.notices path made %d network call(s); expected 0", got)
	}
	// The notice must still be attached, read purely from the local cache.
	if _, present := metaFromOutput(t, out)["notices"]; !present {
		t.Fatalf("expected cached meta.notices without any network call, got: %s", out)
	}
}

// TASK C: severity grading — a delta containing a security entry grades warning.
func TestUpdateNoticeSeverity_SecurityEntryWarning(t *testing.T) {
	orig := changelogSource
	changelogSource = `# Changelog

## [1.1.0] - 2026-06-07

### Security

- fixed a credential leak

## [1.0.0] - 2026-06-01

### Added

- initial release
`
	t.Cleanup(func() { changelogSource = orig })

	if got := updateNoticeSeverity("1.0.0", "1.1.0"); got != "warning" {
		t.Fatalf("security delta: severity = %q, want warning", got)
	}
}

// TASK C: severity grading — a major bump grades warning even without a
// security entry in the delta.
func TestUpdateNoticeSeverity_MajorBumpWarning(t *testing.T) {
	orig := changelogSource
	changelogSource = `# Changelog

## [2.0.0] - 2026-06-07

### Changed

- rewrote the query engine

## [1.0.0] - 2026-06-01

### Added

- initial release
`
	t.Cleanup(func() { changelogSource = orig })

	if got := updateNoticeSeverity("1.5.0", "2.0.0"); got != "warning" {
		t.Fatalf("major bump: severity = %q, want warning", got)
	}
}

// TASK C: severity grading — a plain patch with no security entry grades info.
func TestUpdateNoticeSeverity_PatchInfo(t *testing.T) {
	orig := changelogSource
	changelogSource = `# Changelog

## [1.0.1] - 2026-06-07

### Fixed

- corrected a typo

## [1.0.0] - 2026-06-01

### Added

- initial release
`
	t.Cleanup(func() { changelogSource = orig })

	if got := updateNoticeSeverity("1.0.0", "1.0.1"); got != "info" {
		t.Fatalf("patch delta: severity = %q, want info", got)
	}
}

// A cached "update available" notice must not survive once the running binary
// has caught up to (or passed) the cached latest version. Otherwise, for the
// whole cache-TTL window after an upgrade, an already-current CLI keeps
// advertising an update to the version it is already on.
func TestReadCachedUpdateNotices_DropsStaleAfterUpgrade(t *testing.T) {
	enableNoticeCacheForTest(t)
	writeUpdateNoticeCache([]updateNotice{{
		Type:            "update_available",
		Severity:        "info",
		Message:         "kibana-cli 1.1.9 is available (current 1.1.8)",
		CurrentVersion:  "1.1.8",
		LatestVersion:   "1.1.9",
		UpdateAvailable: true,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		Source:          "doctor",
	}})

	orig := version
	version = "1.1.9" // user upgraded to the cached latest
	t.Cleanup(func() { version = orig })

	if got := readCachedUpdateNotices(); len(got) != 0 {
		t.Fatalf("expected no notice once running version caught up to cached latest, got %d: %+v", len(got), got)
	}
}

// While the running version is still behind the cached latest, the notice is
// kept, but its current_version/message reflect the running binary rather than
// the (now-superseded) version cached at write time.
func TestReadCachedUpdateNotices_RefreshesCurrentVersion(t *testing.T) {
	enableNoticeCacheForTest(t)
	writeUpdateNoticeCache([]updateNotice{{
		Type:            "update_available",
		Severity:        "info",
		Message:         "kibana-cli 1.1.9 is available (current 1.1.7)",
		CurrentVersion:  "1.1.7",
		LatestVersion:   "1.1.9",
		UpdateAvailable: true,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		Source:          "doctor",
	}})

	orig := version
	version = "1.1.8" // upgraded past the cached current, still behind latest
	t.Cleanup(func() { version = orig })

	got := readCachedUpdateNotices()
	if len(got) != 1 {
		t.Fatalf("expected the notice to survive while still behind latest, got %d", len(got))
	}
	if got[0].CurrentVersion != "1.1.8" {
		t.Errorf("current_version should reflect the running binary, got %q", got[0].CurrentVersion)
	}
	if want := "kibana-cli 1.1.9 is available (current 1.1.8)"; got[0].Message != want {
		t.Errorf("message should reflect running version, got %q want %q", got[0].Message, want)
	}
}

func TestUpdateNoticeAutoDisabledDetectsWindowsGoTestBinary(t *testing.T) {
	origArgs := os.Args
	origEnabled := updateNoticeTestCacheEnabled
	os.Args = []string{`C:\tmp\kibana-cli.test.exe`}
	updateNoticeTestCacheEnabled = false
	t.Setenv(updateNoticeEnvOptOut, "")
	t.Cleanup(func() {
		os.Args = origArgs
		updateNoticeTestCacheEnabled = origEnabled
	})

	if !updateNoticeAutoDisabled() {
		t.Fatal("expected Windows Go test binary to disable update notice cache")
	}
}
