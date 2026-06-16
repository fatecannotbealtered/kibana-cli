package cmd

import (
	"errors"
	"strings"
	"time"

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
		printJSONFailure(st)
		return
	}
	msg := st.Message
	if msg == "" {
		msg = st.Error
	}
	output.Error(msg)
	if st.Hint != "" {
		output.AuxGray("  " + st.Hint)
	}
}

func printJSONSuccess(data any) {
	output.PrintJSON(output.SuccessEnvelope(data, elapsedDurationMs()))
}

func printJSONFailure(st AgentStatus) {
	msg := st.Message
	if msg == "" {
		msg = st.Error
	}
	details := map[string]any{
		"status":    st.Status,
		"exitCode":  st.ExitCode,
		"errorCode": st.ErrorCode,
	}
	if st.Hint != "" {
		details["hint"] = st.Hint
	}
	if st.StatusCode > 0 {
		details["statusCode"] = st.StatusCode
	}
	output.PrintJSON(output.FailureEnvelope(st.ErrorCode, msg, details, elapsedDurationMs()))
}

func elapsedDurationMs() int64 {
	if cmdStartTime.IsZero() {
		return 0
	}
	return time.Since(cmdStartTime).Milliseconds()
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

// failIntegrity reports a non-retryable release-integrity failure (missing or
// invalid signature, or checksum mismatch). It is deliberately not mapped to a
// retryable network code: a forged or corrupt release is not fixed by retrying.
func failIntegrity(msg string) error {
	st := AgentStatus{
		OK:        false,
		Status:    StatusAPIError,
		Message:   msg,
		Error:     msg,
		Hint:      output.HintForErrorCode(output.ErrIntegrity),
		ErrorCode: output.ErrIntegrity,
		ExitCode:  ExitGeneral,
	}
	emitAgentFailure(st)
	return ErrSilent
}

func failConfirmRequired(action string, preview map[string]any, token string) error {
	msg := "write command requires --confirm token from --dry-run"
	st := AgentStatus{
		OK:        false,
		Status:    "confirm_required",
		Message:   msg,
		Error:     msg,
		Hint:      "Run the same command with --dry-run, review the preview, then pass --confirm " + token,
		ErrorCode: output.ErrConfirmRequired,
		ExitCode:  ExitConfirmRequired,
	}
	applyAgentExit(st)
	if jsonMode {
		details := map[string]any{
			"status":        st.Status,
			"exitCode":      st.ExitCode,
			"errorCode":     st.ErrorCode,
			"hint":          st.Hint,
			"action":        action,
			"preview":       preview,
			"confirm_token": token,
		}
		output.PrintJSON(output.FailureEnvelope(st.ErrorCode, msg, details, elapsedDurationMs()))
		return ErrSilent
	}
	output.Error(st.Message)
	output.AuxGray("  " + st.Hint)
	return ErrSilent
}

func failConflict(msg string, details map[string]any) error {
	st := AgentStatus{
		OK:        false,
		Status:    "precondition_conflict",
		Message:   msg,
		Error:     msg,
		Hint:      "Re-run --dry-run and retry with the new confirm token",
		ErrorCode: output.ErrConflict,
		ExitCode:  ExitConflict,
	}
	applyAgentExit(st)
	if jsonMode {
		if details == nil {
			details = map[string]any{}
		}
		details["status"] = st.Status
		details["exitCode"] = st.ExitCode
		details["errorCode"] = st.ErrorCode
		details["hint"] = st.Hint
		output.PrintJSON(output.FailureEnvelope(st.ErrorCode, msg, details, elapsedDurationMs()))
		return ErrSilent
	}
	output.Error(st.Message)
	output.AuxGray("  " + st.Hint)
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
		strings.Contains(lower, "deadline exceeded") {
		return output.ErrTimeout, ExitTimeout
	}
	if strings.Contains(lower, "dial") ||
		strings.Contains(lower, "tls") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "certificate") ||
		strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "unexpected eof") {
		return output.ErrNetwork, ExitNetwork
	}
	return output.ErrUnknown, ExitNetwork
}
