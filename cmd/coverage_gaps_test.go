package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/zalando/go-keyring"
)

func TestExecuteAndExecuteContext(t *testing.T) {
	rootCmd.SetArgs([]string{"--version"})
	if err := Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	lastExit = ExitAuth
	if LastExitCode() != ExitAuth {
		t.Fatalf("LastExitCode=%d", LastExitCode())
	}
	lastExit = 0
	rootCmd.SetArgs([]string{"--version"})
	if err := ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
}

func TestResolveBuildVersion(t *testing.T) {
	if got := resolveBuildVersion("1.2.3"); got != "1.2.3" {
		t.Fatalf("got %q", got)
	}
	t.Run("packageJSONBeatsBuildInfo", func(t *testing.T) {
		orig := readBuildInfo
		readBuildInfo = func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, true
		}
		defer func() { readBuildInfo = orig }()
		if got := resolveBuildVersion("dev"); got != "1.1.6" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("fallback", func(t *testing.T) {
		orig := readBuildInfo
		readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
		defer func() { readBuildInfo = orig }()
		if got := resolveBuildVersion("dev"); got != "1.1.6" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("develUsesPackageJSON", func(t *testing.T) {
		orig := readBuildInfo
		readBuildInfo = func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
		}
		defer func() { readBuildInfo = orig }()
		if got := resolveBuildVersion("dev"); got != "1.1.6" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestApplyTimeoutFromEnv_ExplicitZeroUsesDefault(t *testing.T) {
	resetCLIState(t)
	_ = rootCmd.PersistentFlags().Set("timeout", "0")
	activeCmd = rootCmd
	if got := applyTimeoutFromEnv(rootCmd); got != defaultTimeoutSeconds {
		t.Fatalf("got %d want %d", got, defaultTimeoutSeconds)
	}
}

func TestApplyInsecureFromEnv_Variants(t *testing.T) {
	resetCLIState(t)
	insecureTLS = true
	applyInsecureFromEnv()
	if !insecureTLS {
		t.Fatal("should stay true")
	}
	insecureTLS = false
	t.Setenv("KIBANA_CLI_INSECURE", "1")
	applyInsecureFromEnv()
	if !insecureTLS {
		t.Fatal("expected insecure from env=1")
	}
}

func TestApplyTimeoutFromEnv_InvalidAndExplicitZero(t *testing.T) {
	resetCLIState(t)
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Int("timeout", defaultTimeoutSeconds, "")
	_ = cmd.PersistentFlags().Set("timeout", "0")
	if got := applyTimeoutFromEnv(cmd); got != defaultTimeoutSeconds {
		t.Fatalf("explicit zero timeout got %d", got)
	}
	resetCLIState(t)
	t.Setenv("KIBANA_CLI_TIMEOUT", "not-a-number")
	if got := applyTimeoutFromEnv(nil); got != defaultTimeoutSeconds {
		t.Fatalf("invalid env timeout got %d", got)
	}
}

func TestGetFieldsFlag_NilAndEmptyParts(t *testing.T) {
	if got := getFieldsFlag(nil); got != nil {
		t.Fatalf("nil cmd: %v", got)
	}
	rootCmd.SetArgs([]string{"search", "--fields", "a,,b,"})
	_ = searchCmd.Flags().Set("fields", "a,,b,")
	got := getFieldsFlag(searchCmd)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestNewKibanaClient_Success(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	client, cfg, err := newKibanaClient()
	if err != nil || client == nil || cfg == nil {
		t.Fatalf("client=%v cfg=%v err=%v", client, cfg, err)
	}
}

func TestDryRunOutput_TextNilDetail(t *testing.T) {
	resetCLIState(t)
	dryRun = true
	jsonMode = false
	defer func() { dryRun = false; jsonMode = false }()
	out := captureStdout(t, func() {
		if !dryRunOutput("act", nil) {
			t.Fatal("expected true")
		}
	})
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("out=%s", out)
	}
}

func TestDryRunOutput_JSONNilDetail(t *testing.T) {
	resetCLIState(t)
	dryRun = true
	jsonMode = true
	defer func() { dryRun = false; jsonMode = false }()
	out := captureStdout(t, func() {
		if !dryRunOutput("act", nil) {
			t.Fatal("expected true")
		}
	})
	if !strings.Contains(out, `"dry_run":true`) && !strings.Contains(out, `"dry_run": true`) {
		t.Fatalf("out=%s", out)
	}
}

func TestFirstFieldValue_NonStringAndMiss(t *testing.T) {
	if got := firstFieldValue(map[string]any{"level": 1}, []string{"level"}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := firstFieldValue(map[string]any{"other": "x"}, []string{"level"}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := firstFieldValue(map[string]any{"level": ""}, []string{"level"}); got != "" {
		t.Fatalf("empty string field got %q", got)
	}
}

func TestRunConfigInit_WriteBlocked(t *testing.T) {
	home := setupTestHome(t)
	path := filepath.Join(home, ".kibana-cli", "field-map.yaml")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	_, code := runConfirmedCLI(t, []string{"config", "init", "--force", "--json"})
	if code != ExitAuth {
		t.Fatalf("expected write failure exit %d", code)
	}
}

func TestRunContext_SuccessReturnNil(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	out, code := runCLI(t, []string{"context"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestRunContext_ConfigErrorOnKibana(t *testing.T) {
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	out, code := runCLI(t, []string{"context", "--json"})
	if code != ExitAuth {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"kibana"`) {
		t.Fatalf("expected kibana context: %s", j)
	}
}

func TestRunDoctor_NotConfiguredSetsErrorJSON(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"doctor", "--json"})
	if code != ExitAuth {
		t.Fatalf("exit %d", code)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"error"`) {
		t.Fatalf("doctor json missing error: %s", j)
	}
}

func TestRunBootstrapCheck_InvalidConfigFileOutcome(t *testing.T) {
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.OK || out.ConfigError == "" {
		t.Fatalf("expected config error outcome: %+v", out)
	}
	_ = out
}

func TestRunDoctor_SuccessReturnNil(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	out, code := runCLI(t, []string{"doctor"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestMarkWrite_NilAnnotations(t *testing.T) {
	c := &cobra.Command{}
	markWrite(c)
	if c.Annotations["write"] != "true" {
		t.Fatal("expected write annotation")
	}
}

func TestMarkWrite_ExistingAnnotations(t *testing.T) {
	c := &cobra.Command{Annotations: map[string]string{"note": "x"}}
	markWrite(c)
	if c.Annotations["write"] != "true" || c.Annotations["note"] != "x" {
		t.Fatalf("annotations=%v", c.Annotations)
	}
}

func TestLastExitCode_Direct(t *testing.T) {
	lastExit = ExitNetwork
	if LastExitCode() != ExitNetwork {
		t.Fatalf("got %d", LastExitCode())
	}
	lastExit = ExitOK
}

func TestRunSearch_SkipsEmptyFieldPair(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	resetCLIState(t)
	jsonMode = true
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "u")
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	if f := searchCmd.Flags().Lookup("field"); f != nil {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace([]string{"", "msg=timeout"})
		}
	}
	_ = searchCmd.Flags().Set("index", "logs-*")
	err := runSearch(searchCmd, nil)
	if err != nil {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestRunAgg_NotConfigured(t *testing.T) {
	setupTestHome(t)
	resetCLIState(t)
	jsonMode = true
	_ = aggCmd.Flags().Set("index", "logs-*")
	_ = aggCmd.Flags().Set("terms", "level")
	lastExit = 0
	err := runAgg(aggCmd, nil)
	if err == nil || lastExit != ExitAuth {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestRunBootstrapCheck_MustLoadHostValidation(t *testing.T) {
	keyring.MockInit()
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	_, code := runConfirmedCLI(t, []string{
		"auth", "login",
		"--host", srv.URL,
		"--user", "ops",
		"--password", "secret",
		"--json",
	})
	if code != ExitOK {
		t.Fatal(code)
	}
	t.Setenv("KIBANA_CLI_HOST", "://bad")
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.OK || out.ConfigError == "" {
		t.Fatalf("expected MustLoad validation failure: %+v", out)
	}
	_ = home
}

func TestRunSearch_ValidateIndexRejected(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	resetCLIState(t)
	jsonMode = true
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "u")
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	_ = searchCmd.Flags().Set("index", "-metrics")
	lastExit = 0
	err := runSearch(searchCmd, nil)
	if err == nil || lastExit != ExitBadArgs {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestRunSearch_InvalidSizeReturnsErr(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	resetCLIState(t)
	jsonMode = true
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "u")
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	_ = searchCmd.Flags().Set("index", "logs-*")
	_ = searchCmd.Flags().Set("size", "0")
	lastExit = 0
	err := runSearch(searchCmd, nil)
	if err == nil || lastExit != ExitBadArgs {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestSearch_InvalidIndexAndFieldAndAPICases(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchFail: true})
	defer srv.Close()
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST": srv.URL, "KIBANA_CLI_USER": "u", "KIBANA_CLI_PASSWORD": "p",
	}, []string{"search", "--index", "logs-*", "--json"})
	if code != ExitNetwork {
		t.Fatalf("search API error exit %d", code)
	}

	srv2 := newMockKibanaServerWith(mockKibanaOptions{SearchTotalGte: true})
	defer srv2.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST": srv2.URL, "KIBANA_CLI_USER": "u", "KIBANA_CLI_PASSWORD": "p",
	}, []string{"search", "--index", "logs-*", "--size", "5000", "--json"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(lastJSONLine(out), `"totalRelation"`) {
		t.Fatalf("expected totalRelation: %s", out)
	}
	if !strings.Contains(lastJSONLine(out), `"limit_capped"`) {
		t.Fatalf("expected limit_capped: %s", out)
	}

	srv3 := newMockKibanaServerWith(mockKibanaOptions{
		SearchSource: map[string]any{"level": "ERROR", "msg": "plain"},
	})
	defer srv3.Close()
	out, code = runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST": srv3.URL, "KIBANA_CLI_USER": "u", "KIBANA_CLI_PASSWORD": "p",
	}, []string{"search", "--index", "logs-*"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "-  ") && !strings.Contains(out, "plain") {
		t.Fatalf("expected text hit with missing timestamp: %s", out)
	}
}

func TestRunAgg_IndexResolveAPIError(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{IndexPatternFail: true, IndexPatternStatus: 404})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	resetCLIState(t)
	jsonMode = true
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "u")
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	_ = aggCmd.Flags().Set("data-view", "missing")
	_ = aggCmd.Flags().Set("terms", "level")
	lastExit = 0
	err := runAgg(aggCmd, nil)
	if err == nil || lastExit != ExitNotFound {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestRunAgg_Precise(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	resetCLIState(t)
	jsonMode = true
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "u")
	t.Setenv("KIBANA_CLI_PASSWORD", "p")
	_ = aggCmd.Flags().Set("index", "logs-*")
	_ = aggCmd.Flags().Set("terms", "level")
	_ = aggCmd.Flags().Set("precise", "true")
	if err := runAgg(aggCmd, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAgg_BroadDefault(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST": srv.URL, "KIBANA_CLI_USER": "u", "KIBANA_CLI_PASSWORD": "p",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--json"})
	if code != ExitOK {
		t.Fatal(code)
	}
}

func TestDoctor_SearchErrorPlain(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchProbeFail: true, SearchProbeStatus: 403})
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST": srv.URL, "KIBANA_CLI_USER": "u", "KIBANA_CLI_PASSWORD": "p",
	}, []string{"doctor"})
	if code != ExitForbidden {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "403") && !strings.Contains(out, "forbidden") {
		t.Fatalf("expected search error in doctor output: %s", out)
	}
}

func TestContext_ConfigLoadError(t *testing.T) {
	home := setupTestHome(t)
	cfgPath := filepath.Join(home, ".kibana-cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	out, code := runCLI(t, []string{"context"})
	if code != ExitAuth {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Context") {
		t.Fatalf("expected context output: %s", out)
	}
}
