package output

import (
	"encoding/json"
	"fmt"
	"os"
)

const SchemaVersion = "1.0"

// JSONCompact switches PrintJSON from pretty output to single-line JSON.
var JSONCompact bool

func PrintJSON(v any) {
	var (
		data []byte
		err  error
	)
	if JSONCompact {
		data, err = json.Marshal(v)
	} else {
		data, err = json.MarshalIndent(v, "", "  ")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "json marshal error: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

type ErrorCode string

const (
	ErrConfig          ErrorCode = "E_CONFIG"
	ErrAuth            ErrorCode = "E_AUTH"
	ErrForbidden       ErrorCode = "E_FORBIDDEN"
	ErrNotFound        ErrorCode = "E_NOT_FOUND"
	ErrRateLimit       ErrorCode = "E_RATE_LIMITED"
	ErrServer          ErrorCode = "E_SERVER"
	ErrValidation      ErrorCode = "E_VALIDATION"
	ErrNetwork         ErrorCode = "E_NETWORK"
	ErrTimeout         ErrorCode = "E_TIMEOUT"
	ErrConfirmRequired ErrorCode = "E_CONFIRMATION_REQUIRED"
	ErrConflict        ErrorCode = "E_CONFLICT"
	ErrIntegrity       ErrorCode = "E_INTEGRITY"
	ErrIO              ErrorCode = "E_IO"
	ErrInterrupted     ErrorCode = "E_INTERRUPTED"
	ErrUnknown         ErrorCode = "E_UNKNOWN"
)

type Envelope struct {
	OK            bool           `json:"ok"`
	SchemaVersion string         `json:"schema_version"`
	Data          any            `json:"data,omitempty"`
	Error         *EnvelopeError `json:"error,omitempty"`
	Meta          EnvelopeMeta   `json:"meta"`
}

type EnvelopeMeta struct {
	DurationMS int64 `json:"duration_ms"`
}

type EnvelopeError struct {
	Code      ErrorCode      `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	Retryable bool           `json:"retryable"`
}

func SuccessEnvelope(data any, durationMs int64) Envelope {
	return Envelope{
		OK:            true,
		SchemaVersion: SchemaVersion,
		Data:          data,
		Meta:          EnvelopeMeta{DurationMS: durationMs},
	}
}

func FailureEnvelope(code ErrorCode, message string, details map[string]any, durationMs int64) Envelope {
	if details == nil {
		details = map[string]any{}
	}
	return Envelope{
		OK:            false,
		SchemaVersion: SchemaVersion,
		Error: &EnvelopeError{
			Code:      code,
			Message:   message,
			Details:   details,
			Retryable: RetryableForErrorCode(code),
		},
		Meta: EnvelopeMeta{DurationMS: durationMs},
	}
}

func RetryableForErrorCode(code ErrorCode) bool {
	switch code {
	case ErrRateLimit, ErrServer, ErrNetwork, ErrTimeout, ErrInterrupted:
		return true
	default:
		return false
	}
}

func ErrorCodeFromStatus(statusCode int) ErrorCode {
	switch statusCode {
	case 401:
		return ErrAuth
	case 403:
		return ErrForbidden
	case 404:
		return ErrNotFound
	case 429:
		return ErrRateLimit
	default:
		if statusCode >= 500 {
			return ErrServer
		}
		if statusCode >= 400 {
			return ErrValidation
		}
		return ErrUnknown
	}
}

func HintForErrorCode(code ErrorCode) string {
	switch code {
	case ErrConfig:
		return "Run 'kibana-cli auth login' or set KIBANA_CLI_HOST with KIBANA_CLI_USER and KIBANA_CLI_PASSWORD"
	case ErrAuth:
		return "Check Kibana username/password (KIBANA_CLI_USER + KIBANA_CLI_PASSWORD)"
	case ErrForbidden:
		return "Check Kibana roles and index read privileges for your user"
	case ErrNotFound:
		return "Verify index name or pattern exists"
	case ErrRateLimit:
		return "Wait and retry; reduce query frequency or result size"
	case ErrServer:
		return "Kibana or upstream search error; try again later"
	case ErrValidation:
		return "Check command arguments and query syntax"
	case ErrNetwork:
		return "Check KIBANA_CLI_HOST (Kibana base URL) and network connectivity"
	case ErrTimeout:
		return "Increase --timeout or retry after checking network latency"
	case ErrConfirmRequired:
		return "Run with --dry-run, review the preview, then pass the returned --confirm token"
	case ErrConflict:
		return "Re-run --dry-run and retry with the new confirm token"
	case ErrIntegrity:
		return "Release integrity verification failed (signature or checksum); do not retry. Re-run update to fetch the current release, or report a possible supply-chain issue"
	case ErrIO:
		return "Local filesystem error during update (disk full, file locked, or partial write); fix the environment, then re-run update"
	case ErrInterrupted:
		return "Update was cancelled by a signal; nothing was left half-applied. Re-run update — it is idempotent"
	default:
		return ""
	}
}

// PrintErrorJSONWithCode writes a unified Agent error envelope to stdout (--json contract).
func PrintErrorJSONWithCode(msg string, statusCode int, code ErrorCode) {
	details := map[string]any{
		"status":    "api_error",
		"errorCode": code,
		"exitCode":  ExitCodeForHTTP(statusCode),
	}
	if statusCode > 0 {
		details["statusCode"] = statusCode
	}
	if hint := HintForErrorCode(code); hint != "" {
		details["hint"] = hint
	}
	PrintJSON(FailureEnvelope(code, msg, details, 0))
}

// ExitCodeForErrorCode maps a semantic error code to its process exit code. It
// is the single source of truth for the code->exit contract so the output layer
// and the command layer cannot drift (see CLI-SPEC §6).
func ExitCodeForErrorCode(code ErrorCode) int {
	switch code {
	case ErrValidation:
		return 2
	case ErrNotFound:
		return 3
	case ErrAuth, ErrForbidden, ErrConfig:
		return 4
	case ErrConfirmRequired:
		return 5
	case ErrConflict:
		return 6
	case ErrRateLimit, ErrServer, ErrNetwork:
		return 7
	case ErrTimeout:
		return 8
	case ErrIntegrity, ErrIO:
		return 1
	case ErrInterrupted:
		return 130
	default:
		return 1
	}
}

// ExitCodeForHTTP is the single source of truth mapping an upstream HTTP status
// to a process exit code. Both the output layer and the cmd layer route through
// this so the status→exit contract can never drift between the two.
func ExitCodeForHTTP(statusCode int) int {
	switch {
	case statusCode == 401:
		return 4
	case statusCode == 403:
		return 4
	case statusCode == 404:
		return 3
	case statusCode == 429:
		return 7
	case statusCode >= 500:
		return 7
	default:
		if statusCode >= 400 {
			return 2
		}
		return 7
	}
}
