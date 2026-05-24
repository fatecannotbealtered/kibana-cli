package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

type entry struct {
	Ts   string   `json:"ts"`
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
	Exit int      `json:"exit"`
	Ms   int64    `json:"ms"`
}

var testDir string
var cleanupMu sync.Mutex
var lastCleanupDay string

// SetDirForTest overrides the audit directory (tests only).
func SetDirForTest(dir string) { testDir = dir }

func Dir() string {
	if testDir != "" {
		return testDir
	}
	return filepath.Join(config.Dir(), "audit")
}

func Log(cmdPath string, args []string, exitCode int, durationMs int64) {
	if os.Getenv("KIBANA_NO_AUDIT") == "1" {
		return
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	maybeCleanup(dir)
	e := entry{
		Ts:   time.Now().Format(time.RFC3339Nano),
		Cmd:  cmdPath,
		Args: sanitizeArgs(args),
		Exit: exitCode,
		Ms:   durationMs,
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	path := filepath.Join(dir, "audit-"+time.Now().Format("2006-01")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	_, _ = f.Write(data)
	_ = f.Close()
}

func retentionMonths() int {
	s := os.Getenv("KIBANA_AUDIT_RETENTION_MONTHS")
	if s == "" {
		return 3
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 3
	}
	return n
}

func maybeCleanup(dir string) {
	cleanupMu.Lock()
	defer cleanupMu.Unlock()
	today := time.Now().Format("2006-01-02")
	if lastCleanupDay == today {
		return
	}
	lastCleanupDay = today
	cleanup(dir)
}

func cleanup(dir string) {
	months := retentionMonths()
	if months == 0 {
		return
	}
	cutoff := time.Now().AddDate(0, -months, 0).Format("2006-01")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "audit-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		ym := strings.TrimPrefix(name, "audit-")
		ym = strings.TrimSuffix(ym, ".jsonl")
		if ym < cutoff {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

var sensitiveFlags = map[string]bool{
	"password": true,
	"p":        true,
	"pass":     true,
}

func sanitizeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		lower := strings.ToLower(arg)
		if strings.Contains(lower, "=") {
			flag := strings.TrimPrefix(strings.TrimPrefix(strings.SplitN(lower, "=", 2)[0], "--"), "-")
			if sensitiveFlags[flag] {
				parts := strings.SplitN(arg, "=", 2)
				out = append(out, parts[0]+"=***")
				continue
			}
		}
		stripped := strings.TrimPrefix(strings.TrimPrefix(lower, "--"), "-")
		if sensitiveFlags[stripped] {
			i++
			continue
		}
		out = append(out, arg)
	}
	return out
}

func Files() ([]string, error) {
	dir := Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}
