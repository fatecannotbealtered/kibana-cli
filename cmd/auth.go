package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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
	authLoginHostFlag      string
	authLoginUserFlag      string
	authLoginPasswordFlag  string
	authLoginPlaintextFlag bool
)

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Save Kibana credentials",
	Long: `Save credentials interactively or pass flags for non-interactive use.

Prefer environment variables in CI/agents:
  export KIBANA_CLI_HOST=... KIBANA_CLI_USER=... KIBANA_CLI_PASSWORD=...

Examples:
  kibana-cli auth login
  kibana-cli auth login --host https://kibana.example.com --user dev_ro`,
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
	authLoginCmd.Flags().BoolVar(&authLoginPlaintextFlag, "plaintext", false, "Store password in config.json (not recommended; default uses OS credential store)")
	markWrite(authLoginCmd)
	markWrite(authLogoutCmd)
}

func runAuthLogin(_ *cobra.Command, _ []string) error {
	host := strings.TrimSpace(authLoginHostFlag)
	user := strings.TrimSpace(authLoginUserFlag)
	password := authLoginPasswordFlag

	if host != "" && user != "" && password != "" {
		return finishLogin(host, user, password)
	}

	reader := bufio.NewReader(os.Stdin)
	if host == "" {
		fmt.Println()
		output.Bold("  kibana-cli Login")
		output.Gray("  ────────────────────────────────────────")
		fmt.Println()
		fmt.Print("  Kibana URL (e.g. https://kibana.example.com): ")
		line, _ := reader.ReadString('\n')
		host = strings.TrimSpace(line)
	}
	if err := config.ValidateKibanaHost(host); err != nil {
		return failValidation(err.Error())
	}
	host = strings.TrimRight(host, "/")

	if user == "" {
		fmt.Print("  Username: ")
		line, _ := reader.ReadString('\n')
		user = strings.TrimSpace(line)
	}
	if password == "" {
		fmt.Print("  Password: ")
		_, password = readPasswordPair(reader, user)
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
	if dryRunOutput("save credentials", map[string]any{"host": host, "authMode": cfg.AuthMode()}) {
		return nil
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
	store := config.CredentialStoreKeyring
	if authLoginPlaintextFlag {
		store = config.CredentialStoreFile
	}
	if err := config.Save(cfg, config.SaveOptions{Plaintext: authLoginPlaintextFlag}); err != nil {
		return failNetwork("failed to save config: " + err.Error())
	}
	if jsonMode {
		output.PrintJSON(map[string]any{
			"ok":              true,
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
		output.Info("Kibana " + vr.KibanaVersion)
	}
	if !vr.SearchReachable {
		output.Warn("Log search probe failed — check index read privileges")
	}
	if authLoginPlaintextFlag {
		output.Warn("Password saved in plaintext config.json — prefer OS credential store (default)")
	} else {
		output.Success("Password saved in OS credential store (" + config.CredentialStoreKeyring + ")")
	}
	output.Info("Config saved to " + config.FilePath())
	output.Gray("  Try: kibana-cli context --json")
	return nil
}

func readPasswordPair(reader *bufio.Reader, user string) (string, string) {
	if term.IsTerminal(int(syscall.Stdin)) {
		b, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err == nil {
			return user, strings.TrimSpace(string(b))
		}
	}
	line, _ := reader.ReadString('\n')
	return user, strings.TrimSpace(line)
}

func runAuthLogout(_ *cobra.Command, _ []string) error {
	if dryRunOutput("delete credentials", map[string]any{"path": config.FilePath()}) {
		return nil
	}
	if err := config.Delete(); err != nil {
		return failNetwork(err.Error())
	}
	if jsonMode {
		output.PrintJSON(map[string]any{"ok": true, "status": "logged_out"})
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
	result := map[string]any{
		"ok":              config.IsConfigured(),
		"configured":      config.IsConfigured(),
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
		output.PrintJSON(result)
		return nil
	}
	fmt.Println()
	output.Bold("  kibana-cli Auth Status")
	output.Gray("  ────────────────────────────────────────")
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
