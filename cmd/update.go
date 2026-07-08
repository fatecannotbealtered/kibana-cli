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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

const (
	updateNPMPackage              = "@fateforge/kibana-cli"
	updateGoPackage               = "github.com/fatecannotbealtered/kibana-cli/cmd/kibana-cli"
	updateSkillRepo               = "fatecannotbealtered/kibana-cli"
	updateMaxErrorBodyLen         = 512
	updateMaxSignatureBundleBytes = 1 << 20

	// updateTrustRootSource describes — honestly — where the Sigstore trust
	// material came from. The TUF anchor (root.json) is embedded in the binary,
	// but the trusted_root.json target is refreshed from the public Sigstore TUF
	// CDN (cache-first via WithForceCache). UNRESOLVED: a fully offline embedded
	// trusted_root is not available in sigstore-go v1.2.1, so a cold cache still
	// needs one network refresh. See verifySigstoreBundle.
	updateTrustRootSource = "embedded-tuf-anchor+cached-trusted-root-refresh"
)

// Update runs as staged work with exactly one atomic commit point (the binary
// swap). Every failure and interruption reports the stage it happened in so the
// agent can tell — from the envelope alone — whether the installed binary
// changed.
const (
	updateStageDiscover        = "discover"
	updateStageDownload        = "download"
	updateStageVerifySignature = "verify_signature"
	updateStageVerifyChecksum  = "verify_checksum"
	updateStageReplace         = "replace"
	updateStageSkillSync       = "skill_sync"
)

var (
	updateRepo               = "fatecannotbealtered/kibana-cli"
	updateGitHubAPIBase      = "https://api.github.com"
	updateCheckOnly          bool
	updateTargetVersion      string
	updateTargetVersionAlias string
	updateExecutablePath     = os.Executable
	updateGOOS               = func() string { return runtime.GOOS }
	updateGOARCH             = func() string { return runtime.GOARCH }
	updateReplaceBinary      = replaceExecutable
	updateSkillSync          = runUpdateSkillSync
	updateRunPackageManager  = runPackageManagerInstall
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for or install the latest kibana-cli release",
	Long: `Check GitHub Releases for a newer kibana-cli version and update safely in one call.

A bare ` + "`update`" + ` upgrades regardless of install method:
  - Package-manager installs (npm / Go): the binary is OWNED by the package
    manager, so instead of mutating its files in place, the command DRIVES the
    manager — it runs the install command for you (e.g. npm install -g
    @fateforge/kibana-cli@<version>), then syncs the Skill. Integrity is the
    package manager's own; signature_status stays "not_checked". The new version
    takes effect on the next invocation.
  - Standalone binaries are replaced in place only after the Sigstore signature on
    checksums.txt is verified in-process against this repo's tagged release workflow
    identity and the archive SHA256 is verified against checksums.txt. An unsigned or
    unverifiable release is refused; there is no skip path.

--check and --dry-run are optional read-only flags: they report or preview the
plan (including the package-manager command) without changing anything.`,
	Args: cobra.NoArgs,
	RunE: runUpdate,
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "Check for updates without changing files")
	updateCmd.Flags().StringVar(&updateTargetVersion, "target-version", "", "Install or check a specific release version (e.g. X.Y.Z or vX.Y.Z)")
	// --version is the historical name for the target-version flag, kept as a
	// hidden deprecated alias so existing callers do not break. CLI-SPEC §2/§14
	// standardize on --target-version.
	updateCmd.Flags().StringVar(&updateTargetVersionAlias, "version", "", "Deprecated alias for --target-version")
	_ = updateCmd.Flags().MarkHidden("version")
	_ = updateCmd.Flags().MarkDeprecated("version", "use --target-version")
}

// resolveUpdateTargetVersion folds the deprecated --version alias into the
// canonical --target-version value. --target-version wins when both are set.
func resolveUpdateTargetVersion() string {
	if v := strings.TrimSpace(updateTargetVersion); v != "" {
		return v
	}
	return strings.TrimSpace(updateTargetVersionAlias)
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
	Status            string         `json:"status"`
	Message           string         `json:"message"`
	PreviousVersion   string         `json:"previous_version,omitempty"`
	CurrentVersion    string         `json:"current_version"`
	TargetVersion     string         `json:"target_version"`
	LatestVersion     string         `json:"latest_version,omitempty"`
	UpdateAvailable   bool           `json:"update_available"`
	InstallMethod     string         `json:"install_method"`
	Path              string         `json:"path,omitempty"`
	Asset             string         `json:"asset,omitempty"`
	URL               string         `json:"url,omitempty"`
	Command           string         `json:"command,omitempty"`
	Hint              string         `json:"hint,omitempty"`
	DryRun            bool           `json:"dry_run,omitempty"`
	ChecksumVerified  bool           `json:"checksum_verified,omitempty"`
	SignatureStatus   string         `json:"signature_status,omitempty"`
	SignatureVerified bool           `json:"signature_verified,omitempty"`
	TrustRootSource   string         `json:"trust_root_source,omitempty"`
	SkillSyncCommand  string         `json:"skill_sync_command,omitempty"`
	SkillSyncStatus   string         `json:"skill_sync_status,omitempty"`
	Notices           []updateNotice `json:"notices,omitempty"`
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
	// Single command, no confirm token: a bare `update` performs discover ->
	// download -> verify -> replace -> skill_sync in one call. Self-update is
	// exempt from the §7 dry-run/confirm write gate; the safety guarantee is the
	// in-process Sigstore verification, not an agent's review of a preview.
	// --check and --dry-run stay as optional read-only flags.
	targetFlag := resolveUpdateTargetVersion()
	release, err := fetchUpdateRelease(apiCtx(), targetFlag)
	if err != nil {
		return handleUpdateError(err, updateStageDiscover, false, "not_run")
	}
	targetVersion := normalizeReleaseVersion(release.TagName)
	if targetVersion == "" {
		return failUpdateStage(output.ErrValidation, ExitBadArgs, "release has no tag_name", updateStageDiscover, false, "not_run")
	}
	currentVersion := normalizeReleaseVersion(version)
	installPath, _ := updateExecutablePath()
	installMethod := detectInstallMethod(installPath)
	available := updateAvailable(currentVersion, targetVersion, targetFlag != "")

	result := updateResult{
		Status:           "up_to_date",
		Message:          "kibana-cli is already up to date",
		CurrentVersion:   version,
		TargetVersion:    targetVersion,
		LatestVersion:    targetVersion,
		UpdateAvailable:  available,
		InstallMethod:    installMethod,
		Path:             installPath,
		URL:              release.HTMLURL,
		SignatureStatus:  "not_checked",
		SkillSyncCommand: updateSkillSyncCommand(),
		SkillSyncStatus:  "not_run",
	}
	if updateCheckOnly {
		result.Notices = updateNoticesFromResult(result, "update_check")
	}
	// Idempotent: already on the latest (or requested) version is a no-op ok.
	if !available {
		writeUpdateNoticeCache(nil)
		printUpdateResult(result)
		return nil
	}

	result.Status = "update_available"
	result.Message = fmt.Sprintf("kibana-cli %s is available", targetVersion)
	result.Command = updateInstallCommand(installMethod, targetVersion)

	if updateCheckOnly {
		result.Notices = updateNoticesFromResult(result, "update_check")
		printUpdateResult(result)
		return nil
	}
	if installMethod == "npm" || installMethod == "go" {
		return runPackageManagerUpdate(result, installMethod, targetVersion)
	}
	if installPath == "" {
		return failUpdateStage(output.ErrConfig, ExitAuth, "could not determine current executable path", updateStageReplace, false, "not_run")
	}

	assetName, err := releaseAssetName(targetVersion)
	if err != nil {
		return failUpdateStage(output.ErrValidation, ExitBadArgs, err.Error(), updateStageDiscover, false, "not_run")
	}
	result.Asset = assetName

	// --dry-run is an OPTIONAL read-only preview of the plan. It issues NO
	// confirm_token and NO expires_at — it is no longer a gate before update.
	if dryRun {
		result.DryRun = true
		result.Message = fmt.Sprintf("would update kibana-cli from %s to %s", version, targetVersion)
		printUpdateResultPreview(result)
		return nil
	}

	asset, ok := findReleaseAsset(release.Assets, assetName)
	if !ok {
		return failUpdateStage(output.ErrValidation, ExitBadArgs, "release asset not found: "+assetName, updateStageDiscover, false, "not_run")
	}
	checksums, ok := findReleaseAsset(release.Assets, "checksums.txt")
	if !ok {
		return failUpdateStage(output.ErrValidation, ExitBadArgs, "release checksums.txt not found", updateStageDiscover, false, "not_run")
	}
	signatureBundle, signatureBundleFound := findReleaseAsset(release.Assets, "checksums.txt.sigstore.json")

	archiveData, err := downloadUpdateURL(apiCtx(), asset.BrowserDownloadURL)
	if err != nil {
		return handleUpdateError(err, updateStageDownload, false, "not_run")
	}
	checksumData, err := downloadUpdateURL(apiCtx(), checksums.BrowserDownloadURL)
	if err != nil {
		return handleUpdateError(err, updateStageDownload, false, "not_run")
	}
	signatureStatus, err := verifyChecksumSignature(apiCtx(), checksumData, signatureBundle, signatureBundleFound)
	if err != nil {
		return handleVerifySignatureError(err, signatureStatus)
	}
	if err := verifyArchiveChecksum(archiveData, checksumData, assetName); err != nil {
		return failUpdateStage(output.ErrIntegrity, ExitGeneral, err.Error(), updateStageVerifyChecksum, false, "not_run")
	}
	binName := "kibana-cli"
	if updateGOOS() == "windows" {
		binName += ".exe"
	}
	binaryData, err := extractReleaseBinary(archiveData, assetName, binName)
	if err != nil {
		// Extraction reads the already-downloaded, integrity-verified archive into
		// a temp dir; a failure here is a local IO/archive fault, not a network
		// blip — atomic swap not yet committed.
		return failUpdateStage(output.ErrIO, ExitGeneral, err.Error(), updateStageReplace, false, "not_run")
	}
	if err := updateReplaceBinary(installPath, binaryData); err != nil {
		code, exit := classifyReplaceError(err)
		return failUpdateStage(code, exit, "failed to replace executable: "+err.Error(), updateStageReplace, false, "not_run")
	}

	// The binary swap committed: from here `current_version` is the new version
	// and binary_replaced is true. A skill_sync failure is now a PARTIAL SUCCESS,
	// not a hard failure that loses the fact the binary already updated.
	result.PreviousVersion = version
	result.CurrentVersion = targetVersion
	result.UpdateAvailable = false
	result.ChecksumVerified = true
	result.SignatureStatus = signatureStatus
	result.SignatureVerified = signatureStatus == "verified"
	result.TrustRootSource = updateTrustRootSource
	writeUpdateNoticeCache(nil)

	if err := updateSkillSync(apiCtx(), updateSkillRepo); err != nil {
		return failSkillSyncPartial(result, err)
	}

	result.Status = "updated"
	result.Message = fmt.Sprintf("updated kibana-cli from %s to %s", version, targetVersion)
	result.Hint = fmt.Sprintf("run \"kibana-cli changelog --since %s\" before continuing", normalizeReleaseVersion(result.PreviousVersion))
	result.SkillSyncStatus = "synced"
	printUpdateResult(result)
	return nil
}

// classifyReplaceError maps a binary-swap failure to its agent next-action. The
// swap touches only local files (temp write, chmod, same-dir rename), so failures
// here are local environment faults — permission -> E_FORBIDDEN (exit 4),
// everything else (disk full, file locked, partial write) -> E_IO (exit 1).
// These were previously misclassified as a retryable network/config error.
func classifyReplaceError(err error) (output.ErrorCode, int) {
	if errors.Is(err, os.ErrPermission) {
		return output.ErrForbidden, ExitForbidden
	}
	return output.ErrIO, ExitGeneral
}

// failSkillSyncPartial reports the binary-replaced-but-Skill-unsynced state as a
// partial success: ok:false, binary_replaced:true, with the skill_sync_command
// the agent must run. The agent now knows it is on the new binary and only needs
// to retry the Skill sync — not re-run the whole update.
func failSkillSyncPartial(result updateResult, err error) error {
	result.Status = "skill_sync_failed"
	result.SkillSyncStatus = "failed"
	result.Message = fmt.Sprintf(
		"updated kibana-cli to %s, but Skill sync failed: %s — run %q, then \"kibana-cli changelog --since %s\"",
		result.CurrentVersion, err.Error(), result.SkillSyncCommand, normalizeReleaseVersion(result.PreviousVersion),
	)
	details := updateStageDetails(updateStageSkillSync, result.CurrentVersion, true, "failed")
	details["skill_sync_command"] = result.SkillSyncCommand
	details["previous_version"] = result.PreviousVersion
	details["target_version"] = result.TargetVersion
	details["update_available"] = false
	details["signature_verified"] = result.SignatureVerified
	details["signature_status"] = result.SignatureStatus
	st := AgentStatus{
		OK:        false,
		Status:    "skill_sync_failed",
		Message:   result.Message,
		Error:     result.Message,
		Hint:      "binary is at " + result.CurrentVersion + "; run " + result.SkillSyncCommand + ", then changelog --since " + normalizeReleaseVersion(result.PreviousVersion),
		ErrorCode: output.ErrNetwork,
		ExitCode:  ExitNetwork,
	}
	applyAgentExit(st)
	if jsonMode {
		output.PrintJSON(output.FailureEnvelope(st.ErrorCode, st.Message, details, elapsedDurationMs()))
		return ErrSilent
	}
	output.Warn(st.Message)
	return ErrSilent
}

// updateStageDetails builds the staged-failure detail block every update error
// envelope must carry, so the agent can determine its post-failure state without
// re-probing: stage, current_version (the version actually running NOW),
// binary_replaced, and skill_sync_status.
func updateStageDetails(stage, currentVersion string, binaryReplaced bool, skillSyncStatus string) map[string]any {
	return map[string]any{
		"stage":             stage,
		"current_version":   currentVersion,
		"binary_replaced":   binaryReplaced,
		"skill_sync_status": skillSyncStatus,
	}
}

// failUpdateStage emits a staged update-failure envelope with the §14 invariant
// fields. currentVersion is always the version the tool is running now.
func failUpdateStage(code output.ErrorCode, exit int, msg, stage string, binaryReplaced bool, skillSyncStatus string) error {
	details := updateStageDetails(stage, version, binaryReplaced, skillSyncStatus)
	st := AgentStatus{
		OK:        false,
		Status:    StatusAPIError,
		Message:   msg,
		Error:     msg,
		Hint:      output.HintForErrorCode(code),
		ErrorCode: code,
		ExitCode:  exit,
	}
	if hint := output.HintForErrorCode(code); hint != "" {
		details["hint"] = hint
	}
	applyAgentExit(st)
	if jsonMode {
		output.PrintJSON(output.FailureEnvelope(code, msg, details, elapsedDurationMs()))
		return ErrSilent
	}
	output.Error(msg)
	if st.Hint != "" {
		output.AuxGray("  " + st.Hint)
	}
	return ErrSilent
}

func updateSkillSyncCommand() string {
	return "npx skills add " + updateSkillRepo + " -y -g"
}

func runUpdateSkillSync(ctx context.Context, repo string) error {
	command := exec.CommandContext(ctx, "npx", "skills", "add", repo, "-y", "-g")
	outputBytes, err := command.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(outputBytes))
		if msg != "" {
			return fmt.Errorf("%w: %s", err, truncateUpdateMessage(msg, 300))
		}
		return err
	}
	return nil
}

// runPackageManagerUpdate handles `update` for a package-manager-managed install
// (npm or Go). A standalone binary is replaced in place after in-process Sigstore
// verification; a package-managed binary is OWNED by the package manager, so
// replacing it in place would desync the manager's metadata. Instead the tool
// DRIVES the package manager — it runs the exact install command on the user's
// behalf (the same command `update --check` prints), then syncs the Skill, so a
// bare `update` upgrades in one call regardless of install method. Integrity on
// this path is the package manager's own (npm registry integrity/provenance), so
// signature_status stays "not_checked". The new version takes effect on the next
// invocation (this process is still the old image).
func runPackageManagerUpdate(result updateResult, method, targetVersion string) error {
	// --dry-run is an optional read-only preview: show the command, run nothing.
	if dryRun {
		result.DryRun = true
		result.Status = "package_manager_preview"
		result.Message = fmt.Sprintf("would update kibana-cli from %s to %s by running: %s", version, targetVersion, result.Command)
		printUpdateResultPreview(result)
		return nil
	}

	if err := updateRunPackageManager(apiCtx(), method, targetVersion); err != nil {
		// The package manager owns download + integrity + replace; a failure here
		// leaves the installed binary unchanged (binary_replaced:false).
		return failPackageManagerStage(result, err)
	}

	// The package manager replaced the on-disk binary; this process is still the
	// old image, so the new version is effective on the next invocation.
	result.PreviousVersion = version
	result.CurrentVersion = targetVersion
	result.UpdateAvailable = false
	result.Status = "updated"
	result.Message = fmt.Sprintf("updated kibana-cli from %s to %s via %s (effective on next run)", version, targetVersion, method)
	writeUpdateNoticeCache(nil)

	if err := updateSkillSync(apiCtx(), updateSkillRepo); err != nil {
		return failSkillSyncPartial(result, err)
	}
	result.SkillSyncStatus = "synced"
	result.Hint = fmt.Sprintf("run \"kibana-cli changelog --since %s\" before continuing", normalizeReleaseVersion(result.PreviousVersion))
	printUpdateResult(result)
	return nil
}

// runPackageManagerInstall drives the package manager to install the target
// version — the same command updateInstallCommand prints. argv is built directly
// (no shell) so the version string cannot be reinterpreted by a shell.
func runPackageManagerInstall(ctx context.Context, method, targetVersion string) error {
	var name string
	var args []string
	switch method {
	case "npm":
		name = "npm"
		args = []string{"install", "-g", updateNPMPackage + "@" + normalizeReleaseVersion(targetVersion)}
	case "go":
		name = "go"
		args = []string{"install", updateGoPackage + "@" + normalizeReleaseTag(targetVersion)}
	default:
		return fmt.Errorf("unsupported package manager: %s", method)
	}
	command := exec.CommandContext(ctx, name, args...)
	outputBytes, err := command.CombinedOutput()
	if err != nil {
		out := strings.TrimSpace(string(outputBytes))
		if out != "" {
			return fmt.Errorf("%w: %s", err, truncateUpdateMessage(out, 300))
		}
		return err
	}
	return nil
}

// failPackageManagerStage reports a failed package-manager-driven update. The
// package manager owns download/integrity/replace, so a failure leaves the
// installed binary unchanged (binary_replaced:false). The exact command is
// surfaced so the agent can run it manually or relay it to the user.
func failPackageManagerStage(result updateResult, err error) error {
	msg := fmt.Sprintf("package-manager update failed: %s — run %q manually", strings.TrimSpace(err.Error()), result.Command)
	details := updateStageDetails(updateStageReplace, version, false, "not_run")
	details["install_method"] = result.InstallMethod
	details["command"] = result.Command
	st := AgentStatus{
		OK:        false,
		Status:    StatusAPIError,
		Message:   msg,
		Error:     msg,
		Hint:      "run " + result.Command,
		ErrorCode: output.ErrIO,
		ExitCode:  ExitGeneral,
	}
	applyAgentExit(st)
	if jsonMode {
		output.PrintJSON(output.FailureEnvelope(st.ErrorCode, msg, details, elapsedDurationMs()))
		return ErrSilent
	}
	output.Error(msg)
	if st.Hint != "" {
		output.AuxGray("  " + st.Hint)
	}
	return ErrSilent
}

func truncateUpdateMessage(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// verifyChecksumSignature enforces a mandatory, in-process Sigstore signature
// check on checksums.txt before the release is trusted. There is no skip path: a
// release without a signature bundle, or one whose signature does not verify
// against this repo's release-workflow identity, is refused. The returned status
// is always "verified" on the nil-error path.
func verifyChecksumSignature(ctx context.Context, checksumData []byte, bundle updateAsset, bundleFound bool) (string, error) {
	if !bundleFound {
		// A genuinely unsigned release is an integrity refusal, not a network
		// blip: there is nothing to retry. Keep it on the E_INTEGRITY path.
		return "missing", errors.New("release does not include checksums.txt.sigstore.json; refusing to install an unsigned release")
	}

	bundleData, err := downloadUpdateURL(ctx, bundle.BrowserDownloadURL)
	if err != nil {
		// Downloading the signature bundle is a network step: a failure here is
		// transient (network/timeout/interrupt), not an integrity failure.
		return "download_failed", &updateRetryableVerifyError{fmt.Errorf("downloading checksum signature bundle: %w", err)}
	}
	if len(bundleData) > updateMaxSignatureBundleBytes {
		return "failed", fmt.Errorf("checksum signature bundle exceeds %d bytes", updateMaxSignatureBundleBytes)
	}
	tmpDir, err := os.MkdirTemp("", "kibana-cli-signature-*")
	if err != nil {
		return "failed", fmt.Errorf("creating signature temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	checksumPath := filepath.Join(tmpDir, "checksums.txt")
	bundlePath := filepath.Join(tmpDir, "checksums.txt.sigstore.json")
	if err := os.WriteFile(checksumPath, checksumData, 0o600); err != nil {
		return "failed", fmt.Errorf("writing checksum temp file: %w", err)
	}
	if err := os.WriteFile(bundlePath, bundleData, 0o600); err != nil {
		return "failed", fmt.Errorf("writing checksum signature bundle: %w", err)
	}

	if err := updateVerifySignature(ctx, checksumPath, bundlePath, updateSignerIdentityRegexp()); err != nil {
		return "failed", err
	}
	return "verified", nil
}

// updateRetryableVerifyError marks a verify-stage failure that is transient
// (a network step inside signature verification — the bundle download or the
// TUF trust-root refresh), so the agent should back off and re-run `update`
// rather than treat it as a non-retryable supply-chain integrity failure.
type updateRetryableVerifyError struct{ err error }

func (e *updateRetryableVerifyError) Error() string { return e.err.Error() }
func (e *updateRetryableVerifyError) Unwrap() error { return e.err }

// handleVerifySignatureError classifies a verify_signature-stage failure by the
// agent's next action, NOT by lumping everything into E_INTEGRITY:
//
//   - context cancelled (SIGINT/SIGTERM) -> E_INTERRUPTED (exit 130), retryable;
//   - a network step (bundle download or TUF trust-root refresh) failing ->
//     the network/timeout taxonomy (retryable), because the release may be
//     perfectly valid and only the trust material was unreachable;
//   - a signature, identity, or transparency-log mismatch -> E_INTEGRITY
//     (exit 1, non-retryable): a forged or corrupt release is not a transient
//     blip to loop on.
func handleVerifySignatureError(err error, signatureStatus string) error {
	if isInterruptError(err) {
		return failUpdateInterrupted(updateStageVerifySignature, false, "not_run")
	}
	var trustErr *errTrustRootUnavailable
	var retryErr *updateRetryableVerifyError
	if errors.As(err, &trustErr) || errors.As(err, &retryErr) {
		code, exit := classifyUpdateNetworkError(err)
		return failUpdateStage(code, exit, "verifying release signature: "+err.Error(), updateStageVerifySignature, false, "not_run")
	}
	// Integrity failure is non-retryable: a missing or invalid signature is a
	// supply-chain red flag, not a transient blip an agent should retry.
	return failUpdateStage(output.ErrIntegrity, ExitGeneral, "verifying release signature: "+err.Error(), updateStageVerifySignature, false, "not_run")
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

// handleUpdateError classifies a discover/download failure and emits the staged
// update-failure envelope. Interruption (a cancelled context from SIGINT/SIGTERM)
// is mapped to E_INTERRUPTED so the agent receives a parseable terminal state
// instead of a bare killed process. Before the binary swap, the post-state is
// always "no change, still on <current>".
func handleUpdateError(err error, stage string, binaryReplaced bool, skillSyncStatus string) error {
	if isInterruptError(err) {
		return failUpdateInterrupted(stage, binaryReplaced, skillSyncStatus)
	}
	var httpErr *updateHTTPError
	if errors.As(err, &httpErr) {
		code := output.ErrorCodeFromStatus(httpErr.StatusCode)
		details := updateStageDetails(stage, version, binaryReplaced, skillSyncStatus)
		details["statusCode"] = httpErr.StatusCode
		if hint := output.HintForErrorCode(code); hint != "" {
			details["hint"] = hint
		}
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
		applyAgentExit(st)
		if jsonMode {
			output.PrintJSON(output.FailureEnvelope(code, httpErr.Error(), details, elapsedDurationMs()))
			return ErrSilent
		}
		emitAgentFailure(st)
		return ErrSilent
	}
	code, exit := classifyUpdateNetworkError(err)
	return failUpdateStage(code, exit, err.Error(), stage, binaryReplaced, skillSyncStatus)
}

// classifyUpdateNetworkError maps a non-interrupt transport failure to the
// retryable taxonomy: a timeout (client deadline or DeadlineExceeded) is
// E_TIMEOUT (exit 8) so the agent backs off on a deadline distinctly from a
// generic connection failure; an embedded HTTP status maps via the §6 table;
// everything else is E_NETWORK (exit 7). All three are retryable transients,
// never E_INTEGRITY.
func classifyUpdateNetworkError(err error) (output.ErrorCode, int) {
	if isUpdateTimeoutError(err) {
		return output.ErrTimeout, ExitTimeout
	}
	var httpErr *updateHTTPError
	if errors.As(err, &httpErr) {
		return output.ErrorCodeFromStatus(httpErr.StatusCode), exitCodeForStatus(httpErr.StatusCode)
	}
	return output.ErrNetwork, ExitNetwork
}

// isUpdateTimeoutError reports whether err is a request timeout — either a
// context deadline (the command --timeout budget elapsed) or a net.Error whose
// Timeout() is true (the http.Client.Timeout fired). A cancelled context
// (SIGINT) is handled separately as E_INTERRUPTED and is not a timeout.
func isUpdateTimeoutError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// isInterruptError reports whether err is the cancellation of the update context
// by SIGINT/SIGTERM (signal.NotifyContext in main cancels apiCtx()).
func isInterruptError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	// A download interrupted mid-flight surfaces as a wrapped url/net error whose
	// root cause is context.Canceled; errors.Is above already unwraps it.
	return false
}

// failUpdateInterrupted emits the terminal E_INTERRUPTED envelope (exit 130) on
// stdout so an interrupted agent always receives a parseable terminal state. The
// message states the post-state per the stage invariant: before the swap, "no
// change, still on <current>".
func failUpdateInterrupted(stage string, binaryReplaced bool, skillSyncStatus string) error {
	msg := "update cancelled by signal; no change, still on " + normalizeReleaseVersion(version)
	if binaryReplaced {
		msg = "update cancelled by signal after the binary was replaced; binary is at " +
			normalizeReleaseVersion(version) + ", Skill sync incomplete — run " + updateSkillSyncCommand()
	}
	return failUpdateStage(output.ErrInterrupted, ExitInterrupted, msg, stage, binaryReplaced, skillSyncStatus)
}

func printUpdateResult(result updateResult) {
	if jsonMode {
		printJSONSuccess(result)
		return
	}
	switch result.Status {
	case "updated":
		output.Success(result.Message)
	case "up_to_date":
		output.Success(result.Message)
	case "package_manager_required":
		output.Warn(result.Message)
	default:
		output.Info(result.Message)
	}
	if result.Command != "" {
		output.Gray("  " + result.Command)
	}
	if result.Hint != "" {
		output.Gray("  " + result.Hint)
	}
	if result.URL != "" {
		output.Gray("  " + result.URL)
	}
}

// printUpdateResultPreview renders the optional read-only `update --dry-run`
// preview. It returns NO confirm_token and NO expires_at — dry-run is no longer
// a gate, just a plan preview the agent may inspect.
func printUpdateResultPreview(result updateResult) {
	if jsonMode {
		printJSONSuccess(result)
		return
	}
	output.Info(result.Message)
	if result.Command != "" {
		output.Gray("  " + result.Command)
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

// replaceExecutable swaps the running binary in place with a cross-platform
// rename trick: write .<base>.new, rename the in-use target out to .<base>.old,
// move .new into place, and roll back from .old on failure. On Windows the
// running image can be renamed (it is not deletable), so the same path works
// without a .cmd helper or a wait-for-exit move loop; a .old left locked by the
// running process is removed best-effort and otherwise ignored.
func replaceExecutable(exePath string, binaryData []byte) error {
	target := exePath
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		target = resolved
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("stat executable %s: %w", target, err)
	}
	mode := info.Mode()
	if mode.Perm() == 0 {
		mode = 0o755
	}
	dir := filepath.Dir(target)
	base := filepath.Base(target)
	newPath := filepath.Join(dir, "."+base+".new")
	backupPath := filepath.Join(dir, "."+base+".old")

	_ = os.Remove(newPath)
	if err := os.WriteFile(newPath, binaryData, mode.Perm()); err != nil {
		return fmt.Errorf("writing replacement binary %s: %w", newPath, err)
	}
	if err := os.Chmod(newPath, mode.Perm()); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("setting executable mode on %s: %w", newPath, err)
	}

	_ = os.Remove(backupPath)
	if err := os.Rename(target, backupPath); err != nil {
		return fmt.Errorf("preparing to replace %s: %w; replacement left at %s", target, err, newPath)
	}
	if err := os.Rename(newPath, target); err != nil {
		_ = os.Rename(backupPath, target)
		return fmt.Errorf("replacing %s: %w; original restored", target, err)
	}
	_ = os.Remove(backupPath)
	return nil
}
