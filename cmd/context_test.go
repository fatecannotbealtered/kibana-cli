package cmd

import (
	"strings"
	"testing"
)

func TestContext_TableSuccess(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"context", "--format", "text"})
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Context") || !strings.Contains(out, "Host:") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestContext_TableNotConfigured(t *testing.T) {
	setupTestHome(t)
	out, code := runCLI(t, []string{"context", "--format", "text"})
	if code != ExitBadArgs {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "not configured") && !strings.Contains(out, "Context") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestContext_TableSearchUnavailable(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{SearchProbeFail: true, SearchProbeStatus: 403})
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"context", "--format", "text"})
	if code != ExitForbidden {
		t.Fatalf("exit %d: %s", code, out)
	}
	combined := captureCLIOutput(t, func() {
		jsonMode = false
		printContext(&contextResult{
			AgentStatus: agentSearchUnavailable("agent", "7.10.0", "forbidden", 403),
			Kibana: &contextKibana{
				Configured: true, Host: "http://k", AuthMode: "basic", Source: "env",
				Authenticated: true, SearchReachable: false, SearchError: "forbidden",
			},
		})
	})
	if !strings.Contains(combined, "forbidden") || !strings.Contains(combined, "Host:") {
		t.Fatalf("output: %q", combined)
	}
}

func TestContext_TableAuthFailed(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{AuthFail: true})
	defer srv.Close()
	setupTestHome(t)
	out, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"context", "--format", "text"})
	if code != ExitAuth {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Context") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestPrintContext_JSON(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	out := captureStdout(t, func() {
		printContext(&contextResult{
			AgentStatus: agentReady("agent", "7.10.0"),
			Kibana: &contextKibana{
				Configured: true, Host: "http://k", AuthMode: "basic",
				Authenticated: true, SearchReachable: true,
			},
		})
	})
	if !strings.Contains(out, `"ok"`) {
		t.Fatalf("json output: %q", out)
	}
}

func TestPrintContext_SuccessPlain(t *testing.T) {
	resetCLIState(t)
	jsonMode = false
	out := captureCLIOutput(t, func() {
		printContext(&contextResult{
			AgentStatus: agentReady("agent", "7.10.0"),
			Kibana: &contextKibana{
				Configured: true, Host: "http://k", AuthMode: "basic", Source: "env",
				Authenticated: true, SearchReachable: true,
			},
		})
	})
	if !strings.Contains(out, "Ready") || !strings.Contains(out, "Host:") {
		t.Fatalf("output: %q", out)
	}
}

func TestPrintContext_FailureWithHint(t *testing.T) {
	resetCLIState(t)
	jsonMode = false
	st := agentNotConfigured()
	out := captureCLIOutput(t, func() {
		printContext(&contextResult{
			AgentStatus: st,
			Kibana:      &contextKibana{Configured: false},
		})
	})
	if !strings.Contains(out, "not configured") || !strings.Contains(out, st.Hint) {
		t.Fatalf("output: %q", out)
	}
}
