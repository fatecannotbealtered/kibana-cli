package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// StoreSchemaVersion is the on-disk config.json layout version.
const StoreSchemaVersion = 2

// ContextEntry is one named connection target (system) on disk.
// Secrets never live here — the password is stored in the OS keyring, keyed by
// host+username (see credentialKey). DefaultIndex / FieldMapFile let each system
// carry its own index pattern and field-map override.
type ContextEntry struct {
	Host            string `json:"host"`
	Username        string `json:"username,omitempty"`
	CredentialStore string `json:"credentialStore,omitempty"`
	CredentialKind  string `json:"credentialKind,omitempty"`
	DefaultIndex    string `json:"defaultIndex,omitempty"`
	FieldMapFile    string `json:"fieldMapFile,omitempty"`
	KibanaVersion   string `json:"kibanaVersion,omitempty"`
}

// Store is the full on-disk config.json: a set of named contexts plus a pointer
// to the active one.
type Store struct {
	SchemaVersion  int                      `json:"schemaVersion"`
	CurrentContext string                   `json:"currentContext,omitempty"`
	Contexts       map[string]*ContextEntry `json:"contexts,omitempty"`
}

func (e *ContextEntry) usesKeyringStore() bool {
	return e != nil && strings.TrimSpace(e.CredentialStore) == CredentialStoreKeyring
}

// ActiveMeta returns the non-secret metadata (default index, field-map file) of
// the selected context without touching the keyring. Both empty when the active
// connection comes from environment variables or nothing is configured.
func ActiveMeta(contextFlag string) (defaultIndex, fieldMapFile string) {
	// Only a full env triad replaces the stored context; a lone KIBANA_CLI_PASSWORD
	// must not suppress the context's defaultIndex / fieldMapFile.
	if envAuthComplete() {
		return "", ""
	}
	store, err := LoadStore()
	if err != nil {
		return "", ""
	}
	if entry, ok := store.Contexts[selectContextName(contextFlag, store)]; ok {
		return entry.DefaultIndex, entry.FieldMapFile
	}
	return "", ""
}

// LoadStore reads config.json. A missing file yields an empty store, not an error.
func LoadStore() (*Store, error) {
	data, err := os.ReadFile(FilePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Store{SchemaVersion: StoreSchemaVersion, Contexts: map[string]*ContextEntry{}}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	// Reject a hand-edited plaintext password before trusting the file: ContextEntry
	// has no password field, so a present "password" key signals a mis-edit.
	if strings.Contains(string(data), `"password"`) {
		return nil, fmt.Errorf("plaintext password in %s is not supported; run 'kibana-cli auth login' or use KIBANA_CLI_PASSWORD", FilePath())
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", FilePath(), err)
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = StoreSchemaVersion
	}
	if s.Contexts == nil {
		s.Contexts = map[string]*ContextEntry{}
	}
	return &s, nil
}

// SaveStore writes config.json with 0600 perms under a 0700 dir.
func SaveStore(s *Store) error {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = StoreSchemaVersion
	}
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := configJSONMarshal(s)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(FilePath(), data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}
