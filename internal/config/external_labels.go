package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/promapi"
)

// ParseExternalLabels parses external labels passed on the command line, the same
// labels that are set on vmalert with -external.label flags. Every item must be a
// name=value pair, for example account=alpha. Items are trimmed and empty items are
// ignored, so "account=alpha, dc=test" split on commas works too.
func ParseExternalLabels(items []string) (map[string]string, error) {
	labels := map[string]string{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, value, ok := strings.Cut(item, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("invalid external label %q, expected name=value, for example account=alpha", item)
		}
		if !model.LegacyValidation.IsValidLabelName(name) {
			return nil, fmt.Errorf("invalid external label name %q in %q", name, item)
		}
		if prev, ok := labels[name]; ok && prev != value {
			return nil, fmt.Errorf("external label %q is set twice, to %q and %q", name, prev, value)
		}
		labels[name] = value
	}
	return labels, nil
}

// disableOnlyForPrometheus disables an online check only for Prometheus servers if it doesn't
// query VictoriaMetrics servers, it then still runs for them with --offline. Offline mode
// doesn't run discovery, so all servers are static. VictoriaMetrics servers with vmalertURI
// are disabled too, external labels set on vmalert can't be read offline. It returns false
// for other checks and for configs without VictoriaMetrics servers, which are handled
// exactly like upstream.
func (cfg *Config) disableOnlyForPrometheus(name string) bool {
	if !checks.StaticOnVictoriaMetrics(name) {
		return false
	}
	// Without VictoriaMetrics servers disable it like upstream does.
	if !slices.ContainsFunc(cfg.Prometheus, func(p PrometheusConfig) bool {
		return p.getBackend() == promapi.BackendVictoriaMetrics
	}) {
		return false
	}
	for _, prom := range cfg.Prometheus {
		if prom.getBackend() == promapi.BackendVictoriaMetrics && prom.VMAlertURI == "" {
			continue
		}
		instance := fmt.Sprintf("%s(%s)", name, prom.Name)
		if !slices.Contains(cfg.Checks.Disabled, instance) {
			cfg.Checks.Disabled = append(cfg.Checks.Disabled, instance)
		}
	}
	return true
}

// SetExternalLabels adds labels to externalLabels of every VictoriaMetrics server and
// discovery template, overriding labels with the same name set in the config file.
// Prometheus servers expose their own external labels via the configuration API and
// are not changed. It returns the number of updated servers and templates.
func (cfg *Config) SetExternalLabels(labels map[string]string) (updated int) {
	if len(labels) == 0 {
		return 0
	}
	merge := func(dst map[string]string) map[string]string {
		merged := make(map[string]string, len(dst)+len(labels))
		maps.Copy(merged, dst)
		maps.Copy(merged, labels)
		return merged
	}

	for i := range cfg.Prometheus {
		if cfg.Prometheus[i].getBackend() != promapi.BackendVictoriaMetrics {
			continue
		}
		cfg.Prometheus[i].ExternalLabels = merge(cfg.Prometheus[i].ExternalLabels)
		updated++
	}

	if cfg.Discovery != nil {
		updateTemplates := func(tmpls []PrometheusTemplate) {
			for i := range tmpls {
				if tmpls[i].getBackend() != promapi.BackendVictoriaMetrics {
					continue
				}
				tmpls[i].ExternalLabels = merge(tmpls[i].ExternalLabels)
				updated++
			}
		}
		for i := range cfg.Discovery.FilePath {
			updateTemplates(cfg.Discovery.FilePath[i].Template)
		}
		for i := range cfg.Discovery.PrometheusQuery {
			updateTemplates(cfg.Discovery.PrometheusQuery[i].Template)
		}
	}

	return updated
}
