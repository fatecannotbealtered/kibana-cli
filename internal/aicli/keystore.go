// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The state store (SEC-030): <state>/confirm.key (32 random bytes), the operation
// journal under <state>/operations, the default scope file and the update-notice
// cache. A broken store never degrades to "no check": a missing, corrupt, linked or
// unreadable key or directory means no token and no write (CLI-055).

const keySize = 32

var errKeyExists = errors.New("confirm key already exists")

// stateDirPath resolves the state directory: App.StateDir, else <PREFIX>_STATE_DIR,
// else ~/.<Name>. It does not create anything.
func (a *App) stateDirPath() (string, *Error) {
	dir := a.StateDir
	if dir == "" {
		dir = a.env(a.envPrefix() + "_STATE_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", ioError("The home directory is unknown, so the state directory cannot be found; nothing was sent.",
				fmt.Sprintf("Set %s_STATE_DIR to a private directory.", a.envPrefix()), err)
		}
		dir = filepath.Join(home, "."+a.toolName())
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", ioError("The state directory path is invalid; nothing was sent.", "", err)
	}
	a.debugOnce("state directory: %s", abs)
	return abs, nil
}

// ensureStateDir creates the state directory (0700) when missing and refuses one
// that is a link, not a directory, or open to other users on POSIX systems.
func (a *App) ensureStateDir() (string, *Error) {
	dir, e := a.stateDirPath()
	if e != nil {
		return "", e
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
			return "", ioError("The state directory cannot be created; nothing was sent.",
				fmt.Sprintf("Check that %s can be created, or set %s_STATE_DIR.", dir, a.envPrefix()), mkErr)
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return "", ioError("The state directory cannot be read; nothing was sent.", fmt.Sprintf("Check %s.", dir), err)
	}
	if e := checkStateDirInfo(dir, info); e != nil {
		return "", e
	}
	return dir, nil
}

// checkStateDirInfo refuses linked, non-directory or (POSIX) shared state dirs.
func checkStateDirInfo(dir string, info fs.FileInfo) *Error {
	mode := info.Mode()
	if mode&fs.ModeSymlink != 0 || mode&fs.ModeIrregular != 0 {
		return ioError("The state directory is a link or reparse point; the tool refuses writes.",
			fmt.Sprintf("Replace %s with a real directory owned by you.", dir), nil)
	}
	if !info.IsDir() {
		return ioError("The state directory path is not a directory; the tool refuses writes.",
			fmt.Sprintf("Ask the user to move %s aside so the tool can create its state directory.", dir), nil)
	}
	if runtime.GOOS != "windows" && mode.Perm()&0o077 != 0 {
		return ioError("The state directory is accessible to other users; the tool refuses writes.",
			fmt.Sprintf("Run `chmod 700 %s`.", dir), nil)
	}
	return nil
}

// inspectStateDir reports the state directory without creating it (context, doctor).
func (a *App) inspectStateDir() (dir string, available bool, problem string) {
	dir, e := a.stateDirPath()
	if e != nil {
		return "", false, e.Message
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return dir, true, ""
	}
	if err != nil {
		return dir, false, err.Error()
	}
	if e := checkStateDirInfo(dir, info); e != nil {
		return dir, false, e.Message
	}
	if _, _, e := readKey(filepath.Join(dir, "confirm.key")); e != nil {
		return dir, false, e.Message
	}
	ops := filepath.Join(dir, "operations")
	if oi, err := os.Lstat(ops); err == nil {
		if oi.Mode()&fs.ModeSymlink != 0 || !oi.IsDir() {
			return dir, false, "the operations journal path is not a directory"
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return dir, false, err.Error()
	}
	return dir, true, ""
}

// protection names how the state directory dir is protected
// (context.state.protection, SEC-030, decision 30). On POSIX the library creates and
// verifies 0700/0600. On Windows a directory inside the user profile inherits the
// profile's owner-only ACL; anywhere else nothing was verified, so the honest value
// is windows-unverified (doctor warns). The library sets no explicit ACL of its
// own, so it never claims windows-explicit-acl. context and doctor share this.
func protection(dir string) string {
	if runtime.GOOS != "windows" {
		return "posix-0700"
	}
	if dir != "" && insideProfile(dir) {
		return "windows-profile-acl"
	}
	return "windows-unverified"
}

// insideProfile reports whether dir is inside the user's home directory (Windows
// relies on the profile ACL there).
func insideProfile(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(home), strings.ToLower(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// loadKey returns the 32-byte confirm key from <state>/confirm.key. With create it
// makes a new key when none exists (exclusive create, 0600); without create a missing
// key returns nil. A key that exists but is not exactly 32 readable bytes in a
// regular file is E_IO: the tool never falls back to an unkeyed digest.
func (a *App) loadKey(stateDir string, create bool) ([]byte, *Error) {
	path := filepath.Join(stateDir, "confirm.key")
	for attempt := 0; attempt < 5; attempt++ {
		key, exists, e := readKey(path)
		if e != nil {
			return nil, e
		}
		if exists {
			return key, nil
		}
		if !create {
			return nil, nil
		}
		key, err := createKey(stateDir, path)
		if errors.Is(err, errKeyExists) {
			continue
		}
		if err != nil {
			return nil, ioError("The confirm key cannot be created; no token was issued and nothing was sent.",
				fmt.Sprintf("Check that %s is writable.", stateDir), err)
		}
		return key, nil
	}
	return nil, ioError("The confirm key could not be read after it was created; nothing was sent.", "Run the command again.", nil)
}

// readKey reads and validates the key file. exists is false only when it is absent.
func readKey(path string) (key []byte, exists bool, e *Error) {
	broken := func(why string, err error) *Error {
		return ioError("The confirm key "+why+"; the tool refuses writes until it is fixed.",
			fmt.Sprintf("Delete %s so the tool creates a new key; tokens issued before become invalid.", path), err)
	}
	for attempt := 0; ; attempt++ {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, broken("cannot be read", err)
		}
		mode := info.Mode()
		if mode&fs.ModeSymlink != 0 || mode&fs.ModeIrregular != 0 || !mode.IsRegular() {
			return nil, true, broken("is not a regular file", nil)
		}
		if runtime.GOOS != "windows" && mode.Perm()&0o077 != 0 {
			return nil, true, ioError("The confirm key is readable by other users; the tool refuses writes.",
				fmt.Sprintf("Run `chmod 600 %s`, or delete it so the tool creates a new key.", path), nil)
		}
		if info.Size() != keySize {
			// A concurrent creator on a file system without hard links may still be
			// writing; give it a moment before failing closed.
			if info.Size() < keySize && attempt < 3 && time.Since(info.ModTime()) < 2*time.Second {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return nil, true, broken("has the wrong size", nil)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, true, broken("cannot be read", err)
		}
		if len(b) != keySize {
			return nil, true, broken("has the wrong size", nil)
		}
		return b, true, nil
	}
}

// createKey writes a new random key to a temporary file and links it into place, so
// a reader never sees a partial key and two creators cannot both win.
func createKey(dir, path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".confirm.key-*.tmp")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpName, 0o600); err != nil {
			return nil, err
		}
	}
	linkErr := os.Link(tmpName, path)
	if linkErr == nil {
		syncDir(dir)
		return key, nil
	}
	if errors.Is(linkErr, fs.ErrExist) {
		return nil, errKeyExists
	}
	// Hard links unsupported here: fall back to an exclusive create.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, errKeyExists
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	syncDir(dir)
	return key, nil
}

// syncDir flushes a directory entry change to disk where the platform supports it.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
