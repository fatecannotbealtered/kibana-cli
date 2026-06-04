package cmd

import (
	"context"
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
	ExitOK        = 0
	ExitBadArgs   = 2
	ExitAuth      = 3
	ExitNotFound  = 4
	ExitForbidden = 5
	ExitRateLimit = 6
	ExitNetwork   = 7
)

const defaultTimeoutSeconds = 60

var ErrSilent = errors.New("")

var version = "1.0.2"

var (
	jsonMode       bool
	forceMode      bool
	quietMode      bool
	dryRun         bool
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

	rootCmd.PersistentFlags().BoolVar(&jsonMode, "json", false, "Output result as JSON")
	rootCmd.PersistentFlags().BoolVar(&forceMode, "force", false, "Overwrite existing field-map.yaml on config init")
	rootCmd.PersistentFlags().BoolVar(&quietMode, "quiet", false, "Suppress non-JSON stdout output (for scripts and AI Agents)")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Preview planned action without executing (writes and read queries)")
	rootCmd.PersistentFlags().BoolVar(&insecureTLS, "insecure", false, "Skip TLS certificate verification (corporate/self-signed CA)")
	rootCmd.PersistentFlags().IntVar(&timeoutSeconds, "timeout", defaultTimeoutSeconds, "HTTP request timeout in seconds")

	cobra.OnInitialize(func() { output.Quiet = quietMode })

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		lastExit = 0
		cmdStartTime = time.Now()
		activeCmd = cmd
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
	case status == 401:
		return ExitAuth
	case status == 403:
		return ExitForbidden
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
		output.PrintJSON(detail)
	} else {
		output.Info("[dry-run] " + action)
	}
	return true
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
