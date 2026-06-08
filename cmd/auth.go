package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Configure Kibana authentication",
	Long: `Configure Kibana base URL and HTTP Basic credentials.

Authentication precedence (highest first):
  1. KIBANA_CLI_HOST / KIBANA_CLI_USER / KIBANA_CLI_PASSWORD
  2. ~/.kibana-cli/config.json (+ OS credential store for password)

Use the Kibana site root URL only (e.g. https://kibana.example.com), not a Discover /app/ link.`,
}

var (
	authLoginHostFlag     string
	authLoginUserFlag     string
	authLoginPasswordFlag string
)

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Save Kibana credentials",
	Long: `Save credentials non-interactively.

Prefer environment variables in CI/agents:
  export KIBANA_CLI_HOST=... KIBANA_CLI_USER=... KIBANA_CLI_PASSWORD=...

Examples:
  kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...'
  kibana-cli auth login --host https://kibana.example.com --user dev_ro --password '...' --dry-run`,
	RunE: runAuthLogin,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove saved credentials",
	RunE:  runAuthLogout,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication configuration source",
	RunE:  runAuthStatus,
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authStatusCmd)
	authLoginCmd.Flags().StringVar(&authLoginHostFlag, "host", "", "Kibana base URL (e.g. https://kibana.example.com)")
	authLoginCmd.Flags().StringVar(&authLoginUserFlag, "user", "", "Username for HTTP Basic auth")
	authLoginCmd.Flags().StringVar(&authLoginPasswordFlag, "password", "", "Password for HTTP Basic auth (avoid in argv; prefer env)")
	markWrite(authLoginCmd)
	markWrite(authLogoutCmd)
}

func runAuthLogin(_ *cobra.Command, _ []string) error {
	host := strings.TrimSpace(authLoginHostFlag)
	user := strings.TrimSpace(authLoginUserFlag)
	password := authLoginPasswordFlag
	if host == "" {
		host = strings.TrimSpace(os.Getenv("KIBANA_CLI_HOST"))
	}
	if user == "" {
		user = strings.TrimSpace(os.Getenv("KIBANA_CLI_USER"))
	}
	if password == "" {
		password = os.Getenv("KIBANA_CLI_PASSWORD")
	}

	if host == "" || user == "" || password == "" {
		return failValidation("provide --host, --user, and --password (or use KIBANA_CLI_USER/KIBANA_CLI_PASSWORD)")
	}

	return finishLogin(host, user, password)
}

func finishLogin(host, user, password string) error {
	host = strings.TrimRight(host, "/")
	if err := config.ValidateKibanaHost(host); err != nil {
		return failValidation(err.Error())
	}
	if user == "" || password == "" {
		return failValidation("provide --user and --password (or use KIBANA_CLI_USER/KIBANA_CLI_PASSWORD)")
	}
	cfg := &config.Config{Host: host, Username: user, Password: password}
	store := config.CredentialStoreKeyring
	skipped, err := writePlan(
		"save credentials",
		map[string]any{"host": host, "username": user, "authMode": cfg.AuthMode(), "credentialStore": store},
		map[string]any{"host": host, "username": user, "authMode": cfg.AuthMode(), "credentialStore": store, "passwordHash": secretHash(password)},
	)
	if err != nil || skipped {
		return err
	}
	client := kibanaclient.NewClient(cfg)
	vr, err := client.Validate(apiCtx())
	if err != nil || !vr.Valid {
		msg := "could not verify credentials against Kibana"
		if err != nil {
			msg = err.Error()
		}
		return failAuth(msg)
	}
	cfg.KibanaVersion = vr.KibanaVersion
	if err := config.Save(cfg, config.SaveOptions{}); err != nil {
		return failConfig("failed to save config: " + err.Error())
	}
	if jsonMode {
		printJSONSuccess(map[string]any{
			"status":          "ok",
			"host":            host,
			"authMode":        cfg.AuthMode(),
			"username":        vr.Username,
			"kibanaVersion":   vr.KibanaVersion,
			"searchReachable": vr.SearchReachable,
			"credentialStore": store,
		})
		return nil
	}
	output.Success("Logged in as " + vr.Username)
	if vr.KibanaVersion != "" {
		output.AuxInfo("Kibana " + vr.KibanaVersion)
	}
	if !vr.SearchReachable {
		output.Warn("Log search probe failed — check index read privileges")
	}
	output.Success("Password saved in OS credential store (" + config.CredentialStoreKeyring + ")")
	output.AuxInfo("Config saved to " + config.FilePath())
	output.AuxGray("  Try: kibana-cli context")
	return nil
}

// deleteConfigHook is set by tests to simulate config.Delete failures.
var deleteConfigHook func() error

func runAuthLogout(_ *cobra.Command, _ []string) error {
	skipped, err := writePlan("delete credentials", map[string]any{"path": config.FilePath()}, nil)
	if err != nil || skipped {
		return err
	}
	del := config.Delete
	if deleteConfigHook != nil {
		del = deleteConfigHook
	}
	if err := del(); err != nil {
		return failConfig(err.Error())
	}
	if jsonMode {
		printJSONSuccess(map[string]any{"status": "logged_out"})
		return nil
	}
	output.Success("Logged out. Config removed.")
	return nil
}

func runAuthStatus(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return failConfig(err.Error())
	}
	configured := config.IsConfigured()
	ok := configured && cfg.Host != "" && config.ValidateKibanaHost(cfg.Host) == nil
	result := map[string]any{
		"status":          map[bool]string{true: "configured", false: "invalid"}[ok],
		"configured":      configured,
		"host":            cfg.Host,
		"authMode":        cfg.AuthMode(),
		"source":          config.AuthSource(),
		"credentialStore": config.CredentialStoreLabel(cfg),
		"kibanaVersion":   cfg.KibanaVersion,
	}
	if jsonMode {
		if !config.IsConfigured() {
			st := agentNotConfigured()
			emitAgentFailure(st)
			return ErrSilent
		}
		printJSONSuccess(result)
		return nil
	}
	fmt.Println()
	output.AuxBold("  kibana-cli Auth Status")
	output.AuxGray("  ────────────────────────────────────────")
	fmt.Println()
	if result["configured"].(bool) {
		output.Success(fmt.Sprintf("Configured (host=%s, auth=%s, source=%s)", cfg.Host, cfg.AuthMode(), config.AuthSource()))
	} else {
		st := agentNotConfigured()
		emitAgentFailure(st)
		return ErrSilent
	}
	return nil
}
