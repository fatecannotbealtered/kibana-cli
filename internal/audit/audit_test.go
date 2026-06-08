package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resetAuditGlobals(t *testing.T) {
	t.Helper()
	SetDirForTest("")
	lastCleanupDay = ""
}

func TestDir(t *testing.T) {
	resetAuditGlobals(t)
	tmp := t.TempDir()
	SetDirForTest(tmp)
	defer resetAuditGlobals(t)
	if got := Dir(); got != tmp {
		t.Fatalf("Dir() = %q want %q", got, tmp)
	}
}

func TestSanitizeArgs(t *testing.T) {
	got := sanitizeArgs([]string{
		"auth", "login",
		"--password", "secret",
		"-p=xyz",
		"--pass", "pval",
		"pass", "skip",
		"--pass=hidden",
	})
	joined := strings.Join(got, " ")
	for _, leak := range []string{"secret", "xyz", "pval", "hidden"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("secret leaked in %v", got)
		}
	}
	if !strings.Contains(joined, "login") {
		t.Fatalf("got %v", got)
	}
	if !strings.Contains(joined, "***") {
		t.Fatalf("expected redaction markers: %v", got)
	}
}

func TestRetentionMonths(t *testing.T) {
	cases := []struct {
		env  string
		want int
	}{
		{"", 3},
		{"not-a-number", 3},
		{"-2", 3},
		{"12", 12},
		{"0", 0},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			if tc.env == "" {
				_ = os.Unsetenv("KIBANA_AUDIT_RETENTION_MONTHS")
			} else {
				t.Setenv("KIBANA_AUDIT_RETENTION_MONTHS", tc.env)
			}
			if got := retentionMonths(); got != tc.want {
				t.Fatalf("retentionMonths() = %d want %d", got, tc.want)
			}
		})
	}
}

func TestLogDisabled(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	SetDirForTest(dir)
	t.Setenv("KIBANA_NO_AUDIT", "1")
	defer func() {
		_ = os.Unsetenv("KIBANA_NO_AUDIT")
		resetAuditGlobals(t)
	}()

	Log("cmd", []string{"a"}, 0, 1)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no audit files, got %d", len(entries))
	}
}

func TestLogWritesJSONL(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	SetDirForTest(dir)
	defer resetAuditGlobals(t)

	Log("auth login", []string{"--password", "x"}, 0, 10)
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"x"`) {
		t.Fatal("password should be redacted from audit")
	}
	var e entry
	if err := json.Unmarshal(data[:len(data)-1], &e); err != nil {
		t.Fatal(err)
	}
	if e.Cmd != "auth login" || e.Exit != 0 || e.Ms != 10 {
		t.Fatalf("entry %+v", e)
	}
	if !strings.HasSuffix(e.Ts, "Z") {
		t.Fatalf("audit timestamp must be UTC: %s", e.Ts)
	}
}

func TestLogMarshalFailure(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	SetDirForTest(dir)
	defer resetAuditGlobals(t)

	orig := jsonMarshal
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("marshal failed") }
	defer func() { jsonMarshal = orig }()

	Log("cmd", nil, 0, 1)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no audit file after marshal error, got %v", entries)
	}
}

func TestLogMkdirAllFailure(t *testing.T) {
	resetAuditGlobals(t)
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "file-not-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	SetDirForTest(blocker)
	defer resetAuditGlobals(t)

	Log("cmd", nil, 1, 5)
	if _, err := os.Stat(blocker); err != nil {
		t.Fatal(err)
	}
}

func TestLogOpenFileFailure(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "audit-"+time.Now().Format("2006-01")+".jsonl")
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	SetDirForTest(dir)
	defer resetAuditGlobals(t)

	Log("cmd", []string{"a"}, 0, 1)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() == filepath.Base(blocker) {
			return
		}
	}
	t.Fatalf("expected audit path to remain a directory, got %v", entries)
}

func TestCleanup(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "audit-2000-01.jsonl")
	if err := os.WriteFile(oldFile, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	keepFile := filepath.Join(dir, "audit-2099-12.jsonl")
	if err := os.WriteFile(keepFile, []byte("keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KIBANA_AUDIT_RETENTION_MONTHS", "3")
	cleanup(dir)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatal("expected old audit file removed")
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("expected recent audit file kept: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("non-audit file should remain")
	}
}

func TestCleanupRetentionZero(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "audit-2000-01.jsonl")
	if err := os.WriteFile(oldFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIBANA_AUDIT_RETENTION_MONTHS", "0")
	cleanup(dir)
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatal("retention 0 should skip cleanup")
	}
}

func TestCleanupReadDirError(t *testing.T) {
	resetAuditGlobals(t)
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIBANA_AUDIT_RETENTION_MONTHS", "3")
	cleanup(blocker)
}

func TestMaybeCleanupSkipsSameDay(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	lastCleanupDay = time.Now().Format("2006-01-02")
	oldFile := filepath.Join(dir, "audit-2000-01.jsonl")
	if err := os.WriteFile(oldFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIBANA_AUDIT_RETENTION_MONTHS", "3")
	maybeCleanup(dir)
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatal("same-day cleanup should not run")
	}
}

func TestMaybeCleanupRunsOncePerDay(t *testing.T) {
	resetAuditGlobals(t)
	dir := t.TempDir()
	lastCleanupDay = ""
	oldFile := filepath.Join(dir, "audit-2000-01.jsonl")
	if err := os.WriteFile(oldFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIBANA_AUDIT_RETENTION_MONTHS", "3")
	maybeCleanup(dir)
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatal("expected cleanup on new day")
	}
	if lastCleanupDay != time.Now().Format("2006-01-02") {
		t.Fatalf("lastCleanupDay = %q", lastCleanupDay)
	}
}

func TestFiles(t *testing.T) {
	resetAuditGlobals(t)
	missing := filepath.Join(t.TempDir(), "no-audit-dir")
	SetDirForTest(missing)
	defer resetAuditGlobals(t)

	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if files != nil {
		t.Fatalf("missing dir: got %v", files)
	}

	dir := t.TempDir()
	SetDirForTest(dir)
	b := filepath.Join(dir, "audit-b.jsonl")
	a := filepath.Join(dir, "audit-a.jsonl")
	for _, p := range []string{b, a} {
		if err := os.WriteFile(p, []byte("x\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0700); err != nil {
		t.Fatal(err)
	}

	files, err = Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != a || files[1] != b {
		t.Fatalf("files = %v", files)
	}
}

func TestFilesReadDirError(t *testing.T) {
	resetAuditGlobals(t)
	SetDirForTest(filepath.Join(t.TempDir(), string([]byte{0}), "audit"))
	defer resetAuditGlobals(t)

	if _, err := Files(); err == nil {
		t.Fatal("expected error for invalid audit path")
	}
}

func TestDirUsesConfigWhenNoOverride(t *testing.T) {
	resetAuditGlobals(t)
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	_ = os.Setenv("HOME", tmp)
	_ = os.Setenv("USERPROFILE", tmp)
	defer func() {
		_ = os.Setenv("HOME", origHome)
		_ = os.Setenv("USERPROFILE", origUserProfile)
		resetAuditGlobals(t)
	}()

	want := filepath.Join(tmp, ".kibana-cli", "audit")
	if got := Dir(); got != want {
		t.Fatalf("Dir() = %q want %q", got, want)
	}
}
