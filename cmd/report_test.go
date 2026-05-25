package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
)

func TestEmitAgentFailure_JSON(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	lastExit = 0
	emitAgentFailure(AgentStatus{
		OK: false, Status: StatusValidationError, Message: "bad", Error: "bad",
		Hint: "fix it", ErrorCode: output.ErrValidation, ExitCode: ExitBadArgs,
	})
	if lastExit != ExitBadArgs {
		t.Fatalf("exit %d", lastExit)
	}
}

func TestEmitAgentFailure_PlainWithHint(t *testing.T) {
	resetCLIState(t)
	jsonMode = false
	out := captureCLIOutput(t, func() {
		emitAgentFailure(AgentStatus{
			OK: false, Status: StatusAPIError, Message: "", Error: "network down",
			Hint: "check VPN", ExitCode: ExitNetwork,
		})
	})
	if !strings.Contains(out, "network down") || !strings.Contains(out, "check VPN") {
		t.Fatalf("output: %q", out)
	}
}

func TestFailConfig(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	err := failConfig("invalid config file")
	if !errors.Is(err, ErrSilent) || lastExit != ExitBadArgs {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestFailAuth(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	err := failAuth("bad credentials")
	if !errors.Is(err, ErrSilent) || lastExit != ExitAuth {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestFailNetwork(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	err := failNetwork("connection reset")
	if !errors.Is(err, ErrSilent) || lastExit != ExitNetwork {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestHandleAPIError_APIError(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	apiErr := &kibanaclient.APIError{StatusCode: http.StatusForbidden, Message: "forbidden"}
	err := handleAPIError(apiErr, true)
	if !errors.Is(err, ErrSilent) || lastExit != ExitForbidden {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestHandleAPIError_Generic(t *testing.T) {
	resetCLIState(t)
	jsonMode = true
	err := handleAPIError(errors.New("dial tcp: refused"), true)
	if !errors.Is(err, ErrSilent) || lastExit != ExitNetwork {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestHandleAPIError_ViaAggProxy(t *testing.T) {
	srv := newMockKibanaServerWith(mockKibanaOptions{ProxyStatus: http.StatusBadGateway})
	defer srv.Close()
	home := setupTestHome(t)
	writeFieldMap(t, home, testFieldMapYAML)
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST":     srv.URL,
		"KIBANA_CLI_USER":     "ops",
		"KIBANA_CLI_PASSWORD": "secret",
	}, []string{"agg", "--index", "logs-*", "--terms", "level", "--json"})
	if code != ExitNetwork {
		t.Fatalf("exit %d want %d", code, ExitNetwork)
	}
}

func TestClassifySearchProbeError_MoreStatuses(t *testing.T) {
	code, exit := classifySearchProbeError("", http.StatusNotFound)
	if code != output.ErrNotFound || exit != ExitNotFound {
		t.Fatalf("404: code=%s exit=%d", code, exit)
	}
	code, exit = classifySearchProbeError("bad request", http.StatusBadRequest)
	if code != output.ErrValidation || exit != ExitBadArgs {
		t.Fatalf("400: code=%s exit=%d", code, exit)
	}
}

func TestNewKibanaClient_PartialEnv(t *testing.T) {
	setupTestHome(t)
	resetCLIState(t)
	jsonMode = true
	lastExit = 0
	_, code := runCLIWithEnv(t, map[string]string{
		"KIBANA_CLI_HOST": "http://example.com",
	}, []string{"patterns", "list", "--json"})
	if code != ExitBadArgs {
		t.Fatalf("exit %d want partial-env config error", code)
	}
}
