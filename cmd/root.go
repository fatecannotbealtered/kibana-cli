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

var ErrSilent = errors.New("")

var version = "1.1.0"

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
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return v
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
		if !isWriteCommand(cmd) {
			return nil
		}
		audit.Log(cmd.CommandPath(), os.Args[1:], lastExit, time.Since(cmdStartTime).Milliseconds())
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
	return rootCmd.ExecuteContext(ctx)
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
		detail["dryRun"] = true
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
	token := confirmTokenFor(action, tokenDetail)
	if dryRun {
		if jsonMode {
			printJSONSuccess(map[string]any{
				"preview": map[string]any{
					"action":  action,
					"changes": []map[string]any{preview},
				},
				"confirm_token": token,
				"expires_at":    time.Now().UTC().Add(confirmTokenTTL).Format(time.RFC3339),
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
	if strings.TrimSpace(confirmToken) != token {
		return false, failConflict("confirm token is invalid or stale", map[string]any{
			"action": action,
		})
	}
	return false, nil
}

func confirmTokenFor(action string, detail map[string]any) string {
	payload := map[string]any{
		"schema_version": output.SchemaVersion,
		"command":        commandPathForToken(),
		"action":         action,
		"detail":         detail,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte(action)
	}
	sum := sha256.Sum256(b)
	return "ct_" + hex.EncodeToString(sum[:])[:24]
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
