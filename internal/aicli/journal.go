// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// The operation journal (CLI-058, CLI-060): one record per operation at
// <state>/operations/<id>.json, replaced atomically (temp file + fsync + rename);
// single use is the exclusive creation of <id>.consumed. A record is written before
// anything is sent; if it cannot be written, nothing is sent.

// record is an operation record as stored.
type record struct {
	V            int             `json:"v"`
	OperationID  string          `json:"operation_id"`
	Kind         string          `json:"kind"` // single, batch or item
	BatchID      *string         `json:"batch_id"`
	Command      string          `json:"command"`
	Status       string          `json:"status"`
	Target       *targetJSON     `json:"target"`
	Effects      []string        `json:"effects"`
	Dangerous    bool            `json:"dangerous"`
	ScopeID      *string         `json:"scope_id"`
	CreatedAt    string          `json:"created_at"`
	ExpiresAt    string          `json:"expires_at"`
	DispatchedAt *string         `json:"dispatched_at"`
	LeaseUntil   *string         `json:"lease_until"`
	FinishedAt   *string         `json:"finished_at"`
	Claims       *claims         `json:"claims"`
	Preview      json.RawMessage `json:"preview"`
	Receipt      json.RawMessage `json:"receipt"`
	Error        *recordError    `json:"error"`
	Resolution   *resolution     `json:"resolution"`
	Items        []string        `json:"items"`
	ReplayOf     *string         `json:"replay_of,omitempty"`
	LateReceipt  json.RawMessage `json:"late_receipt,omitempty"`
	// Result is the confirmed write's data.result (at most maxStoredResult bytes),
	// so an idempotent replay can return the original ref and url (CLI-059, CLI-062).
	Result json.RawMessage `json:"result,omitempty"`
}

// maxStoredResult caps the result kept in a record.
const maxStoredResult = 4096

type targetJSON struct {
	Key   string `json:"key"`
	Kind  string `json:"kind,omitempty"`
	Label string `json:"label,omitempty"`
}

type recordError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type resolution struct {
	Outcome    string `json:"outcome"`
	Evidence   string `json:"evidence"`
	ResolvedAt string `json:"resolved_at"`
}

func strPtr(s string) *string { return &s }

func timeString(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// effectiveStatus applies expiry and lease rules to a stored status (BLUEPRINT 5.3).
func effectiveStatus(r *record, now time.Time) string {
	switch r.Status {
	case "planned":
		if exp, ok := parseTime(r.ExpiresAt); !ok || now.After(exp) {
			return "expired"
		}
	case "dispatching":
		if r.LeaseUntil == nil {
			return "unknown"
		}
		if lease, ok := parseTime(*r.LeaseUntil); !ok || now.After(lease) {
			return "unknown"
		}
	}
	return r.Status
}

func unresolved(status string) bool { return contains(unresolvedStatusValues, status) }

// journal is the operations directory of one state dir.
type journal struct {
	dir string
}

// openJournal returns the journal of stateDir. With create it makes the operations
// directory (0700). A path that is a link or not a directory is E_IO (fail closed).
func (a *App) openJournal(stateDir string, create bool) (*journal, *Error) {
	dir := filepath.Join(stateDir, "operations")
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) && create {
		if mkErr := os.Mkdir(dir, 0o700); mkErr != nil && !errors.Is(mkErr, fs.ErrExist) {
			return nil, ioError("The operation journal cannot be created; nothing was sent.", fmt.Sprintf("Check that %s is writable.", stateDir), mkErr)
		}
		info, err = os.Lstat(dir)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return &journal{dir: dir}, nil
	}
	if err != nil {
		return nil, ioError("The operation journal cannot be read; nothing was sent.", fmt.Sprintf("Check %s.", dir), err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeIrregular != 0 || !info.IsDir() {
		return nil, ioError("The operation journal path is not a directory; the tool refuses writes.",
			fmt.Sprintf("Ask the user to inspect %s and move it aside; the tool recreates the journal.", dir), nil)
	}
	return &journal{dir: dir}, nil
}

func (j *journal) recordPath(id string) string { return filepath.Join(j.dir, id+".json") }

// modTime is the last write of a record file (the zero time's epoch when unknown).
func (j *journal) modTime(id string) time.Time {
	if info, err := os.Stat(j.recordPath(id)); err == nil {
		return info.ModTime().UTC()
	}
	return time.Unix(0, 0).UTC()
}

// write stores a record atomically: temp file, fsync, rename over the old version.
func (j *journal) write(r *record) *Error {
	if !validOperationID(r.OperationID) {
		return internalError("Refusing to write a record with an invalid operation id.")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return internalError("The operation record cannot be encoded.")
	}
	if err := writeFileAtomic(j.dir, r.OperationID+".json", append(b, '\n')); err != nil {
		return ioError("The operation record cannot be written; nothing was sent.",
			fmt.Sprintf("Check that %s is writable.", j.dir), err)
	}
	return nil
}

// writeFileAtomic replaces dir/name with data via a synced temporary file.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
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
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpName, 0o600); err != nil {
			return err
		}
	}
	if err := renameRetry(tmpName, filepath.Join(dir, name)); err != nil {
		return err
	}
	done = true
	syncDir(dir)
	return nil
}

// renameRetry renames, retrying briefly on Windows where a concurrent reader can
// hold the target open for a moment.
func renameRetry(from, to string) error {
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		if err = os.Rename(from, to); err == nil || runtime.GOOS != "windows" {
			return err
		}
		time.Sleep(time.Duration(2+attempt) * time.Millisecond)
	}
	return err
}

// readFileRetry reads a file, retrying briefly on Windows while a writer replaces it.
func readFileRetry(path string) ([]byte, error) {
	var b []byte
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		b, err = os.ReadFile(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) || runtime.GOOS != "windows" {
			return b, err
		}
		time.Sleep(time.Duration(2+attempt) * time.Millisecond)
	}
	return b, err
}

// errCorruptRecord marks a record file that cannot be trusted.
var errCorruptRecord = errors.New("corrupt operation record")

// recordKeys are the keys a stored record may have, spelled exactly as written.
var recordKeys = []string{"v", "operation_id", "kind", "batch_id", "command", "status", "target", "effects", "dangerous",
	"scope_id", "created_at", "expires_at", "dispatched_at", "lease_until", "finished_at", "claims", "preview", "receipt",
	"error", "resolution", "items", "replay_of", "late_receipt", "result"}

// read loads a record. exists is false when there is no record; err is
// errCorruptRecord when the file exists but cannot be trusted.
func (j *journal) read(id string) (*record, bool, error) {
	if !validOperationID(id) {
		return nil, false, nil
	}
	b, err := readFileRetry(j.recordPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !wellFormedRecord(b) {
		return nil, true, errCorruptRecord
	}
	var r record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, true, errCorruptRecord
	}
	if r.V != 2 || r.OperationID != id || !contains([]string{"single", "batch", "item"}, r.Kind) ||
		!contains(operationStatusValues, r.Status) || r.Status == "expired" {
		return nil, true, errCorruptRecord
	}
	if r.Kind != "batch" && r.Target == nil {
		return nil, true, errCorruptRecord
	}
	return &r, true, nil
}

// wellFormedRecord checks a record's shape before it is decoded (CLI-058, decision
// 35): operation_id, status, command and created_at are strings and effects is an
// array of strings, each under its exact key. encoding/json matches keys without
// regard to case, so a key that differs from a record key only in case (a second
// "Status") makes the record unreadable instead of overriding the real one.
func wellFormedRecord(b []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return false
	}
	for k := range raw {
		for _, known := range recordKeys {
			if k != known && strings.EqualFold(k, known) {
				return false
			}
		}
	}
	// A JSON null decodes into a Go string or slice without an error, so the raw
	// value must open as a string or an array.
	opens := func(k string, c byte) bool {
		v := bytes.TrimSpace(raw[k])
		return len(v) > 0 && v[0] == c
	}
	for _, k := range []string{"operation_id", "status", "command", "created_at"} {
		var s string
		if !opens(k, '"') || json.Unmarshal(raw[k], &s) != nil {
			return false
		}
	}
	var effects []string
	return opens("effects", '[') && json.Unmarshal(raw["effects"], &effects) == nil
}

// consume creates the single-use marker for id. It returns false when the marker
// already exists (the token was used); the exclusive create is atomic across processes.
func (j *journal) consume(id string, at time.Time) (bool, *Error) {
	path := filepath.Join(j.dir, id+".consumed")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, ioError("The single-use marker cannot be written; nothing was sent.", fmt.Sprintf("Check that %s is writable.", j.dir), err)
	}
	_, _ = fmt.Fprintf(f, "{\"operation_id\":%q,\"consumed_at\":%q}\n", id, timeString(at))
	_ = f.Sync()
	_ = f.Close()
	syncDir(j.dir)
	return true, nil
}

// isConsumed reports whether the single-use marker exists.
func (j *journal) isConsumed(id string) (bool, *Error) {
	_, err := os.Lstat(filepath.Join(j.dir, id+".consumed"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return true, ioError("The single-use marker cannot be read; nothing was sent.", fmt.Sprintf("Check %s.", j.dir), err)
}

// lock serialises read-modify-write of one record between the dispatching process
// and operation resolve. Stale locks (older than 30 s) are broken.
func (j *journal) lock(id string) (func(), *Error) {
	unlock, _, err := acquireLock(filepath.Join(j.dir, id+".lock"), "", 3*time.Second)
	switch {
	case err == nil:
		return unlock, nil
	case errors.Is(err, errLockBusy):
		return nil, ioError("The operation record is locked by another process.", "Run the command again in a moment.", nil)
	}
	return nil, ioError("The operation record cannot be locked.", fmt.Sprintf("Check that %s is writable.", j.dir), err)
}

// staleLockAge is the age at which a lock file is taken to be left behind by a
// process that crashed while holding it.
const staleLockAge = 30 * time.Second

// errLockBusy is a lock still held by another process when the wait ended.
var errLockBusy = errors.New("the lock is held by another process")

// acquireLock takes the lock at path, the one primitive behind the journal's record
// locks, the per-target confirm locks and the scope budget lock: an exclusive create
// of the file, which holds owner (an operation id, or nothing). A lock file older
// than 30 s is broken. It waits up to wait; a lock still held then is errLockBusy,
// returned with the owner the file names.
func acquireLock(path, owner string, wait time.Duration) (func(), string, error) {
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = f.WriteString(owner)
			_ = f.Close()
			return func() { _ = os.Remove(path) }, "", nil
		}
		// On Windows a lock file that another process is deleting refuses a new
		// create for a moment: that is a busy lock too.
		busy := errors.Is(err, fs.ErrExist) || (runtime.GOOS == "windows" && errors.Is(err, fs.ErrPermission))
		if !busy {
			return nil, "", err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			if !errors.Is(err, fs.ErrExist) {
				return nil, "", err
			}
			holder, _ := readSmallFile(path, 256)
			return nil, strings.TrimSpace(string(holder)), errLockBusy
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ensurePrivateDir creates dir (0700) inside the state directory when it is missing
// and refuses one that is a link, a reparse point or not a directory (SEC-030).
func ensurePrivateDir(dir string) *Error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if mkErr := os.Mkdir(dir, 0o700); mkErr != nil && !errors.Is(mkErr, fs.ErrExist) {
			return ioError("A directory of the state store cannot be created; nothing was sent.", fmt.Sprintf("Check that %s is writable.", filepath.Dir(dir)), mkErr)
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return ioError("A directory of the state store cannot be read; nothing was sent.", fmt.Sprintf("Check %s.", dir), err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || info.Mode()&fs.ModeIrregular != 0 || !info.IsDir() {
		return ioError("A path in the state store is not a directory; the tool refuses writes.",
			fmt.Sprintf("Ask the user to inspect %s and move it aside; the tool recreates it.", dir), nil)
	}
	return nil
}

// listed is one journal entry: a record, or a corrupt file.
type listed struct {
	id      string
	rec     *record
	corrupt bool
	modTime time.Time
}

// list reads every record in the journal. A missing journal is empty.
func (j *journal) list() ([]listed, *Error) {
	entries, err := os.ReadDir(j.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ioError("The operation journal cannot be listed; nothing was sent.", fmt.Sprintf("Check %s.", j.dir), err)
	}
	var out []listed
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validOperationID(id) {
			continue
		}
		var mod time.Time
		if info, err := ent.Info(); err == nil {
			mod = info.ModTime()
		}
		r, exists, err := j.read(id)
		switch {
		case !exists:
			continue
		case err != nil:
			out = append(out, listed{id: id, corrupt: true, modTime: mod})
		default:
			out = append(out, listed{id: id, rec: r, modTime: mod})
		}
	}
	return out, nil
}

// blocking finds an operation whose outcome is unresolved on any of targets, or a
// corrupt record (whose target is unknown, so it blocks every target). A batch that
// is still dispatching blocks every target of its preview: its items wait their turn
// as planned records, and the batch's record is what blocks them (CLI-057 step 14).
func (j *journal) blocking(targets []string, exclude map[string]bool, now time.Time) (string, *Error) {
	all, e := j.list()
	if e != nil {
		return "", e
	}
	want := toSet(targets)
	sort.Slice(all, func(i, k int) bool { return all[i].id < all[k].id })
	for _, l := range all {
		if exclude[l.id] {
			continue
		}
		if l.corrupt {
			return l.id, nil
		}
		if l.rec.Resolution != nil {
			continue
		}
		if l.rec.Kind == "batch" {
			if effectiveStatus(l.rec, now) == "dispatching" {
				for _, t := range batchTargetKeys(l.rec) {
					if want[t] {
						return l.id, nil
					}
				}
			}
			continue
		}
		if l.rec.Target == nil {
			continue
		}
		if unresolved(effectiveStatus(l.rec, now)) && want[l.rec.Target.Key] {
			return l.id, nil
		}
	}
	return "", nil
}

// batchTargetKeys is the target keys a batch record's preview lists.
func batchTargetKeys(r *record) []string {
	var keys []string
	targets, _ := decodeGeneric(r.Preview)["targets"].([]any)
	for _, t := range targets {
		if m, ok := t.(map[string]any); ok {
			if k, ok := m["key"].(string); ok {
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// findIdempotent returns every operation, of any command, that was confirmed with
// the idempotency key idem (CLI-062): single writes and batches, not those still
// planned; the latest first.
func (j *journal) findIdempotent(idem, exclude string) ([]*record, *Error) {
	all, e := j.list()
	if e != nil {
		return nil, e
	}
	var found []*record
	for _, l := range all {
		r := l.rec
		if r == nil || r.OperationID == exclude || r.Claims == nil || r.Claims.Idem != idem || r.Kind == "item" {
			continue
		}
		if r.Status == "planned" {
			continue
		}
		found = append(found, r)
	}
	sort.Slice(found, func(i, k int) bool {
		if found[i].CreatedAt != found[k].CreatedAt {
			return found[i].CreatedAt > found[k].CreatedAt
		}
		return found[i].OperationID > found[k].OperationID
	})
	return found, nil
}

// prune removes settled records older than the retention window and keeps at most
// maxRecords, oldest settled first. Unresolved and corrupt records are never removed.
// A batch goes with its items (A7): they are pruned together, by the batch's finish
// time, and the batch stays while any of its items is unresolved. An item whose
// batch record is gone is pruned on its own. The scope budget lives in
// scope-usage.json, which pruning never touches.
func (j *journal) prune(now time.Time) {
	all, e := j.list()
	if e != nil {
		return
	}
	horizon := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	byID := map[string]listed{}
	for _, l := range all {
		byID[l.id] = l
	}
	open := func(r *record) bool {
		st := effectiveStatus(r, now)
		return r.Resolution == nil && (unresolved(st) || st == "planned")
	}
	// unit is one record, or a batch with its items.
	type unit struct {
		ids     []string
		created string
		ref     string
	}
	var settled []unit
	kept := 0
	inBatch := map[string]bool{}
	for _, l := range all {
		if l.rec != nil && l.rec.Kind == "batch" {
			for _, id := range l.rec.Items {
				if it, ok := byID[id]; ok && it.rec != nil && it.rec.BatchID != nil && *it.rec.BatchID == l.id {
					inBatch[id] = true
				}
			}
		}
	}
	for _, l := range all {
		switch {
		case l.corrupt:
			kept++
			continue
		case inBatch[l.id]:
			continue // pruned with its batch
		}
		u := unit{ids: []string{l.id}, created: l.rec.CreatedAt, ref: l.rec.CreatedAt}
		if l.rec.FinishedAt != nil {
			u.ref = *l.rec.FinishedAt
		}
		isOpen := open(l.rec)
		if l.rec.Kind == "batch" {
			// A batch's own status stays unknown when an item was unknown, and items are
			// resolved one by one: the batch is settled once it has stopped running and
			// every item is settled.
			st := effectiveStatus(l.rec, now)
			isOpen = st == "planned" || st == "dispatching"
			for _, id := range l.rec.Items {
				if !inBatch[id] {
					continue
				}
				u.ids = append(u.ids, id)
				it := byID[id].rec
				if open(it) {
					isOpen = true
				}
			}
		}
		if isOpen {
			kept += len(u.ids)
			continue
		}
		if t, ok := parseTime(u.ref); ok && t.Before(horizon) {
			for _, id := range u.ids {
				j.remove(id)
			}
			continue
		}
		settled = append(settled, u)
	}
	total := kept
	for _, u := range settled {
		total += len(u.ids)
	}
	if total <= maxRecords {
		return
	}
	sort.Slice(settled, func(i, k int) bool { return settled[i].created < settled[k].created })
	for _, u := range settled {
		if total <= maxRecords {
			break
		}
		for _, id := range u.ids {
			j.remove(id)
		}
		total -= len(u.ids)
	}
}

func (j *journal) remove(id string) {
	_ = os.Remove(j.recordPath(id))
	_ = os.Remove(filepath.Join(j.dir, id+".consumed"))
}
