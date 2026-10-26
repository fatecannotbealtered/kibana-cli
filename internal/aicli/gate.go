// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The shared write gate (CLI-050 .. CLI-063, SEC-020). Every command with an effect
// other than observe reports through Write or WriteBatch:
//
//   - without --confirm the gate plans: it checks --dangerous, the scope and earlier
//     unknown outcomes, calls Preview, records the operation and returns the preview
//     with a confirm token (as data with --dry-run, as E_CONFIRMATION_REQUIRED
//     without it). Nothing is sent.
//   - with --confirm it re-checks everything in a fixed order, consumes the token
//     exactly once, records the operation as dispatching, calls Apply and derives
//     ok and the error code from the receipt.
//
// No output mode, flag or environment variable turns the gate off.

// Target identifies what a write acts on. Key is the canonical key scopes match (for
// example "acme/web!5"); Kind and Label are for people.
type Target struct{ Key, Kind, Label string }

// FieldChange is one planned change in a preview (preview.changes[]).
type FieldChange struct {
	Field                      string
	Before, After, Add, Remove any
	Note                       string
}

// Block is a reason a previewed write cannot be applied (preview.blocked_by[]).
type Block struct{ Kind, Message string }

// Preview is what Preview returns: the planned change, read-only. BaseVersion is the
// target's current version (updated_at, etag, revision) when the upstream has one;
// the gate re-reads it at confirm and refuses on drift. CanApply must be true for a
// token to be issued; BlockedBy says why not. Destinations (deliver) and Outputs
// (artifact, persist) are checked against the scope. Untrusted lists paths relative
// to the preview object that carry external content (for example "changes[].before").
// Dangerous makes this plan dangerous although its command is not (a commit that
// deletes a file, Q13): --dangerous is then needed in both steps, the preview says
// dangerous: true and the scope must grant dangerous; a plan whose danger changes
// between the dry-run and the confirm is state_drift.
type Preview struct {
	Action                  string
	Changes                 []FieldChange
	BaseVersion, BaseSource string
	CanApply                bool
	BlockedBy               []Block
	Destinations, Outputs   []string
	Untrusted               []string
	Dangerous               bool
}

// WriteSpec describes one write. Preview is read-only and is called at dry-run, at
// the refusal and again at confirm. Apply performs the change; its error decides the
// outcome: NotDispatched (nothing was sent), Rejected (the upstream definitively
// refused; nothing changed), anything else an unknown outcome (E_OUTCOME_UNKNOWN).
// Timeout is Apply's deadline (default 2 minutes); the record's lease ends 5 s after
// it (CLI-058). ReadBack is the read command, without the tool name, that shows
// whether the change landed (for example "note get 1"); an unknown outcome's
// next_action names it. Build it from the registry path and validated inputs only,
// never from upstream text (SEC-002). A "{target}" in it is replaced by the
// resolved target key, for a target that ResolveTarget finds.
//
// ResolveTarget, when set, is used instead of Target for a target that takes a read
// to resolve (the canonical key of a variable named by its name, Q1). The gate calls
// it at a dry-run or bare call before the scope check, and at confirm only after the
// token checks (format, key, record, MAC, single use, expiry, inputs, --dangerous),
// so a used token is token_used before any tool code runs. Its error is returned as
// the tool's error, nothing sent; a resolved target that differs from the one the
// token was issued for is token_mismatch (target). Do no such read before App.Write.
type WriteSpec struct {
	Target        Target
	ResolveTarget func(ctx context.Context) (Target, error)
	Preview       func(ctx context.Context) (Preview, error)
	Apply         func(ctx context.Context, op Op) (Result, error)
	Timeout       time.Duration
	ReadBack      string
}

// Op is handed to Apply: the operation id and the idempotency key to send upstream.
// Apply MUST honour its context: return by the context's deadline (the write's
// Timeout) and stop when it is cancelled. Once the lease (the deadline plus 5 s) has
// passed, operation resolve may record an outcome by hand; a confirm whose record
// was resolved while Apply still ran reports E_OUTCOME_UNKNOWN (CLI-058).
type Op struct{ ID, IdempotencyKey string }

// leaseGrace is how long a record's lease outlasts Apply's deadline (CLI-058), so
// operation resolve cannot start while Apply may still be running.
const leaseGrace = 5 * time.Second

// targetLockWait is how long a confirm waits for another confirm on the same target
// (CLI-057 step 9) before it answers lock_held.
var targetLockWait = 10 * time.Second

// BatchItem is one target of a batch write. Apply may be nil when the batch sends
// its items through BatchSpec.ApplyAll.
type BatchItem struct {
	Target  Target
	Preview func(ctx context.Context) (Preview, error)
	Apply   func(ctx context.Context, op Op) (Result, error)
}

// BatchSpec describes a batch write (CLI-063). ContinueOnError overrides the
// --continue-on-error flag (default true, false for dangerous commands). Timeout is
// the deadline of each Apply (or ApplyAll) call.
//
// Action is the batch preview's action ("Close the merge requests"); empty means the
// first item's, with the number of targets (Q19).
//
// ResolveItems, when set, is used instead of Items for a list that takes reads to
// resolve, and is called when WriteSpec.ResolveTarget would be (Q1).
//
// ApplyAll sends the items through the upstream's native bulk call instead of each
// item's Apply (Q12): in chunks of at most MaxPerCall items (all at once when 0),
// one call per chunk, in item order. It returns one ItemOutcome per op, in the same
// order, each classified like an item's Apply result. A missing or extra outcome is
// a defect and makes every item of the chunk unknown; an error of the whole call
// makes every item of the chunk unknown, unless it is NotDispatched or Rejected,
// which then applies to each. Records and receipts stay per item.
type BatchSpec struct {
	Items           []BatchItem
	ContinueOnError *bool
	Timeout         time.Duration
	Action          string
	ResolveItems    func(ctx context.Context) ([]BatchItem, error)
	ApplyAll        func(ctx context.Context, ops []Op) ([]ItemOutcome, error)
	MaxPerCall      int
}

// ItemOutcome is one item's result of BatchSpec.ApplyAll: what the item's Apply
// would have returned.
type ItemOutcome struct {
	Result Result
	Err    error
}

type notDispatchedError struct{ err error }

func (e *notDispatchedError) Error() string { return "not dispatched: " + e.err.Error() }
func (e *notDispatchedError) Unwrap() error { return e.err }

type rejectedError struct{ err error }

func (e *rejectedError) Error() string { return "rejected: " + e.err.Error() }
func (e *rejectedError) Unwrap() error { return e.err }

// NotDispatched marks an Apply error raised before anything was sent: the operation
// is not_started, nothing changed, and the error's code is reported.
func NotDispatched(err error) error {
	if err == nil {
		err = errors.New("not dispatched")
	}
	return &notDispatchedError{err: err}
}

// Rejected marks an Apply error where the upstream definitively refused the request
// and nothing changed (for example HTTP 400, 403, 404, 409, 422). The error's code
// is reported with dispatch_state dispatched.
func Rejected(err error) error {
	if err == nil {
		err = errors.New("rejected")
	}
	return &rejectedError{err: err}
}

const defaultTimeout = time.Duration(defaultLeaseSeconds) * time.Second

// gateRun is one pass through the gate.
type gateRun struct {
	a             *App
	st            *runState
	cmd           *cobra.Command
	m             *Meta
	path          string
	dryRun        bool
	confirm       string
	confirmSet    bool
	dangerousFlag bool
	idem          string
	// planDangerous is a plan made dangerous by Preview.Dangerous (Q13).
	planDangerous bool
	// dispatched is the operation id once its dispatching record is written
	// (CLI-057 step 14): from then on no error says that nothing was sent.
	dispatched string
}

// Write runs a single-target write through the gate.
func (a *App) Write(cmd *cobra.Command, spec WriteSpec) (err error) {
	g, err := a.startGate(cmd, false)
	if g == nil {
		return err
	}
	defer g.recoverAfterDispatch(&err)
	if spec.Preview == nil || spec.Apply == nil || (strings.TrimSpace(spec.Target.Key) == "" && spec.ResolveTarget == nil) {
		return g.fail(internalError("App.Write needs WriteSpec.Target.Key (or ResolveTarget), Preview and Apply."))
	}
	if spec.ResolveTarget == nil {
		if e := checkTargetKey(spec.Target.Key); e != nil {
			return g.fail(e)
		}
	}
	if g.confirmSet {
		return g.confirmSingle(spec)
	}
	spec, e := g.resolveTarget(spec)
	if e != nil {
		return g.fail(e)
	}
	return g.planSingle(spec)
}

// resolveTarget calls WriteSpec.ResolveTarget when it is set (Q1) and checks the key
// it returns. Its error is the tool's own, and nothing was sent.
func (g *gateRun) resolveTarget(spec WriteSpec) (WriteSpec, *Error) {
	if spec.ResolveTarget == nil {
		return spec, nil
	}
	t, e := callTool(g.a, spec.ResolveTarget, "The write target could not be resolved; nothing was sent.")
	if e != nil {
		return spec, e
	}
	if strings.TrimSpace(t.Key) == "" {
		return spec, internalError("WriteSpec.ResolveTarget returned a target without a key.")
	}
	if e := checkTargetKey(t.Key); e != nil {
		return spec, e
	}
	spec.Target, spec.ResolveTarget = t, nil // resolved once per run
	return spec, nil
}

// callTool runs one of the tool's own hooks (ResolveTarget, ResolveItems) with the
// run's context. Its error keeps its code with dispatch_state not_started; a panic is
// an internal error, and nothing was sent either way.
func callTool[T any](a *App, fn func(context.Context) (T, error), panicMessage string) (out T, e *Error) {
	defer func() {
		if r := recover(); r != nil {
			e = internalError("%s", panicMessage)
			e.Details["cause"] = truncateRunes(fmt.Sprint(r), 300)
			e.Untrusted = []string{"cause"}
		}
	}()
	out, err := fn(a.ctx())
	if err != nil {
		e = copyError(asError(err))
		e.Details["dispatch_state"] = "not_started"
		return out, e
	}
	return out, nil
}

// checkTargetKey refuses target keys that could slip past scope prefix matching:
// dot-dot path segments and control characters (SEC-012). Tools build keys from
// resolved, canonical identifiers; this catches one built from raw input.
func checkTargetKey(key string) *Error {
	bad := strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f })
	for _, seg := range strings.FieldsFunc(key, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." || seg == "." {
			bad = true
		}
	}
	if !bad {
		return nil
	}
	e := Validation("target", "a canonical identifier without . or .. segments", "The write target is not a canonical identifier; nothing was sent.")
	e.Details["next_action"] = "Give the resource by its id or ref, without relative path segments."
	return e
}

// startGate checks the command may use the gate and reads the gate flags.
func (a *App) startGate(cmd *cobra.Command, batch bool) (*gateRun, error) {
	st, perr := a.runFor(cmd)
	if perr != nil {
		return nil, perr
	}
	g := &gateRun{a: a, st: st, cmd: cmd, path: cmdPath(cmd)}
	st.writeCalls++
	if st.writeCalls > 1 {
		return nil, g.fail(internalError("A command can pass the write gate once per run."))
	}
	m := a.metaFor(cmd)
	if m == nil || !m.isWrite() {
		return nil, g.fail(internalError("This command is not registered as a write (Meta.Effects), so the gate refuses it."))
	}
	g.m = m
	if batch != (m.BatchFlag != "") {
		return nil, g.fail(internalError("Commands with Meta.BatchFlag use App.WriteBatch; others use App.Write."))
	}
	shadow := ""
	for _, name := range excludedFromBinding {
		if f := cmd.Flags().Lookup(name); name != "help" && f != nil && f != cmd.Root().PersistentFlags().Lookup(name) {
			shadow = name
			break
		}
	}
	if shadow != "" {
		return nil, g.fail(internalError("The command defines --%s, which shadows a global control flag; the gate refuses it.", shadow))
	}
	g.dryRun, _ = cmd.Flags().GetBool("dry-run")
	g.dangerousFlag, _ = cmd.Flags().GetBool("dangerous")
	g.confirm, _ = cmd.Flags().GetString("confirm")
	g.confirmSet = cmd.Flags().Changed("confirm")
	if f := cmd.Flags().Lookup("idempotency-key"); f != nil && f.Changed {
		g.idem = claimString(f.Value.String())
	}
	if g.dryRun && g.confirmSet {
		e := Usage("conflicting_flags", "--confirm", "--dry-run and --confirm cannot be combined; nothing was changed.")
		e.Details["next_action"] = "Drop --dry-run: run the same command with --confirm <token> only (or drop --confirm to preview again)."
		return nil, g.fail(e)
	}
	return g, nil
}

// fail records the gate's error as the run's outcome (the first one wins).
func (g *gateRun) fail(e *Error) error {
	if !g.st.gate.set {
		g.st.gate = result{set: true, err: e}
	}
	return e
}

// succeed records the gate's success data as the run's outcome.
func (g *gateRun) succeed(data map[string]any, untrusted []string) error {
	gen, err := toGenericData(data)
	if err != nil {
		if g.dispatched != "" {
			return g.fail(g.unknownAfterDispatch(g.dispatched))
		}
		return g.fail(internalError("The write result cannot be encoded as JSON."))
	}
	if !g.st.gate.set {
		g.st.gate = result{set: true, data: gen, raw: data, untrusted: untrusted, fromGate: true}
	}
	return nil
}

// unknownAfterDispatch is the error of a failure the library cannot classify once
// the write was dispatched (CLI-057, A2): the outcome is unknown, never "nothing was
// sent".
func (g *gateRun) unknownAfterDispatch(id string) *Error {
	lookup := fmt.Sprintf("%s operation show --id %s", g.tool(), id)
	return NewError("E_OUTCOME_UNKNOWN", "The write may have been applied, but its result could not be reported; nothing will be retried.",
		map[string]any{"operation_id": id, "lookup": lookup, "dispatch_state": "unknown",
			"next_action": fmt.Sprintf("Run `%s` and re-read the target before anything else; do not resend.", lookup)})
}

// recoverAfterDispatch turns a panic after the dispatching record was written into
// E_OUTCOME_UNKNOWN with the operation id (A2); before that point the panic goes on
// to Execute, which reports it as an internal error (nothing was sent).
func (g *gateRun) recoverAfterDispatch(err *error) {
	r := recover()
	if r == nil {
		return
	}
	if g.dispatched == "" {
		panic(r)
	}
	e := g.unknownAfterDispatch(g.dispatched)
	e.Details["cause"] = truncateRunes(fmt.Sprint(r), 300)
	e.Untrusted = append(e.Untrusted, "cause")
	*err = g.fail(e)
}

func (g *gateRun) tool() string { return g.a.toolName() }

func (g *gateRun) dangerousRequired() *Error {
	return NewError("E_CONFIRMATION_REQUIRED",
		"This command is dangerous: --dangerous is required in both the preview and the confirm step. Nothing was changed.",
		map[string]any{
			"problem":        "dangerous_flag_required",
			"dispatch_state": "not_started",
			"next_action":    "Run the same command again with --dangerous added (keep --dry-run or --confirm as they were).",
		})
}

// dangerousRefusal is dangerous_flag_required at plan time with the read-only
// preview attached, so the plan can be shown to the user without another call. It
// carries no token: a token is issued only to a preview run with --dangerous.
func (g *gateRun) dangerousRefusal(spec WriteSpec) *Error {
	e := g.dangerousRequired()
	pv, perr := g.callPreview(spec.Preview)
	if perr != nil {
		return e
	}
	e = g.attachPreview(e, spec, pv)
	if hasKey(e.Details, "preview") {
		e.Details["next_action"] = "details.preview shows the plan; run the same command again with --dangerous added to get its confirm token (keep --dry-run if you used it)."
	}
	return e
}

func (g *gateRun) tokenMismatch(parameter, message string) *Error {
	next := "Run the same command with --dry-run to get a token for these inputs, or confirm with the inputs the token was issued for."
	if parameter == "--confirm" {
		next = "Pass the confirm_token from the latest preview unchanged, or run the command again with --dry-run for a new token."
	}
	e := conflictError("token_mismatch", message+" Nothing was sent.", next)
	e.Details["parameter"] = parameter
	return e
}

func (g *gateRun) tokenUsed(id string) *Error {
	e := conflictError("token_used", "This confirm token was already used; nothing was sent again.",
		fmt.Sprintf("Check the result with `%s operation show --id %s`; run --dry-run again only if another change is needed.", g.tool(), id))
	e.Details["operation_id"] = id
	return e
}

func (g *gateRun) unresolvedConflict(id string) *Error {
	e := conflictError("unresolved_operation", "An earlier operation on this target has an unknown outcome; nothing was sent.",
		fmt.Sprintf("Run `%s operation show --id %s` and check the upstream; then record the outcome with `%s operation resolve --id %s --outcome applied|not_applied|partial --evidence <text>`.",
			g.tool(), id, g.tool(), id))
	e.Details["active_operation"] = id
	return e
}

func (g *gateRun) checkUnresolved(j *journal, targets []string, exclude map[string]bool) *Error {
	id, e := j.blocking(targets, exclude, g.a.now())
	if e != nil {
		return e
	}
	if id != "" {
		return g.unresolvedConflict(id)
	}
	return nil
}

func (g *gateRun) need(targets, destinations, outputs []string, operations int) scopeNeed {
	return scopeNeed{effects: g.m.effectStrings(), dangerous: g.dangerous(), targets: targets,
		destinations: destinations, outputs: outputs, operations: operations}
}

// dangerous reports a write that needs --dangerous: its command is dangerous, or its
// plan is (Preview.Dangerous, Q13).
func (g *gateRun) dangerous() bool { return g.m.dangerous() || g.planDangerous }

// planDangerousRefusal is dangerous_flag_required for a plan that Preview made
// dangerous (Q13), with the plan attached as dangerousRefusal attaches it.
func (g *gateRun) planDangerousRefusal(spec WriteSpec, pv Preview) *Error {
	e := g.attachPreview(g.dangerousRequired(), spec, pv)
	if hasKey(e.Details, "preview") {
		e.Details["next_action"] = "details.preview shows the plan; run the same command again with --dangerous added to get its confirm token (keep --dry-run if you used it)."
	}
	return e
}

// createOnlyExisting refuses an output path that exists for a command that declares
// artifact but not persist (Q9): it never overwrites, so no scope grant could help.
func (g *gateRun) createOnlyExisting(paths []string) *Error {
	if !g.m.hasEffect(Artifact) || g.m.hasEffect(Persist) {
		return nil
	}
	for _, p := range paths {
		if outputExists(p) {
			return createOnlyExists(p)
		}
	}
	return nil
}

// outputExists reports whether a local output path exists, after resolving links; a
// path whose existence cannot be read counts as existing, the stricter case.
func outputExists(p string) bool {
	r, reason := resolvePath(p)
	if reason != "" {
		return false // a junction is refused by the scope check (C2)
	}
	_, err := os.Lstat(r)
	return !errors.Is(err, fs.ErrNotExist)
}

// createOnlyExists is Q9's refusal of an existing output path by a command that only
// creates files: E_VALIDATION invalid_value with the path, nothing written.
func createOnlyExists(path string) *Error {
	e := Validation("", "", "The output file already exists, and this command never overwrites files; nothing was written.")
	e.Details["path"] = path
	e.Details["dispatch_state"] = "not_started"
	e.Details["next_action"] = "Choose a new file name, or move the existing file away first."
	return e
}

// withoutCreateOnlyExisting leaves out of a scope request the existing outputs of a
// command that only creates files (Q9): roots would not help it.
func (g *gateRun) withoutCreateOnlyExisting(paths []string) []string {
	if !g.m.hasEffect(Artifact) || g.m.hasEffect(Persist) {
		return paths
	}
	var out []string
	for _, p := range paths {
		if !outputExists(p) {
			out = append(out, p)
		}
	}
	return out
}

// identity resolves the account and host from App.Identity (local configuration).
func (g *gateRun) identity() (Identity, *Error) {
	if g.a.Identity == nil {
		return Identity{}, nil
	}
	id, err := g.a.Identity(g.a.ctx())
	if err != nil {
		e := copyError(asError(err))
		if e.Code == "E_UNKNOWN" {
			e = NewError("E_CONFIG", "The account this write would act as cannot be resolved; nothing was sent.", nil)
		}
		e.Details["dispatch_state"] = "not_started"
		return Identity{}, e
	}
	id.Host = redactHost(id.Host)
	return id, nil
}

// copyError returns a shallow copy so the gate can adjust details safely.
func copyError(e *Error) *Error {
	c := *e
	c.Details = cloneMap(e.Details)
	c.Untrusted = append([]string(nil), e.Untrusted...)
	return &c
}

// callPreview runs the tool's read-only Preview. Its errors keep their code; the
// write itself was not started.
func (g *gateRun) callPreview(fn func(context.Context) (Preview, error)) (pv Preview, e *Error) {
	defer func() {
		if r := recover(); r != nil {
			e = internalError("The preview failed with an internal error; nothing was sent.")
			e.Details["cause"] = truncateRunes(fmt.Sprint(r), 300)
			e.Untrusted = []string{"cause"}
		}
	}()
	pv, err := fn(g.a.ctx())
	if err != nil {
		e = copyError(asError(err))
		e.Details["dispatch_state"] = "not_started"
		return pv, e
	}
	return pv, nil
}

// callApply runs Apply with the lease as its deadline. A panic is an unknown outcome:
// nobody can tell whether the request went out.
func (g *gateRun) callApply(fn func(context.Context, Op) (Result, error), op Op, timeout time.Duration) (res Result, err error) {
	ctx, cancel := context.WithTimeout(g.a.ctx(), timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
			err = fmt.Errorf("apply panicked: %v", r)
		}
	}()
	return fn(ctx, op)
}

// callApplyAll runs one native bulk call for a chunk of batch items (Q12) and returns
// one outcome per op. An error of the whole call becomes every item's error
// (NotDispatched and Rejected keep their meaning; anything else is unknown), as does
// a panic; a call that returns a different number of outcomes is a defect, and every
// item of the chunk is unknown.
func (g *gateRun) callApplyAll(fn func(context.Context, []Op) ([]ItemOutcome, error), ops []Op, timeout time.Duration) []ItemOutcome {
	ctx, cancel := context.WithTimeout(g.a.ctx(), timeout)
	defer cancel()
	outs, err := func() (outs []ItemOutcome, err error) {
		defer func() {
			if r := recover(); r != nil {
				outs, err = nil, fmt.Errorf("apply panicked: %v", r)
			}
		}()
		return fn(ctx, ops)
	}()
	each := make([]ItemOutcome, len(ops))
	switch {
	case err != nil:
		for i := range each {
			each[i] = ItemOutcome{Err: err}
		}
	case len(outs) != len(ops):
		g.a.defect(fmt.Sprintf("BatchSpec.ApplyAll returned %d outcomes for %d items; every item of that call is reported unknown.", len(outs), len(ops)))
		for i := range each {
			each[i] = ItemOutcome{Err: errors.New("the bulk call returned no outcome for this item")}
		}
	default:
		copy(each, outs)
	}
	return each
}

// checkDeclared makes deliver and local-file writes declare what the scope must cover.
func (g *gateRun) checkDeclared(pv Preview) *Error {
	if g.m.hasEffect(Deliver) && len(pv.Destinations) == 0 {
		return internalError("A deliver command must list Preview.Destinations so the scope can be checked.")
	}
	if g.m.hasEffect(Artifact, Persist) && len(pv.Outputs) == 0 {
		return internalError("A command with artifact or persist effects must list Preview.Outputs so the scope can be checked.")
	}
	return nil
}

func canApplyOf(pv Preview) (bool, []Block) {
	blocks := pv.BlockedBy
	can := pv.CanApply && len(blocks) == 0
	if !can && len(blocks) == 0 {
		blocks = []Block{{Kind: "preview", Message: "The preview does not allow this change."}}
	}
	return can, blocks
}

func targetObject(t Target) map[string]any {
	o := map[string]any{"key": t.Key}
	if t.Kind != "" {
		o["kind"] = t.Kind
	}
	if t.Label != "" {
		o["label"] = t.Label
	}
	return o
}

func targetPtr(t Target) *targetJSON { return &targetJSON{Key: t.Key, Kind: t.Kind, Label: t.Label} }

func changeObjects(changes []FieldChange) []any {
	out := []any{}
	for _, c := range changes {
		o := map[string]any{"field": c.Field}
		for k, v := range map[string]any{"before": c.Before, "after": c.After, "add": c.Add, "remove": c.Remove} {
			if gv, err := toGeneric(v); err == nil && gv != nil {
				o[k] = gv
			}
		}
		if c.Note != "" {
			o["note"] = c.Note
		}
		out = append(out, o)
	}
	return out
}

func blockObjects(blocks []Block) []any {
	out := []any{}
	for _, b := range blocks {
		out = append(out, map[string]any{"kind": b.Kind, "message": b.Message})
	}
	return out
}

func (g *gateRun) action(pv Preview) string {
	if strings.TrimSpace(pv.Action) != "" {
		return pv.Action
	}
	return "Run " + g.path
}

// previewObject is the preview as emitted and stored (BLUEPRINT 4.3).
func (g *gateRun) previewObject(id string, t Target, pv Preview, inputs map[string]any, canApply bool, blocks []Block) map[string]any {
	obj := map[string]any{
		"operation_id": id, "command": g.path, "action": g.action(pv), "target": targetObject(t),
		"effects": g.m.effectStrings(), "dangerous": g.m.dangerous() || pv.Dangerous, "changes": changeObjects(pv.Changes),
		"inputs": inputs, "can_apply": canApply, "blocked_by": blockObjects(blocks),
	}
	if pv.BaseVersion != "" {
		src := pv.BaseSource
		if src == "" {
			src = "version"
		}
		obj["base"] = map[string]any{"version": pv.BaseVersion, "source": src}
	}
	if len(pv.Destinations) > 0 {
		obj["destinations"] = append([]string{}, pv.Destinations...)
	}
	if len(pv.Outputs) > 0 {
		obj["outputs"] = append([]string{}, pv.Outputs...)
	}
	gen, err := toGeneric(obj)
	if m, ok := gen.(map[string]any); ok && err == nil {
		return m
	}
	return obj
}

func (g *gateRun) claims(id string, b *binding, ident Identity, target, scope, base string, exp time.Time) claims {
	return claims{V: 2, Tool: claimString(g.tool()), ToolVersion: claimString(g.a.version()), Op: id, Cmd: claimString(g.path),
		Inv: b.inv, Inputs: b.digests, Account: claimString(ident.Account), Host: claimString(ident.Host),
		Target: target, Scope: scope, Base: base, Idem: g.idem, Exp: timeString(exp)}
}

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func decodeGeneric(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func stringsAt(m map[string]any, key string) []string {
	list, _ := m[key].([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// withPreview adds details.preview to a scope refusal when the preview can be built
// (it is read-only), so the person granting the scope sees what was asked for, and
// widens the refusal to the destinations and outputs the preview names, so that one
// grant covers the whole write.
func (g *gateRun) withPreview(e *Error, spec WriteSpec, sc scopeState, need scopeNeed, state string) *Error {
	pv, perr := g.callPreview(spec.Preview)
	if perr != nil {
		return e
	}
	outputs := g.withoutCreateOnlyExisting(pv.Outputs)
	if len(need.destinations)+len(need.outputs) == 0 && len(pv.Destinations)+len(outputs) > 0 {
		full := need
		full.destinations, full.outputs = pv.Destinations, outputs
		full.dangerous = full.dangerous || pv.Dangerous
		if wider := g.a.checkScope(sc, full, state); wider != nil && wider.Code == e.Code {
			e = wider
		}
	}
	return g.attachPreview(e, spec, pv)
}

// attachPreview puts a refused write's read-only preview in details.preview. Its
// operation id is never recorded and no token is issued.
func (g *gateRun) attachPreview(e *Error, spec WriteSpec, pv Preview) *Error {
	id, err := newOperationID(false)
	if err != nil {
		return e
	}
	can, blocks := canApplyOf(pv)
	inputs := map[string]any{}
	if key := g.scratchKey(); key != nil {
		if b, berr := g.a.bindInvocation(g.st, g.cmd, g.m, key); berr == nil {
			inputs = b.display
		}
	}
	e.Details["preview"] = g.previewObject(id, spec.Target, pv, inputs, can, blocks)
	if len(pv.Untrusted) > 0 {
		e.Untrusted = append(e.Untrusted, prefixPaths("preview", pv.Untrusted)...)
	}
	return e
}

// scratchKey is a throw-away key used only to render preview inputs in a refusal
// (secrets are shown as "***"; nothing keyed with it is stored).
func (g *gateRun) scratchKey() []byte {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	return key
}

// issued is a planned write: its preview, operation id and token (nil, with a nil
// expiry, when the write cannot apply); or, when replay is set, the answer to a
// repeated idempotent request found before Preview (P4).
type issued struct {
	pobj      map[string]any
	id        string
	token     any
	expires   any
	canApply  bool
	untrusted []string
	replay    map[string]any
}

// mint issues the token for a plan that can apply.
func mint(pobj map[string]any, id string, key []byte, cl claims, canApply bool, exp time.Time, untrusted []string) (*issued, *Error) {
	out := &issued{pobj: pobj, id: id, canApply: canApply, untrusted: untrusted}
	if canApply {
		t, err := mintToken(key, cl)
		if err != nil {
			return nil, internalError("The confirm token could not be issued; nothing was changed.")
		}
		out.token, out.expires = t, timeString(exp)
	}
	return out, nil
}

// issue reports a plan: dry-run data, or E_CONFIRMATION_REQUIRED for a bare call.
func (g *gateRun) issue(p *issued) error {
	if g.dryRun {
		data := map[string]any{"preview": p.pobj, "confirm_token": p.token, "expires_at": p.expires, "operation_id": p.id}
		return g.succeed(data, prefixPaths("data.preview", p.untrusted))
	}
	msg := "Confirmation required: nothing was changed; details.preview shows what the command would do."
	next := fmt.Sprintf("Check details.preview; if it is what the user asked for, run the same command again with%s --confirm %v.", g.dangerousArg(), p.token)
	if !p.canApply {
		msg = "The write cannot be applied as previewed (details.preview.blocked_by); nothing was changed."
		next = "Resolve what details.preview.blocked_by names, then run the command again."
	}
	e := NewError("E_CONFIRMATION_REQUIRED", msg, map[string]any{
		"problem": "confirmation_required", "preview": p.pobj, "confirm_token": p.token, "expires_at": p.expires,
		"operation_id": p.id, "dispatch_state": "not_started", "next_action": next,
	})
	e.Untrusted = prefixPaths("preview", p.untrusted)
	return g.fail(e)
}

// dangerousArg is " --dangerous" for a dangerous write, for next_action texts.
func (g *gateRun) dangerousArg() string {
	if g.dangerous() {
		return " --dangerous"
	}
	return ""
}

// withFreshPlan answers token_expired and state_drift with a new plan for the same
// command: its preview, token, expiry and operation id go into the conflict details,
// so the agent checks the new preview and confirms it in one call instead of two.
// When the write cannot be planned or applied now, the conflict stands alone.
func (g *gateRun) withFreshPlan(e *Error, spec WriteSpec) *Error {
	if g.m.dangerous() && !g.dangerousFlag {
		return e
	}
	spec, re := g.resolveTarget(spec) // an expired token: the new plan is a dry-run (Q1)
	if re != nil {
		return e
	}
	p, pe := g.prepareSingle(spec, false)
	if pe != nil || !p.canApply {
		return e
	}
	e.Details["preview"] = p.pobj
	e.Details["confirm_token"] = p.token
	e.Details["expires_at"] = p.expires
	e.Details["operation_id"] = p.id
	e.Untrusted = append(e.Untrusted, prefixPaths("preview", p.untrusted)...)
	e.Details["next_action"] = fmt.Sprintf("details.preview is a new plan: if it still matches what the user asked for, run the same command again with%s --confirm %v.",
		g.dangerousArg(), p.token)
	return e
}

// ---------------------------------------------------------------- single write

func (g *gateRun) planSingle(spec WriteSpec) error {
	if g.m.dangerous() && !g.dangerousFlag {
		return g.fail(g.dangerousRefusal(spec))
	}
	p, e := g.prepareSingle(spec, true)
	if e != nil {
		return g.fail(e)
	}
	if p.replay != nil {
		return g.succeed(p.replay, p.untrusted)
	}
	return g.issue(p)
}

// lookupKey is the idempotency lookup of a dry-run or bare call (P4): it runs
// before Preview, so an earlier request with this --idempotency-key is answered
// even when a Preview would refuse the duplicate. It returns the operation to
// replay, or the mismatch, resolved or unresolved conflict, as at confirm.
func (g *gateRun) lookupKey(j *journal, state, target string) (*record, *Error) {
	ident, e := g.identity()
	if e != nil {
		return nil, e
	}
	key, e := g.a.loadKey(state, true)
	if e != nil {
		return nil, e
	}
	b, e := g.a.bindInvocation(g.st, g.cmd, g.m, key)
	if e != nil {
		return nil, e
	}
	return g.idempotent(j, b, "", target, ident, true)
}

// prepareSingle runs the plan steps of a single write (CLI-051): scope, the
// idempotency lookup when a key is given (lookup; P4), unresolved operations, the
// read-only preview, the binding, the planned record and the token.
func (g *gateRun) prepareSingle(spec WriteSpec, lookup bool) (*issued, *Error) {
	state, e := g.a.ensureStateDir()
	if e != nil {
		return nil, e
	}
	j, e := g.a.openJournal(state, true)
	if e != nil {
		return nil, e
	}
	sc := g.a.loadScope(state)
	need := g.need([]string{spec.Target.Key}, nil, nil, 1)
	if e := g.a.checkScope(sc, need, state); e != nil {
		return nil, g.withPreview(e, spec, sc, need, state)
	}
	if lookup && g.idem != "" {
		prev, e := g.lookupKey(j, state, claimString(spec.Target.Key))
		if e != nil {
			return nil, e
		}
		if prev != nil {
			data, untrusted, _, _ := g.replayOf(j, prev)
			return &issued{replay: data, untrusted: untrusted}, nil
		}
	}
	if e := g.checkUnresolved(j, []string{spec.Target.Key}, nil); e != nil {
		return nil, e
	}
	pv, e := g.callPreview(spec.Preview)
	if e != nil {
		return nil, e
	}
	if e := g.checkDeclared(pv); e != nil {
		return nil, e
	}
	// A plan Preview made dangerous needs --dangerous like a dangerous command (Q13).
	g.planDangerous = pv.Dangerous
	if pv.Dangerous && !g.m.dangerous() && !g.dangerousFlag {
		return nil, g.planDangerousRefusal(spec, pv)
	}
	if e := g.createOnlyExisting(pv.Outputs); e != nil {
		return nil, e
	}
	need.destinations, need.outputs, need.dangerous = pv.Destinations, pv.Outputs, g.dangerous()
	if e := g.a.checkScope(sc, need, state); e != nil {
		return nil, g.withPreview(e, spec, sc, need, state)
	}
	ident, e := g.identity()
	if e != nil {
		return nil, e
	}
	key, e := g.a.loadKey(state, true)
	if e != nil {
		return nil, e
	}
	b, e := g.a.bindInvocation(g.st, g.cmd, g.m, key)
	if e != nil {
		return nil, e
	}
	if _, e := g.idempotent(j, b, "", claimString(spec.Target.Key), ident, false); e != nil {
		return nil, e
	}
	id, err := newOperationID(false)
	if err != nil {
		return nil, ioError("No random operation id could be generated; nothing was sent.", "", err)
	}
	now := g.a.now()
	exp := now.Add(tokenLifetime)
	canApply, blocks := canApplyOf(pv)
	pobj := g.previewObject(id, spec.Target, pv, b.display, canApply, blocks)
	cl := g.claims(id, b, ident, claimString(spec.Target.Key), sc.digest, claimString(pv.BaseVersion), exp)
	rec := &record{V: 2, OperationID: id, Kind: "single", Command: g.path, Status: "planned", Target: targetPtr(spec.Target),
		Effects: g.m.effectStrings(), Dangerous: g.dangerous(), ScopeID: sc.id(), CreatedAt: timeString(now),
		ExpiresAt: timeString(exp), Claims: &cl, Preview: rawJSON(pobj)}
	if e := j.write(rec); e != nil {
		return nil, e
	}
	j.prune(now)
	return mint(pobj, id, key, cl, canApply, exp, pv.Untrusted)
}

// loadRecord finds the record a token names; a missing record is token_mismatch, an
// unreadable or corrupt one E_IO.
func (g *gateRun) loadRecord(j *journal, part, kind string) (*record, *Error) {
	prefix := "op_"
	if kind == "batch" {
		prefix = "opb_"
	}
	r, exists, err := j.read(prefix + part)
	if !exists {
		return nil, g.tokenMismatch("--confirm", "No open operation matches this confirm token.")
	}
	if err != nil {
		return nil, ioError("The operation record for this token cannot be read; nothing was sent.",
			fmt.Sprintf("Run `%s operation show --id %s%s` and ask the user to check the state directory.", g.tool(), prefix, part), err)
	}
	if r.Kind != kind {
		return nil, g.tokenMismatch("--confirm", "The confirm token belongs to a different kind of operation.")
	}
	return r, nil
}

// verifyStoredToken runs confirm steps 1-6: format, key, record, MAC, single use,
// expiry. Single use comes before expiry (A1): a used token answers token_used
// whatever its age, and never comes back as token_expired with a fresh plan that
// would send the write a second time.
func (g *gateRun) verifyStoredToken(kind string) (string, []byte, *journal, *record, *Error) {
	part, macHex, ok := parseToken(g.confirm)
	if !ok {
		e := g.tokenMismatch("--confirm", "The confirm token is not in the form this tool issues.")
		// Only the exact form is accepted (CLI-055); say what to drop when the token
		// came wrapped or upper-cased, as from a copied `--confirm <token>`.
		bare := strings.Trim(strings.TrimSpace(g.confirm), "<>\"' ")
		if _, _, fits := parseToken(strings.ToLower(bare)); fits {
			e.Details["next_action"] = "Pass the confirm_token exactly as the preview issued it: lower case, without angle brackets, quotes or spaces around it."
		}
		return "", nil, nil, nil, e
	}
	state, e := g.a.ensureStateDir()
	if e != nil {
		return "", nil, nil, nil, e
	}
	key, e := g.a.loadKey(state, false)
	if e != nil {
		return "", nil, nil, nil, e
	}
	if key == nil {
		return "", nil, nil, nil, g.tokenMismatch("--confirm", "No confirm key exists, so this token cannot be from this tool.")
	}
	j, e := g.a.openJournal(state, false)
	if e != nil {
		return "", nil, nil, nil, e
	}
	rec, e := g.loadRecord(j, part, kind)
	if e != nil {
		return "", nil, nil, nil, e
	}
	if rec.Claims == nil || rec.Claims.Op != rec.OperationID || !verifyMAC(key, *rec.Claims, macHex) {
		return "", nil, nil, nil, g.tokenMismatch("--confirm", "The confirm token does not match its operation.")
	}
	used, e := j.isConsumed(rec.OperationID)
	if e != nil {
		return "", nil, nil, nil, e
	}
	if used || rec.Status != "planned" {
		return "", nil, nil, nil, g.tokenUsed(rec.OperationID)
	}
	if exp, ok := parseTime(rec.Claims.Exp); !ok || !g.a.now().Before(exp) {
		return "", nil, nil, nil, conflictError("token_expired", "The confirm token has expired; nothing was sent.",
			"Run the same command again with --dry-run to get a new token.")
	}
	return state, key, j, rec, nil
}

// lockTargets takes the per-target confirm locks (CLI-057 step 9, A3): one file per
// target, <state>/locks/<first 32 hex characters of SHA-256(target key)>.lock, taken
// in sorted order (a batch takes one per item) and held from the confirm checks
// until the dispatching record is written, so two confirms on one target never
// interleave. A lock still held after 10 s is lock_held. owner, the operation id,
// is written into each file so a waiting confirm can name it.
func (g *gateRun) lockTargets(state string, keys []string, owner string) (func(), *Error) {
	dir := filepath.Join(state, "locks")
	if e := ensurePrivateDir(dir); e != nil {
		return nil, e
	}
	byPath := map[string]string{}
	for _, k := range keys {
		byPath[filepath.Join(dir, sha256Hex([]byte(k))[:32]+".lock")] = k
	}
	var held []func()
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i]()
		}
		held = nil
	}
	deadline := time.Now().Add(targetLockWait)
	for _, path := range sortedKeys(byPath) {
		unlock, holder, err := acquireLock(path, owner, max(time.Until(deadline), 0))
		if err != nil {
			release()
			if errors.Is(err, errLockBusy) {
				return nil, g.lockHeld(byPath[path], holder)
			}
			return nil, ioError("The confirm lock cannot be taken; nothing was sent.", fmt.Sprintf("Check that %s is writable.", dir), err)
		}
		held = append(held, unlock)
	}
	return release, nil
}

// lockHeld is the conflict of a confirm that waited 10 s for another confirm on the
// same target (A3).
func (g *gateRun) lockHeld(target, holder string) *Error {
	e := conflictError("lock_held", "Another confirm on the same target is still running; nothing was sent.",
		"Run the same command again in a few seconds; the confirm token stays valid until it expires.")
	e.Details["target"] = target
	if validOperationID(holder) {
		e.Details["active_operation"] = holder
	}
	return e
}

// consumeCounted is confirm step 13: under the budget lock it creates the single-use
// marker of operation id and adds n operations to its scope's count in
// scope-usage.json (A4). A marker that exists is token_used; a count that would pass
// limits.max_operations is the budget_spent refusal, and then no marker is created.
// n is 0 for a replay, which sends nothing.
func (g *gateRun) consumeCounted(j *journal, state string, sc scopeState, need scopeNeed, id string, n int, now time.Time) *Error {
	unlock, e := lockScopeUsage(state, id)
	if e != nil {
		return e
	}
	defer unlock()
	used, e := j.isConsumed(id)
	if e != nil {
		return e
	}
	if used {
		return g.tokenUsed(id)
	}
	if scopeID := sc.id(); scopeID != nil && n > 0 {
		usage, e := readScopeUsage(state)
		if e != nil {
			return e
		}
		count := usage.Scopes[*scopeID]
		if limit := sc.limit(); limit > 0 && count+n > limit {
			need.operations = n
			return g.a.scopeRefusal(sc, "budget_spent", need, map[string]any{}, count)
		}
		usage.Scopes[*scopeID] = count + n
		if e := writeScopeUsage(state, usage); e != nil {
			return e
		}
	}
	created, e := j.consume(id, now)
	if e != nil {
		return e
	}
	if !created {
		return g.tokenUsed(id)
	}
	return nil
}

// recheckUsed is the single-use check again, under the target lock (CLI-057 step 9):
// the loser of a race on one token gets token_used, never a drift with a fresh plan.
func (g *gateRun) recheckUsed(j *journal, id string) *Error {
	used, e := j.isConsumed(id)
	if e != nil {
		return e
	}
	if used {
		return g.tokenUsed(id)
	}
	return nil
}

// idempotencyResolved refuses a request whose idempotency key belongs to an
// operation resolved by hand as applied or partial, or that ended partial (A5).
func (g *gateRun) idempotencyResolved(id string) *Error {
	e := conflictError("idempotency_resolved", "A request with this idempotency key was already resolved by hand; nothing was sent.",
		fmt.Sprintf("Run `%s operation show --id %s` to see what was recorded; if the change is missing, repeat the request with a new --idempotency-key.",
			g.tool(), id))
	e.Details["operation_id"] = id
	return e
}

// resolvedDuringApply is the report of a write whose record was resolved by hand
// while Apply still ran (A6): what Apply returned no longer decides anything.
func (g *gateRun) resolvedDuringApply(id string) *Error {
	return NewError("E_OUTCOME_UNKNOWN", "The operation was resolved by hand while it was still running; its outcome is unknown.",
		map[string]any{"operation_id": id, "lookup": fmt.Sprintf("%s operation show --id %s", g.tool(), id), "dispatch_state": "unknown",
			"next_action": fmt.Sprintf("Run `%s operation show --id %s` and re-read the target before anything else; do not resend.", g.tool(), id)})
}

// compareInputs runs confirm step 7: the command, the tool version and every bound
// input must match the claims. It returns the first differing parameter.
func (g *gateRun) compareInputs(c *claims, b *binding) string {
	if c.Tool != claimString(g.tool()) || c.Cmd != claimString(g.path) {
		return "command"
	}
	if c.ToolVersion != claimString(g.a.version()) {
		return "tool_version"
	}
	names := map[string]bool{}
	for n := range c.Inputs {
		names[n] = true
	}
	for n := range b.digests {
		names[n] = true
	}
	for _, n := range sortedKeys(names) {
		if c.Inputs[n] != b.digests[n] {
			if l, ok := b.labels[n]; ok {
				return l
			}
			return g.labelOf(n)
		}
	}
	if c.Idem != g.idem {
		return "--idempotency-key"
	}
	if c.Inv != b.inv {
		return "--confirm"
	}
	return ""
}

// inputMismatch is token_mismatch for a bound input that differs from the token's,
// with a hint when one side came from a file and the other was given literally:
// both bind the exact bytes, so a file's trailing newline is a difference.
func (g *gateRun) inputMismatch(rec *record, b *binding, label string) *Error {
	e := g.tokenMismatch(label, fmt.Sprintf("The confirm token was issued for a different %s.", label))
	inputs, _ := decodeGeneric(rec.Preview)["inputs"].(map[string]any)
	for _, file := range sortedKeys(g.m.FileInputs) {
		bound := g.m.FileInputs[file]
		if label != "--"+bound && label != "--"+file {
			continue
		}
		then, now := inputs[snake(bound)], b.display[snake(bound)]
		if then == nil || now == nil || isFileShown(then) == isFileShown(now) {
			break
		}
		if isFileShown(then) {
			n, _ := intOf(then.(map[string]any)["bytes"])
			e.Details["next_action"] = fmt.Sprintf("The token binds the exact bytes of the file given to --%s (%d bytes, including any trailing newline), "+
				"which differ from the literal --%s. Confirm with the same --%s, or run --dry-run again with the literal value.", file, n, bound, file)
		} else {
			e.Details["next_action"] = fmt.Sprintf("The token binds the literal --%s, which differs from the file's exact bytes (including any trailing newline). "+
				"Confirm with --%s as before, or run --dry-run again with --%s.", bound, bound, file)
		}
		break
	}
	return e
}

// isFileShown reports whether a preview input shows file content ({sha256, bytes}).
func isFileShown(v any) bool {
	m, ok := v.(map[string]any)
	return ok && hasKey(m, "sha256") && hasKey(m, "bytes")
}

// labelOf names a bound input that is absent from the current invocation.
func (g *gateRun) labelOf(name string) string {
	if name == "args" || name == "profile" || strings.HasPrefix(name, "setting.") {
		return name
	}
	if lookupFlag(g.cmd, name) != nil {
		return "--" + name
	}
	for _, file := range sortedKeys(g.m.FileInputs) {
		if g.m.FileInputs[file] == name {
			return labelFor(g.cmd, name, file)
		}
	}
	return "--" + name
}

// checkDrift re-reads the target through Preview at confirm (CLI-057): a changed base
// version or a changed plan is state_drift.
func (g *gateRun) checkDrift(base string, stored map[string]any, pv Preview, t Target, id string) *Error {
	drift := func(why string) *Error {
		e := conflictError("state_drift", "The target changed since the preview ("+why+"); nothing was sent.",
			"Run the same command again with --dry-run, check the new preview, then confirm its token.")
		e.Details["target"] = t.Key
		return e
	}
	if claimString(pv.BaseVersion) != base {
		return drift("its version differs")
	}
	can, blocks := canApplyOf(pv)
	if !can {
		e := drift("it can no longer be applied")
		e.Details["blocked_by"] = blockObjects(blocks)
		return e
	}
	fresh := g.previewObject(id, t, pv, map[string]any{}, true, nil)
	for _, k := range []string{"action", "changes", "destinations", "outputs"} {
		if !reflect.DeepEqual(stored[k], fresh[k]) {
			return drift("the planned " + k + " differ")
		}
	}
	// A plan whose danger changed is another plan (Q13).
	if stored["dangerous"] != fresh["dangerous"] {
		if fresh["dangerous"] == true {
			return drift("it is now dangerous")
		}
		return drift("it is no longer dangerous")
	}
	return nil
}

func (g *gateRun) confirmSingle(spec WriteSpec) error {
	state, key, j, rec, e := g.verifyStoredToken("single")
	if e != nil {
		if e.Details["conflict"] == "token_expired" {
			e = g.withFreshPlan(e, spec)
		}
		return g.fail(e)
	}
	b, e := g.a.bindInvocation(g.st, g.cmd, g.m, key)
	if e != nil {
		return g.fail(e)
	}
	if p := g.compareInputs(rec.Claims, b); p != "" {
		return g.fail(g.inputMismatch(rec, b, p))
	}
	stored := decodeGeneric(rec.Preview)
	g.planDangerous = stored["dangerous"] == true // a plan Preview made dangerous (Q13)
	if g.dangerous() && !g.dangerousFlag {
		return g.fail(g.dangerousRequired())
	}
	// The tool's target resolution runs only now, after every token check (Q1).
	spec, e = g.resolveTarget(spec)
	if e != nil {
		return g.fail(e)
	}
	// Steps 9 to 14 run under the target's lock (A3).
	unlock, e := g.lockTargets(state, []string{spec.Target.Key}, rec.OperationID)
	if e != nil {
		return g.fail(e)
	}
	defer func() { unlock() }()
	if e := g.recheckUsed(j, rec.OperationID); e != nil {
		return g.fail(e)
	}
	if e := g.createOnlyExisting(stringsAt(stored, "outputs")); e != nil {
		return g.fail(e)
	}
	sc := g.a.loadScope(state)
	need := g.need([]string{spec.Target.Key}, stringsAt(stored, "destinations"), stringsAt(stored, "outputs"), 0)
	if e := g.a.checkScope(sc, need, ""); e != nil {
		return g.fail(e)
	}
	if sc.digest != rec.Claims.Scope {
		return g.fail(conflictError("scope_changed", "The scope file changed since the preview; nothing was sent.",
			"Run the same command again with --dry-run to get a token under the current scope."))
	}
	need.operations = 1
	if e := g.a.checkScope(sc, need, state); e != nil {
		return g.fail(e)
	}
	if e := g.checkUnresolved(j, []string{spec.Target.Key}, map[string]bool{rec.OperationID: true}); e != nil {
		return g.fail(e)
	}
	pv, e := g.callPreview(spec.Preview)
	if e != nil {
		return g.fail(e)
	}
	if e := g.checkDrift(rec.Claims.Base, stored, pv, spec.Target, rec.OperationID); e != nil {
		if !hasKey(e.Details, "blocked_by") {
			e = g.withFreshPlan(e, spec)
		}
		return g.fail(e)
	}
	ident, e := g.identity()
	if e != nil {
		return g.fail(e)
	}
	switch {
	case claimString(ident.Account) != rec.Claims.Account:
		return g.fail(g.tokenMismatch("account", "The confirm token was issued for a different account."))
	case claimString(ident.Host) != rec.Claims.Host:
		return g.fail(g.tokenMismatch("host", "The confirm token was issued for a different host."))
	case claimString(spec.Target.Key) != rec.Claims.Target:
		return g.fail(g.tokenMismatch("target", "The confirm token was issued for a different target."))
	}
	prev, e := g.idempotent(j, b, rec.OperationID, claimString(spec.Target.Key), ident, true)
	if e != nil {
		return g.fail(e)
	}
	now := g.a.now()
	if prev != nil {
		return g.replay(j, state, sc, rec, prev, now)
	}
	if e := g.consumeCounted(j, state, sc, need, rec.OperationID, 1, now); e != nil {
		return g.fail(e)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	rec.Status = "dispatching"
	rec.DispatchedAt = strPtr(timeString(now))
	rec.LeaseUntil = strPtr(timeString(now.Add(timeout + leaseGrace)))
	if e := j.write(rec); e != nil {
		return g.fail(e)
	}
	g.dispatched = rec.OperationID
	unlock() // the dispatching record now blocks the target (step 15)
	unlock = func() {}
	res, applyErr := g.callApply(spec.Apply, Op{ID: rec.OperationID, IdempotencyKey: g.idem}, timeout)
	out := g.classify(rec.OperationID, spec.Target, stored, res, applyErr)
	if resolved := g.finishRecord(j, rec, out); resolved {
		return g.fail(g.resolvedDuringApply(rec.OperationID))
	}
	return g.report(rec.OperationID, out, res, spec)
}

// idempotent is the idempotency lookup (CLI-062, CLI-057 step 12, A5) over every
// earlier operation of any command, single write or batch, confirmed with this
// --idempotency-key. They match only when the command path, inputs digest, target,
// account and host are all equal; any difference is idempotency_mismatch naming the
// latest one that differs, so a reused key never answers with another request's
// receipt. At confirm (full) their states then decide, the one that never sends a
// write twice first: one still unknown → unresolved_operation; one resolved by hand
// as applied or partial, or ended partial → idempotency_resolved; one applied → it
// is returned, to replay; otherwise (not applied, failed, resolved as not applied)
// nil, and the request may be sent again. target is the claim value (a batch's is
// the digest of its target list).
func (g *gateRun) idempotent(j *journal, b *binding, self, target string, ident Identity, full bool) (*record, *Error) {
	if g.idem == "" {
		return nil, nil
	}
	found, e := j.findIdempotent(g.idem, self)
	if e != nil || len(found) == 0 {
		return nil, e
	}
	for _, prev := range found {
		c := prev.Claims
		if prev.Command != g.path || c.Cmd != claimString(g.path) || c.Inv != b.inv || c.Target != target ||
			c.Account != claimString(ident.Account) || c.Host != claimString(ident.Host) {
			ce := conflictError("idempotency_mismatch", "This idempotency key was already used for a different request; nothing was sent.",
				"Use a new --idempotency-key for a different request, or repeat the original request exactly.")
			ce.Details["operation_id"] = prev.OperationID
			return nil, ce
		}
	}
	if !full {
		return nil, nil
	}
	now := g.a.now()
	for _, prev := range found {
		if prev.Resolution == nil && unresolved(effectiveStatus(prev, now)) {
			return nil, g.unresolvedConflict(prev.OperationID)
		}
	}
	var replayable *record
	for _, prev := range found {
		st := effectiveStatus(prev, now)
		switch {
		case prev.Resolution != nil && prev.Resolution.Outcome != "not_applied", prev.Resolution == nil && st == "partial":
			// Part or all of it took effect: sending it again could apply that twice.
			return nil, g.idempotencyResolved(prev.OperationID)
		case prev.Resolution == nil && st == "applied" && (prev.Kind == "batch" || rawPresent(prev.Receipt)):
			if replayable == nil {
				replayable = prev
			}
		case prev.Resolution == nil && st == "applied":
			return nil, g.idempotencyResolved(prev.OperationID) // applied, with nothing to replay
		}
	}
	return replayable, nil
}

// origin follows replay_of from an applied record to the original operation (a
// replay of a replay answers with the original). It returns the original's id and
// the deepest record that can still be read: the original, or the replay whose
// copies stand in for it once the original was pruned.
func origin(j *journal, r *record) (string, *record) {
	id := r.OperationID
	for hops := 0; hops < 8 && r.ReplayOf != nil; hops++ {
		id = *r.ReplayOf
		o, exists, err := j.read(id)
		if !exists || err != nil {
			break
		}
		r = o
	}
	return id, r
}

// replayOf is the answer to a repeated idempotent request (CLI-062): the original
// operation's id, its receipt with replayed: true and its result (projected by
// --fields, P12). It also returns the original's id and the record the answer
// came from, which a confirm records its replay against.
func (g *gateRun) replayOf(j *journal, prev *record) (map[string]any, []string, string, *record) {
	origID, src := origin(j, prev)
	receipt := decodeGeneric(src.Receipt)
	receipt["replayed"] = true
	untrusted := []string{}
	if obs, ok := receipt["observed"].(map[string]any); ok && len(obs) > 0 {
		untrusted = append(untrusted, "data.receipt.observed")
	}
	// The original result (the created resource's ref and url, CLI-059) comes back
	// with the replay; its paths of external text are not recorded, so all of it is
	// declared untrusted.
	var result any = map[string]any{}
	if rawPresent(src.Result) {
		if r, err := decodeAny(src.Result); err == nil && r != nil {
			result = g.projectResult(r)
			untrusted = append(untrusted, "data.result")
		}
	}
	return map[string]any{"operation_id": origID, "receipt": receipt, "result": result}, untrusted, origID, src
}

// replay answers a repeated idempotent request at confirm with the original receipt
// and result (CLI-062) and records the replay; nothing is sent and nothing is counted
// against the budget.
func (g *gateRun) replay(j *journal, state string, sc scopeState, rec, prev *record, now time.Time) error {
	data, untrusted, origID, src := g.replayOf(j, prev)
	if e := g.consumeCounted(j, state, sc, scopeNeed{}, rec.OperationID, 0, now); e != nil {
		return g.fail(e)
	}
	rec.Status = "applied"
	rec.FinishedAt = strPtr(timeString(now))
	rec.ReplayOf = strPtr(origID)
	rec.Receipt = rawJSON(data["receipt"])
	rec.Result = src.Result
	if e := j.write(rec); e != nil {
		g.a.Notice(journalNotice())
	}
	return g.succeed(data, untrusted)
}

// projectResult projects a confirmed write's result with the command's output schema
// as App.OK does for a read: its default fields (or Meta.DefaultFields) without
// --fields (Q16), what --fields replaces or adds with it (P12). The receipt is never
// projected.
func (g *gateRun) projectResult(result any) any {
	gen, err := toGeneric(result)
	if err != nil || gen == nil {
		return result
	}
	schema := g.a.schemaOf(g.m)
	shape := schema.Shape
	if shape == "" {
		shape = "object"
		if _, isList := gen.([]any); isList {
			shape = "array"
		}
	}
	return g.a.project(g.st, g.m, schema, shape, gen)
}

// decodeAny decodes a stored JSON value with numbers kept exact.
func decodeAny(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

func journalNotice() Notice {
	return Notice{Type: "journal_write_failed", Severity: "warning",
		Message: "The operation record could not be updated; `operation show` may report this operation as unknown."}
}

// writeOutcome is a classified Apply result.
type writeOutcome struct {
	receipt    map[string]any
	ok         bool
	code       string
	cause      *Error
	dispatch   string
	status     string
	outcome    Outcome
	observed   map[string]any
	resultData any
	// unencodable: Apply's Result.Data cannot be encoded as JSON, so resultData is {}
	// and the response carries a result_unencodable notice (A2).
	unencodable bool
}

// unencodableResult reports a dispatched write whose result could not be encoded
// (A2): a result_unencodable notice for the agent and a defect for conformance. The
// receipt stands; operation show has it.
func (g *gateRun) unencodableResult(id string) {
	g.a.Notice(Notice{Type: "result_unencodable", Severity: "warning",
		Message: fmt.Sprintf("The change was dispatched, but the tool's result could not be encoded, so data.result is empty; run `%s operation show --id %s` for the receipt.",
			g.tool(), id)})
	g.a.defect("Apply returned a Result.Data that cannot be encoded as JSON; the gate reported data.result as {} (CLI-057).")
}

// gateCodes are codes the gate derives itself; they never come from a cause.
var gateCodes = []string{"E_OUTCOME_UNKNOWN", "E_PARTIAL", "E_VERIFY_FAILED", "E_VERIFY_INCOMPLETE", "E_CONFIRMATION_REQUIRED"}

func causeOf(err error) *Error {
	var e *Error
	if errors.As(err, &e) && e != nil && !contains(gateCodes, e.Code) {
		return copyError(e)
	}
	return nil
}

// classify turns Apply's result into a receipt and the derived ok/code (CLI-059).
func (g *gateRun) classify(id string, t Target, stored map[string]any, res Result, applyErr error) writeOutcome {
	m := g.m
	changes := m.hasEffect(Change, Configure)
	delivers := m.hasEffect(Deliver)
	persists := m.hasEffect(Artifact, Persist)
	var o Outcome
	var cause *Error
	dispatch := "dispatched"
	var nd *notDispatchedError
	var rj *rejectedError
	observed := map[string]any{}
	mismatched := append([]string{}, res.Mismatched...)
	switch {
	case applyErr == nil:
		o = normalizeOutcome(res.Outcome, m)
		if res.Observed != nil {
			if gv, err := toGeneric(res.Observed); err == nil {
				if mm, ok := gv.(map[string]any); ok {
					observed = mm
				}
			}
		}
		if res.Outcome.Verification == "" {
			changesList, _ := stored["changes"].([]any)
			v, mm := autoVerify(changesList, observed)
			o.Verification = v
			for _, f := range mm {
				if !contains(mismatched, f) {
					mismatched = append(mismatched, f)
				}
			}
		}
		// A mismatched field is one whose read-back is in observed (CLI-059).
		kept := mismatched[:0]
		for _, f := range mismatched {
			if _, ok := observed[strings.SplitN(f, ".", 2)[0]]; ok && !contains(kept, f) {
				kept = append(kept, f)
			}
		}
		dropped := len(kept) < len(mismatched)
		mismatched = kept
		if dropped {
			g.a.defect("Apply named mismatched fields that are not in Result.Observed (CLI-059); the gate dropped them.")
		}
		switch {
		case len(mismatched) > 0 && (o.Verification == "passed" || o.Verification == "not_run"):
			o.Verification = "failed"
		case len(observed) == 0 && o.Verification == "passed":
			// Verification that passed or failed shows what it observed (decision 15);
			// without a read-back the gate cannot report it as checked.
			o.Verification = "not_run"
			g.a.defect("Apply reported verification passed with an empty Result.Observed (decision 15); the receipt says not_run.")
		case o.Verification == "failed" && len(mismatched) == 0:
			// A failure that names no observed field could not be completed.
			o.Verification = "incomplete"
			g.a.defect("Apply reported verification failed without a mismatched field in Result.Observed; the receipt says incomplete.")
		case dropped && o.Verification == "passed":
			o.Verification = "incomplete"
		}
	case errors.As(applyErr, &nd):
		o = Outcome{Execution: "not_started", Change: pick(changes, "not_applied", "not_applicable"),
			Delivery: pick(delivers, "not_attempted", "not_applicable"), Persistence: pick(persists, "unchanged", "not_applicable"),
			Verification: "not_applicable"}
		cause = causeOf(nd.err)
		dispatch = "not_started"
	case errors.As(applyErr, &rj):
		o = Outcome{Execution: "failed", Change: pick(changes, "not_applied", "not_applicable"),
			Delivery: pick(delivers, "rejected", "not_applicable"), Persistence: pick(persists, "unchanged", "not_applicable"),
			Verification: "not_applicable", FailedStage: "apply"}
		cause = causeOf(rj.err)
	default:
		o = Outcome{Execution: "unknown", Change: pick(changes, "unknown", "not_applicable"),
			Delivery: pick(delivers, "unknown", "not_applicable"), Persistence: pick(persists, "unknown", "not_applicable"),
			Verification: "not_run"}
		dispatch = "unknown"
	}
	o = sanitizeOutcome(o)
	causeCode := ""
	if cause != nil {
		causeCode = cause.Code
		if _, ok := g.a.lookupCode(causeCode); !ok {
			causeCode = ""
		}
	}
	ok, code := deriveReceipt(o, causeCode)
	if code == "E_OUTCOME_UNKNOWN" {
		dispatch = "unknown"
	}
	receipt := map[string]any{
		"operation_id": id, "command": g.path, "target": targetObject(t), "effects": m.effectStrings(),
		"outcome": map[string]any{"execution": o.Execution, "change": o.Change, "delivery": o.Delivery,
			"persistence": o.Persistence, "verification": o.Verification},
		"observed": observed, "mismatched": nonNil(mismatched), "replayed": false,
	}
	if o.FailedStage != "" {
		receipt["failed_stage"] = o.FailedStage
	}
	var data any = map[string]any{}
	unencodable := false
	if applyErr == nil && res.Data != nil {
		if _, err := json.Marshal(res.Data); err != nil {
			unencodable = true
		} else {
			data = res.Data
		}
	}
	return writeOutcome{receipt: receipt, ok: ok, code: code, cause: cause, dispatch: dispatch,
		status: recordStatus(ok, code, o), outcome: o, observed: observed, resultData: data, unencodable: unencodable}
}

// finishRecord stores the final status and receipt (confirm step 18). A record that
// was resolved meanwhile keeps its resolution, with the late receipt beside it, and
// finishRecord reports true: what Apply returned no longer decides the outcome (A6).
func (g *gateRun) finishRecord(j *journal, rec *record, out writeOutcome) bool {
	unlock, e := j.lock(rec.OperationID)
	if e == nil {
		defer unlock()
	}
	if cur, exists, err := j.read(rec.OperationID); exists && err == nil && cur.Resolution != nil {
		cur.LateReceipt = rawJSON(out.receipt)
		if werr := j.write(cur); werr != nil {
			g.a.Notice(journalNotice())
		}
		return true
	}
	now := timeString(g.a.now())
	rec.Status = out.status
	rec.FinishedAt = &now
	rec.LeaseUntil = nil
	rec.Receipt = rawJSON(out.receipt)
	if out.ok {
		if b := rawJSON(out.resultData); len(b) <= maxStoredResult {
			rec.Result = b
		}
	}
	if !out.ok {
		msg := ""
		if out.cause != nil {
			msg = truncateRunes(out.cause.Message, 200)
		}
		rec.Error = &recordError{Code: out.code, Message: msg}
	}
	if werr := j.write(rec); werr != nil {
		g.a.Notice(journalNotice())
	}
	return false
}

// report emits a confirmed write per the receipt rules (confirm step 19).
func (g *gateRun) report(id string, out writeOutcome, res Result, spec WriteSpec) error {
	lookup := fmt.Sprintf("%s operation show --id %s", g.tool(), id)
	if out.ok {
		untrusted := []string{}
		if out.unencodable {
			g.unencodableResult(id)
		} else {
			untrusted = prefixPaths("data.result", res.Untrusted)
		}
		if len(out.observed) > 0 {
			untrusted = append(untrusted, "data.receipt.observed")
		}
		return g.succeed(map[string]any{"operation_id": id, "receipt": out.receipt, "result": g.projectResult(out.resultData)}, untrusted)
	}
	var e *Error
	switch out.code {
	case "E_OUTCOME_UNKNOWN":
		check := fmt.Sprintf("Read %s upstream", spec.Target.Key)
		if rb := strings.TrimSpace(spec.ReadBack); rb != "" {
			check = fmt.Sprintf("Run `%s %s`", g.tool(), strings.ReplaceAll(rb, "{target}", spec.Target.Key))
		}
		e = NewError(out.code, "The write may have been applied; nothing will be retried. Look the operation up before doing anything else.",
			map[string]any{"operation_id": id, "lookup": lookup, "receipt": out.receipt, "dispatch_state": "unknown",
				"next_action": fmt.Sprintf("Do not resend. %s to see whether the change landed, then record what you found with `%s operation resolve --id %s --outcome applied|not_applied|partial --evidence <text>`.",
					check, g.tool(), id)})
	case "E_PARTIAL":
		e = NewError(out.code, "Part of the write took effect; details.receipt shows which parts.",
			map[string]any{"operation_id": id, "receipt": out.receipt, "dispatch_state": "dispatched",
				"next_action": fmt.Sprintf("Read details.receipt; never resend the whole request. `%s` shows the record.", lookup)})
	case "E_VERIFY_FAILED":
		e = NewError(out.code, "The write was applied, but the read-back disagrees with the request (details.receipt.mismatched).",
			map[string]any{"operation_id": id, "receipt": out.receipt, "dispatch_state": "dispatched",
				"next_action": "Re-read the target and tell the user which fields differ; do not resend blindly."})
	case "E_VERIFY_INCOMPLETE":
		e = NewError(out.code, "The write was applied, but the read-back could not be completed.",
			map[string]any{"operation_id": id, "receipt": out.receipt, "dispatch_state": "dispatched",
				"next_action": "Re-read the target to check the change before relying on it."})
	default:
		if out.cause != nil && out.cause.Code == out.code {
			e = copyError(out.cause)
		} else {
			e = NewError(out.code, "The write was not applied.", nil)
		}
		// The receipt moves to error.details.receipt when ok is false (CLI-059); a
		// dispatched write's error always says what happened to it (decision 1).
		e.Details["operation_id"] = id
		e.Details["receipt"] = out.receipt
		e.Details["dispatch_state"] = out.dispatch
		if len(out.observed) > 0 {
			e.Untrusted = append(e.Untrusted, "receipt.observed")
		}
		if na, _ := e.Details["next_action"].(string); out.dispatch == "not_started" && strings.TrimSpace(na) == "" {
			e.Details["next_action"] = "Nothing was sent. Fix the cause, then run the command again with --dry-run for a new token."
		} else if na == "" {
			e.Details["next_action"] = "The upstream refused the write and nothing changed; fix the request, then run --dry-run again."
		}
		if strings.TrimSpace(e.Message) == "" {
			e.Message = "The write was not applied."
		}
		return g.fail(e)
	}
	if len(out.observed) > 0 {
		e.Untrusted = append(e.Untrusted, "receipt.observed")
	}
	return g.fail(e)
}

// ---------------------------------------------------------------- batch write

// WriteBatch runs a batch write through the gate (CLI-063): one preview listing every
// target with per-item operation ids, one token for the batch, per-item receipts and
// a summary. The batch is ok only when every item succeeded; otherwise
// E_OUTCOME_UNKNOWN if any item is unknown, else E_PARTIAL if any item applied, else
// the first failed item's code. Items are de-duplicated by target key, order kept.
func (a *App) WriteBatch(cmd *cobra.Command, spec BatchSpec) (err error) {
	g, err := a.startGate(cmd, true)
	if g == nil {
		return err
	}
	defer g.recoverAfterDispatch(&err)
	if spec.ResolveItems != nil {
		// The items are resolved at the dry-run before the scope check, and at
		// confirm after the token checks (Q1).
		if g.confirmSet {
			return g.confirmBatch(spec, nil)
		}
		items, e := g.batchItems(spec)
		if e != nil {
			return g.fail(e)
		}
		return g.planBatch(spec, items)
	}
	items, e := g.batchItems(spec)
	if e != nil {
		return g.fail(e)
	}
	if g.confirmSet {
		return g.confirmBatch(spec, items)
	}
	return g.planBatch(spec, items)
}

// batchItems returns the batch's items, from BatchSpec.ResolveItems when it is set
// (Q1), checked and de-duplicated by target key with their order kept.
func (g *gateRun) batchItems(spec BatchSpec) ([]BatchItem, *Error) {
	list := spec.Items
	if spec.ResolveItems != nil {
		resolved, e := callTool(g.a, spec.ResolveItems, "The batch targets could not be resolved; nothing was sent.")
		if e != nil {
			return nil, e
		}
		list = resolved
	}
	var items []BatchItem
	seen := map[string]bool{}
	for _, it := range list {
		if it.Preview == nil || (it.Apply == nil && spec.ApplyAll == nil) || strings.TrimSpace(it.Target.Key) == "" {
			return nil, internalError("Every BatchItem needs Target.Key and Preview, and Apply unless BatchSpec.ApplyAll is set.")
		}
		if e := checkTargetKey(it.Target.Key); e != nil {
			return nil, e
		}
		if seen[it.Target.Key] {
			continue
		}
		seen[it.Target.Key] = true
		items = append(items, it)
	}
	if len(items) == 0 {
		e := Usage("missing_parameter", "--"+g.m.BatchFlag, "The batch names no targets; nothing was changed.")
		e.Details["next_action"] = fmt.Sprintf("Give at least one target with --%s.", g.m.BatchFlag)
		return nil, e
	}
	return items, nil
}

// itemPlan is one previewed batch item.
type itemPlan struct {
	item   BatchItem
	id     string
	pv     Preview
	can    bool
	blocks []Block
	pobj   map[string]any
}

func batchKeys(items []BatchItem) []string {
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.Target.Key
	}
	return keys
}

// digestList binds an ordered list of strings as one claim value.
func digestList(list []string) string {
	enc, _ := canonicalJSON(claimStrings(list))
	return "sha256:" + sha256Hex(enc)
}

// previewItems previews every item. An item the upstream says cannot be changed (not
// found, invalid, forbidden, conflict, ambiguous) is blocked; any other failure fails
// the batch.
func (g *gateRun) previewItems(items []BatchItem) ([]itemPlan, *Error) {
	plans := make([]itemPlan, 0, len(items))
	for _, it := range items {
		pv, e := g.callPreview(it.Preview)
		p := itemPlan{item: it}
		if e != nil {
			switch e.Code {
			case "E_NOT_FOUND", "E_VALIDATION", "E_FORBIDDEN", "E_CONFLICT", "E_AMBIGUOUS":
				p.can = false
				p.blocks = []Block{{Kind: strings.ToLower(strings.TrimPrefix(e.Code, "E_")), Message: e.Message}}
			default:
				return nil, e
			}
		} else {
			if de := g.checkDeclared(pv); de != nil {
				return nil, de
			}
			p.pv = pv
			p.can, p.blocks = canApplyOf(pv)
		}
		plans = append(plans, p)
	}
	return plans, nil
}

func (g *gateRun) planBatch(spec BatchSpec, items []BatchItem) error {
	if g.m.dangerous() && !g.dangerousFlag {
		return g.fail(g.dangerousRequired())
	}
	state, e := g.a.ensureStateDir()
	if e != nil {
		return g.fail(e)
	}
	j, e := g.a.openJournal(state, true)
	if e != nil {
		return g.fail(e)
	}
	keys := batchKeys(items)
	sc := g.a.loadScope(state)
	need := g.need(keys, nil, nil, len(items))
	if e := g.a.checkScope(sc, need, state); e != nil {
		return g.fail(e)
	}
	// With --idempotency-key, an earlier batch with this key is answered before any
	// Preview runs (P4).
	if g.idem != "" {
		prev, e := g.lookupKey(j, state, digestList(keys))
		if e != nil {
			return g.fail(e)
		}
		if prev != nil {
			data, untrusted, _, _, e := g.batchReplayOf(j, prev)
			if e != nil {
				return g.fail(e)
			}
			return g.succeed(data, untrusted)
		}
	}
	if e := g.checkUnresolved(j, keys, nil); e != nil {
		return g.fail(e)
	}
	plans, e := g.previewItems(items)
	if e != nil {
		return g.fail(e)
	}
	for _, p := range plans {
		need.destinations = append(need.destinations, p.pv.Destinations...)
		need.outputs = append(need.outputs, p.pv.Outputs...)
		g.planDangerous = g.planDangerous || p.pv.Dangerous // one dangerous item plan makes the batch dangerous (Q13)
	}
	if g.planDangerous && !g.m.dangerous() && !g.dangerousFlag {
		return g.fail(g.dangerousRequired())
	}
	if e := g.createOnlyExisting(need.outputs); e != nil {
		return g.fail(e)
	}
	need.dangerous = g.dangerous()
	if e := g.a.checkScope(sc, need, state); e != nil {
		return g.fail(e)
	}
	ident, e := g.identity()
	if e != nil {
		return g.fail(e)
	}
	key, e := g.a.loadKey(state, true)
	if e != nil {
		return g.fail(e)
	}
	b, e := g.a.bindInvocation(g.st, g.cmd, g.m, key)
	if e != nil {
		return g.fail(e)
	}
	if _, e := g.idempotent(j, b, "", digestList(keys), ident, false); e != nil {
		return g.fail(e)
	}
	batchID, err := newOperationID(true)
	if err != nil {
		return g.fail(ioError("No random operation id could be generated; nothing was sent.", "", err))
	}
	now := g.a.now()
	exp := now.Add(tokenLifetime)
	canApply := true
	var aggregate []Block
	var targets, previewItems []any
	var bases, untrusted, itemIDs []string
	for i := range plans {
		p := &plans[i]
		id, err := newOperationID(false)
		if err != nil {
			return g.fail(ioError("No random operation id could be generated; nothing was sent.", "", err))
		}
		p.id = id
		itemIDs = append(itemIDs, id)
		p.pobj = g.previewObject(id, p.item.Target, p.pv, map[string]any{}, p.can, p.blocks)
		if !p.can {
			canApply = false
			for _, bl := range p.blocks {
				aggregate = append(aggregate, Block{Kind: bl.Kind, Message: p.item.Target.Key + ": " + bl.Message})
			}
		}
		targets = append(targets, targetObject(p.item.Target))
		previewItems = append(previewItems, map[string]any{"target": targetObject(p.item.Target), "operation_id": id,
			"changes": p.pobj["changes"], "can_apply": p.can, "blocked_by": p.pobj["blocked_by"]})
		bases = append(bases, claimString(p.pv.BaseVersion))
		untrusted = append(untrusted, prefixPaths("items[]", p.pv.Untrusted)...)
	}
	action := strings.TrimSpace(spec.Action) // the batch's own action (Q19)
	if action == "" {
		action = g.action(plans[0].pv)
		if len(plans) > 1 {
			action = fmt.Sprintf("%s (%d targets)", action, len(plans))
		}
	}
	pobj := map[string]any{
		"operation_id": batchID, "command": g.path, "action": action, "effects": g.m.effectStrings(),
		"dangerous": g.dangerous(), "inputs": b.display, "can_apply": canApply, "blocked_by": blockObjects(aggregate),
		"targets": targets, "total": len(plans), "items": previewItems,
	}
	if len(need.destinations) > 0 {
		pobj["destinations"] = need.destinations
	}
	if len(need.outputs) > 0 {
		pobj["outputs"] = need.outputs
	}
	if gen, gerr := toGeneric(pobj); gerr == nil {
		if mm, ok := gen.(map[string]any); ok {
			pobj = mm
		}
	}
	cl := g.claims(batchID, b, ident, digestList(keys), sc.digest, digestList(bases), exp)
	for _, p := range plans {
		ir := &record{V: 2, OperationID: p.id, Kind: "item", BatchID: strPtr(batchID), Command: g.path, Status: "planned",
			Target: targetPtr(p.item.Target), Effects: g.m.effectStrings(), Dangerous: g.m.dangerous() || p.pv.Dangerous, ScopeID: sc.id(),
			CreatedAt: timeString(now), ExpiresAt: timeString(exp), Preview: rawJSON(p.pobj)}
		if e := j.write(ir); e != nil {
			return g.fail(e)
		}
	}
	rec := &record{V: 2, OperationID: batchID, Kind: "batch", Command: g.path, Status: "planned", Effects: g.m.effectStrings(),
		Dangerous: g.dangerous(), ScopeID: sc.id(), CreatedAt: timeString(now), ExpiresAt: timeString(exp), Claims: &cl,
		Preview: rawJSON(pobj), Items: itemIDs}
	if e := j.write(rec); e != nil {
		return g.fail(e)
	}
	j.prune(now)
	p, me := mint(pobj, batchID, key, cl, canApply, exp, untrusted)
	if me != nil {
		return g.fail(me)
	}
	return g.issue(p)
}

func (g *gateRun) confirmBatch(spec BatchSpec, items []BatchItem) error {
	state, key, j, rec, e := g.verifyStoredToken("batch")
	if e != nil {
		return g.fail(e)
	}
	b, e := g.a.bindInvocation(g.st, g.cmd, g.m, key)
	if e != nil {
		return g.fail(e)
	}
	if p := g.compareInputs(rec.Claims, b); p != "" {
		return g.fail(g.inputMismatch(rec, b, p))
	}
	stored := decodeGeneric(rec.Preview)
	g.planDangerous = stored["dangerous"] == true // an item plan Preview made dangerous (Q13)
	if g.dangerous() && !g.dangerousFlag {
		return g.fail(g.dangerousRequired())
	}
	// The planned item records, in order.
	itemRecs := make([]*record, 0, len(rec.Items))
	for _, id := range rec.Items {
		ir, exists, err := j.read(id)
		if !exists || err != nil || ir.Kind != "item" || ir.BatchID == nil || *ir.BatchID != rec.OperationID {
			return g.fail(ioError("A planned item of this batch cannot be read; nothing was sent.",
				fmt.Sprintf("Run `%s operation show --id %s`, then run the command again with --dry-run.", g.tool(), rec.OperationID), err))
		}
		if ir.Status != "planned" {
			return g.fail(g.tokenUsed(rec.OperationID))
		}
		itemRecs = append(itemRecs, ir)
	}
	if items == nil {
		// The tool's item resolution runs only now, after every token check (Q1).
		resolved, e := g.batchItems(spec)
		if e != nil {
			return g.fail(e)
		}
		items = resolved
	}
	keys := batchKeys(items)
	// Steps 9 to 14 run under every item target's lock (A3).
	unlock, e := g.lockTargets(state, keys, rec.OperationID)
	if e != nil {
		return g.fail(e)
	}
	defer func() { unlock() }()
	if e := g.recheckUsed(j, rec.OperationID); e != nil {
		return g.fail(e)
	}
	if e := g.createOnlyExisting(stringsAt(stored, "outputs")); e != nil {
		return g.fail(e)
	}
	sc := g.a.loadScope(state)
	need := g.need(keys, stringsAt(stored, "destinations"), stringsAt(stored, "outputs"), 0)
	if e := g.a.checkScope(sc, need, ""); e != nil {
		return g.fail(e)
	}
	if sc.digest != rec.Claims.Scope {
		return g.fail(conflictError("scope_changed", "The scope file changed since the preview; nothing was sent.",
			"Run the same command again with --dry-run to get a token under the current scope."))
	}
	need.operations = len(items)
	if e := g.a.checkScope(sc, need, state); e != nil {
		return g.fail(e)
	}
	exclude := map[string]bool{rec.OperationID: true}
	for _, id := range rec.Items {
		exclude[id] = true
	}
	if e := g.checkUnresolved(j, keys, exclude); e != nil {
		return g.fail(e)
	}
	if digestList(keys) != rec.Claims.Target || len(items) != len(itemRecs) {
		return g.fail(g.tokenMismatch("target", "The confirm token was issued for a different set of targets."))
	}
	var bases []string
	for i, it := range items {
		if it.Target.Key != itemRecs[i].Target.Key {
			return g.fail(g.tokenMismatch("target", "The confirm token was issued for a different set of targets."))
		}
		pv, e := g.callPreview(it.Preview)
		if e != nil {
			return g.fail(e)
		}
		bases = append(bases, claimString(pv.BaseVersion))
		itemStored := decodeGeneric(itemRecs[i].Preview)
		base := ""
		if bm, ok := itemStored["base"].(map[string]any); ok {
			base, _ = bm["version"].(string)
		}
		if e := g.checkDrift(claimString(base), itemStored, pv, it.Target, itemRecs[i].OperationID); e != nil {
			return g.fail(e)
		}
	}
	if digestList(bases) != rec.Claims.Base {
		return g.fail(conflictError("state_drift", "A target changed since the preview; nothing was sent.",
			"Run the same command again with --dry-run, check the new preview, then confirm its token."))
	}
	ident, e := g.identity()
	if e != nil {
		return g.fail(e)
	}
	if claimString(ident.Account) != rec.Claims.Account {
		return g.fail(g.tokenMismatch("account", "The confirm token was issued for a different account."))
	}
	if claimString(ident.Host) != rec.Claims.Host {
		return g.fail(g.tokenMismatch("host", "The confirm token was issued for a different host."))
	}
	// The batch's own key is looked up like a single write's (A5).
	prev, e := g.idempotent(j, b, rec.OperationID, digestList(keys), ident, true)
	if e != nil {
		return g.fail(e)
	}
	now := g.a.now()
	if prev != nil {
		return g.replayBatch(j, state, sc, rec, itemRecs, prev, now)
	}
	if e := g.consumeCounted(j, state, sc, need, rec.OperationID, len(items), now); e != nil {
		return g.fail(e)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	chunk := 1 // one Apply call per item, or one ApplyAll call per chunk (Q12)
	if spec.ApplyAll != nil {
		chunk = len(items)
		if spec.MaxPerCall > 0 && spec.MaxPerCall < chunk {
			chunk = spec.MaxPerCall
		}
	}
	calls := (len(items) + chunk - 1) / chunk
	rec.Status = "dispatching"
	rec.DispatchedAt = strPtr(timeString(now))
	rec.LeaseUntil = strPtr(timeString(now.Add(timeout*time.Duration(calls) + leaseGrace)))
	if e := j.write(rec); e != nil {
		return g.fail(e)
	}
	g.dispatched = rec.OperationID
	unlock() // the dispatching batch record now blocks every item's target (step 15)
	unlock = func() {}
	continueOnError := !g.m.dangerous()
	if f := g.cmd.Flags().Lookup("continue-on-error"); f != nil {
		continueOnError, _ = g.cmd.Flags().GetBool("continue-on-error")
	}
	if spec.ContinueOnError != nil {
		continueOnError = *spec.ContinueOnError
	}
	return g.runBatch(j, state, sc, rec, spec, items, itemRecs, chunk, timeout, continueOnError)
}

// batchReplayOf is the answer to a repeated batch (A5): the original batch's id, its
// per-item receipts (replayed: true) and results (projected by --fields, P12), and
// a summary. It also returns the record the answer came from and, per item, the
// receipt and result a confirm records its replay with. Items that cannot be read
// make the replay impossible: E_IO, and nothing is sent.
func (g *gateRun) batchReplayOf(j *journal, prev *record) (map[string]any, []string, *record, [][2]json.RawMessage, *Error) {
	origID, src := origin(j, prev)
	var entries []any
	var copies [][2]json.RawMessage
	observedAny, resultAny := false, false
	for _, id := range src.Items {
		it, exists, err := j.read(id)
		if !exists || err != nil || it.Target == nil || !rawPresent(it.Receipt) {
			return nil, nil, nil, nil, ioError("The earlier batch with this idempotency key cannot be read, so its result cannot be repeated; nothing was sent.",
				fmt.Sprintf("Run `%s operation show --id %s` and ask the user to check the state directory.", g.tool(), origID), err)
		}
		receipt := decodeGeneric(it.Receipt)
		receipt["replayed"] = true
		entry := map[string]any{"target": it.Target.Key, "operation_id": it.OperationID, "ok": true, "receipt": receipt}
		if obs, ok := receipt["observed"].(map[string]any); ok && len(obs) > 0 {
			observedAny = true
		}
		if rawPresent(it.Result) {
			if r, err := decodeAny(it.Result); err == nil && r != nil {
				entry["result"] = g.projectResult(r)
				resultAny = true
			}
		}
		entries = append(entries, entry)
		copies = append(copies, [2]json.RawMessage{rawJSON(receipt), it.Result})
	}
	var untrusted []string
	if observedAny {
		untrusted = append(untrusted, "data.items[].receipt.observed")
	}
	if resultAny {
		untrusted = append(untrusted, "data.items[].result")
	}
	summary := map[string]any{"total": len(entries), "succeeded": len(entries), "failed": 0, "unknown": 0, "skipped": 0}
	return map[string]any{"operation_id": origID, "items": entries, "summary": summary}, untrusted, src, copies, nil
}

// replayBatch answers a repeated batch at confirm (A5): nothing is sent, nothing is
// counted, and the new batch and its items are recorded as replays of the originals.
func (g *gateRun) replayBatch(j *journal, state string, sc scopeState, rec *record, itemRecs []*record, prev *record, now time.Time) error {
	data, untrusted, src, copies, e := g.batchReplayOf(j, prev)
	if e != nil {
		return g.fail(e)
	}
	if e := g.consumeCounted(j, state, sc, scopeNeed{}, rec.OperationID, 0, now); e != nil {
		return g.fail(e)
	}
	for i, ir := range itemRecs {
		if i >= len(src.Items) {
			break
		}
		ir.Status, ir.FinishedAt, ir.ReplayOf = "applied", strPtr(timeString(now)), strPtr(src.Items[i])
		ir.Receipt, ir.Result = copies[i][0], copies[i][1]
		if e := j.write(ir); e != nil {
			g.a.Notice(journalNotice())
		}
	}
	rec.Status, rec.FinishedAt, rec.ReplayOf = "applied", strPtr(timeString(now)), strPtr(data["operation_id"].(string))
	if e := j.write(rec); e != nil {
		g.a.Notice(journalNotice())
	}
	return g.succeed(data, untrusted)
}

// runBatch applies the items in order and reports the batch (CLI-063): one Apply
// call per item, or one BatchSpec.ApplyAll call per chunk of chunk items (Q12), each
// outcome mapped onto its item. Items that were never dispatched (skipped after a
// stop) give their budget back (A4).
func (g *gateRun) runBatch(j *journal, state string, sc scopeState, rec *record, spec BatchSpec, items []BatchItem, itemRecs []*record,
	chunk int, timeout time.Duration, continueOnError bool) error {
	var results []any
	var firstFail *writeOutcome
	succeeded, failed, unknownN, skipped, undispatched := 0, 0, 0, 0, 0
	anyApplied, anyDispatched, stop, observedAny := false, false, false, false
	var resultUntrusted []string
	defer func() {
		if id := sc.id(); id != nil {
			refundScopeUsage(state, *id, undispatched)
		}
	}()
	// notSent records item i as never started: skipped after a stop, or in a chunk
	// whose records could not all be written.
	notSent := func(i int) map[string]any {
		it, ir := items[i], itemRecs[i]
		o := sanitizeOutcome(Outcome{Execution: "not_started",
			Change:       pick(g.m.hasEffect(Change, Configure), "not_applied", "not_applicable"),
			Delivery:     pick(g.m.hasEffect(Deliver), "not_attempted", "not_applicable"),
			Persistence:  pick(g.m.hasEffect(Artifact, Persist), "unchanged", "not_applicable"),
			Verification: "not_applicable"})
		receipt := g.skippedReceipt(ir.OperationID, it.Target, o)
		ir.Status = "not_applied"
		ir.FinishedAt = strPtr(timeString(g.a.now()))
		ir.LeaseUntil = nil
		ir.Receipt = rawJSON(receipt)
		if e := j.write(ir); e != nil {
			g.a.Notice(journalNotice())
		}
		undispatched++
		return map[string]any{"target": it.Target.Key, "operation_id": ir.OperationID, "ok": false, "receipt": receipt}
	}
	// report records the outcome of dispatched item i.
	report := func(i int, res Result, applyErr error) bool {
		it, ir := items[i], itemRecs[i]
		out := g.classify(ir.OperationID, it.Target, decodeGeneric(ir.Preview), res, applyErr)
		if resolved := g.finishRecord(j, ir, out); resolved {
			// Resolved by hand while Apply still ran: the item's outcome is unknown (A6).
			out.ok, out.code, out.cause, out.dispatch = false, "E_OUTCOME_UNKNOWN", nil, "unknown"
		}
		if out.dispatch != "not_started" {
			anyDispatched = true
		}
		entry := map[string]any{"target": it.Target.Key, "operation_id": ir.OperationID, "ok": out.ok, "receipt": out.receipt}
		if len(out.observed) > 0 {
			observedAny = true
		}
		o := out.outcome
		if o.Change == "applied" || o.Delivery == "delivered" || o.Persistence == "saved" || o.Change == "partial" ||
			o.Delivery == "partial" || o.Persistence == "partial" {
			anyApplied = true
		}
		switch {
		case out.ok:
			succeeded++
			switch {
			case out.unencodable:
				entry["result"] = out.resultData
				g.unencodableResult(ir.OperationID)
			case res.Data != nil:
				entry["result"] = g.projectResult(out.resultData)
				resultUntrusted = append(resultUntrusted, prefixPaths("data.items[].result", res.Untrusted)...)
			}
		case out.code == "E_OUTCOME_UNKNOWN":
			unknownN++
			entry["error"] = g.itemError(out)
		default:
			failed++
			entry["error"] = g.itemError(out)
			if firstFail == nil {
				fo := out
				firstFail = &fo
			}
		}
		results = append(results, entry)
		return out.ok
	}
	for start := 0; start < len(items); start += chunk {
		end := min(start+chunk, len(items))
		if stop {
			for i := start; i < end; i++ {
				results = append(results, notSent(i))
				skipped++
			}
			continue
		}
		// The chunk's records turn dispatching before its call (CLI-057 step 14).
		now := g.a.now()
		ops := make([]Op, 0, end-start)
		var recErr *Error
		failedAt := -1
		for i := start; i < end; i++ {
			ir := itemRecs[i]
			ir.Status = "dispatching"
			ir.DispatchedAt = strPtr(timeString(now))
			ir.LeaseUntil = strPtr(timeString(now.Add(timeout + leaseGrace)))
			if e := j.write(ir); e != nil {
				recErr, failedAt = e, i
				break
			}
			ops = append(ops, Op{ID: ir.OperationID, IdempotencyKey: itemKey(g.idem, items[i].Target.Key)})
		}
		if recErr != nil {
			// Nothing of this chunk was sent: the item whose record could not be
			// written reports the journal error, and the others were not started.
			out := writeOutcome{ok: false, code: recErr.Code, cause: recErr, dispatch: "not_started"}
			for i := start; i < end; i++ {
				if i == failedAt {
					results = append(results, map[string]any{"target": items[i].Target.Key, "operation_id": itemRecs[i].OperationID,
						"ok": false, "error": g.itemError(out)})
					failed++
					undispatched++
					continue
				}
				results = append(results, notSent(i))
				skipped++
			}
			if firstFail == nil {
				firstFail = &out
			}
			stop = true
			continue
		}
		var outcomes []ItemOutcome
		if spec.ApplyAll == nil {
			res, applyErr := g.callApply(items[start].Apply, ops[0], timeout)
			outcomes = []ItemOutcome{{Result: res, Err: applyErr}}
		} else {
			outcomes = g.callApplyAll(spec.ApplyAll, ops, timeout)
		}
		for k := range outcomes {
			if !report(start+k, outcomes[k].Result, outcomes[k].Err) && !continueOnError {
				stop = true
			}
		}
	}
	summary := map[string]any{"total": len(items), "succeeded": succeeded, "failed": failed, "unknown": unknownN, "skipped": skipped}
	status := "applied"
	switch {
	case unknownN > 0:
		status = "unknown"
	case failed+skipped > 0 && anyApplied:
		status = "partial"
	case failed+skipped > 0:
		status = "not_applied"
	}
	if unlock, e := j.lock(rec.OperationID); e == nil {
		rec.Status = status
		rec.FinishedAt = strPtr(timeString(g.a.now()))
		rec.LeaseUntil = nil
		if werr := j.write(rec); werr != nil {
			g.a.Notice(journalNotice())
		}
		unlock()
	} else {
		g.a.Notice(journalNotice())
	}
	untrusted := resultUntrusted
	if observedAny {
		untrusted = append(untrusted, "data.items[].receipt.observed")
	}
	if status == "applied" {
		return g.succeed(map[string]any{"operation_id": rec.OperationID, "items": results, "summary": summary}, untrusted)
	}
	lookup := fmt.Sprintf("%s operation show --id %s", g.tool(), rec.OperationID)
	var e *Error
	switch {
	case unknownN > 0:
		e = NewError("E_OUTCOME_UNKNOWN", "Some items of the batch may have been applied; nothing will be retried. Look the operation up first.",
			map[string]any{"lookup": lookup, "dispatch_state": "unknown",
				"next_action": fmt.Sprintf("Run `%s` and check the items marked unknown before any retry; resolve each with `%s operation resolve`.", lookup, g.tool())})
	case anyApplied:
		e = NewError("E_PARTIAL", "Part of the batch took effect; details.items shows each item.",
			map[string]any{"dispatch_state": "dispatched",
				"next_action": "Read details.items; retry only the failed items, never the whole batch."})
	default:
		code := "E_SERVER"
		if firstFail != nil {
			code = firstFail.code
		}
		if firstFail != nil && firstFail.cause != nil && firstFail.cause.Code == code {
			e = copyError(firstFail.cause)
			e.Message = "No item of the batch was applied: " + strings.TrimSuffix(e.Message, ".") + "."
		} else {
			e = NewError(code, "No item of the batch was applied.", nil)
		}
		e.Details["dispatch_state"] = pick(anyDispatched, "dispatched", "not_started")
		e.Details["next_action"] = "Read details.items, fix the cause, then run the command again with --dry-run."
	}
	e.Details["operation_id"] = rec.OperationID
	e.Details["items"] = results
	e.Details["summary"] = summary
	if observedAny {
		e.Untrusted = append(e.Untrusted, "items[].receipt.observed")
	}
	return g.fail(e)
}

// itemKey is the upstream idempotency key of one batch item (CLI-062, A5): the
// user's key, which stays bound into the batch token, plus the first 16 hex
// characters of the SHA-256 of the item's target key. It is the same on every
// dry-run of the same batch, so an upstream that honours keys recognises a repeated
// batch, and it differs per target, so it never folds item 2..n into item 1. No
// user key means no item key.
func itemKey(userKey, targetKey string) string {
	if userKey == "" {
		return ""
	}
	return userKey + "/" + sha256Hex([]byte(targetKey))[:16]
}

func (g *gateRun) skippedReceipt(id string, t Target, o Outcome) map[string]any {
	return map[string]any{
		"operation_id": id, "command": g.path, "target": targetObject(t), "effects": g.m.effectStrings(),
		"outcome": map[string]any{"execution": o.Execution, "change": o.Change, "delivery": o.Delivery,
			"persistence": o.Persistence, "verification": o.Verification},
		"observed": map[string]any{}, "mismatched": []string{}, "replayed": false,
	}
}

// itemError is the compact per-item error of a batch result.
func (g *gateRun) itemError(out writeOutcome) map[string]any {
	spec, _ := g.a.lookupCode(out.code)
	msg := spec.Meaning
	if out.cause != nil && out.cause.Code == out.code && strings.TrimSpace(out.cause.Message) != "" {
		msg = out.cause.Message
	}
	return map[string]any{"code": out.code, "retryable": spec.Retryable, "message": truncateRunes(msg, 160)}
}
