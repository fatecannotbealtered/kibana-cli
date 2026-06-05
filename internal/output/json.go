package output

import (
	"encoding/json"
	"fmt"
	"os"
)

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
	ErrConfig     ErrorCode = "CONFIG_ERROR"
	ErrAuth       ErrorCode = "AUTH_REQUIRED"
	ErrForbidden  ErrorCode = "FORBIDDEN"
	ErrNotFound   ErrorCode = "NOT_FOUND"
	ErrRateLimit  ErrorCode = "RATE_LIMITED"
	ErrServer     ErrorCode = "SERVER_ERROR"
	ErrValidation ErrorCode = "VALIDATION_ERROR"
	ErrNetwork    ErrorCode = "NETWORK_ERROR"
	ErrUnknown    ErrorCode = "UNKNOWN_ERROR"
)

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
	default:
		return ""
	}
}

// PrintErrorJSONWithCode writes a unified Agent error envelope to stdout (--json contract).
func PrintErrorJSONWithCode(msg string, statusCode int, code ErrorCode) {
	payload := map[string]any{
		"ok":        false,
		"status":    "api_error",
		"message":   msg,
		"error":     msg,
		"errorCode": code,
		"exitCode":  exitCodeForHTTP(statusCode),
	}
	if statusCode > 0 {
		payload["statusCode"] = statusCode
	}
	if hint := HintForErrorCode(code); hint != "" {
		payload["hint"] = hint
	}
	PrintJSON(payload)
}

func exitCodeForHTTP(statusCode int) int {
	switch {
	case statusCode == 401:
		return 3
	case statusCode == 403:
		return 5
	case statusCode == 404:
		return 4
	case statusCode == 429:
		return 6
	case statusCode >= 500:
		return 7
	default:
		if statusCode >= 400 {
			return 2
		}
		return 7
	}
}
