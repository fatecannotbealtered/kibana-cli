package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAgentContract_NotConfigured(t *testing.T) {
	home := setupTestHomeDir(t)
	bin := buildCLIBinary(t)
	cmd := exec.Command(bin, "context", "--json")
	cmd.Env = filteredEnv(home, map[string]string{
		"KIBANA_CLI_HOST":     "",
		"KIBANA_CLI_USER":     "",
		"KIBANA_CLI_PASSWORD": "",
	})
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if exitCode != ExitAuth {
		t.Fatalf("exit %d want %d: %s", exitCode, ExitAuth, out)
	}
	assertAgentJSON(t, string(out), false, AgentStatusNotConfigured, ExitAuth)
}

func filteredEnv(home string, overrides map[string]string) []string {
	skip := map[string]bool{
		"KIBANA_CLI_HOST": true, "KIBANA_CLI_USER": true, "KIBANA_CLI_PASSWORD": true,
		"KIBANA_CLI_KIBANA_VERSION": true, "KIBANA_CLI_INSECURE": true,
		"KIBANA_CLI_TIMEOUT": true, "KIBANA_CLI_ALLOWED_INDEX_PREFIXES": true,
		"HOME": true, "USERPROFILE": true,
	}
	var env []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if skip[key] {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "HOME="+home, "USERPROFILE="+home)
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}
