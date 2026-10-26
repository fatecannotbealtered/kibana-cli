// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

//go:build unix

package aicli

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether the running user owns the file (C4: a scope
// or settings file another user owns is not the user's grant).
func ownedByCurrentUser(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
