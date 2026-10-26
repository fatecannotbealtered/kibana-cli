package cmd

import (
	"strings"
	"testing"
)

// TestProfileList: the profiles of settings.json with their host, user and default
// index, the default and the active one; never a secret, never the keyring.
func TestProfileList(t *testing.T) {
	e := newTestEnv(t)
	r := e.run("profile", "list")
	if r.exit != 0 || len(r.items()) != 0 || num(r.data()["total"]) != 0 {
		t.Fatalf("no settings file: %s", r.out)
	}
	e.writeSettings(`{"default_profile": "prod", "profiles": {"prod": {"host": "https://kibana.example.com/", "user": "alice", "default_index": "app-log-*"}, "test": {"host": "https://kibana-test.example.com", "user": "bob"}}}`)
	r = e.run("profiles", "ls", "--profile", "test")
	items := r.items()
	if r.exit != 0 || len(items) != 2 || items[0]["ref"] != "profiles/prod" || items[0]["host"] != "https://kibana.example.com" ||
		items[0]["default"] != true || items[0]["active"] != false || items[1]["active"] != true || items[0]["default_index"] != "app-log-*" {
		t.Fatalf("profiles: %s", r.out)
	}
	if strings.Contains(r.out, "password") || testKeyring.Writes() != 0 {
		t.Fatalf("no secret in the list: %s", r.out)
	}
	e.writeSettings(`{"profiles": {"prod": {"password": "x"}}}`)
	if r = e.run("profile", "list"); r.code() != "E_CONFIG" {
		t.Fatalf("a secret in settings.json: %s", r.out)
	}
}
