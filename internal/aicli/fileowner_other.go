// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

//go:build !unix

package aicli

import "io/fs"

// ownedByCurrentUser has no owner to compare outside POSIX systems: on Windows the
// profile ACL protects the file (SEC-030), and fileTrustProblem skips the check.
func ownedByCurrentUser(fs.FileInfo) bool { return true }
