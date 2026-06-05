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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

const (
	updateNPMPackage      = "@fatecannotbealtered-/kibana-cli"
	updateGoPackage       = "github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli"
	updateMaxErrorBodyLen = 512
)

var (
	updateRepo           = "fatecannotbealtered/kibana-cli"
	updateGitHubAPIBase  = "https://api.github.com"
	updateCheckOnly      bool
	updateTargetVersion  string
	updateExecutablePath = os.Executable
	updateGOOS           = func() string { return runtime.GOOS }
	updateGOARCH         = func() string { return runtime.GOARCH }
	updateReplaceBinary  = replaceExecutable
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for or install the latest kibana-cli release",
	Long: `Check GitHub Releases for a newer kibana-cli version and update safely.

Package-manager installs are not modified in place. When kibana-cli is managed by
npm or Go, the command prints the exact package-manager command to run instead.
Standalone Unix binaries are updated in place after SHA256 verification against
the release checksums.txt file.`,
	Args: cobra.NoArgs,
	RunE: runUpdate,
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "Check for updates without changing files")
	updateCmd.Flags().StringVar(&updateTargetVersion, "version", "", "Install or check a specific release version (e.g. 1.0.3 or v1.0.3)")
}

type updateRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Assets  []updateAsset `json:"assets"`
}

type updateAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type updateResult struct {
	OK               bool   `json:"ok"`
	Status           string `json:"status"`
	Message          string `json:"message"`
	CurrentVersion   string `json:"currentVersion"`
	TargetVersion    string `json:"targetVersion"`
	LatestVersion    string `json:"latestVersion,omitempty"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	InstallMethod    string `json:"installMethod"`
	Path             string `json:"path,omitempty"`
	Asset            string `json:"asset,omitempty"`
	URL              string `json:"url,omitempty"`
	Command          string `json:"command,omitempty"`
	DryRun           bool   `json:"dryRun,omitempty"`
	ChecksumVerified bool   `json:"checksumVerified,omitempty"`
}

type updateHTTPError struct {
	StatusCode int
	URL        string
	Message    string
}

func (e *updateHTTPError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.URL != "" {
		return fmt.Sprintf("update request failed %d for %s: %s", e.StatusCode, e.URL, msg)
	}
	return fmt.Sprintf("update request failed %d: %s", e.StatusCode, msg)
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	targetFlag := strings.TrimSpace(updateTargetVersion)
	release, err := fetchUpdateRelease(apiCtx(), targetFlag)
	if err != nil {
		return handleUpdateError(err)
	}
	targetVersion := normalizeReleaseVersion(release.TagName)
	if targetVersion == "" {
		return failValidation("release has no tag_name")
	}
	currentVersion := normalizeReleaseVersion(version)
	installPath, _ := updateExecutablePath()
	installMethod := detectInstallMethod(installPath)
	available := updateAvailable(currentVersion, targetVersion, targetFlag != "")

	result := updateResult{
		OK:              true,
		Status:          "up_to_date",
		Message:         "kibana-cli is already up to date",
		CurrentVersion:  version,
		TargetVersion:   targetVersion,
		LatestVersion:   targetVersion,
		UpdateAvailable: available,
		InstallMethod:   installMethod,
		Path:            installPath,
		URL:             release.HTMLURL,
	}
	if !available {
		printUpdateResult(result)
		return nil
	}

	result.Status = "update_available"
	result.Message = fmt.Sprintf("kibana-cli %s is available", targetVersion)
	result.Command = updateInstallCommand(installMethod, targetVersion)

	if updateCheckOnly {
		printUpdateResult(result)
		return nil
	}
	if dryRun {
		result.Status = "dry_run"
		result.Message = "update planned but not installed"
		result.DryRun = true
		result.Asset, _ = releaseAssetName(targetVersion)
		printUpdateResult(result)
		return nil
	}
	if installMethod == "npm" || installMethod == "go" {
		result.Status = "package_manager_required"
		result.Message = "kibana-cli is managed by a package manager; run the suggested command to update"
		printUpdateResult(result)
		return nil
	}
	if updateGOOS() == "windows" {
		result.Status = "manual_update_required"
		result.Message = "Windows cannot safely replace the running executable; download the release asset and replace the binary after exiting"
		result.Asset, _ = releaseAssetName(targetVersion)
		printUpdateResult(result)
		return nil
	}
	if installPath == "" {
		return failConfig("could not determine current executable path")
	}

	assetName, err := releaseAssetName(targetVersion)
	if err != nil {
		return failValidation(err.Error())
	}
	asset, ok := findReleaseAsset(release.Assets, assetName)
	if !ok {
		return failValidation("release asset not found: " + assetName)
	}
	checksums, ok := findReleaseAsset(release.Assets, "checksums.txt")
	if !ok {
		return failValidation("release checksums.txt not found")
	}

	archiveData, err := downloadUpdateURL(apiCtx(), asset.BrowserDownloadURL)
	if err != nil {
		return handleUpdateError(err)
	}
	checksumData, err := downloadUpdateURL(apiCtx(), checksums.BrowserDownloadURL)
	if err != nil {
		return handleUpdateError(err)
	}
	if err := verifyArchiveChecksum(archiveData, checksumData, assetName); err != nil {
		return failValidation(err.Error())
	}
	binName := "kibana-cli"
	if updateGOOS() == "windows" {
		binName += ".exe"
	}
	binaryData, err := extractReleaseBinary(archiveData, assetName, binName)
	if err != nil {
		return failValidation(err.Error())
	}
	markWrite(cmd)
	if err := updateReplaceBinary(installPath, binaryData); err != nil {
		return failConfig("failed to replace executable: " + err.Error())
	}

	result.Status = "updated"
	result.Message = fmt.Sprintf("updated kibana-cli from %s to %s", version, targetVersion)
	result.Asset = assetName
	result.ChecksumVerified = true
	printUpdateResult(result)
	return nil
}

func fetchUpdateRelease(ctx context.Context, target string) (*updateRelease, error) {
	u := updateReleaseAPIURL(target)
	data, err := downloadUpdateURL(ctx, u)
	if err != nil {
		return nil, err
	}
	var release updateRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, fmt.Errorf("parse update release response: %w", err)
	}
	return &release, nil
}

func updateReleaseAPIURL(target string) string {
	base := strings.TrimRight(updateGitHubAPIBase, "/")
	repo := strings.Trim(updateRepo, "/")
	if strings.TrimSpace(target) == "" {
		return base + "/repos/" + repo + "/releases/latest"
	}
	return base + "/repos/" + repo + "/releases/tags/" + normalizeReleaseTag(target)
}

func downloadUpdateURL(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kibana-cli")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: time.Duration(applyTimeoutFromEnv(activeCmd)) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode >= 400 {
		return nil, &updateHTTPError{StatusCode: resp.StatusCode, URL: u, Message: parseUpdateErrorMessage(data)}
	}
	return data, nil
}

func parseUpdateErrorMessage(data []byte) string {
	var raw struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &raw) == nil && raw.Message != "" {
		return raw.Message
	}
	msg := strings.TrimSpace(string(data))
	if len(msg) > updateMaxErrorBodyLen {
		msg = msg[:updateMaxErrorBodyLen] + "..."
	}
	return msg
}

func handleUpdateError(err error) error {
	var httpErr *updateHTTPError
	if errors.As(err, &httpErr) {
		code := output.ErrorCodeFromStatus(httpErr.StatusCode)
		st := AgentStatus{
			OK:         false,
			Status:     StatusAPIError,
			Message:    httpErr.Error(),
			Error:      httpErr.Error(),
			Hint:       output.HintForErrorCode(code),
			ErrorCode:  code,
			StatusCode: httpErr.StatusCode,
			ExitCode:   exitCodeForStatus(httpErr.StatusCode),
		}
		emitAgentFailure(st)
		return ErrSilent
	}
	return failNetwork(err.Error())
}

func printUpdateResult(result updateResult) {
	if jsonMode {
		output.PrintJSON(result)
		return
	}
	switch result.Status {
	case "updated":
		output.Success(result.Message)
	case "up_to_date":
		output.Success(result.Message)
	case "package_manager_required", "manual_update_required":
		output.Warn(result.Message)
	default:
		output.Info(result.Message)
	}
	if result.Command != "" {
		output.Gray("  " + result.Command)
	}
	if result.URL != "" {
		output.Gray("  " + result.URL)
	}
}

func detectInstallMethod(exe string) string {
	exe = filepath.Clean(exe)
	if exe != "" && pathHasSegment(exe, "node_modules") && npmPackageRoot(exe) != "" {
		return "npm"
	}
	if isGoInstallPath(exe) {
		return "go"
	}
	return "binary"
}

func npmPackageRoot(exe string) string {
	for dir := filepath.Dir(exe); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err == nil {
			var pkg struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Name == updateNPMPackage {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return ""
}

func pathHasSegment(path, segment string) bool {
	for _, part := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool {
		return r == os.PathSeparator || r == '/' || r == '\\'
	}) {
		if part == segment {
			return true
		}
	}
	return false
}

func isGoInstallPath(exe string) bool {
	dir := filepath.Clean(filepath.Dir(exe))
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" && sameCleanPath(dir, gobin) {
		return true
	}
	for _, gp := range filepath.SplitList(os.Getenv("GOPATH")) {
		if gp == "" {
			continue
		}
		if sameCleanPath(dir, filepath.Join(gp, "bin")) {
			return true
		}
	}
	return false
}

func sameCleanPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func updateInstallCommand(method, targetVersion string) string {
	tag := normalizeReleaseTag(targetVersion)
	switch method {
	case "npm":
		return "npm install -g " + updateNPMPackage + "@" + strings.TrimPrefix(tag, "v")
	case "go":
		return "go install " + updateGoPackage + "@" + tag
	default:
		return ""
	}
}

func updateAvailable(current, target string, explicitTarget bool) bool {
	current = normalizeReleaseVersion(current)
	target = normalizeReleaseVersion(target)
	if target == "" {
		return false
	}
	if current == "" || strings.EqualFold(current, "dev") {
		return true
	}
	if explicitTarget {
		return !strings.EqualFold(current, target)
	}
	return compareVersions(target, current) > 0
}

func normalizeReleaseTag(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(v), "v") {
		return "v" + strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	}
	return "v" + v
}

func normalizeReleaseVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	return v
}

func compareVersions(a, b string) int {
	av, aok := parseVersionParts(a)
	bv, bok := parseVersionParts(b)
	if !aok || !bok {
		return strings.Compare(a, b)
	}
	for i := 0; i < len(av); i++ {
		if av[i] > bv[i] {
			return 1
		}
		if av[i] < bv[i] {
			return -1
		}
	}
	return 0
}

func parseVersionParts(v string) ([3]int, bool) {
	var out [3]int
	v = normalizeReleaseVersion(v)
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func releaseAssetName(targetVersion string) (string, error) {
	goos := updateGOOS()
	goarch := updateGOARCH()
	if goos == "windows" && goarch == "arm64" {
		goarch = "amd64"
	}
	switch goos {
	case "darwin", "linux", "windows":
	default:
		return "", fmt.Errorf("unsupported update platform: %s-%s", goos, goarch)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported update architecture: %s-%s", goos, goarch)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("kibana-cli-%s-%s-%s%s", normalizeReleaseVersion(targetVersion), goos, goarch, ext), nil
}

func findReleaseAsset(assets []updateAsset, name string) (updateAsset, bool) {
	for _, a := range assets {
		if a.Name == name && a.BrowserDownloadURL != "" {
			return a, true
		}
	}
	return updateAsset{}, false
}

func verifyArchiveChecksum(archiveData, checksumData []byte, assetName string) error {
	want, err := checksumForAsset(checksumData, assetName)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archiveData)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum verification failed for %s", assetName)
	}
	return nil
}

func checksumForAsset(checksumData []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(checksumData), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		if fields[len(fields)-1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum entry not found for %s", assetName)
}

func extractReleaseBinary(archiveData []byte, assetName, binaryName string) ([]byte, error) {
	switch {
	case strings.HasSuffix(assetName, ".zip"):
		return extractZipBinary(archiveData, binaryName)
	case strings.HasSuffix(assetName, ".tar.gz"):
		return extractTarGzBinary(archiveData, binaryName)
	default:
		return nil, fmt.Errorf("unsupported release archive: %s", assetName)
	}
}

func extractZipBinary(archiveData []byte, binaryName string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(archiveData), int64(len(archiveData)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if filepath.Base(f.Name) != binaryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		_ = rc.Close()
		return data, readErr
	}
	return nil, fmt.Errorf("%s not found in release archive", binaryName)
}

func extractTarGzBinary(archiveData []byte, binaryName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, fmt.Errorf("%s not found in release archive", binaryName)
}

func replaceExecutable(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".kibana-cli-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	backup := path + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	cleanupTmp = false
	_ = os.Remove(backup)
	return nil
}
