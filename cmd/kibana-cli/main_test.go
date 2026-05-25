package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/cmd"
)

func TestRun_Version(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"kibana-cli", "--version"}
	if code := run(); code != 0 {
		t.Fatalf("exit %d want 0", code)
	}
}

func TestRun_ErrSilentUsesLastExitCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"kibana-cli", "search", "--index", "", "--json"}
	if code := run(); code != cmd.ExitBadArgs {
		t.Fatalf("exit %d want %d", code, cmd.ExitBadArgs)
	}
}

func TestRun_NonSilentError(t *testing.T) {
	origRunCLI := runCLI
	runCLI = func(context.Context) error { return errors.New("boom") }
	defer func() { runCLI = origRunCLI }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origErr := os.Stderr
	os.Stderr = w
	code := run()
	_ = w.Close()
	os.Stderr = origErr
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	if code != cmd.ExitBadArgs {
		t.Fatalf("exit %d want %d", code, cmd.ExitBadArgs)
	}
	if !bytes.Contains(buf.Bytes(), []byte("boom")) {
		t.Fatalf("stderr=%q", buf.String())
	}
}

func TestMainDelegatesToCmdExecute(t *testing.T) {
	if runCLI == nil {
		t.Fatal("runCLI must delegate to cmd.ExecuteContext")
	}
}

func TestMainEntry(t *testing.T) {
	origArgs := os.Args
	origExit := exitFn
	defer func() {
		os.Args = origArgs
		exitFn = origExit
	}()
	os.Args = []string{"kibana-cli", "--version"}
	var gotCode int
	exitFn = func(code int) { gotCode = code }
	main()
	if gotCode != 0 {
		t.Fatalf("main exit code %d want 0", gotCode)
	}
}
