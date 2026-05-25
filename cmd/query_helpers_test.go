package cmd

import (
	"errors"
	"testing"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
	"github.com/spf13/cobra"
)

func TestLoadFieldMapOrExit_InvalidYAML(t *testing.T) {
	home := setupTestHome(t)
	writeFieldMap(t, home, ":\n\t[")
	resetCLIState(t)
	jsonMode = true
	_, err := loadFieldMapOrExit()
	if !errors.Is(err, ErrSilent) || lastExit != ExitBadArgs {
		t.Fatalf("err=%v exit=%d", err, lastExit)
	}
}

func TestDataViewDryRunIndex(t *testing.T) {
	if got := dataViewDryRunIndex("abc"); got != "<data-view:abc>" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveIndexFromFlags_IndexOnly(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("index", "", "")
	cmd.Flags().String("data-view", "", "")
	_ = cmd.Flags().Set("index", "logs-*")
	idx, err := resolveIndexFromFlags(cmd, nil)
	if err != nil || idx != "logs-*" {
		t.Fatalf("idx=%q err=%v", idx, err)
	}
}

func TestResolveIndexFromFlags_DataViewResolve(t *testing.T) {
	srv := newMockKibanaServer()
	defer srv.Close()
	cfg := &config.Config{Host: srv.URL, Username: "u", Password: "p"}
	client := kibanaclient.NewClient(cfg)
	cmd := &cobra.Command{}
	cmd.Flags().String("index", "", "")
	cmd.Flags().String("data-view", "", "")
	_ = cmd.Flags().Set("data-view", "dv-1")
	idx, err := resolveIndexFromFlags(cmd, client)
	if err != nil || idx != "logs-*" {
		t.Fatalf("idx=%q err=%v", idx, err)
	}
}

func TestResolveIndexFromFlags_DataViewDryRun(t *testing.T) {
	resetCLIState(t)
	dryRun = true
	defer func() { dryRun = false }()
	t.Cleanup(func() { dryRun = false })
	cmd := &cobra.Command{}
	cmd.Flags().String("index", "", "")
	cmd.Flags().String("data-view", "", "")
	_ = cmd.Flags().Set("data-view", "dv-1")
	idx, err := resolveIndexFromFlags(cmd, nil)
	if err != nil || idx != "<data-view:dv-1>" {
		t.Fatalf("idx=%q err=%v", idx, err)
	}
}

func TestResolveIndexFromFlags_BothFlagsWarns(t *testing.T) {
	resetCLIState(t)
	srv := newMockKibanaServer()
	defer srv.Close()
	cfg := &config.Config{Host: srv.URL, Username: "u", Password: "p"}
	client := kibanaclient.NewClient(cfg)
	cmd := &cobra.Command{}
	cmd.Flags().String("index", "", "")
	cmd.Flags().String("data-view", "", "")
	_ = cmd.Flags().Set("index", "other-*")
	_ = cmd.Flags().Set("data-view", "dv-1")
	idx, err := resolveIndexFromFlags(cmd, client)
	if err != nil || idx != "logs-*" {
		t.Fatalf("idx=%q err=%v", idx, err)
	}
}

func TestResolveIndexFromFlags_APIError(t *testing.T) {
	resetCLIState(t)
	srv := newMockKibanaServerWith(mockKibanaOptions{IndexPatternFail: true, IndexPatternStatus: 404})
	defer srv.Close()
	cfg := &config.Config{Host: srv.URL, Username: "u", Password: "p"}
	client := kibanaclient.NewClient(cfg)
	cmd := &cobra.Command{}
	cmd.Flags().String("index", "", "")
	cmd.Flags().String("data-view", "", "")
	_ = cmd.Flags().Set("data-view", "missing")
	_, err := resolveIndexFromFlags(cmd, client)
	if err == nil {
		t.Fatal("expected API error")
	}
}
