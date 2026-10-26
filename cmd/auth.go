package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// The auth group (CLI-101, SEC-040): `auth login` checks a Kibana user's password
// with Kibana, stores it in the OS keyring (service kibana-cli, account
// <host>|basic|<user>, the 1.x entry) and the host and user in the profile of
// settings.json; `auth status` checks the sign-in with Kibana; `auth logout` removes
// the keyring entry. Login and logout change the tool's own credentials, so they
// declare configure: dangerous by default and gated like any write (CLI-025). A
// person signs in; agents never handle a password (SEC-040), so the documented input
// is --password-file - (stdin) or KIBANA_CLI_PASSWORD.

const authEvidence = "simulated:cmd/auth_test.go"

func registerAuth(app *aicli.App) {
	group := addGroup(app, "auth", "Sign in to Kibana with a user and password, check the sign-in, and sign out")
	app.Schema("auth_login", aicli.SchemaDef{Shape: "object",
		Fields:    []string{"profile", "host", "user", "account", "credentials_source", "password_source", "settings_file"},
		Untrusted: []string{"account"}})
	app.Schema("auth_status", aicli.SchemaDef{Shape: "object",
		Fields:    []string{"profile", "host", "user", "account", "name", "roles", "credentials_source", "kibana_version", "kibana_version_source"},
		Untrusted: []string{"account", "name", "roles"}})
	app.Schema("auth_logout", aicli.SchemaDef{Shape: "object", Fields: []string{"profile", "host", "user", "password_stored"}})
	addAuthLogin(app, group)
	addAuthStatus(app, group)
	addAuthLogout(app, group)
}

// profileTarget is the scope key of a profile's credentials (SEC-012).
func profileTarget(profile string) string { return "profiles/" + profile }

// settingsProfileName is CLI-101's profile name; auth checks a requested name before
// the gate so a bad --profile fails at once (App.SetProfileSettings checks it again).
var settingsProfileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// authProfile is the profile auth login and logout act on: App.Profile, which for
// these Meta.AllowUnknownProfile commands is also a profile --profile or
// KIBANA_CLI_PROFILE names that settings.json lacks; "default" when there is no
// settings file. A settings file with profiles and none active needs --profile.
func authProfile(app *aicli.App, verb string) (string, error) {
	name, source := app.Profile()
	if name != "" {
		if (source == "flag" || source == "env") && !settingsProfileName.MatchString(name) {
			param := map[string]string{"flag": "--profile", "env": "KIBANA_CLI_PROFILE"}[source]
			e := aicli.Usage("invalid_value", param, "A profile name uses a-z, 0-9, _ and -, up to 64 characters.")
			e.Details["expected"] = "^[a-z0-9][a-z0-9_-]{0,63}$"
			return "", e
		}
		return name, nil
	}
	if _, _, e := app.Setting(nil, "host"); e != nil {
		return "", e
	}
	if len(app.Profiles()) > 0 {
		return "", noActiveProfile(verb)
	}
	return "default", nil
}

func addAuthLogin(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "login", RunE: func(cmd *cobra.Command, _ []string) error {
		profile, err := authProfile(app, "sign in to")
		if err != nil {
			return err
		}
		host, storedHost, err := loginHost(app, cmd)
		if err != nil {
			return err
		}
		user, storedUser, err := loginUser(app, cmd)
		if err != nil {
			return err
		}
		password, passwordSource, err := loginPassword(app, cmd, host, user)
		if err != nil {
			return err
		}
		var me *kibanaclient.AuthenticateUser // the account the password belongs to, checked by Preview
		key := credentialKey(host, user)
		return app.Write(cmd, aicli.WriteSpec{
			Target:   aicli.Target{Key: profileTarget(profile), Kind: "profile", Label: profile},
			ReadBack: "auth status --profile " + profile,
			Preview: func(ctx context.Context) (aicli.Preview, error) {
				client, err := clientFor(app, host, user, password)
				if err != nil {
					return aicli.Preview{}, err
				}
				if me, err = client.Authenticate(ctx); err != nil {
					return aicli.Preview{}, readError(err)
				}
				note := fmt.Sprintf("stored in the OS keyring as %s / %s, never in settings.json", keyringService, key)
				if passwordSource == "keyring_existing" {
					note = fmt.Sprintf("the password already stored in the OS keyring as %s / %s works and is kept; nothing is written to the keyring", keyringService, key)
				}
				return aicli.Preview{Action: fmt.Sprintf("Sign profile %s in to %s as %s", profile, host, user), CanApply: true,
					Changes: []aicli.FieldChange{
						{Field: "host", Before: orNil(storedHost), After: host},
						{Field: "user", Before: orNil(storedUser), After: user},
						{Field: "account", After: me.Username},
						{Field: "password", Note: note},
					}, Untrusted: []string{"changes[].after"}}, nil
			},
			Apply: func(ctx context.Context, op aicli.Op) (aicli.Result, error) {
				if me == nil { // the gate ran Preview at this confirm
					return aicli.Result{}, aicli.NotDispatched(errors.New("the password was not checked"))
				}
				writeSettings := storedHost != host || storedUser != user
				if err := storeLogin(app, profile, host, user, password, passwordSource != "keyring_existing", writeSettings); err != nil {
					return aicli.Result{}, err
				}
				// Read back: the profile's host and user from settings.json, and the
				// account the stored password signs in as, asked of Kibana again.
				obs := map[string]any{}
				if h, u, ok := profileHostUser(app, profile); ok {
					obs["host"], obs["user"] = h, u
				}
				if pw, err := readPassword(host, user); err == nil {
					app.AddSecret(pw)
					if client, err := clientFor(app, host, user, pw); err == nil {
						if again, err := client.Authenticate(ctx); err == nil {
							obs["account"] = again.Username
						}
					}
				}
				return aicli.Result{Observed: obs, Data: map[string]any{"profile": profile, "host": host, "user": user, "account": me.Username,
					"credentials_source": "keyring", "password_source": passwordSource, "settings_file": app.SettingsPath()},
					Untrusted: []string{"account"}}, nil
			},
		})
	}}
	c.Flags().String("host", "", "Kibana base URL, like https://kibana.example.com (default: the profile's host or KIBANA_CLI_HOST)")
	c.Flags().String("user", "", "Kibana user (default: the profile's user or KIBANA_CLI_USER)")
	c.Flags().String("password-file", "", "Read the password from this file, or - for stdin (the documented way)")
	c.Flags().String("password", "", "The password itself, for one-off use only: it shows in process lists; prefer --password-file - or KIBANA_CLI_PASSWORD")
	c.MarkFlagsMutuallyExclusive("password", "password-file")
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Check a Kibana user's password with Kibana, then store it in the OS keyring and the host and user in the profile",
		Effects: []aicli.Effect{aicli.Configure}, Output: "auth_login", AllowUnknownProfile: true,
		Secrets: []string{"password"}, FileInputs: map[string]string{"password-file": "password"},
		ErrorCodes: []string{"E_AUTH", "E_CONFIG", "E_FORBIDDEN", "E_IO", "E_NETWORK", "E_RATE_LIMITED", "E_SERVER", "E_TIMEOUT",
			"E_UNSUPPORTED", "E_VALIDATION"},
		Maturity: aicli.Experimental, Evidence: []string{authEvidence},
		Examples: []string{
			"kibana-cli auth login --host https://kibana.example.com --user alice --password-file - --dangerous --dry-run",
			"kibana-cli auth login --host https://kibana.example.com --user alice --password-file - --dangerous --confirm <confirm_token>",
		}})
}

// loginHost is the host auth login signs in to (--host, else the host setting) and
// the host the profile has in settings.json now ("" when none, or when
// KIBANA_CLI_HOST hides it). A new profile in an existing settings file needs --host.
func loginHost(app *aicli.App, cmd *cobra.Command) (host, stored string, err error) {
	raw, source, e := app.Setting(nil, "host")
	if e != nil {
		return "", "", e
	}
	if source == "profile" {
		stored = config.NormalizeHost(raw)
	}
	if cmd.Flags().Changed("host") {
		flag, _ := cmd.Flags().GetString("host")
		if err := config.ValidateKibanaHost(flag); err != nil {
			e := aicli.Validation("--host", "the Kibana base URL, like https://kibana.example.com (http only for localhost)", "--host is not a Kibana base URL; details.reason says why.")
			e.Details["reason"] = err.Error()
			e.Untrusted = []string{"reason"}
			return "", "", e
		}
		return config.NormalizeHost(flag), stored, nil
	}
	h, err := settingHost(app)
	if err != nil {
		return "", "", err
	}
	if h == "" {
		e := aicli.Usage("missing_parameter", "--host", "auth login needs the Kibana URL: --host.")
		e.Details["next_action"] = "Add --host <Kibana base URL>, like --host https://kibana.example.com."
		return "", "", e
	}
	return h, stored, nil
}

// loginUser is the user auth login signs in as (--user, else the user setting) and
// the user the profile has now.
func loginUser(app *aicli.App, cmd *cobra.Command) (user, stored string, err error) {
	raw, source, e := app.Setting(nil, "user")
	if e != nil {
		return "", "", e
	}
	if source == "profile" {
		stored = strings.TrimSpace(raw)
	}
	user = strings.TrimSpace(raw)
	if cmd.Flags().Changed("user") {
		user = flagString(cmd, "user")
	}
	if user == "" {
		e := aicli.Usage("missing_parameter", "--user", "auth login needs the Kibana user: --user.")
		e.Details["next_action"] = "Add --user <name>."
		return "", "", e
	}
	if strings.ContainsAny(user, "|\r\n") {
		return "", "", aicli.Validation("--user", "a Kibana user name without | or line breaks", "--user is not a usable user name.")
	}
	return user, stored, nil
}

// loginPassword is the password to check and store, and where it came from:
// --password-file (stdin with -), --password, KIBANA_CLI_PASSWORD (bound like the
// flag, as a keyed digest), or the keyring entry the host and user already have
// (a 1.x saved password, kept as it is).
func loginPassword(app *aicli.App, cmd *cobra.Command, host, user string) (string, string, error) {
	b, err := app.ReadFileInput(cmd, "password-file")
	switch {
	case err != nil:
		return "", "", err
	case b != nil:
		return checkPassword(strings.TrimRight(string(b), "\r\n"), "provided")
	case cmd.Flags().Changed("password"):
		pw, _ := cmd.Flags().GetString("password")
		return checkPassword(pw, "provided")
	}
	if env := app.Env("KIBANA_CLI_PASSWORD"); env != "" {
		if err := aicli.SetResolved(cmd, "password", env); err != nil {
			return "", "", err
		}
		return checkPassword(env, "environment")
	}
	pw, rerr := readPasswordFor(app, host, user)
	switch {
	case rerr == nil:
		return pw, "keyring_existing", nil
	case !errors.Is(rerr, errNoCredential):
		app.Debugf("keyring: %v", rerr)
	}
	e := aicli.Usage("missing_parameter", "--password-file", "auth login needs the password: --password-file - reads it from stdin.")
	e.Details["next_action"] = fmt.Sprintf("Ask the user to run `kibana-cli auth login --host %s --user %s --password-file -` in their own terminal and type the password there; never handle the password yourself.", host, user)
	return "", "", e
}

func checkPassword(pw, source string) (string, string, error) {
	if pw == "" {
		return "", "", aicli.Validation("--password", "the Kibana user's password", "The password is empty.")
	}
	return pw, source, nil
}

// storeLogin stores the password in the keyring (when writePassword), then the host
// and user in the profile (when writeSettings). If settings.json cannot be written
// the keyring entry is put back as it was, so the outcome is all or nothing; only a
// failed restore leaves it unknown.
func storeLogin(app *aicli.App, profile, host, user, password string, writePw, writeSettings bool) error {
	account := credentialKey(host, user)
	before, gerr := keyringBackend.Get(keyringService, account)
	existed := gerr == nil
	if writePw {
		if gerr != nil && !errors.Is(gerr, keyring.ErrNotFound) {
			return aicli.NotDispatched(ioFailure("The OS keyring cannot be read, so nothing was stored.",
				"Use KIBANA_CLI_PASSWORD in the environment instead (the keyring is unavailable here), or ask the user to unlock the keyring."))
		}
		if err := keyringBackend.Set(keyringService, account, password); err != nil {
			return aicli.NotDispatched(ioFailure("The OS keyring refused the password, so nothing was stored.",
				"Use KIBANA_CLI_PASSWORD in the environment instead (the keyring is unavailable here), or ask the user to unlock the keyring."))
		}
	}
	if !writeSettings {
		return nil
	}
	if e := app.SetProfileSettings(profile, map[string]string{"host": host, "user": user}); e != nil {
		if !writePw {
			return aicli.NotDispatched(e)
		}
		var rerr error
		if existed {
			rerr = keyringBackend.Set(keyringService, account, before)
		} else {
			rerr = keyringBackend.Delete(keyringService, account)
		}
		if rerr != nil {
			return fmt.Errorf("settings.json could not be written (%s) and the keyring entry could not be put back: %w", e.Message, rerr)
		}
		return aicli.NotDispatched(e)
	}
	return nil
}

// ioFailure is E_IO for a local file or keyring failure; nothing was sent.
func ioFailure(message, next string) *aicli.Error {
	return aicli.NewError("E_IO", message, map[string]any{"dispatch_state": "not_started", "next_action": next})
}

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func addAuthStatus(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, _ []string) error {
		cred, err := resolveCredentials(app)
		if err != nil {
			return err
		}
		client, err := clientFor(app, cred.Host, cred.User, cred.Password)
		if err != nil {
			return err
		}
		me, err := client.Authenticate(cmd.Context())
		if err != nil {
			return readError(err)
		}
		out := map[string]any{"profile": orNil(cred.Profile), "host": cred.Host, "user": cred.User, "account": me.Username,
			"roles": nonNilStrings(me.Roles), "credentials_source": cred.Source}
		if me.FullName != "" {
			out["name"] = me.FullName
		}
		// The version is optional: Kibana 8 and 9 hide it from accounts without
		// monitoring privileges, and no command needs it.
		v, source := client.VersionInfo(cmd.Context())
		out["kibana_version"], out["kibana_version_source"] = orNil(v), source
		return app.OK(cmd, out)
	}}
	parent.AddCommand(c)
	app.Register(c, aicli.Meta{
		Summary: "Check the sign-in with Kibana: the account, its roles, where the password came from, and the Kibana version when Kibana tells it",
		Effects: []aicli.Effect{aicli.Observe}, Output: "auth_status", ErrorCodes: kibanaReadCodes,
		Maturity: aicli.Verified, Evidence: []string{authEvidence, liveEvidenceSvc, liveEvidenceIoT},
		Examples: []string{"kibana-cli auth status", "kibana-cli auth status --profile prod"}})
}

func addAuthLogout(app *aicli.App, parent *cobra.Command) {
	c := &cobra.Command{Use: "logout", RunE: func(cmd *cobra.Command, _ []string) error {
		profile, err := authProfile(app, "sign out of")
		if err != nil {
			return err
		}
		host, user, err := logoutHostUser(app, cmd)
		if err != nil {
			return err
		}
		key := credentialKey(host, user)
		return app.Write(cmd, aicli.WriteSpec{
			Target:   aicli.Target{Key: profileTarget(profile), Kind: "profile", Label: profile},
			ReadBack: "auth status --profile " + profile,
			Preview: func(ctx context.Context) (aicli.Preview, error) {
				_, err := readPassword(host, user)
				switch {
				case errors.Is(err, errNoCredential):
					next := "Nothing to remove; `kibana-cli context` shows which credentials are in use."
					if app.Env("KIBANA_CLI_PASSWORD") != "" {
						next = "The password in use comes from KIBANA_CLI_PASSWORD; ask the user to unset it to stop using it."
					}
					return aicli.Preview{}, aicli.NotFound("No password is stored in the OS keyring for this host and user.", next)
				case err != nil:
					return aicli.Preview{}, ioFailure("The OS keyring cannot be read; nothing was changed.", "Ask the user to unlock the keyring, then run the command again.")
				}
				return aicli.Preview{Action: fmt.Sprintf("Remove the password of %s at %s from the OS keyring", user, host), CanApply: true,
					Changes: []aicli.FieldChange{{Field: "password_stored", Before: true, After: false,
						Note: fmt.Sprintf("the entry %s / %s; every profile with this host and user loses it; settings.json keeps the host and user", keyringService, key)}}}, nil
			},
			Apply: func(ctx context.Context, op aicli.Op) (aicli.Result, error) {
				switch err := deletePassword(host, user); {
				case errors.Is(err, errNoCredential):
					return aicli.Result{}, aicli.NotDispatched(aicli.NotFound("The password was already gone from the keyring; nothing was removed.", ""))
				case err != nil:
					return aicli.Result{}, aicli.NotDispatched(ioFailure("The OS keyring refused to remove the password; nothing was changed.",
						"Ask the user to unlock the keyring, then run --dry-run again."))
				}
				_, rerr := readPassword(host, user)
				stored := rerr == nil
				return aicli.Result{Observed: map[string]any{"password_stored": stored},
					Data: map[string]any{"profile": profile, "host": host, "user": user, "password_stored": stored}}, nil
			},
		})
	}}
	c.Flags().String("host", "", "Kibana base URL of the entry to remove (default: the profile's host)")
	c.Flags().String("user", "", "Kibana user of the entry to remove (default: the profile's user)")
	parent.AddCommand(c)
	// AllowUnknownProfile: logout also cleans up after a profile deleted from
	// settings.json (with --host and --user), which aicli would otherwise stop up front.
	app.Register(c, aicli.Meta{
		Summary: "Remove the stored password of the profile's host and user (or --host and --user) from the OS keyring",
		Effects: []aicli.Effect{aicli.Configure}, Output: "auth_logout", AllowUnknownProfile: true,
		ErrorCodes: []string{"E_CONFIG", "E_IO", "E_NOT_FOUND", "E_VALIDATION"},
		Maturity:   aicli.Experimental, Evidence: []string{authEvidence},
		Examples: []string{"kibana-cli auth logout --dangerous --dry-run", "kibana-cli auth logout --dangerous --confirm <confirm_token>"}})
}

// logoutHostUser is the keyring entry logout removes: --host and --user, else the
// host and user settings (KIBANA_CLI_HOST/USER, then the profile).
func logoutHostUser(app *aicli.App, cmd *cobra.Command) (string, string, error) {
	host := flagString(cmd, "host")
	if host != "" {
		if err := config.ValidateKibanaHost(host); err != nil {
			return "", "", aicli.Validation("--host", "the Kibana base URL, like https://kibana.example.com", "--host is not a Kibana base URL.")
		}
		host = config.NormalizeHost(host)
	} else {
		h, err := settingHost(app)
		if err != nil {
			return "", "", err
		}
		host = h
	}
	user := flagString(cmd, "user")
	if user == "" {
		u, _, e := app.Setting(nil, "user")
		if e != nil {
			return "", "", e
		}
		user = strings.TrimSpace(u)
	}
	switch {
	case host == "":
		e := aicli.Usage("missing_parameter", "--host", "No host is configured, so name the entry to remove with --host and --user.")
		e.Details["next_action"] = "Add --host <Kibana URL> --user <name>, or --profile <name> (`kibana-cli profile list`)."
		return "", "", e
	case user == "":
		e := aicli.Usage("missing_parameter", "--user", "No user is configured, so name the entry to remove with --user.")
		e.Details["next_action"] = "Add --user <name>."
		return "", "", e
	}
	return host, user, nil
}
