package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// The Kibana client (CLI-110): aicli's transport (TLS 1.2+, always verified, the
// system roots plus KIBANA_CLI_CA_FILE, HTTPS_PROXY and NO_PROXY) and User-Agent, a
// per-request timeout of at most 30 s (KIBANA_CLI_TIMEOUT lowers it) and reads that
// retry at most twice (internal/kibanaclient). Nothing turns TLS verification off.

// newClient builds the client of this run from the App's settings and credentials.
func newClient(app *aicli.App) (*kibanaclient.Client, kibanaCreds, error) {
	cred, err := resolveCredentials(app)
	if err != nil {
		return nil, kibanaCreds{}, err
	}
	c, err := clientFor(app, cred.Host, cred.User, cred.Password)
	return c, cred, err
}

// clientFor builds a client for an explicit host, user and password (auth login
// checks a password before it is stored).
func clientFor(app *aicli.App, host, user, password string) (*kibanaclient.Client, error) {
	tr, e := app.HTTPTransport()
	if e != nil {
		return nil, e
	}
	timeout, err := requestTimeout(app)
	if err != nil {
		return nil, err
	}
	ver, _, ve := app.Setting(nil, "kibana_version")
	if ve != nil {
		return nil, ve
	}
	return kibanaclient.New(kibanaclient.Config{Host: host, User: user, Password: password, KibanaVersion: strings.TrimSpace(ver),
		Transport: tr, Timeout: timeout, UserAgent: app.UserAgent(), Debugf: app.Debugf}), nil
}

// requestTimeout is the timeout setting: a duration (or a bare number of seconds,
// as 1.x took it) above 0 and at most 30 s.
func requestTimeout(app *aicli.App) (time.Duration, error) {
	raw, source, e := app.Setting(nil, "timeout")
	if e != nil {
		return 0, e
	}
	raw = strings.TrimSpace(raw)
	var d time.Duration
	var err error
	if n, nerr := strconv.Atoi(raw); nerr == nil {
		d = time.Duration(n) * time.Second
	} else {
		d, err = aicli.ParseDuration(raw)
	}
	if err != nil || d <= 0 || d > kibanaclient.MaxTimeout {
		where := map[string]string{"env": "KIBANA_CLI_TIMEOUT", "profile": "the active profile's timeout"}[source]
		if where == "" {
			where = "the timeout setting"
		}
		return 0, aicli.NewError("E_CONFIG", fmt.Sprintf("The request timeout from %s is not a duration between 1ms and 30s.", where), map[string]any{
			"next_action": "Ask the user to set KIBANA_CLI_TIMEOUT to a duration such as 10s, at most 30s, or to unset it.",
		})
	}
	return d, nil
}
