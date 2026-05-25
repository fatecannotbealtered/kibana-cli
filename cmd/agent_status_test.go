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
	tests := []struct {
		name       string
		detail     string
		statusCode int
		wantCode   output.ErrorCode
		wantExit   int
	}{
		{name: "rate limit", statusCode: 429, wantCode: output.ErrRateLimit, wantExit: ExitRateLimit},
		{name: "forbidden", statusCode: 403, wantCode: output.ErrForbidden, wantExit: ExitForbidden},
		{name: "auth", statusCode: 401, wantCode: output.ErrAuth, wantExit: ExitAuth},
		{name: "server", statusCode: 502, wantCode: output.ErrServer, wantExit: ExitNetwork},
		{name: "dial", detail: "dial tcp: connection refused", wantCode: output.ErrNetwork, wantExit: ExitNetwork},
		{name: "context canceled", detail: "context canceled", wantCode: output.ErrNetwork, wantExit: ExitNetwork},
		{name: "deadline exceeded", detail: "context deadline exceeded", wantCode: output.ErrNetwork, wantExit: ExitNetwork},
		{name: "unexpected eof", detail: "unexpected EOF", wantCode: output.ErrNetwork, wantExit: ExitNetwork},
		{name: "unknown detail", detail: "index probe returned empty body", wantCode: output.ErrUnknown, wantExit: ExitNetwork},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, exit := classifySearchProbeError(tc.detail, tc.statusCode)
			if code != tc.wantCode || exit != tc.wantExit {
				t.Fatalf("classifySearchProbeError(%q, %d) = (%s, %d), want (%s, %d)",
					tc.detail, tc.statusCode, code, exit, tc.wantCode, tc.wantExit)
			}
		})
	}
}
