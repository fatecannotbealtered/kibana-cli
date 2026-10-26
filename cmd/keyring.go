package cmd

import (
	"errors"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

// Credentials at rest (SEC-040): the password of a Kibana account is one OS keyring
// entry (Windows Credential Manager, macOS Keychain, Linux Secret Service), service
// "kibana-cli", account "<host>|basic|<user>", holding the password itself. That is
// the entry kibana-cli 1.x wrote, so a 1.x user's saved password keeps working once
// a profile names the same host and user. The settings file holds no secret.

const keyringService = "kibana-cli"

// credentialBackend is where passwords are kept. Production uses the OS keyring;
// tests swap in a memory store (cmd/main_test.go) that counts writes.
type credentialBackend interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (osKeyring) Set(service, user, secret string) error   { return keyring.Set(service, user, secret) }
func (osKeyring) Delete(service, user string) error        { return keyring.Delete(service, user) }

// keyringBackend is the credential store in use (a test seam, not configuration).
var keyringBackend credentialBackend = osKeyring{}

// errNoCredential is a host and user with no keyring entry.
var errNoCredential = errors.New("no keyring entry")

// credentialKey is the keyring account of a host and user, as 1.x built it: the
// host without a trailing slash, "|basic|", the user ("default" when empty).
func credentialKey(host, user string) string {
	user = strings.TrimSpace(user)
	if user == "" {
		user = "default"
	}
	return config.NormalizeHost(host) + "|basic|" + user
}

// readPassword returns the stored password of a host and user: errNoCredential
// when there is none, another error when the keyring cannot be read.
func readPassword(host, user string) (string, error) {
	pw, err := keyringBackend.Get(keyringService, credentialKey(host, user))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errNoCredential
	}
	if err != nil {
		return "", err
	}
	if pw == "" {
		return "", errNoCredential
	}
	return pw, nil
}

// readPasswordFor is readPassword that registers the password with App.AddSecret, so
// debug lines and errors show it as *** (CLI-102): aicli redacts the secret settings
// it resolves, and a keyring password is not one of them.
func readPasswordFor(app *aicli.App, host, user string) (string, error) {
	pw, err := readPassword(host, user)
	if err == nil {
		app.AddSecret(pw)
	}
	return pw, err
}

func writePassword(host, user, password string) error {
	return keyringBackend.Set(keyringService, credentialKey(host, user), password)
}

// deletePassword removes a stored password; a missing entry is errNoCredential.
func deletePassword(host, user string) error {
	err := keyringBackend.Delete(keyringService, credentialKey(host, user))
	if errors.Is(err, keyring.ErrNotFound) {
		return errNoCredential
	}
	return err
}
