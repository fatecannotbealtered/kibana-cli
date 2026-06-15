package cmd

import (
	"strings"
	"testing"
	"time"
)

const testChangelogMarkdown = `# Changelog

## [Unreleased]

### Added

- Added changelog command.

### Changed

- Changed the Agent contract.

## [1.1.0] - 2026-06-07

### Fixed

- Fixed output.
`

func TestChangelog_JSONSince(t *testing.T) {
	orig := changelogSource
	changelogSource = testChangelogMarkdown
	t.Cleanup(func() { changelogSource = orig })

	out, code := runCLI(t, []string{"changelog", "--since", "1.1.0", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data := envelopeData(t, out)
	if data["current_version"] != "1.1.4" {
		t.Fatalf("current version: %v", data["current_version"])
	}
	if !strings.Contains(lastJSONLine(out), `"version":"Unreleased"`) &&
		!strings.Contains(lastJSONLine(out), `"version": "Unreleased"`) {
		t.Fatalf("expected Unreleased entry: %s", out)
	}
	if strings.Contains(lastJSONLine(out), `"version":"1.1.0"`) {
		t.Fatalf("since should filter older entry: %s", out)
	}
}

func TestChangelog_TextNoEntries(t *testing.T) {
	orig := changelogSource
	changelogSource = `# Changelog

## [Unreleased]

## [1.1.0] - 2026-06-07

### Fixed

- Fixed output.
`
	t.Cleanup(func() { changelogSource = orig })

	out, code := runCLI(t, []string{"changelog", "--since", "9.0.0", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "No changelog entries") {
		t.Fatalf("unexpected text: %s", out)
	}
}

func TestConfirmTokenExpiry(t *testing.T) {
	resetCLIState(t)
	detail := map[string]any{"path": "x"}
	token := confirmTokenForExpiry("write field-map.yaml", detail, time.Now().UTC().Add(-time.Minute))
	if err := validateConfirmToken("write field-map.yaml", detail, token); err == nil ||
		!strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired token error, got %v", err)
	}
}
