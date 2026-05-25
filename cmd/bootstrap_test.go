package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
)

func TestRunBootstrapCheck_PartialEnv(t *testing.T) {
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "http://kibana.example.com")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != AgentStatusConfigError {
		t.Fatalf("status=%s", out.Status)
	}
	if lastExit != ExitBadArgs || out.ConfigError == "" {
		t.Fatalf("exit=%d configError=%q", lastExit, out.ConfigError)
	}
}

func TestRunBootstrapCheck_NotConfigured(t *testing.T) {
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	t.Setenv("KIBANA_CLI_PASSWORD", "")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != AgentStatusNotConfigured {
		t.Fatalf("status=%s", out.Status)
	}
	if lastExit != ExitBadArgs {
		t.Fatalf("exit=%d", lastExit)
	}
}

func TestRunBootstrapCheck_InvalidConfigFile(t *testing.T) {
	home := setupTestHome(t)
	cfgDir := filepath.Join(home, ".kibana-cli")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIBANA_CLI_HOST", "")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != AgentStatusConfigError {
		t.Fatalf("status=%s msg=%s", out.Status, out.Message)
	}
	if lastExit != ExitBadArgs {
		t.Fatalf("exit=%d", lastExit)
	}
}

func TestRunBootstrapCheck_InvalidHost(t *testing.T) {
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "not-a-valid-host")
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != AgentStatusConfigError {
		t.Fatalf("status=%s", out.Status)
	}
	if out.ConfigError == "" {
		t.Fatal("expected config error detail")
	}
}

func TestRunBootstrapCheck_AuthFailed(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{AuthFail: true, AuthStatus: 401})
	defer srv.Close()
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != AgentStatusAuthFailed {
		t.Fatalf("status=%s", out.Status)
	}
	if out.AuthValid || out.AuthError == "" {
		t.Fatalf("authValid=%v authError=%q", out.AuthValid, out.AuthError)
	}
	if lastExit != ExitAuth {
		t.Fatalf("exit=%d", lastExit)
	}
}

func TestRunBootstrapCheck_SearchUnavailable(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchProbeFail: true, SearchProbeStatus: 403})
	defer srv.Close()
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != AgentStatusSearchUnavailable {
		t.Fatalf("status=%s", out.Status)
	}
	if out.AuthValid != true || out.SearchReachable {
		t.Fatalf("auth=%v search=%v", out.AuthValid, out.SearchReachable)
	}
	if lastExit != ExitForbidden {
		t.Fatalf("exit=%d", lastExit)
	}
}

func TestRunBootstrapCheck_Ready(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0

	out, err := runBootstrapCheck()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != AgentStatusReady || !out.OK {
		t.Fatalf("status=%s ok=%v", out.Status, out.OK)
	}
	if out.Username != "agent" || out.KibanaVersion == "" || !out.SearchReachable {
		t.Fatalf("username=%q version=%q search=%v", out.Username, out.KibanaVersion, out.SearchReachable)
	}
	if lastExit != ExitOK {
		t.Fatalf("exit=%d", lastExit)
	}
}

func TestApplyBootstrapToContext(t *testing.T) {
	out := &bootstrapOutcome{
		ConfigExists:    true,
		AuthValid:       true,
		Host:            "http://kibana",
		AuthMode:        "basic",
		Username:        "ops",
		KibanaVersion:   "7.10.0",
		SearchReachable: false,
		SearchError:     "forbidden",
		AuthError:       "stale auth detail",
	}
	k := &contextKibana{}
	applyBootstrapToContext(out, k)
	if !k.Configured || !k.Authenticated || k.Host != out.Host || k.SearchError != out.SearchError {
		t.Fatalf("%+v", k)
	}
	if k.AuthError != "stale auth detail" {
		t.Fatalf("authError=%q", k.AuthError)
	}
	out.AuthValid = false
	out.AuthError = "auth broke"
	applyBootstrapToContext(out, k)
	if k.AuthError != "auth broke" {
		t.Fatal(k.AuthError)
	}
}

func TestDoctor_TextMode_Success(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{"doctor"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Doctor") || !strings.Contains(out, "Latency:") {
		t.Fatalf("unexpected doctor text: %s", out)
	}
}

func TestDoctor_TextMode_NotConfigured(t *testing.T) {
	setupTestHome(t)
	t.Setenv("KIBANA_CLI_HOST", "")
	out, code := runCLI(t, []string{"doctor"})
	if code != ExitBadArgs {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "Doctor") {
		t.Fatalf("expected doctor banner: %s", out)
	}
}

func TestDoctor_TextMode_SearchUnavailable(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchProbeFail: true})
	defer srv.Close()
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{"doctor"})
	if code != ExitForbidden {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Console Proxy") && !strings.Contains(out, "roles") {
		t.Fatalf("expected search warning: %s", out)
	}
}

func TestDoctor_JSON_AuthFailed(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{AuthFail: true})
	defer srv.Close()
	out, code := runCLIWithEnv(t, searchMockEnv(srv.URL), []string{"doctor", "--json"})
	if code != ExitAuth {
		t.Fatalf("exit %d: %s", code, out)
	}
	j := lastJSONLine(out)
	if !strings.Contains(j, `"authValid":false`) && !strings.Contains(j, `"authValid": false`) {
		t.Fatalf("unexpected: %s", j)
	}
	if !strings.Contains(j, `"error"`) {
		t.Fatalf("missing error field: %s", j)
	}
}

func TestAgentConfigError_AndDetail(t *testing.T) {
	st := agentConfigError("disk full")
	if st.Status != AgentStatusConfigError || st.ErrorCode != output.ErrConfig {
		t.Fatalf("%+v", st)
	}
	st2 := agentConfigErrorDetail("invalid host")
	if st2.Message != "invalid host" {
		t.Fatalf("%+v", st2)
	}
}
