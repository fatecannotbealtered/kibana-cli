package output

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func Test_isTerminal(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		f, err := os.CreateTemp("", "output-term-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if isTerminal(f) {
			t.Fatal("temp file should not be a terminal")
		}
	})

	t.Run("stat error", func(t *testing.T) {
		bad := os.NewFile(^uintptr(99), "invalid-fd")
		if bad == nil {
			t.Skip("could not create bad file handle")
		}
		if isTerminal(bad) {
			t.Fatal("expected false on stat error")
		}
	})

	t.Run("console device", func(t *testing.T) {
		var path string
		switch runtime.GOOS {
		case "windows":
			path = "CONOUT$"
		case "darwin", "linux", "freebsd", "openbsd":
			path = "/dev/tty"
		default:
			t.Skip("no console path for", runtime.GOOS)
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Skip("console not available:", err)
		}
		defer f.Close()
		if !isTerminal(f) {
			t.Skip("opened device is not reported as char device on this platform")
		}
	})
}

func Test_colorize_and_formatters(t *testing.T) {
	old := noColor
	t.Cleanup(func() { noColor = old })

	noColor = true
	if got := colorize(ansiRed, "plain"); got != "plain" {
		t.Fatalf("noColor: got %q", got)
	}
	if FormatCyanBold("x") != "x" || FormatGray("y") != "y" {
		t.Fatal("format helpers should pass through without color")
	}

	noColor = false
	colored := colorize(ansiGreen, "hi")
	if colored == "hi" || !strings.Contains(colored, ansiGreen) || !strings.Contains(colored, ansiReset) {
		t.Fatalf("expected ANSI wrap: %q", colored)
	}
	if FormatCyanBold("z") == "z" {
		t.Fatal("expected cyan bold formatting")
	}
	if FormatGray("g") == "g" {
		t.Fatal("expected gray formatting")
	}
}

func TestQuietAndPrintFuncs(t *testing.T) {
	oldQuiet := Quiet
	oldNoColor := noColor
	t.Cleanup(func() {
		Quiet = oldQuiet
		noColor = oldNoColor
	})
	noColor = true

	Quiet = true
	if out := captureStdout(func() {
		Success("ok")
		Info("info")
		Bold("bold")
		Gray("gray")
	}); out != "" {
		t.Fatalf("quiet stdout: %q", out)
	}

	Quiet = false
	if out := captureStdout(func() { Success("ok") }); !strings.Contains(out, "ok") {
		t.Fatalf("success: %q", out)
	}
	if out := captureStdout(func() { Info("info") }); !strings.Contains(out, "info") {
		t.Fatalf("info: %q", out)
	}
	if out := captureStdout(func() { Bold("bold") }); !strings.Contains(out, "bold") {
		t.Fatalf("bold: %q", out)
	}
	if out := captureStdout(func() { Gray("gray") }); !strings.Contains(out, "gray") {
		t.Fatalf("gray: %q", out)
	}

	if errOut := captureStderr(func() { Error("err") }); !strings.Contains(errOut, "err") {
		t.Fatalf("error: %q", errOut)
	}
	if errOut := captureStderr(func() { Warn("warn") }); !strings.Contains(errOut, "warn") {
		t.Fatalf("warn: %q", errOut)
	}
}
