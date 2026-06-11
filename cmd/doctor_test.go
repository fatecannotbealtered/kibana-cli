package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestDoctor_Help(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"doctor", "--help"})
	_ = rootCmd.Execute()
	rootCmd.SetOut(nil)
	if f := doctorCmd.Flags().Lookup("help"); f != nil {
		_ = f.Value.Set("false")
	}
	if !strings.Contains(buf.String(), "doctor") {
		t.Fatal("missing doctor in help")
	}
}

func TestDoctor_NoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("KIBANA_CLI_HOST", "")
	t.Setenv("KIBANA_CLI_USER", "")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0
	rootCmd.SetArgs([]string{"doctor"})
	_ = rootCmd.Execute()
	if lastExit != ExitAuth {
		t.Fatalf("expected exit %d, got %d", ExitAuth, lastExit)
	}
}

func TestDoctor_WithMockServer(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	t.Setenv("KIBANA_CLI_HOST", srv.URL)
	t.Setenv("KIBANA_CLI_USER", "ops")
	t.Setenv("KIBANA_CLI_PASSWORD", "secret")
	origExit := lastExit
	defer func() { lastExit = origExit }()
	lastExit = 0
	rootCmd.SetArgs([]string{"doctor"})
	_ = rootCmd.Execute()
	if lastExit != ExitOK {
		t.Fatalf("expected exit 0, got %d", lastExit)
	}
}

func TestDoctor_NoConfig_JSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("KIBANA_CLI_HOST", "")
	origJM := jsonMode
	origExit := lastExit
	defer func() { jsonMode = origJM; lastExit = origExit }()
	lastExit = 0
	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"doctor", "--json"})
		_ = rootCmd.Execute()
	})
	if !strings.Contains(out, `"configExists"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestDoctorVersionCheck(t *testing.T) {
	if !versionMeetsMin("1.1.0", "1.1.0") {
		t.Fatal("expected equal version to pass")
	}
	if versionMeetsMin("1.0.9", "1.1.0") {
		t.Fatal("expected older version to fail")
	}
	result := &doctorResult{Version: "1.0.9", SkillMinVersion: "1.1.0", SecurityTier: securityTier}
	checks := buildDoctorChecks(result)
	if len(checks) == 0 || checks[0].Check != "version" || checks[0].Status != "fail" {
		t.Fatalf("expected failing version check: %+v", checks)
	}
	foundReleaseReadiness := false
	for _, check := range checks {
		if check.Check == "release_readiness" {
			foundReleaseReadiness = true
			if check.Status != "warn" {
				t.Fatalf("release_readiness check = %+v", check)
			}
		}
	}
	if !foundReleaseReadiness {
		t.Fatalf("missing release_readiness check: %+v", checks)
	}
}
