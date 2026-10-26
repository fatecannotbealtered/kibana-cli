// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// Notice is a typed warning attached to meta.notices (CLI-047, CLI-071). Update
// notices (type update_available) also carry the version fields.
type Notice struct {
	Type           string `json:"type"`
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	CurrentVersion string `json:"current_version,omitempty"`
	LatestVersion  string `json:"latest_version,omitempty"`
	Command        string `json:"command,omitempty"`
	CheckedAt      string `json:"checked_at,omitempty"`
}

type metaObject struct {
	DurationMS int64    `json:"duration_ms"`
	Notices    []Notice `json:"notices,omitempty"`
	Untrusted  []string `json:"untrusted,omitempty"`
}

type successEnvelope struct {
	OK            bool       `json:"ok"`
	SchemaVersion string     `json:"schema_version"`
	Data          any        `json:"data"`
	Meta          metaObject `json:"meta"`
}

type errorObject struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

type errorEnvelope struct {
	OK            bool        `json:"ok"`
	SchemaVersion string      `json:"schema_version"`
	Error         errorObject `json:"error"`
	Meta          metaObject  `json:"meta"`
}

// result is what a run reports: success data or an error, plus untrusted paths.
type result struct {
	set       bool
	data      any // generic JSON value (maps, slices, json.Number, ...)
	raw       any // the value as the command passed it, for --format raw
	untrusted []string
	err       *Error
	fromGate  bool // the write gate's own data (previews, receipts, batch results)
}

// runState is the per-Execute state: output mode, the executing command and what
// it reported. Execute creates it; App methods called from RunE read it.
type runState struct {
	start     time.Time // App.Now at start (timestamps)
	startReal time.Time // wall clock for duration_ms
	ctx       context.Context
	args      []string
	cmd       *cobra.Command
	format    string
	pretty    bool
	quiet     bool
	fields    fieldSelection
	notices   []Notice

	out        result // from OK / List
	gate       result // from the write gate: authoritative once set
	cmdErr     error
	panicErr   *Error
	helpShown  bool
	writeCalls int

	positionalConsumed bool
	files              map[string]*fileInput
	stdinFlag          string
	stream             *Stream       // an NDJSON stream, once the command starts one
	profile            *profileState // the active profile, once resolved (settings.go)
	renamedUsed        []string      // old names of renamed flags the arguments used (Q10)
	renamedBoth        [2]string     // a renamed flag given with its new name too: {old, new}
}

// fieldSelection is the parsed --fields value (CLI-040).
type fieldSelection struct {
	set   bool
	all   bool
	add   bool
	names []string
}

func (st *runState) setOut(data, raw any, untrusted []string) {
	st.out = result{set: true, data: data, raw: raw, untrusted: untrusted}
}

// runFor returns the active run state for cmd, or a programming error when an App
// output method is called outside the command that Execute is running.
func (a *App) runFor(cmd *cobra.Command) (*runState, *Error) {
	st := a.run
	if st == nil {
		return nil, internalError("App output methods must be called from a command run by App.Execute.")
	}
	if cmd == nil || (st.cmd != nil && cmd != st.cmd) {
		return nil, internalError("App output methods must receive the running command.")
	}
	if st.cmd == nil {
		st.cmd = cmd
	}
	return st, nil
}

// Notice adds a notice to the current response's meta.notices. It is safe to call
// from several goroutines (Q22).
func (a *App) Notice(n Notice) {
	st := a.run
	if st == nil {
		return
	}
	if strings.TrimSpace(n.Type) == "" {
		n.Type = "notice"
	}
	if !contains(noticeSeverityValues, n.Severity) {
		n.Severity = "info"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, have := range st.notices {
		if have == n {
			return
		}
	}
	st.notices = append(st.notices, n)
}

// OK reports success with data (CLI-002). untrusted lists paths inside data that carry
// external content, relative to data (for example "description" or "items[].title");
// the command's output schema adds its untrusted fields automatically. --fields and
// default fields apply when the command's schema declares them. Write commands must
// report through Write or WriteBatch instead.
func (a *App) OK(cmd *cobra.Command, data any, untrusted ...string) error {
	st, perr := a.runFor(cmd)
	if perr != nil {
		return perr
	}
	m := a.metaFor(cmd)
	if m != nil && m.isWrite() {
		return internalError("A write command reported success without the write gate; it must use App.Write or App.WriteBatch.")
	}
	gen, err := toGenericData(data)
	if err != nil {
		return internalError("The command's output cannot be encoded as JSON.")
	}
	schema := a.schemaOf(m)
	shape := schema.Shape
	if shape == "" {
		if _, isList := gen.([]any); isList {
			shape = "array"
		} else {
			shape = "object"
		}
	}
	gen = a.project(st, m, schema, shape, gen)
	paths := prefixPaths("data", untrusted)
	paths = append(paths, schemaUntrusted(schema, shape)...)
	st.setOut(gen, data, paths)
	return nil
}

// toGeneric converts a Go value into its generic JSON form (maps, slices,
// json.Number, string, bool, nil) so it can be projected and inspected. JSON null
// (a nil value, nil slice or nil map) stays nil.
func toGeneric(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// toGenericData is toGeneric for response data, which is never null.
func toGenericData(v any) (any, error) {
	g, err := toGeneric(v)
	if err == nil && g == nil {
		g = map[string]any{}
	}
	return g, err
}

// encodeJSON writes v as one JSON document with a trailing newline, without HTML
// escaping; compact unless pretty.
func encodeJSON(v any, pretty bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// prefixPaths roots relative paths at prefix ("data" -> "data.items[].title").
func prefixPaths(prefix string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
			continue
		case p == prefix || strings.HasPrefix(p, prefix+".") || strings.HasPrefix(p, prefix+"[]"):
			out = append(out, p)
		case strings.HasPrefix(p, "[]"):
			out = append(out, prefix+p)
		default:
			out = append(out, prefix+"."+p)
		}
	}
	return out
}

// schemaUntrusted maps a schema's untrusted fields to envelope paths for its shape.
func schemaUntrusted(s SchemaDef, shape string) []string {
	var out []string
	for _, f := range s.Untrusted {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		switch shape {
		case "page":
			out = append(out, "data.items[]."+f)
		case "array":
			out = append(out, "data[]."+f)
		default:
			out = append(out, "data."+f)
		}
	}
	return out
}

// presentPaths keeps the untrusted paths that name at least one value in the
// response (Q20): the path of a field the projection removed, or of the items of an
// empty list, names none and is dropped. root holds the response's data
// ({"data": ...}) or error ({"error": {"details": ...}}) in generic JSON form.
func presentPaths(root map[string]any, paths []string) []string {
	var out []string
	for _, p := range paths {
		if pathPresent(root, strings.TrimSpace(p)) {
			out = append(out, p)
		}
	}
	return out
}

// pathPresent walks one untrusted path ("data.items[].title"): each segment names a
// key, and each [] steps into a list.
func pathPresent(root map[string]any, path string) bool {
	nodes := []any{root}
	for _, seg := range strings.Split(path, ".") {
		name := strings.TrimRight(seg, "[]")
		arrays := (len(seg) - len(name)) / 2
		var next []any
		for _, n := range nodes {
			obj, ok := n.(map[string]any)
			if !ok {
				continue
			}
			v, has := obj[name]
			if !has {
				continue
			}
			vals := []any{v}
			for k := 0; k < arrays; k++ {
				var items []any
				for _, x := range vals {
					if list, ok := x.([]any); ok {
						items = append(items, list...)
					}
				}
				vals = items
			}
			next = append(next, vals...)
		}
		if len(next) == 0 {
			return false
		}
		nodes = next
	}
	return true
}

// errorRoot is an error's details as the root presentPaths walks.
func errorRoot(obj errorObject) map[string]any {
	details, err := toGeneric(obj.Details)
	if err != nil {
		details = map[string]any{}
	}
	return map[string]any{"error": map[string]any{"details": details}}
}

// cleanPaths de-duplicates and sorts untrusted paths.
func cleanPaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// project applies --fields and default fields to a command's output (CLI-040).
// Pagination keys are never removed and "ref" is always kept.
func (a *App) project(st *runState, m *Meta, schema SchemaDef, shape string, v any) any {
	defaults := schema.DefaultFields
	if m != nil && len(m.DefaultFields) > 0 {
		defaults = m.DefaultFields
	}
	var keep map[string]bool
	sel := st.fields
	switch {
	case sel.all:
	case sel.set && !sel.add:
		keep = toSet(sel.names)
	case sel.set && sel.add:
		if len(defaults) > 0 {
			keep = toSet(append(append([]string{}, defaults...), sel.names...))
		}
	default:
		if len(defaults) > 0 {
			keep = toSet(defaults)
		}
	}
	if keep == nil {
		return v
	}
	keep["ref"] = true
	switch shape {
	case "page":
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		if items, ok := obj["items"].([]any); ok {
			for i, it := range items {
				items[i] = keepKeys(it, keep)
			}
		}
		return obj
	case "array":
		if items, ok := v.([]any); ok {
			for i, it := range items {
				items[i] = keepKeys(it, keep)
			}
		}
		return v
	default:
		return keepKeys(v, keep)
	}
}

func keepKeys(v any, keep map[string]bool) any {
	obj, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(keep))
	for k, val := range obj {
		if keep[k] {
			out[k] = val
		}
	}
	return out
}

func toSet(list []string) map[string]bool {
	s := make(map[string]bool, len(list))
	for _, x := range list {
		s[x] = true
	}
	return s
}

// finish turns the run's outcome into exactly one response and returns the exit code.
func (a *App) finish(st *runState) int {
	var res result
	switch {
	case st.gate.set:
		res = st.gate
	case st.panicErr != nil:
		res = result{set: true, err: st.panicErr}
	case st.cmdErr != nil:
		res = result{set: true, err: asError(st.cmdErr)}
	case st.out.set:
		res = st.out
	case st.helpShown:
		return 0
	default:
		res = result{set: true, data: map[string]any{}, raw: nil}
	}
	// update reports the versions itself; the notice would describe the binary it
	// may just have replaced.
	if st.cmd == nil || st.cmd != a.updateCmd {
		a.attachUpdateNotice(st)
	}
	var exit int
	if st.stream != nil {
		exit = a.emitStream(st, res)
	} else {
		exit = a.emit(st, res)
	}
	a.recordCoverage(st, exit)
	return exit
}

// emit writes one response in the run's output format and returns the exit code.
func (a *App) emit(st *runState, res result) int {
	stdout, stderr := a.stdout(), a.stderr()
	meta := metaObject{DurationMS: time.Since(st.startReal).Milliseconds(), Notices: st.notices}
	if res.err == nil {
		meta.Untrusted = cleanPaths(presentPaths(map[string]any{"data": res.data}, res.untrusted))
		switch st.format {
		case "text":
			writeText(stdout, res.data)
			a.textNotices(st, stderr)
			return 0
		case "raw":
			switch v := res.raw.(type) {
			case []byte:
				_, _ = stdout.Write(v)
				return 0
			case string:
				_, _ = io.WriteString(stdout, v)
				return 0
			}
			b, err := encodeJSON(res.data, false)
			if err == nil {
				_, _ = stdout.Write(b)
				return 0
			}
			res = result{set: true, err: internalError("The command's output cannot be encoded.")}
		default:
			data := res.data
			if res.fromGate {
				data = fitSuccessBudget(data, meta)
				meta.Untrusted = cleanPaths(presentPaths(map[string]any{"data": data}, meta.Untrusted))
			}
			env := successEnvelope{OK: true, SchemaVersion: SchemaVersion, Data: data, Meta: meta}
			b, err := encodeJSON(env, st.pretty)
			if err == nil {
				_, _ = stdout.Write(b)
				return 0
			}
			res = result{set: true, err: internalError("The command's output cannot be encoded as JSON.")}
		}
	}
	obj, exit, errUntrusted := a.normalizeError(res.err)
	meta.Untrusted = cleanPaths(presentPaths(errorRoot(obj), append(errUntrusted, res.untrusted...)))
	obj = fitErrorBudget(obj, meta, a.toolName())
	meta.Untrusted = cleanPaths(presentPaths(errorRoot(obj), meta.Untrusted))
	if st.format == "text" {
		writeTextError(stderr, obj)
		a.textNotices(st, stderr)
		return exit
	}
	env := errorEnvelope{OK: false, SchemaVersion: SchemaVersion, Error: obj, Meta: meta}
	b, err := encodeJSON(env, st.pretty)
	if err != nil {
		// Keep what says whether a write went out and where to look it up (CLI-011).
		details := map[string]any{"dispatch_state": obj.Details["dispatch_state"], "next_action": "Report this bug in the tool."}
		for _, k := range []string{"operation_id", "lookup"} {
			if s, ok := obj.Details[k].(string); ok {
				details[k] = s
			}
		}
		code, message := "E_UNKNOWN", "The error could not be encoded as JSON."
		if details["dispatch_state"] != "not_started" && hasKey(details, "operation_id") && hasKey(details, "lookup") {
			// A dispatched write's error is never "nothing was sent" (A2).
			code, message = "E_OUTCOME_UNKNOWN", "The write's error could not be encoded as JSON, so its outcome is unknown."
			details["next_action"] = "Run the command in details.lookup and re-read the target before anything else; do not resend."
		}
		obj = errorObject{Code: code, Message: message, Retryable: false, Details: details}
		env = errorEnvelope{OK: false, SchemaVersion: SchemaVersion, Error: obj, Meta: metaObject{DurationMS: meta.DurationMS}}
		b, _ = encodeJSON(env, false)
		exit = coreCodes[code].exit
	}
	_, _ = stdout.Write(b)
	return exit
}

func (a *App) textNotices(st *runState, w io.Writer) {
	if st.quiet {
		return
	}
	for _, n := range st.notices {
		_, _ = fmt.Fprintf(w, "notice (%s): %s\n", n.Severity, n.Message)
	}
}

// errorSize is the size of the compact error envelope in characters.
func errorSize(obj errorObject, meta metaObject) int {
	env := errorEnvelope{OK: false, SchemaVersion: SchemaVersion, Error: obj, Meta: meta}
	b, err := encodeJSON(env, false)
	if err != nil {
		return 0
	}
	return utf8.RuneCount(bytes.TrimRight(b, "\n"))
}

// successSize is the size of the compact success envelope in characters.
func successSize(data any, meta metaObject) int {
	env := successEnvelope{OK: true, SchemaVersion: SchemaVersion, Data: data, Meta: meta}
	b, err := encodeJSON(env, false)
	if err != nil {
		return 0
	}
	return utf8.RuneCount(bytes.TrimRight(b, "\n"))
}

// fitSuccessBudget keeps the gate's own large results within the success budget
// (CLI-042) by the contract's shortening rules: a batch result leaves out the
// receipt and result of items that succeeded, then lists fewer items (failed ones
// first) with omitted; a dry-run's batch preview keeps its first targets and items,
// one to one, with omitted. Other data is the tool's own and is never cut here; the
// full reference and raw output are exempt from the budget anyway.
func fitSuccessBudget(data any, meta metaObject) any {
	if successSize(data, meta) <= successBudgetChars {
		return data
	}
	obj, ok := data.(map[string]any)
	if !ok {
		return data
	}
	out := cloneMap(obj)
	fits := func() bool { return successSize(out, meta) <= successBudgetChars }
	switch {
	case hasKey(out, "items") && hasKey(out, "summary"):
		out["items"] = compactOKItems(out["items"])
		if fits() {
			return out
		}
		for _, n := range []int{200, 100, 50, 20, 10, 5, 1} {
			items, omitted := trimBatchItems(obj["items"], true, n)
			out["items"] = items
			delete(out, "omitted")
			if omitted > 0 {
				out["omitted"] = omitted
			}
			if fits() {
				return out
			}
		}
	case hasKey(out, "preview") && hasKey(out, "confirm_token"):
		p, ok := out["preview"].(map[string]any)
		if !ok || !hasKey(p, "items") {
			return data
		}
		for _, n := range []int{200, 100, 50, 20, 10, 5, 1} {
			out["preview"] = shortenBatchPreview(p, n)
			if fits() {
				return out
			}
		}
	}
	return out
}

// compactOKItems leaves out the receipt and result of batch items that succeeded
// (the first shortening step of contract.json batch.shortening).
func compactOKItems(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(list))
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok || m["ok"] != true {
			out = append(out, it)
			continue
		}
		c := cloneMap(m)
		delete(c, "receipt")
		delete(c, "result")
		out = append(out, c)
	}
	return out
}

// trimBatchItems lists at most n batch items, the items with ok false first (all of
// them before any item that succeeded), each group in input order, and returns how
// many were left out. Items that succeeded carry no receipt or result once trimmed.
func trimBatchItems(v any, compact bool, n int) ([]any, int) {
	list, _ := v.([]any)
	if compact {
		list, _ = compactOKItems(list).([]any)
	}
	ordered := make([]any, 0, len(list))
	var okItems []any
	for _, it := range list {
		if m, isMap := it.(map[string]any); isMap && m["ok"] == true {
			okItems = append(okItems, it)
		} else {
			ordered = append(ordered, it)
		}
	}
	ordered = append(ordered, okItems...)
	if n < 1 {
		n = 1
	}
	if len(ordered) <= n {
		return ordered, 0
	}
	return append([]any{}, ordered[:n]...), len(ordered) - n
}

// shortenBatchPreview keeps the first n targets and items of a batch preview, one to
// one in input order, and sets omitted to the number left out (contract.json
// preview.shortening); total still counts every target. A single-target preview is
// returned as it is.
func shortenBatchPreview(p map[string]any, n int) map[string]any {
	targets, _ := p["targets"].([]any)
	items, hasItems := p["items"].([]any)
	if !hasItems || n < 1 {
		return p
	}
	if len(targets) <= n && len(items) <= n {
		return p
	}
	out := cloneMap(p)
	keep := min(n, len(targets), len(items))
	if keep < 1 {
		keep = 1
	}
	out["targets"] = append([]any{}, targets[:min(keep, len(targets))]...)
	out["items"] = append([]any{}, items[:min(keep, len(items))]...)
	total := len(targets)
	if t, ok := p["total"].(json.Number); ok {
		if i, err := t.Int64(); err == nil {
			total = int(i)
		}
	} else if t, ok := p["total"].(int); ok {
		total = t
	}
	out["omitted"] = total - min(keep, len(targets))
	return out
}

// fitErrorBudget shrinks an error object until its envelope fits the error budget
// (CLI-042). It cuts optional hints first; then the receipts and results of batch
// items that succeeded; then long strings; then lists by the contract's shortening
// rules (a batch preview one to one with omitted, batch items failed first with
// omitted); and finally keeps only what each code requires. tool names the tool in
// the note that says where the full details are.
func fitErrorBudget(obj errorObject, meta metaObject, tool string) errorObject {
	budget := errorBudgetChars
	if errorSize(obj, meta) <= budget {
		return obj
	}
	d := cloneMap(obj.Details)
	obj.Details = d
	fits := func() bool { return errorSize(obj, meta) <= budget }
	// fitsMarked measures what the caller gets: markShortened appends a note to
	// next_action, so the check includes it.
	fitsMarked := func() bool {
		c := obj
		c.Details = cloneMap(obj.Details)
		return errorSize(markShortened(c, tool), meta) <= budget
	}
	for _, k := range []string{"available", "usage", "expected"} {
		delete(d, k)
	}
	if list, ok := d["did_you_mean"].([]string); ok && len(list) > 3 {
		d["did_you_mean"] = list[:3]
	}
	for _, k := range []string{"upstream_message", "cause"} {
		if s, ok := d[k].(string); ok {
			d[k] = truncateRunes(s, 200)
		}
	}
	if fits() {
		return obj
	}
	if items, ok := d["items"]; ok {
		d["items"] = compactOKItems(items)
		if fitsMarked() {
			return markShortened(obj, tool)
		}
	}
	for _, key := range []string{"preview", "receipt"} {
		if v, ok := d[key]; ok {
			d[key] = shortenStrings(v, 80)
		}
	}
	if fitsMarked() {
		return markShortened(obj, tool)
	}
	// A local scope refusal is acted on by a person: its scope details (path,
	// missing, grant_example) outlive the optional preview of the refused write.
	if obj.Code == "E_FORBIDDEN" && hasKey(d, "scope") && hasKey(d, "preview") {
		delete(d, "preview")
		if fitsMarked() {
			return markShortened(obj, tool)
		}
	}
	if v, ok := d["scope"]; ok {
		d["scope"] = shortenStrings(v, 80)
	}
	if fitsMarked() {
		return markShortened(obj, tool)
	}
	allItems := d["items"]
	for _, cut := range []int{10, 5, 3, 1} {
		if p, ok := d["preview"].(map[string]any); ok {
			p = shortenBatchPreview(p, cut)
			if ch, has := p["changes"]; has {
				p = cloneMap(p)
				p["changes"] = headOf(ch, cut)
			}
			d["preview"] = p
		}
		if allItems != nil {
			d["items"], d["omitted"] = trimBatchItems(allItems, true, cut)
		}
		if fitsMarked() {
			return markShortened(obj, tool)
		}
	}
	// Last resort: what each code requires, and a minimal preview, receipt and
	// batch list.
	keep := map[string]bool{"dispatch_state": true, "next_action": true, "problem": true, "conflict": true,
		"parameter": true, "confirm_token": true, "expires_at": true, "operation_id": true, "lookup": true,
		"action": true, "resume": true, "summary": true, "active_operation": true, "omitted": true,
		"stage": true, "current_version": true, "binary_replaced": true, "skill_sync_status": true,
		"target_version": true, "skill_sync_command": true, "http_status": true}
	minimal := map[string]any{}
	for k, v := range d {
		if keep[k] {
			minimal[k] = v
		}
	}
	if p, ok := d["preview"].(map[string]any); ok {
		minimal["preview"] = minimalPreview(p)
	}
	if r, ok := d["receipt"].(map[string]any); ok {
		minimal["receipt"] = minimalReceipt(r)
	}
	if allItems != nil {
		minimal["items"], minimal["omitted"] = trimBatchItems(allItems, true, 1)
		if list, ok := minimal["items"].([]any); ok && len(list) == 1 {
			if m, isMap := list[0].(map[string]any); isMap {
				if r, has := m["receipt"].(map[string]any); has {
					m = cloneMap(m)
					m["receipt"] = minimalReceipt(r)
					minimal["items"] = []any{m}
				}
			}
		}
	}
	if _, ok := d["candidates"]; ok {
		minimal["candidates"] = headOf(d["candidates"], 3)
	}
	if s, ok := d["scope"].(map[string]any); ok {
		ms := map[string]any{"status": s["status"], "path": s["path"]}
		if id, has := s["scope_id"]; has {
			ms["scope_id"] = id
		}
		if miss, has := s["missing"]; has {
			ms["missing"] = shortenStrings(miss, 60)
		}
		if g, has := s["grant_example"]; has {
			ms["grant_example"] = shortenStrings(g, 60)
		}
		minimal["scope"] = ms
	}
	obj.Details = minimal
	obj.Message = truncateRunes(obj.Message, 300)
	obj = markShortened(obj, tool)
	if fits() {
		return obj
	}
	// Still over (long paths, escaped twice in JSON): shorten every string that is
	// not an id, a token, a time or an enum.
	for k, v := range minimal {
		switch k {
		case "confirm_token", "operation_id", "active_operation", "expires_at", "lookup", "dispatch_state", "problem",
			"conflict", "action", "stage", "skill_sync_status", "next_action":
			continue
		}
		minimal[k] = shortenStrings(v, 48)
	}
	if fits() {
		return obj
	}
	// Finally drop what the code does not require: the preview, then the example grant.
	// A token never goes without the preview it confirms: a conflict's fresh plan
	// loses both, and its next step becomes a new dry-run.
	if obj.Code != "E_CONFIRMATION_REQUIRED" || minimal["problem"] != "confirmation_required" {
		if _, had := minimal["preview"]; had && hasKey(minimal, "confirm_token") {
			delete(minimal, "confirm_token")
			delete(minimal, "expires_at")
			if obj.Code == "E_CONFLICT" {
				delete(minimal, "operation_id")
				minimal["next_action"] = "Run the same command again with --dry-run, check the new preview, then confirm its token."
			}
		}
		delete(minimal, "preview")
	}
	if fits() {
		return obj
	}
	if s, ok := minimal["scope"].(map[string]any); ok {
		ms := map[string]any{"status": s["status"], "path": s["path"], "scope_id": s["scope_id"]}
		if miss, has := s["missing"]; has && s["status"] == "insufficient" {
			ms["missing"] = shortenStrings(miss, 32)
		}
		minimal["scope"] = ms
	}
	return obj
}

func markShortened(obj errorObject, tool string) errorObject {
	if om, ok := obj.Details["omitted"].(int); ok && om == 0 {
		delete(obj.Details, "omitted")
	}
	if na, _ := obj.Details["next_action"].(string); na != "" && !strings.Contains(na, "shortened") {
		obj.Details["next_action"] = na + shortenedNote(obj, tool)
	}
	return obj
}

// shortenedNote tells the agent where the full details are, when they exist: a
// preview comes back whole from --dry-run (the success budget is larger), the items
// of a batch from operation list --batch (operation show of a large batch lists only
// some), a single recorded operation from operation show. A refused write was never
// recorded.
func shortenedNote(obj errorObject, tool string) string {
	batchID, _ := obj.Details["operation_id"].(string)
	switch {
	case obj.Code == "E_CONFIRMATION_REQUIRED" && hasKey(obj.Details, "preview"):
		return " (Details were shortened to fit; --dry-run returns the full preview.)"
	case hasKey(obj.Details, "items") && strings.HasPrefix(batchID, "opb_") && validOperationID(batchID):
		command := "operation list --batch " + batchID
		if tool != "" {
			command = tool + " " + command
		}
		return " (Details were shortened to fit; `" + command + "` lists every item.)"
	case hasKey(obj.Details, "items") || hasKey(obj.Details, "receipt"):
		return " (Details were shortened to fit; `operation show --id` returns them in full.)"
	}
	return " (Details were shortened to fit.)"
}

// minimalPreview keeps a preview's required keys, its target (no changes) or its
// first batch target and item (one to one, with omitted), and empty inputs.
func minimalPreview(p map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range previewRequiredKeys {
		if v, ok := p[k]; ok {
			out[k] = v
		}
	}
	out["inputs"] = map[string]any{}
	for _, k := range []string{"destinations", "outputs", "base"} {
		if v, ok := p[k]; ok {
			out[k] = shortenStrings(headOf(v, 3), 60)
		}
	}
	if t, ok := p["target"]; ok {
		out["target"] = t
		out["changes"] = []any{}
	}
	if _, ok := p["items"]; ok {
		short := shortenBatchPreview(p, 1)
		out["total"] = short["total"]
		out["targets"] = short["targets"]
		var items []any
		if list, ok := short["items"].([]any); ok {
			for _, it := range list {
				if m, isMap := it.(map[string]any); isMap {
					m = cloneMap(m)
					m["changes"] = []any{}
					it = m
				}
				items = append(items, it)
			}
		}
		out["items"] = items
		if om, ok := short["omitted"]; ok {
			out["omitted"] = om
		}
	}
	return out
}

// minimalReceipt keeps every key a receipt requires and what its outcome needs:
// mismatched for a failed read-back, failed_stage for a failed execution, and the
// observed values (shortened) that verification passed or failed on.
func minimalReceipt(r map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"operation_id", "command", "target", "effects", "outcome", "replayed", "mismatched", "failed_stage"} {
		if v, ok := r[k]; ok {
			out[k] = v
		}
	}
	obs, _ := r["observed"].(map[string]any)
	small := map[string]any{}
	mismatched := stringsAt(r, "mismatched")
	for _, k := range sortedKeys(obs) {
		if len(small) >= 3 && !contains(mismatched, k) {
			continue
		}
		small[k] = shortenStrings(obs[k], 40)
	}
	out["observed"] = small
	return out
}

func headOf(v any, n int) any {
	if list, ok := v.([]any); ok && len(list) > n {
		return append([]any{}, list[:n]...)
	}
	return v
}

func shortenStrings(v any, n int) any {
	switch x := v.(type) {
	case string:
		return truncateRunes(x, n)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if k == "confirm_token" || k == "operation_id" || k == "expires_at" {
				out[k] = val
				continue
			}
			out[k] = shortenStrings(val, n)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = shortenStrings(val, n)
		}
		return out
	}
	return v
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------- text output

// writeText renders data for people (--format text). It is not a parseable contract.
func writeText(w io.Writer, v any) {
	var b strings.Builder
	renderText(&b, v, "")
	if b.Len() == 0 {
		b.WriteString("ok\n")
	}
	_, _ = io.WriteString(w, b.String())
}

func renderText(b *strings.Builder, v any, indent string) {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(x) {
			val := x[k]
			if isScalar(val) {
				fmt.Fprintf(b, "%s%s: %s\n", indent, k, scalarText(val))
				continue
			}
			fmt.Fprintf(b, "%s%s:\n", indent, k)
			renderText(b, val, indent+"  ")
		}
	case []any:
		if len(x) == 0 {
			fmt.Fprintf(b, "%s(none)\n", indent)
		}
		for _, item := range x {
			if isScalar(item) {
				fmt.Fprintf(b, "%s- %s\n", indent, scalarText(item))
				continue
			}
			if obj, ok := item.(map[string]any); ok && allScalar(obj) {
				parts := make([]string, 0, len(obj))
				for _, k := range sortedKeys(obj) {
					parts = append(parts, k+"="+scalarText(obj[k]))
				}
				fmt.Fprintf(b, "%s- %s\n", indent, strings.Join(parts, "  "))
				continue
			}
			fmt.Fprintf(b, "%s-\n", indent)
			renderText(b, item, indent+"  ")
		}
	default:
		fmt.Fprintf(b, "%s%s\n", indent, scalarText(v))
	}
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}
	return true
}

func allScalar(m map[string]any) bool {
	for _, v := range m {
		if !isScalar(v) {
			if list, ok := v.([]any); ok {
				for _, x := range list {
					if !isScalar(x) {
						return false
					}
				}
				continue
			}
			return false
		}
	}
	return true
}

func scalarText(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = scalarText(e)
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v)
}

func writeTextError(w io.Writer, obj errorObject) {
	var b strings.Builder
	fmt.Fprintf(&b, "error %s: %s\n", obj.Code, obj.Message)
	if na, _ := obj.Details["next_action"].(string); na != "" {
		fmt.Fprintf(&b, "next: %s\n", na)
	}
	for _, k := range []string{"problem", "conflict", "parameter", "operation_id", "confirm_token", "expires_at", "lookup"} {
		if v, ok := obj.Details[k]; ok && v != nil {
			fmt.Fprintf(&b, "%s: %s\n", k, scalarText(v))
		}
	}
	if p, ok := obj.Details["preview"]; ok {
		b.WriteString("preview:\n")
		g, err := toGeneric(p)
		if err == nil {
			renderText(&b, g, "  ")
		}
	}
	_, _ = io.WriteString(w, b.String())
}

// sortedKeys returns the keys of a map in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
