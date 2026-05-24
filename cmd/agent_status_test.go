package cmd

import (
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/output"
)

func TestAgentReady(t *testing.T) {
	st := agentReady("dev_ro", "7.7.1")
	if !st.OK || st.Status != AgentStatusReady || st.Message == "" || st.ExitCode != ExitOK {
		t.Fatalf("%+v", st)
	}
}

func TestAgentNotConfiguredExitCode(t *testing.T) {
	st := agentNotConfigured()
	if st.ExitCode != ExitBadArgs || st.ErrorCode != output.ErrConfig {
		t.Fatalf("%+v", st)
	}
}

func TestAgentSearchUnavailableForbidden(t *testing.T) {
	st := agentSearchUnavailable("dev_ro", "7.7.1", "forbidden", 403)
	if st.OK || st.Status != AgentStatusSearchUnavailable || st.ExitCode != ExitForbidden || st.ErrorCode != output.ErrForbidden {
		t.Fatalf("%+v", st)
	}
}

func TestAgentSearchUnavailableNetwork(t *testing.T) {
	st := agentSearchUnavailable("dev_ro", "7.7.1", "dial tcp: connection refused", 0)
	if st.ExitCode != ExitNetwork || st.ErrorCode != output.ErrNetwork {
		t.Fatalf("%+v", st)
	}
}

func TestClassifySearchProbeError(t *testing.T) {
	code, exit := classifySearchProbeError("", 429)
	if code != output.ErrRateLimit || exit != ExitRateLimit {
		t.Fatalf("%s %d", code, exit)
	}
}
