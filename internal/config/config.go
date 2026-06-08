package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config stores Kibana connection credentials.
// Host is the Kibana base URL (e.g. https://kibana.example.com), not the Discover UI path.
type Config struct {
	Host            string `json:"host"`
	KibanaVersion   string `json:"kibanaVersion,omitempty"`
	CredentialStore string `json:"credentialStore,omitempty"`
	CredentialKind  string `json:"credentialKind,omitempty"`
	Username        string `json:"username,omitempty"`
	Password        string `json:"password,omitempty"`
}

// userHomeDir is os.UserHomeDir in production; tests may override it.
var userHomeDir = os.UserHomeDir

// Dir returns ~/.kibana-cli/
func Dir() string {
	home, err := userHomeDir()
	if err != nil {
		return ".kibana-cli"
	}
	return filepath.Join(home, ".kibana-cli")
}

// FilePath returns ~/.kibana-cli/config.json
func FilePath() string {
	return filepath.Join(Dir(), "config.json")
}

func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if v := strings.TrimSpace(c); v != "" {
			return v
		}
	}
	return ""
}

// envAuthVars returns trimmed KIBANA_CLI_HOST, KIBANA_CLI_USER, KIBANA_CLI_PASSWORD.
// anySet is true when at least one of the three is non-empty.
func envAuthVars() (host, user, password string, anySet bool) {
	host = firstNonEmpty(os.Getenv("KIBANA_CLI_HOST"))
	user = firstNonEmpty(os.Getenv("KIBANA_CLI_USER"))
	password = firstNonEmpty(os.Getenv("KIBANA_CLI_PASSWORD"))
	anySet = host != "" || user != "" || password != ""
	return host, user, password, anySet
}

func validateEnvAuthOverride() error {
	host, user, password, anySet := envAuthVars()
	if !anySet {
		return nil
	}
	if host == "" || user == "" || password == "" {
		return errors.New("partial KIBANA_CLI_* auth env: set all of KIBANA_CLI_HOST, KIBANA_CLI_USER, and KIBANA_CLI_PASSWORD together, or unset all three")
	}
	return nil
}

// Load reads configuration: KIBANA_CLI_* env overrides file.
func Load() (*Config, error) {
	if err := validateEnvAuthOverride(); err != nil {
		return nil, err
	}
	cfg := &Config{}
	data, err := os.ReadFile(FilePath())
	if err == nil {
		if jsonErr := json.Unmarshal(data, cfg); jsonErr != nil {
			return nil, fmt.Errorf("parsing config %s: %w", FilePath(), jsonErr)
		}
		if cfg.Password != "" {
			return nil, fmt.Errorf("plaintext password in %s is not supported; run kibana-cli auth login or use KIBANA_CLI_PASSWORD", FilePath())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if host, user, password, anySet := envAuthVars(); anySet {
		cfg.Host = host
		cfg.Username = user
		cfg.Password = password
	}
	if v := firstNonEmpty(os.Getenv("KIBANA_CLI_KIBANA_VERSION")); v != "" {
		cfg.KibanaVersion = v
	}
	if cfg.usesKeyringStore() && cfg.secretsFromEnv() == "" {
		if err := loadSecretFromKeyring(cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// SaveOptions is reserved for future non-secret persistence options.
type SaveOptions struct{}

// Save persists configuration (keyring by default).
func Save(cfg *Config, _ SaveOptions) error {
	if !KeyringAvailable() {
		return errors.New("OS credential store unavailable; use environment variables or configure the OS credential store")
	}
	if err := saveSecretToKeyring(cfg); err != nil {
		return fmt.Errorf("saving to OS credential store: %w", err)
	}
	disk := cfg.onDiskCopy()
	if err := writeConfigFile(disk); err != nil {
		return err
	}
	return nil
}

var configJSONMarshal = func(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func writeConfigFile(cfg *Config) error {
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := configJSONMarshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(FilePath(), data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

func (c *Config) usesKeyringStore() bool {
	return strings.TrimSpace(c.CredentialStore) == CredentialStoreKeyring
}

func (c *Config) secretsFromEnv() string {
	if firstNonEmpty(os.Getenv("KIBANA_CLI_PASSWORD")) != "" {
		return "env"
	}
	return ""
}

func readDiskConfigOnly() (*Config, error) {
	data, err := os.ReadFile(FilePath())
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", FilePath(), err)
	}
	return &cfg, nil
}

// Delete removes config and keyring secrets.
func Delete() error {
	if cfg, err := readDiskConfigOnly(); err == nil {
		deleteKeyringSecrets(cfg)
	}
	err := os.Remove(FilePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting config: %w", err)
	}
	return nil
}

// IsConfigured reports whether Kibana host and credentials exist.
func IsConfigured() bool {
	cfg, err := Load()
	if err != nil {
		return false
	}
	return cfg.Host != "" && strings.TrimSpace(cfg.Username) != "" && cfg.Password != ""
}

// AuthSource returns env-cli / file / keyring / none.
func AuthSource() string {
	host, user, password, anySet := envAuthVars()
	if anySet && host != "" && user != "" && password != "" {
		return "env-cli"
	}
	if data, err := os.ReadFile(FilePath()); err == nil {
		var disk Config
		if json.Unmarshal(data, &disk) == nil && disk.usesKeyringStore() {
			return "keyring"
		}
		return "file"
	}
	return "none"
}

// CredentialStoreLabel describes where secrets are stored.
func CredentialStoreLabel(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	if cfg.secretsFromEnv() != "" {
		return "environment"
	}
	if cfg.usesKeyringStore() {
		return CredentialStoreKeyring
	}
	return ""
}

// MustLoad validates configuration for Kibana mode.
func MustLoad() (*Config, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	if cfg.Host == "" {
		return nil, errors.New("not configured: run 'kibana-cli auth login' or set KIBANA_CLI_HOST")
	}
	if strings.TrimSpace(cfg.Username) == "" || cfg.Password == "" {
		return nil, errors.New("not configured: set KIBANA_CLI_USER and KIBANA_CLI_PASSWORD or run auth login")
	}
	if err := ValidateKibanaHost(cfg.Host); err != nil {
		return nil, err
	}
	cfg.Host = strings.TrimRight(cfg.Host, "/")
	return cfg, nil
}

func loopbackHost(raw string) string {
	host := strings.TrimPrefix(raw, "http://")
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}

// AuthMode returns "basic" for status output.
func (c *Config) AuthMode() string {
	return "basic"
}
