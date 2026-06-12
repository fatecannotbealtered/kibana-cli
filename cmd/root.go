package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	project "github.com/fatecannotbealtered/kibana-cli"
	"github.com/fatecannotbealtered/kibana-cli/internal/audit"
	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

// Exit codes for machine-readable error classification.
const (
	ExitOK              = 0
	ExitGeneral         = 1
	ExitBadArgs         = 2
	ExitNotFound        = 3
	ExitAuth            = 4
	ExitForbidden       = 4
	ExitConfirmRequired = 5
	ExitConflict        = 6
	ExitRateLimit       = 7
	ExitNetwork         = 7
	ExitTimeout         = 8
)

const defaultTimeoutSeconds = 60
const confirmTokenTTL = 15 * time.Minute

const (
	FormatJSON = "json"
	FormatText = "text"
	FormatRaw  = "raw"
)

const (
	toolName        = "kibana-cli"
	skillMinVersion = "1.1.0"
	securityTier    = "T1"
)

var ErrSilent = errors.New("")

var version = "dev"

var (
	jsonMode       bool
	jsonFlag       bool
	outputFormat   string
	compactMode    bool
	forceMode      bool
	quietMode      bool
	dryRun         bool
	confirmToken   string
	insecureTLS    bool
	timeoutSeconds int
)

var lastExit int
var cmdStartTime time.Time
var activeCmd *cobra.Command

func LastExitCode() int {
	code := lastExit
	return code
}

func apiCtx() context.Context {
	if activeCmd != nil {
		if ctx := activeCmd.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

func setExitCode(code int) {
	if code > lastExit {
		lastExit = code
	}
}

var rootCmd = &cobra.Command{
	Use:           "kibana-cli",
	Short:         "Query logs via Kibana (Console Proxy)",
	Version:       version,
	SilenceErrors: true,
	SilenceUsage:  true,
	Long: fmt.Sprintf("\n  %s\n  %s",
		output.FormatCyanBold("kibana-cli"),
		output.FormatGray("Log search for AI Agents — Kibana Console Proxy only")),
}

var readBuildInfo = debug.ReadBuildInfo

func resolveBuildVersion(v string) string {
	if v != "dev" {
		return v
	}
	if pkgVersion := packageJSONVersion(); pkgVersion != "" {
		return pkgVersion
	}
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" {
		if info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return v
}

func packageJSONVersion() string {
	var raw struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(project.PackageJSON), &raw); err != nil {
		return ""
	}
	return strings.TrimSpace(raw.Version)
}

func init() {
	version = resolveBuildVersion(version)
	rootCmd.Version = version
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	outputFormat = FormatJSON
	jsonMode = true

	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", FormatJSON, "Output format: json, text, or raw")
	rootCmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Output result as JSON (compatibility alias for --format json)")
	rootCmd.PersistentFlags().BoolVar(&compactMode, "compact", false, "Use compact single-line JSON output")
	rootCmd.PersistentFlags().BoolVar(&forceMode, "force", false, "Overwrite existing field-map.yaml on config init")
	rootCmd.PersistentFlags().BoolVar(&quietMode, "quiet", false, "Suppress auxiliary text output")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Preview planned action without executing (writes and read queries)")
	rootCmd.PersistentFlags().StringVar(&confirmToken, "confirm", "", "Confirm token returned by --dry-run for write commands")
	rootCmd.PersistentFlags().BoolVar(&insecureTLS, "insecure", false, "Skip TLS certificate verification (corporate/self-signed CA)")
	rootCmd.PersistentFlags().IntVar(&timeoutSeconds, "timeout", defaultTimeoutSeconds, "HTTP request timeout in seconds")
	installUpdateNoticeHelp(rootCmd)

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		lastExit = 0
		cmdStartTime = time.Now()
		activeCmd = cmd
		if err := applyOutputFormat(cmd); err != nil {
			return err
		}
		initClientOptionsFromEnv()
		return nil
	}

	rootCmd.PersistentPostRunE = func(cmd *cobra.Command, _ []string) error {
		return nil
	}
}

func Execute() error {
	ctx := context.Background()
	return ExecuteContext(ctx)
}

func ExecuteContext(ctx context.Context) error {
	lastExit = 0
	cmdStartTime = time.Now()
	activeCmd = nil
	defer auditWriteAttempt()
	err := rootCmd.ExecuteContext(ctx)
	if err != nil && !errors.Is(err, ErrSilent) {
		return failValidation(err.Error())
	}
	return err
}

func auditWriteAttempt() {
	cmd := activeCmd
	if cmd == nil || !isWriteCommand(cmd) {
		return
	}
	audit.Log(cmd.CommandPath(), os.Args[1:], lastExit, time.Since(cmdStartTime).Milliseconds())
}

func exitCodeForStatus(status int) int {
	switch {
	case status == 401 || status == 403:
		return ExitAuth
	case status == 404:
		return ExitNotFound
	case status == 429:
		return ExitRateLimit
	case status >= 500:
		return ExitNetwork
	default:
		return ExitBadArgs
	}
}

func dryRunOutput(action string, detail map[string]any) bool {
	if !dryRun {
		return false
	}
	if jsonMode {
		if detail == nil {
			detail = map[string]any{}
		}
		detail["action"] = action
		detail["dry_run"] = true
		printJSONSuccess(detail)
	} else {
		output.Info("[dry-run] " + action)
	}
	return true
}

func writePlan(action string, preview, tokenDetail map[string]any) (bool, error) {
	if preview == nil {
		preview = map[string]any{}
	}
	if tokenDetail == nil {
		tokenDetail = preview
	}
	expiresAt := time.Now().UTC().Add(confirmTokenTTL).Truncate(time.Second)
	token := confirmTokenForExpiry(action, tokenDetail, expiresAt)
	if dryRun {
		if jsonMode {
			printJSONSuccess(map[string]any{
				"preview": map[string]any{
					"action":  action,
					"changes": []map[string]any{preview},
				},
				"confirm_token": token,
				"expires_at":    expiresAt.Format(time.RFC3339),
			})
		} else {
			output.Info("[dry-run] " + action)
			output.AuxGray("  confirm: " + token)
		}
		return true, nil
	}
	if strings.TrimSpace(confirmToken) == "" {
		return false, failConfirmRequired(action, preview, token)
	}
	if err := validateConfirmToken(action, tokenDetail, strings.TrimSpace(confirmToken)); err != nil {
		return false, failConflict(err.Error(), map[string]any{"action": action})
	}
	return false, nil
}

func confirmTokenForExpiry(action string, detail map[string]any, expiresAt time.Time) string {
	payload := map[string]any{
		"schema_version":     output.SchemaVersion,
		"command":            commandPathForToken(),
		"args":               operationArgsForToken(),
		"action":             action,
		"detail":             detail,
		"actor":              actorForToken(detail),
		"permission_context": "local_write",
		"expires_at":         expiresAt.Format(time.RFC3339),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte(action)
	}
	sum := confirmDigest32(b)
	return fmt.Sprintf("ct_%d_%s", expiresAt.Unix(), hex.EncodeToString(sum[:])[:24])
}

func validateConfirmToken(action string, detail map[string]any, token string) error {
	parts := strings.Split(token, "_")
	if len(parts) != 3 || parts[0] != "ct" {
		return errors.New("confirm token is invalid or stale")
	}
	unix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || unix <= 0 {
		return errors.New("confirm token is invalid or stale")
	}
	expiresAt := time.Unix(unix, 0).UTC()
	if time.Now().UTC().After(expiresAt) {
		return errors.New("confirm token has expired")
	}
	if token != confirmTokenForExpiry(action, detail, expiresAt) {
		return errors.New("confirm token is invalid or stale")
	}
	return nil
}

func operationArgsForToken() []string {
	args := append([]string{}, os.Args[1:]...)
	if activeCmd != nil {
		args = activeCmd.Flags().Args()
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dry-run", "--json", "--compact", "--quiet":
			continue
		case "--confirm", "--format":
			i++
			continue
		default:
			if strings.HasPrefix(arg, "--confirm=") ||
				strings.HasPrefix(arg, "--format=") ||
				strings.HasPrefix(arg, "--json=") ||
				strings.HasPrefix(arg, "--compact=") ||
				strings.HasPrefix(arg, "--quiet=") {
				continue
			}
			out = append(out, arg)
		}
	}
	return out
}

func actorForToken(detail map[string]any) map[string]any {
	actor := map[string]any{}
	if host, _ := detail["host"].(string); host != "" {
		actor["host"] = host
	}
	if user, _ := detail["username"].(string); user != "" {
		actor["username"] = user
	}
	if len(actor) == 0 {
		if cfg, err := config.Load(); err == nil {
			if cfg.Host != "" {
				actor["host"] = cfg.Host
			}
			if cfg.Username != "" {
				actor["username"] = cfg.Username
			}
			actor["authSource"] = config.AuthSource()
		}
	}
	if len(actor) == 0 {
		actor["scope"] = "local"
	}
	return actor
}

func commandPathForToken() string {
	if activeCmd != nil {
		return activeCmd.CommandPath()
	}
	return rootCmd.CommandPath()
}

func secretHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func applyOutputFormat(cmd *cobra.Command) error {
	format := strings.ToLower(strings.TrimSpace(outputFormat))
	if format == "" {
		format = FormatJSON
	}
	formatSet := flagExplicitlySet(cmd, "format")
	jsonSet := flagExplicitlySet(cmd, "json")
	if jsonSet && formatSet && format != FormatJSON {
		jsonMode = true
		outputFormat = FormatJSON
		output.JSONCompact = compactMode
		output.Quiet = quietMode
		return failValidation("--json cannot be combined with --format " + format)
	}
	if jsonSet && jsonFlag {
		format = FormatJSON
	}
	switch format {
	case FormatJSON, FormatText, FormatRaw:
	default:
		jsonMode = true
		outputFormat = FormatJSON
		output.JSONCompact = compactMode
		output.Quiet = quietMode
		return failValidation("--format must be one of: json, text, raw")
	}
	if format == FormatRaw && !commandSupportsFormat(cmd, FormatRaw) {
		jsonMode = true
		outputFormat = FormatJSON
		output.JSONCompact = compactMode
		output.Quiet = quietMode
		return failValidation(cmd.CommandPath() + " does not support --format raw")
	}
	outputFormat = format
	jsonMode = format == FormatJSON
	output.JSONCompact = compactMode && jsonMode
	output.Quiet = quietMode
	return nil
}

func commandSupportsFormat(cmd *cobra.Command, format string) bool {
	if format == FormatJSON || format == FormatText {
		return true
	}
	if format == FormatRaw {
		return cmd != nil && cmd.CommandPath() == "kibana-cli reference"
	}
	return false
}

func flagExplicitlySet(cmd *cobra.Command, name string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Flags().Changed(name) || c.PersistentFlags().Changed(name) {
			return true
		}
	}
	return false
}

func isWriteCommand(cmd *cobra.Command) bool {
	return cmd.Annotations["write"] == "true"
}

func markWrite(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations["write"] = "true"
}

func applyInsecureFromEnv() {
	if insecureTLS {
		return
	}
	v := strings.TrimSpace(os.Getenv("KIBANA_CLI_INSECURE"))
	if v == "1" || strings.EqualFold(v, "true") {
		insecureTLS = true
	}
}

func timeoutExplicitlySet(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Flags().Changed("timeout") || c.PersistentFlags().Changed("timeout") {
			return true
		}
	}
	return false
}

func applyTimeoutFromEnv(cmd *cobra.Command) int {
	if timeoutExplicitlySet(cmd) {
		if timeoutSeconds > 0 {
			return timeoutSeconds
		}
		return defaultTimeoutSeconds
	}
	if s := strings.TrimSpace(os.Getenv("KIBANA_CLI_TIMEOUT")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return defaultTimeoutSeconds
}

func initClientOptionsFromEnv() {
	applyInsecureFromEnv()
	sec := applyTimeoutFromEnv(activeCmd)
	kibanaclient.SetClientOptions(kibanaclient.ClientOptions{
		Timeout:            time.Duration(sec) * time.Second,
		InsecureSkipVerify: insecureTLS,
	})
}

func newKibanaClient() (*kibanaclient.Client, *config.Config, error) {
	initClientOptionsFromEnv()
	cfg, err := config.MustLoad()
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not configured") {
			st := agentNotConfigured()
			emitAgentFailure(st)
		} else {
			_ = failConfig(msg)
		}
		return nil, nil, ErrSilent
	}
	return kibanaclient.NewClient(cfg), cfg, nil
}

func getFieldsFlag(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	raw, _ := cmd.Flags().GetString("fields")
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range splitCSV(raw) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, strings.TrimSpace(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, strings.TrimSpace(cur))
	return out
}

func projectTopLevelFields(payload map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return payload
	}
	out := map[string]any{}
	selected := map[string]bool{}
	for _, field := range fields {
		selected[field] = true
		if v, ok := payload[field]; ok {
			out[field] = v
		}
	}
	if selected["_untrusted"] {
		if v, ok := payload["_untrusted"]; ok {
			out["_untrusted"] = v
		}
		return out
	}
	if raw, ok := payload["_untrusted"].([]string); ok {
		var kept []string
		for _, tag := range raw {
			top := tag
			if i := strings.Index(top, "."); i >= 0 {
				top = top[:i]
			}
			if selected[top] {
				kept = append(kept, tag)
			}
		}
		if len(kept) > 0 {
			out["_untrusted"] = kept
		}
	}
	return out
}
