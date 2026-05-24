package cmd

import "github.com/fatecannotbealtered/kibana-cli/internal/output"

// Agent status values for doctor/context JSON (--json).
const (
	AgentStatusReady             = "ready"
	AgentStatusNotConfigured     = "not_configured"
	AgentStatusConfigError       = "config_error"
	AgentStatusAuthFailed        = "auth_failed"
	AgentStatusSearchUnavailable = "search_unavailable"
)

// AgentStatus is the machine-readable summary every Agent should read first.
type AgentStatus struct {
	OK         bool              `json:"ok"`
	Status     string            `json:"status"`
	Message    string            `json:"message"`
	Error      string            `json:"error,omitempty"`
	Hint       string            `json:"hint,omitempty"`
	ErrorCode  output.ErrorCode  `json:"errorCode,omitempty"`
	StatusCode int               `json:"statusCode,omitempty"`
	ExitCode   int               `json:"exitCode"`
}

func agentNotConfigured() AgentStatus {
	code := output.ErrConfig
	msg := "Kibana is not configured; run kibana-cli auth login or set KIBANA_CLI_HOST, KIBANA_CLI_USER, and KIBANA_CLI_PASSWORD"
	return AgentStatus{
		OK:        false,
		Status:    AgentStatusNotConfigured,
		Message:   msg,
		Error:     msg,
		Hint:      output.HintForErrorCode(code),
		ErrorCode: code,
		ExitCode:  ExitBadArgs,
	}
}

func agentConfigError(detail string) AgentStatus {
	code := output.ErrConfig
	msg := "Failed to read configuration: " + detail
	return AgentStatus{
		OK:        false,
		Status:    AgentStatusConfigError,
		Message:   msg,
		Error:     msg,
		Hint:      output.HintForErrorCode(code),
		ErrorCode: code,
		ExitCode:  ExitBadArgs,
	}
}

func agentAuthFailed(detail string) AgentStatus {
	code := output.ErrAuth
	msg := "Kibana authentication failed"
	if detail != "" {
		msg = msg + ": " + detail
	}
	return AgentStatus{
		OK:        false,
		Status:    AgentStatusAuthFailed,
		Message:   msg,
		Error:     msg,
		Hint:      output.HintForErrorCode(code),
		ErrorCode: code,
		ExitCode:  ExitAuth,
	}
}

func agentSearchUnavailable(username, kibanaVersion, searchDetail string, searchStatusCode int) AgentStatus {
	code, exit := classifySearchProbeError(searchDetail, searchStatusCode)
	msg := "Authenticated to Kibana"
	if username != "" {
		msg += " as " + username
	}
	if kibanaVersion != "" {
		msg += " (" + kibanaVersion + ")"
	}
	msg += ", but log search via Console Proxy is not available"
	if searchDetail != "" {
		msg += ": " + searchDetail
	}
	hint := output.HintForErrorCode(code)
	if code == output.ErrForbidden {
		hint += "; confirm index read privileges and that Console Proxy is enabled"
	}
	return AgentStatus{
		OK:         false,
		Status:     AgentStatusSearchUnavailable,
		Message:    msg,
		Error:      msg,
		Hint:       hint,
		ErrorCode:  code,
		StatusCode: searchStatusCode,
		ExitCode:   exit,
	}
}

func agentReady(username, kibanaVersion string) AgentStatus {
	msg := "Ready for kibana-cli search and agg"
	if username != "" {
		msg = "Ready for log search as " + username
		if kibanaVersion != "" {
			msg += " on Kibana " + kibanaVersion
		}
	}
	return AgentStatus{
		OK:       true,
		Status:   AgentStatusReady,
		Message:  msg,
		ExitCode: ExitOK,
	}
}

func applyAgentExit(st AgentStatus) {
	setExitCode(st.ExitCode)
}
