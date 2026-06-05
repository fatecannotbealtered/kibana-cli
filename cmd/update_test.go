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
)

func TestUpdate_CheckUpToDate_JSON(t *testing.T) {
	setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.0.2", nil)
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
	if !strings.Contains(j, `"updateAvailable":false`) && !strings.Contains(j, `"updateAvailable": false`) {
		t.Fatalf("expected updateAvailable=false: %s", j)
	}
}

func TestUpdate_CheckAvailable_JSON(t *testing.T) {
	setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.0.3", nil)
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
	if !strings.Contains(j, `"targetVersion":"1.0.3"`) && !strings.Contains(j, `"targetVersion": "1.0.3"`) {
		t.Fatalf("expected target version: %s", j)
	}
}

func TestUpdate_NPMInstallUsesPackageManager(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.0.3", nil)
	defer srv.Close()
	pkgRoot := filepath.Join(home, "node_modules", "@fatecannotbealtered-", "kibana-cli")
	exe := filepath.Join(pkgRoot, "bin", "kibana-cli")
	if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "package.json"), []byte(`{"name":"@fatecannotbealtered-/kibana-cli"}`), 0600); err != nil {
		t.Fatal(err)
	}
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")

	out, code := runCLI(t, []string{"update", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"package_manager_required"`) || !strings.Contains(j, `npm install -g`) {
		t.Fatalf("expected npm update command: %s", j)
	}
}

func TestUpdate_GoInstallUsesPackageManager(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.0.3", nil)
	defer srv.Close()
	gobin := filepath.Join(home, "go-bin")
	exe := filepath.Join(gobin, "kibana-cli")
	t.Setenv("GOBIN", gobin)
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")

	out, code := runCLI(t, []string{"update", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"installMethod":"go"`) && !strings.Contains(j, `"installMethod": "go"`) {
		t.Fatalf("expected go install method: %s", j)
	}
	if !strings.Contains(j, `go install github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli@v1.0.3`) {
		t.Fatalf("expected go install command: %s", j)
	}
}

func TestUpdate_DryRunStandaloneBinary(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.0.3", nil)
	defer srv.Close()
	exe := filepath.Join(home, "bin", "kibana-cli")
	withUpdateHooks(t, srv.URL, exe, "linux", "amd64")

	out, code := runCLI(t, []string{"update", "--dry-run", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"status":"dry_run"`) && !strings.Contains(j, `"status": "dry_run"`) {
		t.Fatalf("unexpected: %s", j)
	}
	if !strings.Contains(j, `kibana-cli-1.0.3-linux-amd64.tar.gz`) {
		t.Fatalf("missing planned asset: %s", j)
	}
}

func TestUpdate_WindowsManualUpdateRequired(t *testing.T) {
	home := setupTestHome(t)
	srv := newUpdateReleaseServer(t, "v1.0.3", nil)
	defer srv.Close()
	exe := filepath.Join(home, "bin", "kibana-cli.exe")
	withUpdateHooks(t, srv.URL, exe, "windows", "arm64")

	out, code := runCLI(t, []string{"update", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"manual_update_required"`) {
		t.Fatalf("expected manual update: %s", j)
	}
	if !strings.Contains(j, `kibana-cli-1.0.3-windows-amd64.zip`) {
		t.Fatalf("expected windows amd64 fallback asset: %s", j)
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
	assetName, err := releaseAssetName("1.0.3")
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{
		assetName:       archive,
		"checksums.txt": checksumLine(assetName, archive),
	}
	srv := newUpdateReleaseServer(t, "v1.0.3", assets)
	defer srv.Close()
	updateGitHubAPIBase = srv.URL

	out, code := runCLI(t, []string{"update", "--version", "v1.0.3", "--json"})
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
	if !strings.Contains(lastJSONLine(out), `"checksumVerified":true`) &&
		!strings.Contains(lastJSONLine(out), `"checksumVerified": true`) {
		t.Fatalf("expected checksumVerified: %s", out)
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
		srv := newUpdateReleaseServer(t, "v1.0.3", nil)
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
		assetName, err := releaseAssetName("1.0.3")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.0.3", map[string][]byte{assetName: []byte("archive")})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "checksums.txt") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("emptyExecutablePath", func(t *testing.T) {
		setupTestHome(t)
		srv := newUpdateReleaseServer(t, "v1.0.3", nil)
		defer srv.Close()
		withUpdateHooks(t, srv.URL, "", "linux", "amd64")
		updateExecutablePath = func() (string, error) { return "", nil }
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "could not determine current executable path") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("unsupportedPlatform", func(t *testing.T) {
		home := setupTestHome(t)
		srv := newUpdateReleaseServer(t, "v1.0.3", nil)
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
		assetName, err := releaseAssetName("1.0.3")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.0.3", map[string][]byte{
			assetName:       archive,
			"checksums.txt": checksumLine(assetName, []byte("different")),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "checksum verification failed") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("extractMissingBinary", func(t *testing.T) {
		home := setupTestHome(t)
		withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		archive := makeTarGz(t, "other", []byte("new"))
		assetName, err := releaseAssetName("1.0.3")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.0.3", map[string][]byte{
			assetName:       archive,
			"checksums.txt": checksumLine(assetName, archive),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "not found in release archive") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})

	t.Run("replaceFailure", func(t *testing.T) {
		home := setupTestHome(t)
		withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
		updateReplaceBinary = func(string, []byte) error { return errors.New("replace denied") }
		archive := makeTarGz(t, "kibana-cli", []byte("new"))
		assetName, err := releaseAssetName("1.0.3")
		if err != nil {
			t.Fatal(err)
		}
		srv := newUpdateReleaseServer(t, "v1.0.3", map[string][]byte{
			assetName:       archive,
			"checksums.txt": checksumLine(assetName, archive),
		})
		defer srv.Close()
		updateGitHubAPIBase = srv.URL
		out, code := runCLI(t, []string{"update", "--json"})
		if code != ExitBadArgs || !strings.Contains(lastJSONLine(out), "replace denied") {
			t.Fatalf("exit %d out=%s", code, out)
		}
	})
}

func TestUpdate_AssetDownloadHTTPError(t *testing.T) {
	home := setupTestHome(t)
	withUpdateHooks(t, "", filepath.Join(home, "bin", "kibana-cli"), "linux", "amd64")
	assetName, err := releaseAssetName("1.0.3")
	if err != nil {
		t.Fatal(err)
	}
	srv := newUpdateReleaseServerWithBrokenDownload(t, "v1.0.3", assetName)
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
	if !updateAvailable("dev", "1.0.3", false) {
		t.Fatal("dev build should be updateable")
	}
	if updateAvailable("1.0.3", "1.0.2", false) {
		t.Fatal("older latest should not update")
	}
	if updateAvailable("1.0.3", "", false) {
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
	if _, err := releaseAssetName("1.0.3"); err != nil {
		t.Fatalf("release asset: %v", err)
	}
	updateGOOS = func() string { return "plan9" }
	if _, err := releaseAssetName("1.0.3"); err == nil {
		t.Fatal("expected unsupported platform")
	}
	updateGOOS = func() string { return "linux" }
	updateGOARCH = func() string { return "386" }
	if _, err := releaseAssetName("1.0.3"); err == nil {
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
	got, err := extractReleaseBinary(zipData, "kibana-cli-1.0.3-windows-amd64.zip", "kibana-cli.exe")
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
		{Status: "manual_update_required", Message: "manual"},
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
	origVersion := version
	t.Cleanup(func() {
		updateRepo = origRepo
		updateGitHubAPIBase = origBase
		updateExecutablePath = origExe
		updateGOOS = origGOOS
		updateGOARCH = origGOARCH
		updateReplaceBinary = origReplace
		version = origVersion
	})
	updateRepo = "fatecannotbealtered/kibana-cli"
	version = "1.0.2"
	if apiBase != "" {
		updateGitHubAPIBase = apiBase
	}
	if exe != "" {
		updateExecutablePath = func() (string, error) { return exe, nil }
	}
	updateGOOS = func() string { return goos }
	updateGOARCH = func() string { return goarch }
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
