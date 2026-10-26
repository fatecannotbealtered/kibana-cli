// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Error is the error a command returns to report a contract error (CLI-010). The
// library turns it into the error envelope: it adds retryable and the exit code from
// the contract table, and fills details.dispatch_state ("not_started" unless set) and
// details.next_action (a default per code unless set).
type Error struct {
	Code    string
	Message string
	Details map[string]any
	// Untrusted lists paths inside Details that carry external content, relative to
	// error.details (for example "upstream_message"); they are declared in meta.untrusted.
	Untrusted []string
	Cause     error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.Cause }

// Candidate is one match of an ambiguous name (E_AMBIGUOUS).
type Candidate struct{ Ref, Label string }

// CodeSpec declares an extension error code from the tool's contract-ext.json (CLI-018).
type CodeSpec struct {
	Exit      int
	Retryable bool
	Meaning   string
}

// NewError builds an error with the given code and details (copied).
func NewError(code, message string, details map[string]any) *Error {
	d := make(map[string]any, len(details)+2)
	for k, v := range details {
		d[k] = v
	}
	return &Error{Code: code, Message: message, Details: d}
}

// Usage reports a command-line mistake (E_USAGE, exit 2). problem is one of the
// contract's problem values; parameter names the flag (with dashes) or argument.
func Usage(problem, parameter, message string) *Error {
	e := NewError("E_USAGE", message, map[string]any{"problem": problem})
	if parameter != "" {
		e.Details["parameter"] = parameter
	}
	return e
}

// Validation reports an input the tool or upstream rejected (E_VALIDATION, exit 2).
func Validation(parameter, expected, message string) *Error {
	e := NewError("E_VALIDATION", message, map[string]any{"problem": "invalid_value"})
	if parameter != "" {
		e.Details["parameter"] = parameter
	}
	if expected != "" {
		e.Details["expected"] = expected
	}
	return e
}

// NotFound reports a missing resource (E_NOT_FOUND, exit 3).
func NotFound(message, nextAction string) *Error {
	e := NewError("E_NOT_FOUND", message, nil)
	if nextAction != "" {
		e.Details["next_action"] = nextAction
	}
	return e
}

// Ambiguous reports a name that matched several resources (E_AMBIGUOUS, exit 2).
// Each candidate is {ref, label} (CLI-048); a candidate without a label shows its
// ref. Labels are external text and are declared untrusted. A name that matched
// fewer than two resources is not ambiguous: that is a bug in the tool and is
// reported as E_UNKNOWN.
func Ambiguous(message string, candidates []Candidate) *Error {
	list := make([]any, 0, len(candidates))
	for _, c := range candidates {
		if strings.TrimSpace(c.Ref) == "" {
			continue
		}
		label := c.Label
		if strings.TrimSpace(label) == "" {
			label = c.Ref
		}
		list = append(list, map[string]any{"ref": c.Ref, "label": label})
	}
	if len(list) < 2 {
		return internalError("Ambiguous needs at least two candidates with a ref; a single match is not ambiguous.")
	}
	e := NewError("E_AMBIGUOUS", message, map[string]any{"candidates": list})
	e.Details["next_action"] = "Pick one of details.candidates (ask the user if unsure) and run the command again with its ref."
	e.Untrusted = []string{"candidates[].label"}
	return e
}

// Forbidden reports a refused action (E_FORBIDDEN, exit 4). Upstream refusals may add
// details.required (the role or permission needed); local scope refusals are built by
// the gate and carry details.scope.
func Forbidden(message string, details map[string]any) *Error {
	return NewError("E_FORBIDDEN", message, details)
}

// HumanRequired reports that a person must act first (E_HUMAN_REQUIRED, exit 9).
// action is one of sign_in, approve, solve_challenge, scan_qr, install_component, other;
// resume is the exact command to run afterwards.
func HumanRequired(action, resume, message string) *Error {
	return NewError("E_HUMAN_REQUIRED", message, map[string]any{
		"action":      action,
		"resume":      resume,
		"next_action": "Ask the user to complete details.action, then run details.resume.",
	})
}

// FromHTTP maps an upstream HTTP status to a contract error by status only, never by
// message text (CLI-016). status 0 means the connection failed before anything was
// sent (E_NETWORK). upstream is the upstream's own error text: it goes to
// details.upstream_message, marked untrusted, never into the message. retryAfter > 0
// becomes details.retry_after_ms on 429.
//
// dispatch_state is not_started for every status: dispatch_state says whether a
// state-changing request was sent, and a read (a 404 included) changes nothing
// (decision 1). During a write's apply step the gate reports what was sent: wrap the
// result with Rejected only when the upstream definitively refused the request (the
// gate then reports dispatched); any other error from Apply is an unknown outcome.
func FromHTTP(status int, message string, upstream string, retryAfter time.Duration) *Error {
	code := httpCode(status)
	e := NewError(code, message, nil)
	if status > 0 {
		e.Details["http_status"] = status
	}
	e.Details["dispatch_state"] = "not_started"
	switch code {
	case "E_VALIDATION":
		if status == 413 {
			e.Details["problem"] = "input_too_large"
		} else {
			e.Details["problem"] = "invalid_value"
		}
	case "E_CONFLICT":
		conflict := httpStatusConflicts[strconv.Itoa(status)]
		if conflict == "" {
			conflict = "upstream_conflict"
		}
		e.Details["conflict"] = conflict
		e.Details["next_action"] = "The upstream refused because of its current state; re-read the target, then run the command again with --dry-run."
	case "E_RATE_LIMITED":
		if retryAfter > 0 {
			e.Details["retry_after_ms"] = retryAfter.Milliseconds()
		}
	}
	if strings.TrimSpace(upstream) != "" {
		e.Details["upstream_message"] = truncateRunes(upstream, 500)
		e.Untrusted = append(e.Untrusted, "upstream_message")
	}
	return e
}

// httpCode maps a status to a code using the contract's http_status_map.
func httpCode(status int) string {
	if status <= 0 {
		return httpStatusMap["connection_failure_before_send"]
	}
	if code, ok := httpStatusMap[strconv.Itoa(status)]; ok {
		return code
	}
	switch {
	case status >= 500 && status <= 599:
		return httpStatusMap["5xx"]
	case status == 410:
		return "E_NOT_FOUND"
	case status >= 400 && status <= 499:
		return "E_VALIDATION"
	}
	return "E_UNKNOWN"
}

// asError converts any error to *Error. Unknown errors become E_UNKNOWN with the
// error text in details.cause (marked untrusted: it may carry upstream text).
func asError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e
	}
	// A read that times out or is interrupted changed nothing (decision 1); a write's
	// Apply errors never come here, the gate classifies them.
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return NewError("E_TIMEOUT", "The command ran out of time before the upstream answered.", map[string]any{
			"dispatch_state": "not_started",
			"next_action":    "Retry with a narrower request; nothing was changed by a read.",
		})
	case errors.Is(err, context.Canceled):
		return NewError("E_INTERRUPTED", "The command was interrupted.", map[string]any{
			"dispatch_state": "not_started",
			"next_action":    "Run the command again if it is still needed.",
		})
	}
	e = NewError("E_UNKNOWN", "The command failed with an internal error; details.cause has the error text.", map[string]any{
		"cause": truncateRunes(err.Error(), 300),
	})
	e.Untrusted = []string{"cause"}
	e.Cause = err
	return e
}

// internalError reports a library or tool programming error as E_UNKNOWN.
func internalError(format string, args ...any) *Error {
	return NewError("E_UNKNOWN", fmt.Sprintf(format, args...), map[string]any{
		"next_action": "This is a bug in the tool; report it with the command you ran.",
	})
}

// ioError reports a local file or state store failure (E_IO); nothing was sent.
func ioError(message, nextAction string, cause error) *Error {
	e := NewError("E_IO", message, map[string]any{"dispatch_state": "not_started"})
	if nextAction != "" {
		e.Details["next_action"] = nextAction
	}
	e.Cause = cause
	return e
}

// conflictError reports an E_CONFLICT with the given details.conflict value.
func conflictError(conflict, message, nextAction string) *Error {
	return NewError("E_CONFLICT", message, map[string]any{
		"conflict":       conflict,
		"dispatch_state": "not_started",
		"next_action":    nextAction,
	})
}

var codePattern = regexp.MustCompile(`^E_[A-Z0-9][A-Z0-9_]*$`)

// lookupCode finds a code in the core table, then in the tool's extensions.
func (a *App) lookupCode(code string) (CodeSpec, bool) {
	if c, ok := coreCodes[code]; ok {
		return CodeSpec{Exit: c.exit, Retryable: c.retryable, Meaning: c.meaning}, true
	}
	if a != nil {
		if c, ok := a.ExtCodes[code]; ok && codePattern.MatchString(code) {
			return c, true
		}
	}
	return CodeSpec{}, false
}

// checkExtCodes validates the tool's extension codes (CLI-018).
func (a *App) checkExtCodes() []string {
	var problems []string
	allowed := map[int]bool{}
	for exit := range exitCodeMeanings {
		allowed[exit] = true
	}
	for _, name := range sortedKeys(a.ExtCodes) {
		spec := a.ExtCodes[name]
		switch {
		case !codePattern.MatchString(name):
			problems = append(problems, fmt.Sprintf("extension code %q does not match ^E_[A-Z0-9][A-Z0-9_]*$", name))
		case coreCodes[name] != codeSpec{}:
			problems = append(problems, fmt.Sprintf("extension code %q shadows a core code", name))
		case !allowed[spec.Exit] || spec.Exit == 0:
			problems = append(problems, fmt.Sprintf("extension code %q uses exit %d, which is not a documented failure exit code", name, spec.Exit))
		case strings.TrimSpace(spec.Meaning) == "":
			problems = append(problems, fmt.Sprintf("extension code %q has no meaning", name))
		}
	}
	return problems
}

// defaultNextAction is the next step used when an error does not name one. A usage
// or validation error that names its parameter says what to give it, from the
// details the agent already has, instead of sending it to --help.
func (a *App) defaultNextAction(code string, details map[string]any) string {
	tool := a.toolName()
	path := tool
	if st := a.run; st != nil && st.cmd != nil {
		path = st.cmd.CommandPath()
	}
	param, _ := details["parameter"].(string)
	param = strings.TrimSpace(param)
	switch {
	case (code == "E_USAGE" || code == "E_VALIDATION") && param != "" && hasKey(details, "expected"):
		return fmt.Sprintf("Give %s a value from details.expected and run the command again.", param)
	case code == "E_USAGE" && param != "" && details["problem"] == "missing_parameter":
		if hasKey(details, "usage") {
			return fmt.Sprintf("Add %s (details.usage shows the form) and run the command again.", param)
		}
		return fmt.Sprintf("Add %s and run the command again.", param)
	}
	switch code {
	case "E_USAGE":
		return fmt.Sprintf("Run `%s --help` for its arguments and flags.", path)
	case "E_VALIDATION":
		return "Fix the value named in details.parameter and run the command again."
	case "E_AMBIGUOUS":
		return "Pick one of details.candidates and run the command again with its ref."
	case "E_UNSUPPORTED":
		return "Do not retry this request; use a supported alternative."
	case "E_NOT_FOUND":
		return "Check the identifier; list the resources to find the right one."
	case "E_AUTH":
		return fmt.Sprintf("Ask the user to configure or refresh the credentials; `%s doctor` shows what is missing.", tool)
	case "E_FORBIDDEN":
		return "Stop and tell the user which permission is missing; do not retry with other credentials."
	case "E_CONFIG":
		return fmt.Sprintf("Run `%s doctor` and fix the reported configuration.", tool)
	case "E_CONFIRMATION_REQUIRED":
		return "Review the preview, then run the same command again with --confirm <token>."
	case "E_CONFLICT":
		return "Re-read the current state, then run the command again with --dry-run."
	case "E_OUTCOME_UNKNOWN":
		return fmt.Sprintf("Run `%s operation list --state unknown` and look the operation up before doing anything else.", tool)
	case "E_NETWORK":
		return "Check connectivity to the upstream, then retry."
	case "E_RATE_LIMITED":
		return "Wait details.retry_after_ms (or a few seconds when absent), then retry."
	case "E_SERVER":
		return "Retry later; if it keeps failing, report the upstream failure to the user."
	case "E_TIMEOUT":
		return "Retry with a narrower request or a longer timeout."
	case "E_HUMAN_REQUIRED":
		return "Ask the user to complete details.action, then run details.resume."
	case "E_PARTIAL":
		return "Read details.receipt or details.items to see what took effect; never resend the whole request."
	case "E_VERIFY_FAILED":
		return "Read details.receipt.mismatched and re-read the target before changing it again."
	case "E_VERIFY_INCOMPLETE":
		return "Re-read the target to check the change before relying on it."
	case "E_INTEGRITY":
		return "Do not retry; tell the user the integrity check failed."
	case "E_IO":
		return "Check the local file or the tool's state directory named in the message."
	case "E_INTERRUPTED":
		return "Run the command again if it is still needed."
	}
	return "Read message and details; report the error if it persists."
}

// normalizeError fills the contract-required detail keys of e for emission and
// returns the error object, the exit code and the untrusted paths (rooted at the
// envelope). Unknown codes become E_UNKNOWN so the code, exit code and retryable
// always agree with the contract table.
func (a *App) normalizeError(e *Error) (errorObject, int, []string) {
	if e == nil {
		e = internalError("A nil error was returned.")
	}
	code := e.Code
	details := make(map[string]any, len(e.Details)+3)
	for k, v := range e.Details {
		details[k] = v
	}
	spec, ok := a.lookupCode(code)
	if !ok {
		details["undeclared_code"] = code
		code = "E_UNKNOWN"
		spec, _ = a.lookupCode(code)
	}
	if code == "E_AMBIGUOUS" && listLen(details["candidates"]) < 2 {
		// An ambiguity without two candidates cannot be acted on: a tool bug.
		details["undeclared_code"] = code
		delete(details, "candidates")
		code = "E_UNKNOWN"
		spec, _ = a.lookupCode(code)
	}
	if ds, _ := details["dispatch_state"].(string); !contains(dispatchStates, ds) {
		details["dispatch_state"] = "not_started"
	}
	// dispatch_state is fixed for some codes (CLI-011, CLI-015, error-details schema).
	switch code {
	case "E_USAGE", "E_CONFIRMATION_REQUIRED", "E_NETWORK":
		details["dispatch_state"] = "not_started"
	case "E_OUTCOME_UNKNOWN":
		if details["dispatch_state"] == "not_started" {
			details["dispatch_state"] = "unknown"
		}
	case "E_PARTIAL", "E_VERIFY_FAILED", "E_VERIFY_INCOMPLETE":
		details["dispatch_state"] = "dispatched"
	}
	if na, _ := details["next_action"].(string); strings.TrimSpace(na) == "" {
		details["next_action"] = a.defaultNextAction(code, details)
	}
	// An empty suggestion list helps nobody and the schema forbids it (minItems 1).
	if v, ok := details["did_you_mean"]; ok && listLen(v) == 0 {
		delete(details, "did_you_mean")
	}
	switch code {
	case "E_USAGE", "E_VALIDATION":
		if p, _ := details["problem"].(string); !contains(problemValues, p) {
			details["problem"] = "invalid_value"
		}
	case "E_CONFLICT":
		if c, _ := details["conflict"].(string); !contains(conflictValues, c) {
			details["conflict"] = "upstream_conflict"
		}
	case "E_HUMAN_REQUIRED":
		if act, _ := details["action"].(string); !contains(humanActionValues, act) {
			details["action"] = "other"
		}
		if r, _ := details["resume"].(string); strings.TrimSpace(r) == "" {
			resume := a.toolName()
			if st := a.run; st != nil && st.cmd != nil {
				resume = st.cmd.CommandPath()
			}
			details["resume"] = resume
		}
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = spec.Meaning
	}
	// No emitted error shows a secret, wherever its text came from: the message, a
	// cause or upstream message, a panic text or any nested detail (D1, SEC-021).
	if secrets := a.errorSecrets(); len(secrets) > 0 {
		message = redactWith(message, secrets)
		if d, ok := redactValue(details, secrets).(map[string]any); ok {
			details = d
		}
	}
	var untrusted []string
	for _, p := range e.Untrusted {
		p = strings.TrimPrefix(p, "error.details.")
		if p != "" {
			untrusted = append(untrusted, "error.details."+p)
		}
	}
	obj := errorObject{Code: code, Message: message, Retryable: spec.Retryable, Details: details}
	return obj, spec.Exit, untrusted
}

// listLen returns the length of a JSON-array-like value ([]any, []string or any
// other slice through its generic form), or -1 when v is not a list.
func listLen(v any) int {
	switch x := v.(type) {
	case nil:
		return -1
	case []any:
		return len(x)
	case []string:
		return len(x)
	}
	if g, err := toGeneric(v); err == nil {
		if list, ok := g.([]any); ok {
			return len(list)
		}
	}
	return -1
}

// truncateRunes shortens s to at most n runes, marking the cut.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
