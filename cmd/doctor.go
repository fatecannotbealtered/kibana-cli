package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/aicli"
	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/fieldmap"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// doctorTimeout bounds each of doctor's requests to Kibana.
const doctorTimeout = 5 * time.Second

// kibanaDoctorChecks is App.DoctorChecks (CLI-036): connectivity (GET /api/status),
// credentials (the password signs in), console_proxy (Elasticsearch through the
// Console Proxy, which every log command uses) and kibana_version (the version and
// where it came from; unknown is not a problem). A network failure fails only
// connectivity; Kibana 9's internal-API restriction is named as what it is.
func kibanaDoctorChecks(ctx context.Context, app *aicli.App) []aicli.Check {
	cred, cerr := resolveCredentials(app)
	var ae *aicli.Error
	if cerr != nil && errors.As(cerr, &ae) && ae.Code == "E_CONFIG" {
		return []aicli.Check{{Check: "connectivity", Status: "warn", Message: "Not checked: " + ae.Message,
			Fix: signInHint("")}}
	}
	if cerr != nil && errors.As(cerr, &ae) && ae.Code == "E_USAGE" {
		return []aicli.Check{{Check: "connectivity", Status: "warn", Message: "Not checked: " + ae.Message,
			Fix: "Run `kibana-cli doctor --profile <name>` (`kibana-cli profile list` lists them)."}}
	}
	host := cred.Host
	if cerr != nil {
		host, _ = settingHost(app)
	}
	c, err := clientFor(app, host, cred.User, cred.Password)
	if err != nil {
		return []aicli.Check{{Check: "connectivity", Status: "fail", Message: errMessage(err), Fix: "Fix the configuration it names, then run `kibana-cli doctor` again."}}
	}
	cctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	start := time.Now()
	var checks []aicli.Check
	_, err = c.Status(cctx)
	var apiErr *kibanaclient.APIError
	switch {
	case err == nil:
		checks = append(checks, aicli.Check{Check: "connectivity", Status: "pass",
			Message: fmt.Sprintf("%s answered in %d ms.", host, time.Since(start).Milliseconds())})
	case errors.As(err, &apiErr):
		// Kibana answered: the connection works, whatever the status says.
		checks = append(checks, aicli.Check{Check: "connectivity", Status: "pass",
			Message: fmt.Sprintf("%s answered GET /api/status with HTTP %d.", host, apiErr.StatusCode)})
	default:
		return append(checks, connectivityFail(host, err))
	}
	if cerr != nil {
		return append(checks, aicli.Check{Check: "credentials", Status: "fail", Message: errMessage(cerr), Fix: signInHint(host)})
	}
	_, err = c.Authenticate(cctx)
	switch {
	case err == nil:
		checks = append(checks,
			aicli.Check{Check: "credentials", Status: "pass", Message: fmt.Sprintf("The password from the %s signs in.", sourceName(cred.Source))},
			aicli.Check{Check: "console_proxy", Status: "pass", Message: "Elasticsearch answers through Kibana's Console Proxy."},
			versionCheck(c.VersionInfo(cctx)))
	case errors.As(err, &apiErr) && apiErr.StatusCode == 401:
		checks = append(checks, aicli.Check{Check: "credentials", Status: "fail",
			Message: fmt.Sprintf("Kibana rejected the user and the password from the %s.", sourceName(cred.Source)),
			Fix:     fmt.Sprintf("Ask the user to run `kibana-cli auth login --host %s --user %s --password-file -` in their terminal, or to fix KIBANA_CLI_PASSWORD.", host, cred.User)})
	case errors.As(err, &apiErr) && apiErr.InternalOriginRefused:
		checks = append(checks, aicli.Check{Check: "console_proxy", Status: "fail",
			Message: "Kibana's internal-API restriction refused the Console Proxy because the x-elastic-internal-origin header did not arrive; the Console Proxy itself is not disabled.",
			Fix:     "kibana-cli sends `x-elastic-internal-origin: kibana`; ask the user to make the reverse proxy in front of Kibana pass that header through."})
	case errors.As(err, &apiErr) && apiErr.RouteMissing:
		checks = append(checks, aicli.Check{Check: "console_proxy", Status: "fail",
			Message: "Kibana has no Console Proxy at this address: the host is not Kibana's base URL, or the Console plugin is disabled.",
			Fix:     "Set the host to Kibana's base URL (no /app/... path), or ask the Kibana admin to enable the Console plugin."})
	case errors.As(err, &apiErr):
		checks = append(checks, aicli.Check{Check: "console_proxy", Status: "warn",
			Message: fmt.Sprintf("The Console Proxy answered HTTP %d to an identity check.", apiErr.StatusCode),
			Fix:     "Run `kibana-cli auth status` for the details."})
	default:
		checks = append(checks, connectivityFail(host, err))
	}
	return checks
}

func connectivityFail(host string, err error) aicli.Check {
	var ne *kibanaclient.NetworkError
	switch {
	case errors.As(err, &ne) && ne.TLS:
		return aicli.Check{Check: "connectivity", Status: "fail", Message: fmt.Sprintf("The TLS certificate of %s is not trusted.", host),
			Fix: "If Kibana uses a company CA, point KIBANA_CLI_CA_FILE at its PEM file, then run `kibana-cli doctor` again."}
	case errors.As(err, &ne) && ne.Timeout:
		return aicli.Check{Check: "connectivity", Status: "fail", Message: fmt.Sprintf("%s did not answer within %d s.", host, int(doctorTimeout.Seconds())),
			Fix: "Check the VPN, HTTPS_PROXY and NO_PROXY, then run `kibana-cli doctor` again."}
	}
	return aicli.Check{Check: "connectivity", Status: "fail", Message: fmt.Sprintf("%s could not be reached.", host),
		Fix: "Check the host setting, the VPN, HTTPS_PROXY and NO_PROXY, then run `kibana-cli doctor` again."}
}

func sourceName(source string) string {
	if source == "env" {
		return "KIBANA_CLI_PASSWORD variable"
	}
	return "OS keyring"
}

// versionSources names where kibanaclient.VersionInfo found the version.
var versionSources = map[string]string{
	kibanaclient.VersionFromSetting:       "the kibana_version setting",
	kibanaclient.VersionFromKibanaStatus:  "Kibana's GET /api/status",
	kibanaclient.VersionFromElasticsearch: "Elasticsearch's GET / through the Console Proxy (Kibana and Elasticsearch share their version)",
}

// versionCheck reports the Kibana version and its source. Unknown passes: Kibana 8
// and 9 hide the version from accounts without monitoring privileges, and no
// command needs it.
func versionCheck(version, source string) aicli.Check {
	if version == "" {
		return aicli.Check{Check: "kibana_version", Status: "pass",
			Message: "Unknown: Kibana hides it from this account and Elasticsearch did not say. No command needs it; set kibana_version to have it reported."}
	}
	return aicli.Check{Check: "kibana_version", Status: "pass", Message: fmt.Sprintf("Kibana %s, from %s.", version, versionSources[source])}
}

func errMessage(err error) string {
	var ae *aicli.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	return "The configuration cannot be resolved."
}

// kibanaContextExtra is App.ContextExtra (CLI-035): what only kibana-cli knows, from
// local state only: the Kibana version when it is configured (doctor and auth status
// ask Kibana), the field map in use, the default index and the index allowlist.
func kibanaContextExtra(app *aicli.App) map[string]any {
	out := map[string]any{"kibana_version": map[string]any{"value": nil, "source": kibanaclient.VersionUnknown}}
	if v, _, e := app.Setting(nil, "kibana_version"); e == nil && strings.TrimSpace(v) != "" {
		out["kibana_version"] = map[string]any{"value": strings.TrimSpace(v), "source": kibanaclient.VersionFromSetting}
	}
	if path, err := fieldMapPath(app, nil); err == nil {
		fm := map[string]any{"path": path, "exists": false, "profiles": []string{}}
		if _, serr := os.Stat(path); serr == nil {
			fm["exists"] = true
			if m, lerr := fieldmap.LoadFile(path); lerr == nil && m != nil {
				fm["profiles"] = sortedKeysOf(m.Profiles)
			}
		}
		out["field_map"] = fm
	}
	if v, _, e := app.Setting(nil, "default_index"); e == nil && strings.TrimSpace(v) != "" {
		out["default_index"] = strings.TrimSpace(v)
	}
	if v, _, e := app.Setting(nil, "allowed_index_prefixes"); e == nil {
		out["allowed_index_prefixes"] = nonNilStrings(config.ParseIndexPrefixes(v))
	}
	return out
}

// timeNow is the App's clock in UTC.
func timeNow(app *aicli.App) time.Time {
	if app.Now != nil {
		return app.Now().UTC()
	}
	return time.Now().UTC()
}
