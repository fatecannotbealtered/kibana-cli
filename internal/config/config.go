package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the resolved active connection — the single context selected for this
// invocation, merged with any environment overrides. It is an in-memory view, not
// the on-disk format (that is Store + ContextEntry).
type Config struct {
	Host            string
	Username        string
	Password        string
	CredentialStore string
	CredentialKind  string
	KibanaVersion   string

	// ContextName is the active context ("env" for the env-var override, "" when
	// nothing is configured). DefaultIndex / FieldMapFile come from the context.
	ContextName  string
	DefaultIndex string
	FieldMapFile string
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
	host, user, password, _ := envAuthVars()
	// Only KIBANA_CLI_HOST anchors an intended env auth override. A lone
	// KIBANA_CLI_USER / KIBANA_CLI_PASSWORD (commonly set to feed `context add`
	// or `auth login`) is not an auth attempt — it is ignored, not an error, so a
	// keyring-backed context still resolves in the same shell.
	if host == "" {
		return nil
	}
	if user == "" || password == "" {
		return errors.New("incomplete KIBANA_CLI_* auth env: KIBANA_CLI_HOST is set — also set KIBANA_CLI_USER and KIBANA_CLI_PASSWORD, or unset all three")
	}
	return nil
}

// envAuthComplete reports whether the full host+user+password triad is set — the
// only case that constitutes an anonymous env override.
func envAuthComplete() bool {
	host, user, password, _ := envAuthVars()
	return host != "" && user != "" && password != ""
}

// envContextName returns the KIBANA_CLI_CONTEXT override (selects a stored context).
func envContextName() string {
	return firstNonEmpty(os.Getenv("KIBANA_CLI_CONTEXT"))
}

// selectContextName resolves which stored context to use (highest first):
// explicit flag → KIBANA_CLI_CONTEXT → store.CurrentContext.
func selectContextName(contextFlag string, store *Store) string {
	if n := strings.TrimSpace(contextFlag); n != "" {
		return n
	}
	if n := envContextName(); n != "" {
		return n
	}
	if store != nil {
		return strings.TrimSpace(store.CurrentContext)
	}
	return ""
}

func applyVersionEnv(cfg *Config) {
	if v := firstNonEmpty(os.Getenv("KIBANA_CLI_KIBANA_VERSION")); v != "" {
		cfg.KibanaVersion = v
	}
}

// Load resolves the active context using the default selection precedence.
func Load() (*Config, error) {
	return LoadFor("")
}

// LoadFor resolves the active context. Precedence (highest first):
//  1. KIBANA_CLI_HOST/USER/PASSWORD triad — anonymous env context.
//  2. contextFlag (the --context value).
//  3. KIBANA_CLI_CONTEXT env.
//  4. store.CurrentContext.
//
// The flag and env only *select*; they never mutate the file.
func LoadFor(contextFlag string) (*Config, error) {
	if err := validateEnvAuthOverride(); err != nil {
		return nil, err
	}
	if envAuthComplete() {
		host, user, password, _ := envAuthVars()
		cfg := &Config{Host: host, Username: user, Password: password, ContextName: "env"}
		applyVersionEnv(cfg)
		return cfg, nil
	}
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	name := selectContextName(contextFlag, store)
	if name != "" {
		entry, ok := store.Contexts[name]
		if !ok {
			return nil, fmt.Errorf("unknown context %q: run 'kibana-cli context list'", name)
		}
		cfg.ContextName = name
		cfg.Host = entry.Host
		cfg.Username = entry.Username
		cfg.CredentialStore = entry.CredentialStore
		cfg.CredentialKind = entry.CredentialKind
		cfg.KibanaVersion = entry.KibanaVersion
		cfg.DefaultIndex = entry.DefaultIndex
		cfg.FieldMapFile = entry.FieldMapFile
		// A full env triad short-circuits above, so reaching here means env auth is
		// not in effect — a lone KIBANA_CLI_PASSWORD must not suppress the keyring.
		if cfg.usesKeyringStore() {
			if err := loadSecretFromKeyring(cfg); err != nil {
				return nil, err
			}
		}
	}
	applyVersionEnv(cfg)
	return cfg, nil
}

// SaveOptions is reserved for future non-secret persistence options.
type SaveOptions struct{}

// Save upserts cfg as a context (named by cfg.ContextName, default "default"),
// stores the password in the keyring, and makes it current if none is set.
func Save(cfg *Config, _ SaveOptions) error {
	if !KeyringAvailable() {
		return errors.New("OS credential store unavailable; use environment variables or configure the OS credential store")
	}
	if err := saveSecretToKeyring(cfg); err != nil {
		return fmt.Errorf("saving to OS credential store: %w", err)
	}
	store, err := LoadStore()
	if err != nil {
		return err
	}
	name := strings.TrimSpace(cfg.ContextName)
	if name == "" || name == "env" {
		name = "default"
	}
	if store.Contexts == nil {
		store.Contexts = map[string]*ContextEntry{}
	}
	store.Contexts[name] = cfg.toContextEntry()
	if strings.TrimSpace(store.CurrentContext) == "" {
		store.CurrentContext = name
	}
	return SaveStore(store)
}

var configJSONMarshal = func(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func (c *Config) usesKeyringStore() bool {
	return strings.TrimSpace(c.CredentialStore) == CredentialStoreKeyring
}

// SetCurrentContext switches the active context (the `context use` write).
func SetCurrentContext(name string) error {
	name = strings.TrimSpace(name)
	store, err := LoadStore()
	if err != nil {
		return err
	}
	if _, ok := store.Contexts[name]; !ok {
		return fmt.Errorf("unknown context %q: run 'kibana-cli context list'", name)
	}
	store.CurrentContext = name
	return SaveStore(store)
}

// RemoveContext deletes a context and its keyring secret; if it was current, the
// pointer falls back to any remaining context.
func RemoveContext(name string) error {
	name = strings.TrimSpace(name)
	store, err := LoadStore()
	if err != nil {
		return err
	}
	entry, ok := store.Contexts[name]
	if !ok {
		return fmt.Errorf("unknown context %q: run 'kibana-cli context list'", name)
	}
	deleteKeyringSecrets(&Config{Host: entry.Host, Username: entry.Username})
	delete(store.Contexts, name)
	if store.CurrentContext == name {
		store.CurrentContext = ""
		for n := range store.Contexts {
			store.CurrentContext = n
			break
		}
	}
	return SaveStore(store)
}

// Delete removes config.json and every context's keyring secret (full reset).
func Delete() error {
	if store, err := LoadStore(); err == nil {
		for _, entry := range store.Contexts {
			deleteKeyringSecrets(&Config{Host: entry.Host, Username: entry.Username})
		}
	}
	err := os.Remove(FilePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("deleting config: %w", err)
	}
	return nil
}

// IsConfigured reports whether the active context has host and credentials.
func IsConfigured() bool {
	cfg, err := Load()
	if err != nil {
		return false
	}
	return cfg.Host != "" && strings.TrimSpace(cfg.Username) != "" && cfg.Password != ""
}

// AuthSource returns env-cli / keyring / file / none for the active context.
func AuthSource() string {
	host, user, password, anySet := envAuthVars()
	if anySet && host != "" && user != "" && password != "" {
		return "env-cli"
	}
	store, err := LoadStore()
	if err != nil || len(store.Contexts) == 0 {
		return "none"
	}
	name := selectContextName("", store)
	entry, ok := store.Contexts[name]
	if !ok {
		return "none"
	}
	if entry.usesKeyringStore() {
		return "keyring"
	}
	return "file"
}

// CredentialStoreLabel describes where the active secret is stored.
func CredentialStoreLabel(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	if cfg.ContextName == "env" {
		return "environment"
	}
	if cfg.usesKeyringStore() {
		return CredentialStoreKeyring
	}
	return ""
}

// MustLoad validates the active context for Kibana mode.
func MustLoad() (*Config, error) {
	return MustLoadFor("")
}

// MustLoadFor resolves and validates a specific context selection.
func MustLoadFor(contextFlag string) (*Config, error) {
	cfg, err := LoadFor(contextFlag)
	if err != nil {
		return nil, err
	}
	if cfg.Host == "" {
		return nil, errors.New("not configured: run 'kibana-cli auth login' / 'kibana-cli context add' or set KIBANA_CLI_HOST")
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
