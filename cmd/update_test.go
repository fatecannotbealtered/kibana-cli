package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
)

func TestUpdate_CheckUpToDate_JSON(t *testing.T) {
	setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.1.0", nil)
	defer srv.Close()
	withUpdateHooks(t, srv.URL, "", "linux", "amd64")

	out, code := runCLI(t, []string{"update", "--check", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"status":"up_to_date"`) && !strings.Contains(j, `"status": "up_to_date"`) {
		t.Fatalf("unexpected: %s", j)
	}
	if !strings.Contains(j, `"update_available":false`) && !strings.Contains(j, `"update_available": false`) {
		t.Fatalf("expected update_available=false: %s", j)
	}
}

func TestUpdate_CheckAvailable_JSON(t *testing.T) {
	setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.1.1", nil)
	defer srv.Close()
	withUpdateHooks(t, srv.URL, "", "linux", "amd64")

	out, code := runCLI(t, []string{"update", "--check", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"status":"update_available"`) && !strings.Contains(j, `"status": "update_available"`) {
		t.Fatalf("unexpected: %s", j)
	}
	if !strings.Contains(j, `"target_version":"1.1.1"`) && !strings.Contains(j, `"target_version": "1.1.1"`) {
		t.Fatalf("expected target version: %s", j)
	}
}

// A bare `update` on an npm install now DRIVES npm: it runs `npm install -g
// @pkg@<ver>` on the user's behalf (not just print it), then syncs the Skill,
// and reports status "updated". signature_status stays "not_checked" (npm
// provenance owns integrity on this path).
func TestUpdate_NPMInstallDrivesPackageManager(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.1.1", nil)
	defer srv.Close()
	pkgRoot := filepath.Join(home, "node_modules", "@fateforge", "kibana-cli")
	exe := filepath.Join(pkgRoot, "bin", "kibana-cli")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "package.json"), []byte(`{"name":"@fateforge/kibana-cli"}`), 0600); err != nil {
		t.Fatal(err)
	}
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")

	var gotMethod, gotVersion string
	var skillSynced bool
	updateRunPackageManager = func(_ context.Context, method, target string) error {
		gotMethod, gotVersion = method, target
		return nil
	}
	updateSkillSync = func(context.Context, string) error { skillSynced = true; return nil }

	out, code := runCLI(t, []string{"update", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if gotMethod != "npm" {
		t.Fatalf("expected npm to be driven, got method %q", gotMethod)
	}
	if normalizeReleaseVersion(gotVersion) != "1.1.1" {
		t.Fatalf("expected target 1.1.1 passed to npm, got %q", gotVersion)
	}
	if !skillSynced {
		t.Fatalf("expected Skill sync to run after npm install")
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"status":"updated"`) && !strings.Contains(j, `"status": "updated"`) {
		t.Fatalf("expected status updated: %s", j)
	}
	if !strings.Contains(j, `"skill_sync_status":"synced"`) && !strings.Contains(j, `"skill_sync_status": "synced"`) {
		t.Fatalf("expected skill_sync_status synced: %s", j)
	}
	if !strings.Contains(j, `"signature_status":"not_checked"`) && !strings.Contains(j, `"signature_status": "not_checked"`) {
		t.Fatalf("expected signature_status not_checked on npm path: %s", j)
	}
}

func TestUpdate_GoInstallDrivesPackageManager(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.1.1", nil)
	defer srv.Close()
	gobin := filepath.Join(home, "go-bin")
	exe := filepath.Join(gobin, "kibana-cli")
	t.Setenv("GOBIN", gobin)
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")

	var gotMethod string
	updateRunPackageManager = func(_ context.Context, method, _ string) error {
		gotMethod = method
		return nil
	}

	out, code := runCLI(t, []string{"update", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if gotMethod != "go" {
		t.Fatalf("expected go to be driven, got method %q", gotMethod)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"install_method":"go"`) && !strings.Contains(j, `"install_method": "go"`) {
		t.Fatalf("expected go install method: %s", j)
	}
	if !strings.Contains(j, `go install github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli@v1.1.1`) {
		t.Fatalf("expected go install command in result: %s", j)
	}
}

// --dry-run on a package-manager install is a read-only preview: it must NOT
// invoke the package manager, and must report the command it WOULD run.
func TestUpdate_PackageManagerDryRunDoesNotExecute(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.1.1", nil)
	defer srv.Close()
	pkgRoot := filepath.Join(home, "node_modules", "@fateforge", "kibana-cli")
	exe := filepath.Join(pkgRoot, "bin", "kibana-cli")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "package.json"), []byte(`{"name":"@fateforge/kibana-cli"}`), 0600); err != nil {
		t.Fatal(err)
	}
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")

	called := false
	updateRunPackageManager = func(context.Context, string, string) error { called = true; return nil }

	out, code := runCLI(t, []string{"update", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if called {
		t.Fatalf("dry-run must not invoke the package manager")
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"dry_run":true`) && !strings.Contains(j, `"dry_run": true`) {
		t.Fatalf("expected dry_run preview: %s", j)
	}
	if !strings.Contains(j, `npm install -g @fateforge/kibana-cli@1.1.1`) {
		t.Fatalf("expected planned npm command: %s", j)
	}
}

// When the package manager fails, the installed binary is unchanged: report
// E_IO (exit 1), binary_replaced:false, and surface the command to run manually.
func TestUpdate_PackageManagerFailureReportsUnchanged(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.1.1", nil)
	defer srv.Close()
	pkgRoot := filepath.Join(home, "node_modules", "@fateforge", "kibana-cli")
	exe := filepath.Join(pkgRoot, "bin", "kibana-cli")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "package.json"), []byte(`{"name":"@fateforge/kibana-cli"}`), 0600); err != nil {
		t.Fatal(err)
	}
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")
	updateRunPackageManager = func(context.Context, string, string) error { return errors.New("ETARGET no matching version") }

	out, code := runCLI(t, []string{"update", "--json"})
	if code != ExitGeneral {
		t.Fatalf("expected exit %d, got %d: %s", ExitGeneral, code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"code":"E_IO"`) && !strings.Contains(j, `"code": "E_IO"`) {
		t.Fatalf("expected E_IO: %s", j)
	}
	if !strings.Contains(j, `"binary_replaced":false`) && !strings.Contains(j, `"binary_replaced": false`) {
		t.Fatalf("expected binary_replaced false: %s", j)
	}
	if !strings.Contains(j, `npm install -g @fateforge/kibana-cli@1.1.1`) {
		t.Fatalf("expected manual command in failure: %s", j)
	}
}

// update --dry-run is an OPTIONAL read-only preview: it must NOT issue a
// confirm_token or expires_at (it is no longer a write gate).
func TestUpdate_DryRunIsReadOnlyNoToken(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.1.1", nil)
	defer srv.Close()
	exe := filepath.Join(home, "bin", "kibana-cli")
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")

	out, code := runCLI(t, []string{"update", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if strings.Contains(j, `"confirm_token"`) {
		t.Fatalf("dry-run must not issue a confirm_token: %s", j)
	}
	if strings.Contains(j, `"expires_at"`) {
		t.Fatalf("dry-run must not issue expires_at: %s", j)
	}
	if !strings.Contains(j, `"dry_run":true`) && !strings.Contains(j, `"dry_run": true`) {
		t.Fatalf("expected dry_run preview: %s", j)
	}
	if !strings.Contains(j, `kibana-cli-1.1.1-linux-amd64.tar.gz`) {
		t.Fatalf("missing planned asset: %s", j)
	}
}

// A bare `update` (no --confirm, no dry-run) performs the whole update in one
// call: the confirm-token gate has been removed from update.
func TestUpdate_BareExecutesWithoutToken(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	withUpdateHooks(t, "", exe, "linux", "amd64")
	archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
		assetName:                     archive,
		"checksums.txt":               checksumLine(assetName, archive),
		"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
	})
	defer srv.Close()
	updateGitHubAPIBase = srv.URL

	out, code := runCLI(t, []string{"update", "--version", "v1.1.1", "--json"})
	if code != ExitOK {
		t.Fatalf("bare update should execute: exit %d out=%s", code, out)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Fatalf("binary not replaced by bare update: %q", data)
	}
	dataMap := envelopeData(t, out)
	if dataMap["status"] != "updated" {
		t.Fatalf("expected status updated: %s", out)
	}
}

func TestUpdate_WindowsInstallsInPlace(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	// Windows arm64 falls back to the amd64 asset.
	withUpdateHooks(t, "", exe, "windows", "arm64")
	archive := makeZip(t, "kibana-cli.exe", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if assetName != "kibana-cli-1.1.1-windows-amd64.zip" {
		t.Fatalf("unexpected windows asset name: %s", assetName)
	}
	assets := map[string][]byte{
		assetName:                     archive,
		"checksums.txt":               checksumLine(assetName, archive),
		"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
	}
	srv := newUpdateReleaseServer(t, "v1.1.1", assets)
	defer srv.Close()
	updateGitHubAPIBase = srv.URL

	out, code := runCLI(t, []string{"update", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Fatalf("binary not replaced in place on windows: %q", data)
	}
	dataMap := envelopeData(t, out)
	if dataMap["status"] != "updated" {
		t.Fatalf("expected status updated on windows: %s", out)
	}
	if dataMap["current_version"] != "1.1.1" {
		t.Fatalf("expected current_version 1.1.1: %s", out)
	}
}

func TestUpdate_StandaloneBinaryInstallsVerifiedAsset(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	withUpdateHooks(t, "", exe, "linux", "amd64")
	archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{
		assetName:                     archive,
		"checksums.txt":               checksumLine(assetName, archive),
		"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
	}
	srv := newUpdateReleaseServer(t, "v1.1.1", assets)
	defer srv.Close()
	updateGitHubAPIBase = srv.URL

	out, code := runCLI(t, []string{"update", "--version", "v1.1.1", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Fatalf("binary not replaced: %q", data)
	}
	if !strings.Contains(lastJSONLine(out), `"checksum_verified":true`) &&
		!strings.Contains(lastJSONLine(out), `"checksum_verified": true`) {
		t.Fatalf("expected checksum_verified: %s", out)
	}
	dataMap := envelopeData(t, out)
	if dataMap["previous_version"] != "1.1.0" || dataMap["current_version"] != "1.1.1" {
		t.Fatalf("unexpected update versions: %s", out)
	}
	hint, _ := dataMap["hint"].(string)
	if !strings.Contains(hint, "changelog --since 1.1.0") {
		t.Fatalf("missing changelog hint: %s", out)
	}
}

func TestUpdate_ReleaseValidationFailures(t *testing.T) {
	t.Run("missingTag", func(t *testing.T) {
		setupTestHome(t)
		srv := newUpdateReleaseServer(t, "", nil)
		defer srv.Close()
		withUpdateHooks(t, srv.URL, "", "linux", "amd64")
		out, code := runCLI(t, []string{"update", "--check", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "release has no tag_name") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("missingAsset", func(t *testing.T) {
		home := setupTestHome(t)
		srv := newUpdateReleaseServer(t, "v1.1.1", nil)
		defer srv.Close()
		withUpdateHooks(t, srv.URL, filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "release asset not found") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("missingChecksum", func(t *testing.T) {
		home := setupTestHome(t)
		withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		assetName, err := releaseAssetName("1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{assetName: []byte("archive")})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "checksums.txt") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("emptyExecutablePath", func(t *testing.T) {
		setupTestHome(t)
		srv := newUpdateReleaseServer(t, "v1.1.1", nil)
		defer srv.Close()
		withUpdateHooks(t, srv.URL, "", "linux", "amd64")
		updateExecutablePath = func() (string, error) { return "", nil }
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitAuth || !strings.Contains(lastJSONLine(out), "could not determine current executable path") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("unsupportedPlatform", func(t *testing.T) {
		home := setupTestHome(t)
		srv := newUpdateReleaseServer(t, "v1.1.1", nil)
		defer srv.Close()
		withUpdateHooks(t, srv.URL, filepath.Join(home, "bin", "kibana-cli"), "plan9", "amd64")
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "unsupported update platform") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})
}

func TestUpdate_DownloadAndInstallFailures(t *testing.T) {
	t.Run("checksumMismatch", func(t *testing.T) {
		home := setupTestHome(t)
		withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		archive := makeTarGz(t, "kibana-cli", []byte("new"))
		assetName, err := releaseAssetName("1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
			assetName:                     archive,
			"checksums.txt":               checksumLine(assetName, []byte("different")),
			"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		// Integrity failure is non-retryable E_INTEGRITY -> exit 1.
		if code != ExitGeneral || !strings.Contains(lastJSONLine(out), "checksum verification failed") {
			t.Fatalf("exit %d out=%s", code, out)
		}
		j := lastJSONLine(out)
		if !strings.Contains(j, "E_INTEGRITY") {
			t.Fatalf("expected E_INTEGRITY: %s", j)
		}
		if strings.Contains(j, `"retryable":true`) || strings.Contains(j, `"retryable": true`) {
			t.Fatalf("integrity failure must be non-retryable: %s", j)
		}
		details := envelopeErrorDetails(t, out)
		if details["stage"] != updateStageVerifyChecksum || details["binary_replaced"] != false {
			t.Fatalf("expected verify_checksum stage, not committed: %s", out)
		}
	})

	t.Run("extractMissingBinary", func(t *testing.T) {
		home := setupTestHome(t)
		withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		archive := makeTarGz(t, "other", []byte("new"))
		assetName, err := releaseAssetName("1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
			assetName:                     archive,
			"checksums.txt":               checksumLine(assetName, archive),
			"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		// Extraction of the verified archive into a temp dir is a local IO fault,
		// not a usage error: E_IO -> exit 1, replace stage, not committed.
		if code != ExitGeneral || !strings.Contains(lastJSONLine(out), "not found in release archive") {
			t.Fatalf("exit %d out=%s", code, out)
		}
		j := lastJSONLine(out)
		if !strings.Contains(j, "E_IO") {
			t.Fatalf("expected E_IO: %s", j)
		}
		details := envelopeErrorDetails(t, out)
		if details["stage"] != updateStageReplace || details["binary_replaced"] != false {
			t.Fatalf("expected replace stage, not committed: %s", out)
		}
	})

	t.Run("replaceIOFailure", func(t *testing.T) {
		home := setupTestHome(t)
		withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		updateReplaceBinary = func(string, []byte) error { return errors.New("disk full") }
		archive := makeTarGz(t, "kibana-cli", []byte("new"))
		assetName, err := releaseAssetName("1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
			assetName:                     archive,
			"checksums.txt":               checksumLine(assetName, archive),
			"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		// Local replace failure is E_IO -> exit 1, NOT a retryable network error.
		if code != ExitGeneral || !strings.Contains(lastJSONLine(out), "disk full") {
			t.Fatalf("exit %d out=%s", code, out)
		}
		j := lastJSONLine(out)
		if !strings.Contains(j, "E_IO") || strings.Contains(j, "E_NETWORK") {
			t.Fatalf("expected E_IO not E_NETWORK: %s", j)
		}
		details := envelopeErrorDetails(t, out)
		if details["stage"] != updateStageReplace || details["binary_replaced"] != false {
			t.Fatalf("expected replace stage, not committed: %s", out)
		}
	})

	t.Run("replacePermissionFailure", func(t *testing.T) {
		home := setupTestHome(t)
		withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		updateReplaceBinary = func(string, []byte) error {
			return fmt.Errorf("rename: %w", os.ErrPermission)
		}
		archive := makeTarGz(t, "kibana-cli", []byte("new"))
		assetName, err := releaseAssetName("1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
			assetName:                     archive,
			"checksums.txt":               checksumLine(assetName, archive),
			"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		// Permission failure during replace -> E_FORBIDDEN, exit 4.
		if code != ExitForbidden {
			t.Fatalf("exit %d out=%s", code, out)
		}
		if !strings.Contains(lastJSONLine(out), "E_FORBIDDEN") {
			t.Fatalf("expected E_FORBIDDEN: %s", lastJSONLine(out))
		}
	})

	t.Run("skillSyncPartialSuccess", func(t *testing.T) {
		home := setupTestHome(t)
		exe := filepath.Join(home, "bin", "kibana-cli")
		if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, []byte("old"), 0700); err != nil {
			t.Fatal(err)
		}
		withUpdateHooks(t, "", exe, "linux", "amd64")
		updateSkillSync = func(context.Context, string) error { return errors.New("npx not found") }
		archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
		assetName, err := releaseAssetName("1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
			assetName:                     archive,
			"checksums.txt":               checksumLine(assetName, archive),
			"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--version", "v1.1.1", "--json"})
		// Binary replaced but Skill sync failed -> partial success: ok:false,
		// binary_replaced:true, retryable, with skill_sync_command.
		if code != ExitNetwork {
			t.Fatalf("exit %d out=%s", code, out)
		}
		// The binary WAS replaced, so the post-state must say so truthfully.
		data, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "new-binary" {
			t.Fatalf("binary should be replaced before skill sync: %q", data)
		}
		payload := envelopePayload(t, out)
		if payload["ok"] != false {
			t.Fatalf("partial success must be ok:false: %s", out)
		}
		details := envelopeErrorDetails(t, out)
		if details["stage"] != updateStageSkillSync {
			t.Fatalf("expected skill_sync stage: %s", out)
		}
		if details["binary_replaced"] != true {
			t.Fatalf("expected binary_replaced:true: %s", out)
		}
		if details["current_version"] != "1.1.1" {
			t.Fatalf("expected current_version 1.1.1 after swap: %s", out)
		}
		if details["skill_sync_status"] != "failed" {
			t.Fatalf("expected skill_sync_status failed: %s", out)
		}
		cmd, _ := details["skill_sync_command"].(string)
		if !strings.Contains(cmd, "npx skills add") {
			t.Fatalf("expected skill_sync_command: %s", out)
		}
		errObj, _ := payload["error"].(map[string]any)
		if errObj["retryable"] != true {
			t.Fatalf("skill sync partial success should be retryable: %s", out)
		}
	})
}

func TestUpdate_AssetDownloadHTTPError(t *testing.T) {
	home := setupTestHome(t)
	withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	srv := newUpdateReleaseServerWithBrokenDownload(t, "v1.1.1", assetName)
	defer srv.Close()
	updateGitHubAPIBase = srv.URL
	out, code := runCLI(t, []string{"update", "--json"})
	j := lastJSONLine(out)
	if code != ExitNotFound || (!strings.Contains(j, `"statusCode":404`) && !strings.Contains(j, `"statusCode": 404`)) {
		t.Fatalf("exit %d out=%s", code, out)
	}
}

func TestUpdate_HTTPError_JSON(t *testing.T) {
	setupTestHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"missing release"}`))
	}))
	defer srv.Close()
	withUpdateHooks(t, srv.URL, "", "linux", "amd64")

	out, code := runCLI(t, []string{"update", "--check", "--json"})
	if code != ExitNotFound {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"statusCode":404`) && !strings.Contains(j, `"statusCode": 404`) {
		t.Fatalf("expected 404 envelope: %s", j)
	}
}

func TestUpdate_ParseAndNetworkErrors(t *testing.T) {
	t.Run("invalidReleaseJSON", func(t *testing.T) {
		setupTestHome(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{`))
		}))
		defer srv.Close()
		withUpdateHooks(t, srv.URL, "", "linux", "amd64")
		out, code := runCLI(t, []string{"update", "--check", "--json"})
		if code != ExitNetwork || !strings.Contains(lastJSONLine(out), "parse update release response") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("invalidURL", func(t *testing.T) {
		if _, err := downloadUpdateURL(context.Background(), "http://\n"); err == nil {
			t.Fatal("expected invalid URL error")
		}
	})
}

func TestUpdateHelpers(t *testing.T) {
	origGOOS := updateGOOS
	origGOARCH := updateGOARCH
	t.Cleanup(func() {
		updateGOOS = origGOOS
		updateGOARCH = origGOARCH
	})
	if compareVersions("1.2.0", "1.1.9") <= 0 {
		t.Fatal("version compare failed")
	}
	if !updateAvailable("dev", "1.1.1", false) {
		t.Fatal("dev build should be updateable")
	}
	if updateAvailable("1.1.1", "1.1.0", false) {
		t.Fatal("older latest should not update")
	}
	if updateAvailable("1.1.1", "", false) {
		t.Fatal("empty target should not update")
	}
	gopath := t.TempDir()
	t.Setenv("GOPATH", gopath)
	if !isGoInstallPath(filepath.Join(gopath, "bin", "kibana-cli")) {
		t.Fatal("expected GOPATH/bin detection")
	}
	if got := updateInstallCommand("go", "1.2.3"); !strings.Contains(got, "@v1.2.3") {
		t.Fatalf("go command: %s", got)
	}
	if _, err := checksumForAsset([]byte("abc  other.zip\n"), "missing.zip"); err == nil {
		t.Fatal("expected missing checksum error")
	}
	if err := verifyArchiveChecksum([]byte("bad"), checksumLine("asset.zip", []byte("good")), "asset.zip"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if err := verifyArchiveChecksum([]byte("good"), checksumLine("asset.zip", []byte("good")), "asset.zip"); err != nil {
		t.Fatalf("expected checksum success: %v", err)
	}
	if got := (&updateHTTPError{StatusCode: 500}).Error(); !strings.Contains(got, "500") {
		t.Fatalf("http error: %s", got)
	}
	if msg := parseUpdateErrorMessage([]byte(strings.Repeat("x", updateMaxErrorBodyLen+10))); len(msg) <= updateMaxErrorBodyLen {
		t.Fatalf("expected truncated long message")
	}
	if parseUpdateErrorMessage([]byte(`{"message":"bad"}`)) != "bad" {
		t.Fatal("expected JSON error message")
	}
	if normalizeReleaseTag("V1.2.3") != "v1.2.3" || normalizeReleaseTag("") != "" {
		t.Fatal("normalize tag failed")
	}
	if compareVersions("bad-b", "bad-a") <= 0 {
		t.Fatal("fallback compare failed")
	}
	if _, ok := parseVersionParts("1.2.3.4"); ok {
		t.Fatal("expected too many version parts to fail")
	}
	if _, ok := parseVersionParts("1.x"); ok {
		t.Fatal("expected invalid version part to fail")
	}
	if _, err := releaseAssetName("1.1.1"); err != nil {
		t.Fatalf("release asset: %v", err)
	}
	updateGOOS = func() string { return "plan9" }
	if _, err := releaseAssetName("1.1.1"); err == nil {
		t.Fatal("expected unsupported platform")
	}
	updateGOOS = func() string { return "linux" }
	updateGOARCH = func() string { return "386" }
	if _, err := releaseAssetName("1.1.1"); err == nil {
		t.Fatal("expected unsupported arch")
	}
	if _, ok := findReleaseAsset([]updateAsset{{Name: "x"}}, "x"); ok {
		t.Fatal("asset without URL should not match")
	}
	if _, ok := findReleaseAsset([]updateAsset{{Name: "x", BrowserDownloadURL: "u"}}, "y"); ok {
		t.Fatal("unexpected asset match")
	}
	if got := detectInstallMethod(filepath.Join(t.TempDir(), "kibana-cli")); got != "binary" {
		t.Fatalf("install method: %s", got)
	}
	if npmPackageRoot(filepath.Join(t.TempDir(), "node_modules", "pkg", "bin", "kibana-cli")) != "" {
		t.Fatal("unexpected npm package root")
	}
	if !pathHasSegment(filepath.Join("a", "node_modules", "b"), "node_modules") {
		t.Fatal("path segment")
	}
}

func TestUpdateArchiveAndReplaceHelpers(t *testing.T) {
	zipData := makeZip(t, "nested/kibana-cli.exe", []byte("zip-bin"))
	got, err := extractReleaseBinary(zipData, "kibana-cli-1.1.1-windows-amd64.zip", "kibana-cli.exe")
	if err != nil || string(got) != "zip-bin" {
		t.Fatalf("zip extract got=%q err=%v", got, err)
	}
	if _, err := extractZipBinary([]byte("bad zip"), "kibana-cli.exe"); err == nil {
		t.Fatal("expected bad zip error")
	}
	if _, err := extractZipBinary(makeZip(t, "other", []byte("x")), "kibana-cli.exe"); err == nil {
		t.Fatal("expected missing zip binary")
	}
	if _, err := extractReleaseBinary([]byte("x"), "asset.bin", "kibana-cli"); err == nil {
		t.Fatal("expected unsupported archive")
	}
	if _, err := extractTarGzBinary([]byte("bad gzip"), "kibana-cli"); err == nil {
		t.Fatal("expected bad gzip")
	}
	if _, err := extractTarGzBinary(makeTarGz(t, "other", []byte("x")), "kibana-cli"); err == nil {
		t.Fatal("expected missing tar binary")
	}
	if err := replaceExecutable(filepath.Join(t.TempDir(), "missing"), []byte("x")); err == nil {
		t.Fatal("expected missing executable error")
	}
}

func TestPrintUpdateResultText(t *testing.T) {
	resetCLIState(t)
	cases := []updateResult{
		{Status: "updated", Message: "updated"},
		{Status: "up_to_date", Message: "ok"},
		{Status: "package_manager_required", Message: "pm", Command: "npm install", URL: "https://example.com"},
		{Status: "update_available", Message: "available"},
	}
	out := captureCLIOutput(t, func() {
		for _, c := range cases {
			printUpdateResult(c)
		}
	})
	if !strings.Contains(out, "npm install") || !strings.Contains(out, "https://example.com") {
		t.Fatalf("text output: %s", out)
	}
}

func withUpdateHooks(t *testing.T, apiBase, exe, goos, goarch string) {
	t.Helper()
	origRepo := updateRepo
	origBase := updateGitHubAPIBase
	origExe := updateExecutablePath
	origGOOS := updateGOOS
	origGOARCH := updateGOARCH
	origReplace := updateReplaceBinary
	origSkillSync := updateSkillSync
	origRunPM := updateRunPackageManager
	origVersion := version
	origVerifySig := updateVerifySignature
	t.Cleanup(func() {
		updateRepo = origRepo
		updateGitHubAPIBase = origBase
		updateExecutablePath = origExe
		updateGOOS = origGOOS
		updateGOARCH = origGOARCH
		updateReplaceBinary = origReplace
		updateSkillSync = origSkillSync
		updateRunPackageManager = origRunPM
		version = origVersion
		updateVerifySignature = origVerifySig
	})
	updateRepo = "fatecannotbealtered/kibana-cli"
	version = "1.1.0"
	if apiBase != "" {
		updateGitHubAPIBase = apiBase
	}
	if exe != "" {
		updateExecutablePath = func() (string, error) { return exe, nil }
	}
	updateGOOS = func() string { return goos }
	updateGOARCH = func() string { return goarch }
	updateSkillSync = func(context.Context, string) error { return nil }
	// Package-manager-driven update is stubbed to a no-op success by default so
	// tests never shell out to a real npm/go. Tests asserting failure or
	// call-capture override this with their own closure.
	updateRunPackageManager = func(context.Context, string, string) error { return nil }
	// In-process Sigstore verification is stubbed in tests; a live OIDC-signed
	// bundle cannot be produced in a unit test. Fail-closed control flow is
	// covered by overriding this with an error-returning stub.
	updateVerifySignature = func(context.Context, string, string, string) error { return nil }
}

func newUpdateReleaseServer(t *testing.T, tag string, downloads map[string][]byte) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/download/") {
			name := strings.TrimPrefix(r.URL.Path, "/download/")
			data, ok := downloads[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(data)
			return
		}
		if strings.Contains(r.URL.Path, "/releases/latest") || strings.Contains(r.URL.Path, "/releases/tags/") {
			assets := make([]updateAsset, 0, len(downloads))
			for name := range downloads {
				assets = append(assets, updateAsset{
					Name:               name,
					BrowserDownloadURL: srv.URL + "/download/" + name,
				})
			}
			_ = json.NewEncoder(w).Encode(updateRelease{
				TagName: tag,
				HTMLURL: srv.URL + "/releases/" + tag,
				Assets:  assets,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	return srv
}

func newUpdateReleaseServerWithBrokenDownload(t *testing.T, tag, brokenAsset string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/download/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"asset missing"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(updateRelease{
			TagName: tag,
			HTMLURL: srv.URL + "/releases/" + tag,
			Assets: []updateAsset{
				{Name: brokenAsset, BrowserDownloadURL: srv.URL + "/download/" + brokenAsset},
				{Name: "checksums.txt", BrowserDownloadURL: srv.URL + "/download/checksums.txt"},
			},
		})
	}))
	return srv
}

func makeTarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checksumLine(name string, data []byte) []byte {
	sum := sha256.Sum256(data)
	return []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name))
}

func TestUpdate_UnsignedReleaseRefused(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli")
	withUpdateHooks(t, "", exe, "linux", "amd64")
	archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	// Release WITHOUT checksums.txt.sigstore.json: must be refused, not skipped.
	srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
		assetName:       archive,
		"checksums.txt": checksumLine(assetName, archive),
	})
	defer srv.Close()
	updateGitHubAPIBase = srv.URL
	out, code := runCLI(t, []string{"update", "--version", "v1.1.1", "--json"})
	if code != ExitGeneral {
		t.Fatalf("exit %d (want %d) out=%s", code, ExitGeneral, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, "E_INTEGRITY") || !strings.Contains(j, "unsigned release") {
		t.Fatalf("expected unsigned-release refusal: %s", j)
	}
	details := envelopeErrorDetails(t, out)
	if details["stage"] != updateStageVerifySignature || details["binary_replaced"] != false {
		t.Fatalf("expected verify_signature stage, not committed: %s", out)
	}
}

// Integrity failures during signature verification are non-retryable E_INTEGRITY
// and must never be reclassified as a transient signature stub mismatch.
func TestUpdate_SignatureVerificationFailsClosed(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli")
	withUpdateHooks(t, "", exe, "linux", "amd64")
	updateVerifySignature = func(context.Context, string, string, string) error {
		return errors.New("identity mismatch")
	}
	archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
		assetName:                     archive,
		"checksums.txt":               checksumLine(assetName, archive),
		"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
	})
	defer srv.Close()
	updateGitHubAPIBase = srv.URL
	out, code := runCLI(t, []string{"update", "--version", "v1.1.1", "--json"})
	if code != ExitGeneral {
		t.Fatalf("exit %d out=%s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, "E_INTEGRITY") || !strings.Contains(j, "identity mismatch") {
		t.Fatalf("expected E_INTEGRITY identity mismatch: %s", j)
	}
	if strings.Contains(j, `"retryable":true`) || strings.Contains(j, `"retryable": true`) {
		t.Fatalf("integrity failure must be non-retryable: %s", j)
	}
}

// SIGINT/SIGTERM before the swap still emits a terminal JSON envelope
// (E_INTERRUPTED, exit 130) stating "no change, still on <current>".
func TestUpdate_InterruptBeforeSwapEmitsEnvelope(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	origVersion := version
	version = "1.1.0"
	t.Cleanup(func() { version = origVersion })

	out := captureCLIOutput(t, func() {
		_ = handleUpdateError(context.Canceled, updateStageDownload, false, "not_run")
	})
	if lastExit != ExitInterrupted {
		t.Fatalf("expected exit 130, got %d", lastExit)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, "E_INTERRUPTED") {
		t.Fatalf("expected E_INTERRUPTED: %s", j)
	}
	if !strings.Contains(j, "still on 1.1.0") {
		t.Fatalf("expected post-state message: %s", j)
	}
	if !strings.Contains(j, `"retryable":true`) && !strings.Contains(j, `"retryable": true`) {
		t.Fatalf("interrupt should be retryable: %s", j)
	}
	if !strings.Contains(j, `"binary_replaced":false`) && !strings.Contains(j, `"binary_replaced": false`) {
		t.Fatalf("expected binary_replaced false: %s", j)
	}
}

// --target-version is the canonical flag; --version is a hidden deprecated
// alias. Both must select the same release.
func TestUpdate_TargetVersionFlagAndAlias(t *testing.T) {
	run := func(t *testing.T, flag string) {
		home := setupTestHome(t)
		exe := filepath.Join(home, "bin", "kibana-cli")
		if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, []byte("old"), 0700); err != nil {
			t.Fatal(err)
		}
		withUpdateHooks(t, "", exe, "linux", "amd64")
		archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
		assetName, err := releaseAssetName("1.1.1")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
			assetName:                     archive,
			"checksums.txt":               checksumLine(assetName, archive),
			"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL

		out, code := runCLI(t, []string{"update", flag, "v1.1.1", "--json"})
		if code != ExitOK {
			t.Fatalf("%s: exit %d out=%s", flag, code, out)
		}
		data := envelopeData(t, out)
		if data["status"] != "updated" || data["current_version"] != "1.1.1" {
			t.Fatalf("%s: unexpected result: %s", flag, out)
		}
	}
	t.Run("targetVersion", func(t *testing.T) { run(t, "--target-version") })
	t.Run("versionAlias", func(t *testing.T) { run(t, "--version") })
}

// A successful update surfaces an honest trust-root source string (embedded TUF
// anchor + cached/refreshed trusted_root), so the unresolved network dependency
// is visible to the agent rather than implied to be fully offline.
func TestUpdate_TrustRootSourceSurfaced(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	withUpdateHooks(t, "", exe, "linux", "amd64")
	archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
		assetName:                     archive,
		"checksums.txt":               checksumLine(assetName, archive),
		"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
	})
	defer srv.Close()
	updateGitHubAPIBase = srv.URL
	out, code := runCLI(t, []string{"update", "--target-version", "v1.1.1", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d out=%s", code, out)
	}
	data := envelopeData(t, out)
	if data["trust_root_source"] != updateTrustRootSource {
		t.Fatalf("expected trust_root_source %q: %s", updateTrustRootSource, out)
	}
}

// A signature verification failure caused by an unreachable TUF trust root is a
// transient network fault, NOT an integrity failure: it must be retryable and
// must not surface E_INTEGRITY.
func TestUpdate_VerifyTrustRootUnavailableIsRetryable(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli")
	withUpdateHooks(t, "", exe, "linux", "amd64")
	updateVerifySignature = func(context.Context, string, string, string) error {
		return &errTrustRootUnavailable{errors.New("loading sigstore trust root: tuf refresh failed")}
	}
	archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
		assetName:                     archive,
		"checksums.txt":               checksumLine(assetName, archive),
		"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
	})
	defer srv.Close()
	updateGitHubAPIBase = srv.URL
	out, code := runCLI(t, []string{"update", "--target-version", "v1.1.1", "--json"})
	// Trust-root refresh failure -> retryable network error, exit 7, NOT E_INTEGRITY.
	if code != ExitNetwork {
		t.Fatalf("exit %d (want %d) out=%s", code, ExitNetwork, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, "E_NETWORK") {
		t.Fatalf("expected E_NETWORK: %s", j)
	}
	if strings.Contains(j, "E_INTEGRITY") {
		t.Fatalf("trust-root refresh failure must not be E_INTEGRITY: %s", j)
	}
	if !strings.Contains(j, `"retryable":true`) && !strings.Contains(j, `"retryable": true`) {
		t.Fatalf("trust-root refresh failure must be retryable: %s", j)
	}
	details := envelopeErrorDetails(t, out)
	if details["stage"] != updateStageVerifySignature || details["binary_replaced"] != false {
		t.Fatalf("expected verify_signature stage, not committed: %s", out)
	}
	// The installed binary must be untouched.
	if data, _ := os.ReadFile(exe); string(data) == "new-binary" {
		t.Fatalf("binary must not be replaced on trust-root failure")
	}
}

// SIGINT during signature verification still emits a terminal E_INTERRUPTED
// envelope (exit 130) and leaves the binary untouched.
func TestUpdate_VerifyInterruptEmitsEnvelope(t *testing.T) {
	home := setupTestHome(t)
	exe := filepath.Join(home, "bin", "kibana-cli")
	withUpdateHooks(t, "", exe, "linux", "amd64")
	updateVerifySignature = func(context.Context, string, string, string) error {
		return context.Canceled
	}
	archive := makeTarGz(t, "kibana-cli", []byte("new-binary"))
	assetName, err := releaseAssetName("1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	srv := newUpdateReleaseServer(t, "v1.1.1", map[string][]byte{
		assetName:                     archive,
		"checksums.txt":               checksumLine(assetName, archive),
		"checksums.txt.sigstore.json": []byte(`{"bundle":"stub"}`),
	})
	defer srv.Close()
	updateGitHubAPIBase = srv.URL
	out, code := runCLI(t, []string{"update", "--target-version", "v1.1.1", "--json"})
	if code != ExitInterrupted {
		t.Fatalf("exit %d (want %d) out=%s", code, ExitInterrupted, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, "E_INTERRUPTED") || !strings.Contains(j, "still on 1.1.0") {
		t.Fatalf("expected interrupted terminal envelope: %s", j)
	}
	details := envelopeErrorDetails(t, out)
	if details["stage"] != updateStageVerifySignature {
		t.Fatalf("expected verify_signature stage: %s", out)
	}
}

// classifyUpdateNetworkError maps a deadline distinctly from a generic network
// failure, and never escalates a transport fault to a non-retryable code.
func TestUpdate_ClassifyNetworkError(t *testing.T) {
	if c, e := classifyUpdateNetworkError(context.DeadlineExceeded); c != output.ErrTimeout || e != ExitTimeout {
		t.Fatalf("deadline -> got %v/%d want E_TIMEOUT/%d", c, e, ExitTimeout)
	}
	if c, e := classifyUpdateNetworkError(&timeoutNetError{}); c != output.ErrTimeout || e != ExitTimeout {
		t.Fatalf("net timeout -> got %v/%d want E_TIMEOUT/%d", c, e, ExitTimeout)
	}
	if c, e := classifyUpdateNetworkError(errors.New("connection refused")); c != output.ErrNetwork || e != ExitNetwork {
		t.Fatalf("generic -> got %v/%d want E_NETWORK/%d", c, e, ExitNetwork)
	}
	if c, e := classifyUpdateNetworkError(&updateHTTPError{StatusCode: 429}); c != output.ErrRateLimit || e != ExitNetwork {
		t.Fatalf("429 -> got %v/%d want E_RATE_LIMITED/7", c, e)
	}
	if isUpdateTimeoutError(context.Canceled) {
		t.Fatal("cancellation must not be classified as a timeout")
	}
}

// timeoutNetError is a net.Error whose Timeout() reports true.
type timeoutNetError struct{}

func (*timeoutNetError) Error() string   { return "i/o timeout" }
func (*timeoutNetError) Timeout() bool   { return true }
func (*timeoutNetError) Temporary() bool { return true }

func TestUpdate_NewErrorCodeMappings(t *testing.T) {
	if output.RetryableForErrorCode(output.ErrIO) {
		t.Fatal("E_IO must be non-retryable")
	}
	if !output.RetryableForErrorCode(output.ErrInterrupted) {
		t.Fatal("E_INTERRUPTED must be retryable")
	}
	if got := output.ExitCodeForErrorCode(output.ErrIO); got != ExitGeneral {
		t.Fatalf("E_IO exit got %d want %d", got, ExitGeneral)
	}
	if got := output.ExitCodeForErrorCode(output.ErrInterrupted); got != ExitInterrupted {
		t.Fatalf("E_INTERRUPTED exit got %d want %d", got, ExitInterrupted)
	}
	if c, e := classifyReplaceError(fmt.Errorf("x: %w", os.ErrPermission)); c != output.ErrForbidden || e != ExitForbidden {
		t.Fatalf("permission replace error: %v %d", c, e)
	}
	if c, e := classifyReplaceError(errors.New("disk full")); c != output.ErrIO || e != ExitGeneral {
		t.Fatalf("io replace error: %v %d", c, e)
	}
}
