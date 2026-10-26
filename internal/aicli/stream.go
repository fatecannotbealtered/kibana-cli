// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// NDJSON streams (CLI-006) and the --output file helper (CLI-042), decision 20.
//
// A stream writes one envelope per line, each with type "item", and ends with one
// line of type "summary": ok true with what the stream covered, or ok false with
// the error that ended it. The exit code matches the summary line.
//
// WriteOutput writes a large result to a local file through the write gate: the
// file is an artifact (new) or persist (overwritten) effect, the scope's outputs or
// roots must allow its path (SEC-012), and the response carries the path and size
// instead of the content.

// streamLine is one NDJSON line: an envelope plus type item or summary.
type streamLine struct {
	OK            bool       `json:"ok"`
	SchemaVersion string     `json:"schema_version"`
	Type          string     `json:"type"`
	Data          any        `json:"data"`
	Meta          metaObject `json:"meta"`
}

type streamErrorLine struct {
	OK            bool        `json:"ok"`
	SchemaVersion string      `json:"schema_version"`
	Type          string      `json:"type"`
	Error         errorObject `json:"error"`
	Meta          metaObject  `json:"meta"`
}

// Stream is the NDJSON stream of the running read command (App.Stream). Item
// writes a line at once; the summary line is written last, when the command
// returns.
type Stream struct {
	a                *App
	st               *runState
	cmd              *cobra.Command
	items            int
	summary          any
	summaryUntrusted []string
	summarySet       bool
	writeErr         error
}

// Stream starts an NDJSON stream for the running read command (CLI-006). Declare
// it in the command's summary and examples. A write command cannot stream; it
// reports through Write or WriteBatch.
func (a *App) Stream(cmd *cobra.Command) (*Stream, error) {
	st, perr := a.runFor(cmd)
	if perr != nil {
		return nil, perr
	}
	if m := a.metaFor(cmd); m != nil && m.isWrite() {
		return nil, internalError("A write command cannot stream; it reports through App.Write or App.WriteBatch.")
	}
	if st.stream == nil {
		st.stream = &Stream{a: a, st: st, cmd: cmd}
	}
	return st.stream, nil
}

// Item writes one line of type item now. untrusted lists paths inside data that
// carry external content, relative to data; the item schema (Meta.StreamItem, else
// the command's output schema) adds its untrusted fields (Q23), and --fields
// applies as for an object. With --format raw the item is written as bytes only: a
// []byte or string as it is, anything else as its compact JSON value.
func (s *Stream) Item(data any, untrusted ...string) error {
	a, st := s.a, s.st
	gen, err := toGenericData(data)
	if err != nil {
		return internalError("A stream item cannot be encoded as JSON.")
	}
	m := a.metaFor(s.cmd)
	schema, own := a.itemSchemaOf(m)
	pm := m
	if !own {
		pm = nil // Meta.DefaultFields belong to the output schema, not to the items
	}
	gen = a.project(st, pm, schema, "object", gen)
	paths := cleanPaths(presentPaths(map[string]any{"data": gen}, append(prefixPaths("data", untrusted), schemaUntrusted(schema, "object")...)))
	s.items++
	out := a.stdout()
	switch st.format {
	case "text":
		writeText(out, gen)
		return nil
	case "raw":
		switch v := data.(type) {
		case []byte:
			_, err = out.Write(v)
			return s.noteWrite(err)
		case string:
			_, err = io.WriteString(out, v)
			return s.noteWrite(err)
		}
		b, err := encodeJSON(gen, false)
		if err != nil {
			return internalError("A stream item cannot be encoded as JSON.")
		}
		_, err = out.Write(b)
		return s.noteWrite(err)
	}
	meta := metaObject{DurationMS: time.Since(st.startReal).Milliseconds(), Untrusted: paths}
	b, err := encodeJSON(streamLine{OK: true, SchemaVersion: SchemaVersion, Type: "item", Data: gen, Meta: meta}, false)
	if err != nil {
		return internalError("A stream item cannot be encoded as JSON.")
	}
	_, err = out.Write(b)
	return s.noteWrite(err)
}

// itemSchemaOf is the schema of a stream's items (Q23): Meta.StreamItem when it is
// set, else the command's output schema (own reports the latter).
func (a *App) itemSchemaOf(m *Meta) (schema SchemaDef, own bool) {
	if m != nil && m.StreamItem != "" && a.reg.schemas != nil {
		return a.reg.schemas[m.StreamItem], false
	}
	return a.schemaOf(m), true
}

// noteWrite remembers a failed write to stdout (a closed pipe): the stream stops.
func (s *Stream) noteWrite(err error) error {
	if err != nil {
		s.writeErr = err
		return ioError("The stream could not be written to stdout.", "", err)
	}
	return nil
}

// Count is the number of item lines written so far.
func (s *Stream) Count() int { return s.items }

// Summary records the summary line, written as the last line when the command
// returns: what the stream covered (counts, and the completeness keys of CLI-041
// when it stopped early). untrusted is relative to data. A command that returns an
// error ends with an error summary instead.
func (s *Stream) Summary(data any, untrusted ...string) {
	s.summary = data
	s.summaryUntrusted = untrusted
	s.summarySet = true
}

// emitStream writes the summary line of a stream and returns its exit code. A raw
// stream (--format raw) is the items' bytes only (Q23): no summary line; an error
// that ends it goes to stderr as text, and the exit code reports it.
func (a *App) emitStream(st *runState, res result) int {
	s := st.stream
	stdout, stderr := a.stdout(), a.stderr()
	meta := metaObject{DurationMS: time.Since(st.startReal).Milliseconds(), Notices: st.notices}
	if st.format == "raw" {
		if res.err == nil {
			return 0
		}
		obj, exit, errUntrusted := a.normalizeError(res.err)
		meta.Untrusted = cleanPaths(errUntrusted)
		writeTextError(stderr, fitErrorBudget(obj, meta, a.toolName()))
		a.textNotices(st, stderr)
		return exit
	}
	if res.err == nil {
		// The summary is what Summary recorded, else what the command reported with
		// OK, else the number of items.
		data, untrusted := any(map[string]any{"count": s.items}), []string(nil)
		switch {
		case s.summarySet:
			gen, err := toGenericData(s.summary)
			if err != nil {
				res.err = internalError("The stream summary cannot be encoded as JSON.")
			} else {
				data, untrusted = gen, prefixPaths("data", s.summaryUntrusted)
			}
		case st.out.set:
			data, untrusted = res.data, res.untrusted
		}
		if res.err == nil {
			if st.format == "text" {
				writeText(stdout, data)
				a.textNotices(st, stderr)
				return 0
			}
			meta.Untrusted = cleanPaths(presentPaths(map[string]any{"data": data}, untrusted))
			if b, err := encodeJSON(streamLine{OK: true, SchemaVersion: SchemaVersion, Type: "summary", Data: data, Meta: meta}, false); err == nil {
				_, _ = stdout.Write(b)
				return 0
			}
			res.err = internalError("The stream summary cannot be encoded as JSON.")
		}
	}
	obj, exit, errUntrusted := a.normalizeError(res.err)
	meta.Untrusted = cleanPaths(presentPaths(errorRoot(obj), errUntrusted))
	obj = fitErrorBudget(obj, meta, a.toolName())
	meta.Untrusted = cleanPaths(presentPaths(errorRoot(obj), meta.Untrusted))
	if st.format == "text" {
		writeTextError(stderr, obj)
		a.textNotices(st, stderr)
		return exit
	}
	b, err := encodeJSON(streamErrorLine{OK: false, SchemaVersion: SchemaVersion, Type: "summary", Error: obj, Meta: meta}, false)
	if err != nil {
		b, _ = encodeJSON(streamErrorLine{OK: false, SchemaVersion: SchemaVersion, Type: "summary", Error: errorObject{Code: "E_UNKNOWN",
			Message: "The error could not be encoded as JSON.", Details: map[string]any{"dispatch_state": "not_started",
				"next_action": "Report this bug in the tool."}}, Meta: metaObject{DurationMS: meta.DurationMS}}, false)
		exit = coreCodes["E_UNKNOWN"].exit
	}
	_, _ = stdout.Write(b)
	return exit
}

// ---------------------------------------------------------------- --output

// OutputSpec is a result written to a local file through the gate
// (App.WriteOutput, CLI-042). Path is the file the user named with --output.
// Produce makes the content when the write is confirmed; an error from it means
// nothing was written. Summary adds fields to data.result beside path, bytes and
// sha256. Timeout is the lease (default 2 minutes).
type OutputSpec struct {
	Path    string
	Produce func(ctx context.Context) ([]byte, error)
	Summary map[string]any
	Timeout time.Duration
}

// WriteOutput writes a result to spec.Path through the write gate (CLI-042,
// decision 20). The command declares the artifact effect (new files), persist
// (overwriting existing ones) or both. The path is bound into the token, checked
// against the scope's outputs and roots after symlink resolution, and the file is
// written atomically; its current content is the preview's base version, so a file
// that changes between the dry-run and the confirm is state_drift. The response's
// data.result carries path, bytes and sha256 (plus spec.Summary).
func (a *App) WriteOutput(cmd *cobra.Command, spec OutputSpec) error {
	m := a.metaFor(cmd)
	if m == nil || !m.hasEffect(Artifact, Persist) {
		return internalError("App.WriteOutput needs a command that declares the artifact or persist effect.")
	}
	if spec.Produce == nil {
		return internalError("App.WriteOutput needs OutputSpec.Produce.")
	}
	if spec.Path == "" {
		e := Usage("missing_parameter", "--output", "Give the file to write with --output.")
		e.Details["next_action"] = "Add --output <file> and run the command again."
		return e
	}
	path, err := filepath.Abs(spec.Path)
	if err != nil {
		return Usage("invalid_value", "--output", "The --output path is not a valid path.")
	}
	creates, overwrites := m.hasEffect(Artifact), m.hasEffect(Persist)
	state := func() (exists bool, version string) {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, "absent"
		}
		if err != nil {
			return true, "unreadable"
		}
		if info.IsDir() {
			return true, "directory"
		}
		if !info.Mode().IsRegular() {
			return true, "not_a_regular_file"
		}
		sum, err := fileDigest(path)
		if err != nil {
			return true, "unreadable"
		}
		return true, "sha256:" + sum
	}
	appeared := func() error {
		return NotDispatched(conflictError("state_drift", "The output file appeared or changed since the preview; nothing was written.",
			"Run the command again with --dry-run and check the new preview."))
	}
	// planned is the file's state the last Preview saw: at confirm, the re-check
	// that runs just before Apply.
	planned := ""
	preview := func(context.Context) (Preview, error) {
		exists, version := state()
		planned = version
		var blocks []Block
		switch {
		case version == "directory":
			blocks = append(blocks, Block{"is_directory", "The output path is a directory; name a file."})
		case version == "not_a_regular_file" || version == "unreadable":
			blocks = append(blocks, Block{"not_a_file", "The output path exists but is not a readable regular file."})
		case exists && !overwrites:
			blocks = append(blocks, Block{"exists", "The output file exists and this command only creates new files."})
		case !exists && !creates:
			blocks = append(blocks, Block{"missing", "The output file does not exist and this command only overwrites files."})
		}
		action, note, source := "Write "+path, "creates a new file", "file"
		if exists {
			action, note, source = "Overwrite "+path, "overwrites the existing file", "file_sha256"
		}
		return Preview{Action: action, Changes: []FieldChange{{Field: "path", After: path, Note: note}},
			BaseVersion: version, BaseSource: source, CanApply: len(blocks) == 0, BlockedBy: blocks, Outputs: []string{path}}, nil
	}
	apply := func(ctx context.Context, _ Op) (Result, error) {
		content, err := spec.Produce(ctx)
		if err != nil {
			return Result{}, NotDispatched(err)
		}
		// Checked again at write time (C1, Q9): a file that appeared since the
		// confirm's checks is an overwrite, which only a persist command may do and
		// the scope's roots must allow.
		exists, version := state()
		if e := a.existingOutputAllowed(cmd, path, exists, overwrites); e != nil {
			return Result{}, NotDispatched(e)
		}
		if version != planned {
			return Result{}, appeared()
		}
		if err := writeOutputFile(path, content, exists); err != nil {
			if errors.Is(err, fs.ErrExist) {
				if e := a.existingOutputAllowed(cmd, path, true, overwrites); e != nil {
					return Result{}, NotDispatched(e)
				}
				return Result{}, appeared()
			}
			return Result{}, NotDispatched(ioError("The output file could not be written; nothing was saved.",
				fmt.Sprintf("Check that %s is writable.", filepath.Dir(path)), err))
		}
		sum := sha256.Sum256(content)
		res := map[string]any{"path": path, "bytes": len(content), "sha256": hex.EncodeToString(sum[:])}
		for k, v := range spec.Summary {
			if _, taken := res[k]; !taken {
				res[k] = v
			}
		}
		observed := map[string]any{"path": nil}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			observed["path"], observed["bytes"] = path, info.Size()
		}
		return Result{Observed: observed, Data: res}, nil
	}
	return a.Write(cmd, WriteSpec{Target: Target{Key: path, Kind: "file", Label: filepath.Base(path)},
		Preview: preview, Apply: apply, Timeout: spec.Timeout})
}

// overwriteAllowed reports whether the current scope's roots cover path, an existing
// file (C1, SEC-011): absent roots, nothing may be overwritten.
func (a *App) overwriteAllowed(path string) bool {
	state, e := a.stateDirPath()
	if e != nil {
		return false
	}
	sc := a.loadScope(state)
	return sc.status == "granted" && pathWithin(path, sc.file.Roots)
}

// overwriteRefused is WriteOutput's write-time refusal of a file that appeared under
// a grant that only allows new files (C1): E_FORBIDDEN with details.scope naming
// the file under missing.roots, and nothing written.
func (a *App) overwriteRefused(cmd *cobra.Command, path string) *Error {
	state, _ := a.stateDirPath()
	sc := a.loadScope(state)
	need := scopeNeed{outputs: []string{path}}
	if m := a.metaFor(cmd); m != nil {
		need.effects, need.dangerous = m.effectStrings(), m.dangerous()
	}
	status := sc.status
	if status == "granted" {
		status = "insufficient"
	}
	e := a.scopeRefusal(sc, status, need, map[string]any{"roots": []string{path}}, 0)
	e.Message = "The output file already exists and the scope does not allow overwriting it; nothing was written."
	e.Details["next_action"] = "Ask the user to add the directory to the scope's roots, or choose a new file name."
	return e
}

// existingOutputAllowed is the write-time check of an output file that exists (C1,
// Q9): a command that only creates files refuses it with Q9's E_VALIDATION, and an
// overwrite needs the scope's roots. A file that does not exist passes.
func (a *App) existingOutputAllowed(cmd *cobra.Command, path string, exists, overwrites bool) *Error {
	switch {
	case !exists:
		return nil
	case !overwrites:
		return createOnlyExists(path)
	case !a.overwriteAllowed(path):
		return a.overwriteRefused(cmd, path)
	}
	return nil
}

// newOutputAllowed is the write-time scope check of a new output file (Q8): links
// resolved, a junction refused (C2), the directory inside the scope's outputs.
func (a *App) newOutputAllowed(cmd *cobra.Command, path string) *Error {
	state, e := a.stateDirPath()
	if e != nil {
		return e
	}
	need := scopeNeed{outputs: []string{path}}
	if m := a.metaFor(cmd); m != nil {
		need.effects, need.dangerous = m.effectStrings(), m.dangerous()
	}
	return a.checkScope(a.loadScope(state), need, "")
}

// OutputFile is a local output file written in pieces inside a write's Apply
// (App.OpenOutput, Q8). It is an io.Writer onto a temporary file beside the target;
// Commit moves the file into place and Abort removes it.
type OutputFile struct {
	a         *App
	cmd       *cobra.Command
	path      string
	tmp       *os.File
	overwrite bool
	size      int64
	sum       hash.Hash
	done      bool
}

// OpenOutput starts a local output file inside the running write's Apply (Q8,
// CLI-042), for content too large to hold in memory (an artifacts archive): a
// temporary file in the target's directory, mode 0600 on POSIX. Commit moves it into
// place, never over an existing file unless overwrite is true and the command
// declares persist (otherwise Q9's E_VALIDATION), with the scope checked as
// WriteOutput checks it at write time: links and junctions resolved, the scope's
// outputs for a new file and its roots for an existing one. Abort, or a failed
// Commit, removes the temporary file; return Commit's error wrapped in
// NotDispatched, since nothing was written. Name the path in Preview.Outputs so the
// gate checks it at the dry-run and the confirm. WriteOutput stays for small
// results built in memory.
func (a *App) OpenOutput(path string, overwrite bool) (*OutputFile, *Error) {
	st := a.run
	if st == nil || st.cmd == nil {
		return nil, internalError("App.OpenOutput must be called from a write's Apply, inside App.Execute.")
	}
	if m := a.metaFor(st.cmd); m == nil || !m.hasEffect(Artifact, Persist) {
		return nil, internalError("App.OpenOutput needs a command that declares the artifact or persist effect.")
	}
	if strings.TrimSpace(path) == "" {
		e := Usage("missing_parameter", "--output", "Give the file to write with --output.")
		e.Details["next_action"] = "Add --output <file> and run the command again."
		return nil, e
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, Usage("invalid_value", "--output", "The --output path is not a valid path.")
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".aicli-output-*")
	if err != nil {
		return nil, ioError("The output file could not be started; nothing was written.",
			fmt.Sprintf("Check that %s exists and is writable.", filepath.Dir(abs)), err)
	}
	return &OutputFile{a: a, cmd: st.cmd, path: abs, tmp: tmp, overwrite: overwrite, sum: sha256.New()}, nil
}

// Write writes to the temporary file.
func (o *OutputFile) Write(p []byte) (int, error) {
	if o.done {
		return 0, errors.New("the output file was already committed or aborted")
	}
	n, err := o.tmp.Write(p)
	o.size += int64(n)
	o.sum.Write(p[:n])
	return n, err
}

// Path is the absolute path the file is committed to.
func (o *OutputFile) Path() string { return o.path }

// Size is the number of bytes written so far.
func (o *OutputFile) Size() int64 { return o.size }

// SHA256 is the hex SHA-256 of the bytes written so far.
func (o *OutputFile) SHA256() string { return hex.EncodeToString(o.sum.Sum(nil)) }

// Abort removes the temporary file; the target is left as it was. It is safe to
// call after Commit.
func (o *OutputFile) Abort() {
	if o.done {
		return
	}
	o.done = true
	_ = o.tmp.Close()
	_ = os.Remove(o.tmp.Name())
}

// Commit moves the written file into place (Q8): new files only, unless the file
// was opened with overwrite and the command declares persist, and with the scope's
// outputs (new) or roots (existing) checked at this moment. On any error nothing
// was written and the temporary file is gone.
func (o *OutputFile) Commit() *Error {
	if o.done {
		return internalError("The output file was already committed or aborted.")
	}
	defer o.Abort()
	tmpName := o.tmp.Name()
	if err := o.tmp.Sync(); err != nil {
		return o.writeFailed(err)
	}
	if err := o.tmp.Close(); err != nil {
		return o.writeFailed(err)
	}
	m := o.a.metaFor(o.cmd)
	overwrite := o.overwrite && m != nil && m.hasEffect(Persist)
	exists := outputExists(o.path)
	if e := o.a.existingOutputAllowed(o.cmd, o.path, exists, overwrite); e != nil {
		return e
	}
	if !exists {
		if e := o.a.newOutputAllowed(o.cmd, o.path); e != nil {
			return e
		}
	}
	if exists {
		if err := renameRetry(tmpName, o.path); err != nil {
			return o.writeFailed(err)
		}
		o.done = true
		return nil
	}
	if err := linkNew(tmpName, o.path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			// The file appeared since the check: the same rules apply to it.
			if e := o.a.existingOutputAllowed(o.cmd, o.path, true, overwrite); e != nil {
				return e
			}
			if err := renameRetry(tmpName, o.path); err == nil {
				o.done = true
				return nil
			}
		}
		return o.writeFailed(err)
	}
	return nil
}

func (o *OutputFile) writeFailed(err error) *Error {
	return ioError("The output file could not be written; nothing was saved.",
		fmt.Sprintf("Check that %s is writable.", filepath.Dir(o.path)), err)
}

// linkNew puts the file tmp at path without replacing anything: a hard link, or,
// where the file system has none, an exclusive create and a copy. It fails with
// fs.ErrExist when path exists.
func linkNew(tmp, path string) error {
	err := os.Link(tmp, path)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return err
	}
	src, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(path)
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// writeOutputFile writes content to path atomically: a synced temporary file in the
// same directory, then a hard link for a new file (it fails if the file appeared
// meanwhile) or a rename over an existing one.
func writeOutputFile(path string, content []byte, overwrite bool) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".aicli-output-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if overwrite {
		return renameRetry(tmpName, path)
	}
	linkErr := os.Link(tmpName, path)
	switch {
	case linkErr == nil:
		return nil
	case errors.Is(linkErr, fs.ErrExist):
		return linkErr
	}
	// No hard links here: create the file exclusively instead.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
