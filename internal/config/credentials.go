package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "kibana-cli"

	// CredentialStoreKeyring stores secrets in the OS credential manager.
	CredentialStoreKeyring = "keyring"
	// CredentialStoreFile stores secrets in config.json (discouraged).
	CredentialStoreFile = "file"
)

// credentialKey builds a stable keyring account name for host + username.
func credentialKey(host, username string) string {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	user := strings.TrimSpace(username)
	if user == "" {
		user = "default"
	}
	return host + "|basic|" + user
}

// KeyringAvailable reports whether the OS secret store accepts read/write.
func KeyringAvailable() bool {
	probe := "kibana-cli-probe"
	if err := keyring.Set(keyringService, probe, "ok"); err != nil {
		return false
	}
	_ = keyring.Delete(keyringService, probe)
	return true
}

func loadSecretFromKeyring(cfg *Config) error {
	if cfg.Host == "" {
		return errors.New("missing host in config")
	}
	key := credentialKey(cfg.Host, cfg.Username)
	secret, err := keyring.Get(keyringService, key)
	if err != nil {
		return fmt.Errorf("reading OS credential store: %w", err)
	}
	cfg.Password = secret
	return nil
}

func saveSecretToKeyring(cfg *Config) error {
	if cfg.Username == "" || cfg.Password == "" {
		return errors.New("missing username or password for keyring")
	}
	key := credentialKey(cfg.Host, cfg.Username)
	return keyring.Set(keyringService, key, cfg.Password)
}

func deleteKeyringSecrets(cfg *Config) {
	if cfg == nil || cfg.Host == "" {
		return
	}
	_ = keyring.Delete(keyringService, credentialKey(cfg.Host, cfg.Username))
}

// onDiskCopy returns config safe to write to config.json (no secrets when using keyring).
func (c *Config) onDiskCopy(store string) *Config {
	out := &Config{
		Host:            c.Host,
		KibanaVersion:   c.KibanaVersion,
		Username:        c.Username,
		CredentialStore: store,
	}
	if store == CredentialStoreFile {
		out.Password = c.Password
	} else {
		out.CredentialKind = "basic"
	}
	return out
}
