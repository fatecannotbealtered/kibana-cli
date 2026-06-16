package cmd

import (
	"strings"
	"time"

	"github.com/fatecannotbealtered/kibana-cli/internal/config"
	"github.com/fatecannotbealtered/kibana-cli/internal/kibanaclient"
)

// bootstrapOutcome is the shared doctor/context validation result.
type bootstrapOutcome struct {
	AgentStatus
	ConfigExists    bool
	AuthValid       bool
	LatencyMs       int64
	Host            string
	AuthMode        string
	Username        string
	KibanaVersion   string
	SearchReachable bool
	SearchError     string
	ConfigError     string
	AuthError       string
	ContextName     string
}

func runBootstrapCheck() (*bootstrapOutcome, error) {
	out := &bootstrapOutcome{}
	cfg, err := config.LoadFor(contextName)
	if err != nil {
		out.AgentStatus = agentConfigError(err.Error())
		out.ConfigError = err.Error()
		applyAgentExit(out.AgentStatus)
		return out, nil
	}
	out.ConfigExists = cfg.Host != "" && strings.TrimSpace(cfg.Username) != "" && cfg.Password != ""
	out.Host = cfg.Host
	out.AuthMode = cfg.AuthMode()
	out.ContextName = cfg.ContextName
	if !out.ConfigExists {
		out.AgentStatus = agentNotConfigured()
		applyAgentExit(out.AgentStatus)
		return out, nil
	}
	initClientOptionsFromEnv()
	validCfg, err := config.MustLoadFor(contextName)
	if err != nil {
		msg := err.Error()
		out.AgentStatus = agentConfigErrorDetail(msg)
		out.ConfigError = msg
		applyAgentExit(out.AgentStatus)
		return out, nil
	}
	client := kibanaclient.NewClient(validCfg)
	start := time.Now()
	vr, err := client.Validate(apiCtx())
	out.LatencyMs = time.Since(start).Milliseconds()
	if err != nil || !vr.Valid {
		detail := "authentication failed"
		if err != nil {
			detail = err.Error()
		}
		out.AgentStatus = agentAuthFailed(detail)
		out.AuthValid = false
		out.AuthError = out.Message
		applyAgentExit(out.AgentStatus)
		return out, nil
	}
	out.AuthValid = true
	out.Username = vr.Username
	out.KibanaVersion = vr.KibanaVersion
	out.SearchReachable = vr.SearchReachable
	out.SearchError = vr.SearchError
	if vr.SearchReachable {
		out.AgentStatus = agentReady(vr.Username, vr.KibanaVersion)
	} else {
		out.AgentStatus = agentSearchUnavailable(vr.Username, vr.KibanaVersion, vr.SearchError, vr.SearchStatusCode)
	}
	applyAgentExit(out.AgentStatus)
	return out, nil
}

func applyBootstrapToContext(out *bootstrapOutcome, k *contextKibana) {
	k.Configured = out.ConfigExists
	k.Source = config.AuthSource()
	if out.Host != "" {
		k.Host = out.Host
		k.AuthMode = out.AuthMode
		k.KibanaVersion = out.KibanaVersion
	}
	k.Authenticated = out.AuthValid
	k.Username = out.Username
	if out.AuthValid {
		k.KibanaVersion = out.KibanaVersion
	}
	k.SearchReachable = out.SearchReachable
	k.SearchError = out.SearchError
	if out.AuthError != "" {
		k.AuthError = out.AuthError
	}
}
