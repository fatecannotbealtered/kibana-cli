package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/config"
)

// kibanaCreds is what a request runs as: the Kibana host, the user and password,
// where the password came from ("env" or "keyring") and the active profile.
type kibanaCreds struct {
	Host, User, Password, Source, Profile string
}

// signInHint is the next step when no credentials are configured. Agents never
// handle a password (SEC-040): the user signs in.
func signInHint(host string) string {
	if host == "" {
		host = "<Kibana URL>"
	}
	return fmt.Sprintf("Ask the user to run `kibana-cli auth login --host %s --user <name> --password-file -` in their own terminal, or to set KIBANA_CLI_HOST, KIBANA_CLI_USER and KIBANA_CLI_PASSWORD; never handle the password yourself.", host)
}

// resolveCredentials resolves the host (KIBANA_CLI_HOST, then the active profile),
// the user (KIBANA_CLI_USER, then the profile) and the password: KIBANA_CLI_PASSWORD,
// else the keyring entry of that host and user. A settings file with several
// profiles and none active needs --profile.
func resolveCredentials(app *aicli.App) (kibanaCreds, error) {
	profile, _ := app.Profile()
	host, err := settingHost(app)
	if err != nil {
		return kibanaCreds{}, err
	}
	user, _, e := app.Setting(nil, "user")
	if e != nil {
		return kibanaCreds{}, e
	}
	user = strings.TrimSpace(user)
	if host == "" || user == "" {
		if profile == "" && len(app.Profiles()) > 0 {
			return kibanaCreds{}, noActiveProfile("use")
		}
		missing := "host"
		if host != "" {
			missing = "user"
		}
		return kibanaCreds{}, aicli.NewError("E_CONFIG", fmt.Sprintf("Kibana is not configured: no %s is set.", missing),
			map[string]any{"next_action": signInHint(host)})
	}
	if pw, src, e := app.Setting(nil, "password"); e != nil {
		return kibanaCreds{}, e
	} else if pw != "" && src == "env" {
		return kibanaCreds{Host: host, User: user, Password: pw, Source: "env", Profile: profile}, nil
	}
	pw, rerr := readPasswordFor(app, host, user)
	switch {
	case errors.Is(rerr, errNoCredential):
		return kibanaCreds{}, aicli.NewError("E_AUTH", "No password is configured for this Kibana user: KIBANA_CLI_PASSWORD is not set and the OS keyring has no entry for the host and user.",
			map[string]any{"next_action": signInHint(host)})
	case rerr != nil:
		app.Debugf("keyring: %v", rerr)
		return kibanaCreds{}, aicli.NewError("E_AUTH", "No password is available: KIBANA_CLI_PASSWORD is not set and the OS keyring could not be read.",
			map[string]any{"next_action": "Ask the user to unlock the OS keyring, or to set KIBANA_CLI_PASSWORD in the environment."})
	}
	return kibanaCreds{Host: host, User: user, Password: pw, Source: "keyring", Profile: profile}, nil
}

// noActiveProfile is E_USAGE for a settings file with profiles but no active one:
// several profiles and no default_profile, as a person may write it.
func noActiveProfile(verb string) *aicli.Error {
	e := aicli.Usage("missing_parameter", "--profile", fmt.Sprintf("No profile is active (settings.json has several profiles and no default_profile), so name the profile to %s.", verb))
	e.Details["next_action"] = "Add --profile <name> (`kibana-cli profile list` lists them), or ask the user to set default_profile in settings.json."
	return e
}

// settingHost is the host setting, checked by config.ValidateKibanaHost and without
// a trailing slash; "" when none is set.
func settingHost(app *aicli.App) (string, error) {
	raw, source, e := app.Setting(nil, "host")
	if e != nil {
		return "", e
	}
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	if err := config.ValidateKibanaHost(raw); err != nil {
		where := map[string]string{"env": "KIBANA_CLI_HOST", "profile": "the active profile's host"}[source]
		if where == "" {
			where = "the host setting"
		}
		e := aicli.NewError("E_CONFIG", fmt.Sprintf("The Kibana host from %s is not a Kibana base URL; details.reason says why.", where), map[string]any{
			"reason":      err.Error(),
			"next_action": "Ask the user to set it to the Kibana base URL only, like https://kibana.example.com (no /app/discover path, no user:password@).",
		})
		e.Untrusted = []string{"reason"}
		return "", e
	}
	return config.NormalizeHost(raw), nil
}

// kibanaIdentity is App.Identity (CLI-035, CLI-054): the account and host a command
// acts as, from local state only. The account is the configured user once a password
// is available for it (KIBANA_CLI_PASSWORD or the keyring entry); otherwise none.
func kibanaIdentity(app *aicli.App) (aicli.Identity, error) {
	raw, _, e := app.Setting(nil, "host")
	if e != nil {
		return aicli.Identity{}, e
	}
	host := config.NormalizeHost(raw)
	id := aicli.Identity{Host: host, Extra: map[string]string{"credentials_source": "none"}}
	user, _, _ := app.Setting(nil, "user")
	user = strings.TrimSpace(user)
	if host == "" || user == "" {
		return id, nil
	}
	if pw, src, _ := app.Setting(nil, "password"); pw != "" && src == "env" {
		id.Account, id.Extra["credentials_source"] = user, "env"
		return id, nil
	}
	if _, err := readPasswordFor(app, host, user); err == nil {
		id.Account, id.Extra["credentials_source"] = user, "keyring"
	}
	return id, nil
}
