package config

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestCredentialKeyDefaultUser(t *testing.T) {
	got := credentialKey("https://kibana.example.com/", "  ")
	if got != "https://kibana.example.com|basic|default" {
		t.Fatalf("got %q", got)
	}
}

func TestKeyringAvailable(t *testing.T) {
	keyring.MockInit()
	if !KeyringAvailable() {
		t.Fatal("expected keyring available with mock")
	}
	keyring.MockInitWithError(errors.New("keyring down"))
	if KeyringAvailable() {
		t.Fatal("expected unavailable when mock returns error")
	}
	keyring.MockInit()
}

func TestLoadSecretFromKeyringErrors(t *testing.T) {
	keyring.MockInit()
	if err := loadSecretFromKeyring(&Config{}); err == nil {
		t.Fatal("expected missing host error")
	}
	keyring.MockInitWithError(errors.New("get failed"))
	if err := loadSecretFromKeyring(&Config{Host: "https://kibana.example.com", Username: "u"}); err == nil {
		t.Fatal("expected keyring get error")
	}
	keyring.MockInit()
}

func TestSaveSecretToKeyringMissingCreds(t *testing.T) {
	keyring.MockInit()
	if err := saveSecretToKeyring(&Config{Host: "https://kibana.example.com"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteKeyringSecrets(t *testing.T) {
	keyring.MockInit()
	deleteKeyringSecrets(nil)
	deleteKeyringSecrets(&Config{Username: "u"})
	cfg := &Config{Host: "https://kibana.example.com", Username: "ops", Password: "secret"}
	if err := saveSecretToKeyring(cfg); err != nil {
		t.Fatal(err)
	}
	deleteKeyringSecrets(cfg)
	if _, err := keyring.Get(keyringService, credentialKey(cfg.Host, cfg.Username)); err == nil {
		t.Fatal("expected secret removed")
	}
}

func TestOnDiskCopyKeyringStore(t *testing.T) {
	in := &Config{Host: "https://kibana.example.com", Username: "u", Password: "p", KibanaVersion: "8.0"}
	out := in.onDiskCopy()
	if out.Password != "" || out.CredentialKind != "basic" || out.CredentialStore != CredentialStoreKeyring {
		t.Fatalf("disk copy: %+v", out)
	}
}
