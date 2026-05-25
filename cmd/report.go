package cmd

import (
	"errors"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/fatecannotbealtered/kibana-cli/internal/output"
)

// Agent-facing status values for non-bootstrap failures.
const (
	StatusValidationError = "validation_error"
	StatusAPIError        = "api_error"
)

// emitAgentFailure prints a unified AgentStatus envelope and sets the process exit code.
func emitAgentFailure(st AgentStatus) {
	applyAgentExit(st)
	if jsonMode {
		output.PrintJSON(st)
		return
	}
	msg := st.Message
	if msg == "" {
		msg = st.Error
	}
	output.Error(msg)
	if st.Hint != "" {
		output.Gray("  " + st.Hint)
	}
}

func failValidation(msg string) error {
	st := AgentStatus{
		OK:        false,
		Status:    StatusValidationError,
		Message:   msg,
		Error:     msg,
		Hint:      output.HintForErrorCode(output.ErrValidation),
		ErrorCode: output.ErrValidation,
		ExitCode:  ExitBadArgs,
	}
	emitAgentFailure(st)
	return ErrSilent
}

func failConfig(msg string) error {
	emitAgentFailure(agentConfigErrorDetail(msg))
	return ErrSilent
}

func failAuth(msg string) error {
	st := agentAuthFailed(msg)
	emitAgentFailure(st)
	return ErrSilent
}

func failNetwork(msg string) error {
	st := AgentStatus{
		OK:        false,
		Status:    StatusAPIError,
		Message:   msg,
		Error:     msg,
		Hint:      output.HintForErrorCode(output.ErrNetwork),
		ErrorCode: output.ErrNetwork,
		ExitCode:  ExitNetwork,
	}
	emitAgentFailure(st)
	return ErrSilent
}

func handleAPIError(err error, _ bool) error {
	var apiErr *kibanaclient.APIError
	if errors.As(err, &apiErr) {
		code := output.ErrorCodeFromStatus(apiErr.StatusCode)
		st := AgentStatus{
			OK:         false,
			Status:     StatusAPIError,
			Message:    apiErr.Error(),
			Error:      apiErr.Error(),
			Hint:       output.HintForErrorCode(code),
			ErrorCode:  code,
			StatusCode: apiErr.StatusCode,
			ExitCode:   exitCodeForStatus(apiErr.StatusCode),
		}
		emitAgentFailure(st)
		return ErrSilent
	}
	st := AgentStatus{
		OK:        false,
		Status:    StatusAPIError,
		Message:   err.Error(),
		Error:     err.Error(),
		Hint:      output.HintForErrorCode(output.ErrNetwork),
		ErrorCode: output.ErrNetwork,
		ExitCode:  ExitNetwork,
	}
	emitAgentFailure(st)
	return ErrSilent
}

func classifySearchProbeError(detail string, statusCode int) (output.ErrorCode, int) {
	switch statusCode {
	case 401:
		return output.ErrAuth, ExitAuth
	case 403:
		return output.ErrForbidden, ExitForbidden
	case 404:
		return output.ErrNotFound, ExitNotFound
	case 429:
		return output.ErrRateLimit, ExitRateLimit
	}
	if statusCode >= 500 {
		return output.ErrServer, ExitNetwork
	}
	if statusCode >= 400 {
		return output.ErrValidation, ExitBadArgs
	}
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "dial") ||
		strings.Contains(lower, "tls") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "certificate") ||
		strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "unexpected eof") {
		return output.ErrNetwork, ExitNetwork
	}
	return output.ErrUnknown, ExitNetwork
}
