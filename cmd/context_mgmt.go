package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
	"github.com/spf13/cobra"
)

// context subcommands manage named connection targets (systems). The bare
// `context` command stays the bootstrap self-description (context.go); these add
// kubectl-style switching on top.

var (
	ctxAddHostFlag       string
	ctxAddUserFlag       string
	ctxAddPasswordFlag   string
	ctxAddDefaultIndex   string
	ctxAddFieldMapFile   string
	ctxAddUseFlag        bool
	ctxAddNoValidateFlag bool
)

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored contexts (systems)",
	RunE:  runContextList,
}

var contextCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print the active context name and host",
	RunE:  runContextCurrent,
}

var contextUseCmd = &cobra.Command{
	Use:   "use",
	Short: "Switch the active context: context use <name>",
	Args:  cobra.ExactArgs(1),
	RunE:  runContextUse,
}

var contextAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add or update a context (system): context add <name> --host ... --user ...",
	Long: `Add or update a named context. Credentials go to the OS keyring; the
password is read from --password or KIBANA_CLI_PASSWORD (prefer the env var to
keep secrets out of argv).`,
	Args: cobra.ExactArgs(1),
	RunE: runContextAdd,
}

var contextRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a context and its stored credentials: context remove <name>",
	Args:  cobra.ExactArgs(1),
	RunE:  runContextRemove,
}

func init() {
	contextCmd.AddCommand(contextListCmd, contextCurrentCmd, contextUseCmd, contextAddCmd, contextRemoveCmd)
	contextAddCmd.Flags().StringVar(&ctxAddHostFlag, "host", "", "Kibana base URL (e.g. https://kibana.example.com)")
	contextAddCmd.Flags().StringVar(&ctxAddUserFlag, "user", "", "Username for HTTP Basic auth")
	contextAddCmd.Flags().StringVar(&ctxAddPasswordFlag, "password", "", "Password (avoid in argv; prefer KIBANA_CLI_PASSWORD)")
	contextAddCmd.Flags().StringVar(&ctxAddDefaultIndex, "default-index", "", "Default index/pattern for this system when --index is omitted")
	contextAddCmd.Flags().StringVar(&ctxAddFieldMapFile, "field-map-file", "", "Per-context field-map file under ~/.kibana-cli/ (overrides the global field-map.yaml)")
	contextAddCmd.Flags().BoolVar(&ctxAddUseFlag, "use", false, "Make this context current after adding")
	contextAddCmd.Flags().BoolVar(&ctxAddNoValidateFlag, "no-validate", false, "Skip the live credential probe (store without contacting Kibana)")
	markWrite(contextUseCmd)
	markWrite(contextAddCmd)
	markWrite(contextRemoveCmd)
}

func runContextList(_ *cobra.Command, _ []string) error {
	store, err := config.LoadStore()
	if err != nil {
		return failConfig(err.Error())
	}
	names := make([]string, 0, len(store.Contexts))
	for n := range store.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		e := store.Contexts[n]
		items = append(items, map[string]any{
			"name":         n,
			"host":         e.Host,
			"username":     e.Username,
			"defaultIndex": e.DefaultIndex,
			"fieldMapFile": e.FieldMapFile,
			"current":      n == store.CurrentContext,
		})
	}
	if jsonMode {
		printJSONSuccess(map[string]any{
			"currentContext": store.CurrentContext,
			"contexts":       items,
			"count":          len(items),
		})
		return nil
	}
	fmt.Println()
	output.AuxBold("  kibana-cli Contexts")
	output.AuxGray("  ────────────────────────────────────────")
	if len(items) == 0 {
		output.Info("No contexts. Add one: kibana-cli context add <name> --host ... --user ...")
		return nil
	}
	for _, it := range items {
		marker := "  "
		if it["current"].(bool) {
			marker = "* "
		}
		fmt.Printf("%s%-16s %s\n", marker, it["name"], it["host"])
	}
	fmt.Println()
	return nil
}

func runContextCurrent(_ *cobra.Command, _ []string) error {
	store, err := config.LoadStore()
	if err != nil {
		return failConfig(err.Error())
	}
	name := selectActiveContextName(store)
	if name == "" {
		msg := "no active context; add one with 'kibana-cli context add'"
		if jsonMode {
			output.PrintJSON(output.FailureEnvelope(output.ErrNotFound, msg, map[string]any{
				"status":   "not_found",
				"exitCode": ExitNotFound,
			}, elapsedDurationMs()))
			setExitCode(ExitNotFound)
			return ErrSilent
		}
		output.Warn(msg)
		setExitCode(ExitNotFound)
		return ErrSilent
	}
	host := ""
	if e, ok := store.Contexts[name]; ok {
		host = e.Host
	}
	if jsonMode {
		printJSONSuccess(map[string]any{"currentContext": name, "host": host})
		return nil
	}
	output.Info(fmt.Sprintf("%s (%s)", name, host))
	return nil
}

// selectActiveContextName mirrors config's selection without exposing internals:
// --context flag → KIBANA_CLI_CONTEXT → store.CurrentContext.
func selectActiveContextName(store *config.Store) string {
	if n := strings.TrimSpace(contextName); n != "" {
		return n
	}
	if n := strings.TrimSpace(os.Getenv("KIBANA_CLI_CONTEXT")); n != "" {
		return n
	}
	return strings.TrimSpace(store.CurrentContext)
}

func runContextUse(_ *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	store, err := config.LoadStore()
	if err != nil {
		return failConfig(err.Error())
	}
	if _, ok := store.Contexts[name]; !ok {
		return failValidation(fmt.Sprintf("unknown context %q: run 'kibana-cli context list'", name))
	}
	skipped, err := writePlan("switch context", map[string]any{"context": name}, nil)
	if err != nil || skipped {
		return err
	}
	if err := config.SetCurrentContext(name); err != nil {
		return failConfig(err.Error())
	}
	if jsonMode {
		printJSONSuccess(map[string]any{"status": "ok", "currentContext": name})
		return nil
	}
	output.Success("Switched to context " + name)
	return nil
}

func runContextAdd(_ *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	if name == "" || name == "env" {
		return failValidation("context name must be non-empty and not 'env'")
	}
	host := strings.TrimSpace(ctxAddHostFlag)
	user := strings.TrimSpace(ctxAddUserFlag)
	password := ctxAddPasswordFlag
	if password == "" {
		password = os.Getenv("KIBANA_CLI_PASSWORD")
	}
	if host == "" || user == "" || password == "" {
		return failValidation("provide --host, --user, and --password (or KIBANA_CLI_PASSWORD)")
	}
	host = strings.TrimRight(host, "/")
	if err := config.ValidateKibanaHost(host); err != nil {
		return failValidation(err.Error())
	}
	cfg := &config.Config{
		ContextName:  name,
		Host:         host,
		Username:     user,
		Password:     password,
		DefaultIndex: strings.TrimSpace(ctxAddDefaultIndex),
		FieldMapFile: strings.TrimSpace(ctxAddFieldMapFile),
	}
	skipped, err := writePlan(
		"add context",
		map[string]any{"context": name, "host": host, "username": user, "defaultIndex": cfg.DefaultIndex, "use": ctxAddUseFlag},
		map[string]any{"context": name, "host": host, "username": user, "passwordHash": secretHash(password)},
	)
	if err != nil || skipped {
		return err
	}
	if !ctxAddNoValidateFlag {
		client := kibanaclient.NewClient(cfg)
		vr, vErr := client.Validate(apiCtx())
		if vErr != nil || !vr.Valid {
			msg := "could not verify credentials against Kibana (use --no-validate to store anyway)"
			if vErr != nil {
				msg = vErr.Error()
			}
			return failAuth(msg)
		}
		cfg.KibanaVersion = vr.KibanaVersion
	}
	if err := config.Save(cfg, config.SaveOptions{}); err != nil {
		return failConfig("failed to save context: " + err.Error())
	}
	if ctxAddUseFlag {
		if err := config.SetCurrentContext(name); err != nil {
			return failConfig(err.Error())
		}
	}
	if jsonMode {
		printJSONSuccess(map[string]any{
			"status":        "ok",
			"context":       name,
			"host":          host,
			"kibanaVersion": cfg.KibanaVersion,
			"current":       ctxAddUseFlag,
		})
		return nil
	}
	output.Success("Stored context " + name)
	output.AuxGray("  Use it: kibana-cli context use " + name)
	return nil
}

func runContextRemove(_ *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	store, err := config.LoadStore()
	if err != nil {
		return failConfig(err.Error())
	}
	if _, ok := store.Contexts[name]; !ok {
		return failValidation(fmt.Sprintf("unknown context %q: run 'kibana-cli context list'", name))
	}
	skipped, err := writePlan("remove context", map[string]any{"context": name}, nil)
	if err != nil || skipped {
		return err
	}
	if err := config.RemoveContext(name); err != nil {
		return failConfig(err.Error())
	}
	if jsonMode {
		printJSONSuccess(map[string]any{"status": "removed", "context": name})
		return nil
	}
	output.Success("Removed context " + name)
	return nil
}
