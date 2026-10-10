package config

import (
	"slices"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/promapi"
)

// knownCheckNames returns names of all checks, including the vmalert only ones.
func knownCheckNames() []string {
	return slices.Concat(checks.CheckNames, checks.VMAlertCheckNames)
}

// withVMAlertChecks adds vmalert only checks to the default list of enabled checks of a
// MetricsQL config. A list of enabled checks set in the config file is kept as it is.
func (cfg *Config) withVMAlertChecks() {
	if cfg.Parser.getQueryLanguage() != metricsql.Name || !slices.Equal(cfg.Checks.Enabled, checks.CheckNames) {
		return
	}
	cfg.Checks.Enabled = knownCheckNames()
}

// vmalertStaticRules returns static checks that only apply to rules evaluated by vmalert.
// They are only registered with `query_language = "metricsql"`, so configs for Prometheus
// enable exactly the checks upstream pint enables.
func (cfg Config) vmalertStaticRules() []staticRule {
	if cfg.Parser.getQueryLanguage() != metricsql.Name {
		return nil
	}
	return []staticRule{
		{name: checks.RuleVMAlertCheckName, checker: checks.NewRuleVMAlertCheck()},
	}
}

// IsMetricsQL returns true if rules are parsed as MetricsQL, they are evaluated by vmalert.
func (cfg Config) IsMetricsQL() bool {
	return cfg.Parser != nil && cfg.Parser.getQueryLanguage() == metricsql.Name
}

// VictoriaMetricsServerNames returns names of servers using the VictoriaMetrics backend.
func (pg *PrometheusGenerator) VictoriaMetricsServerNames() (names []string) {
	for _, server := range pg.servers {
		if server.Backend() == promapi.BackendVictoriaMetrics {
			names = append(names, server.Name())
		}
	}
	return names
}
