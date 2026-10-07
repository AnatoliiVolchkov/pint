package config_test

import (
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/config"
)

func TestParseExternalLabels(t *testing.T) {
	type testCaseT struct {
		expected    map[string]string
		description string
		err         string
		items       []string
	}

	testCases := []testCaseT{
		{
			description: "no labels",
			items:       nil,
			expected:    map[string]string{},
		},
		{
			description: "single label",
			items:       []string{"account=alpha"},
			expected:    map[string]string{"account": "alpha"},
		},
		{
			description: "multiple labels",
			items:       []string{"account=alpha", "dc=test"},
			expected:    map[string]string{"account": "alpha", "dc": "test"},
		},
		{
			description: "spaces and empty items are ignored",
			items:       []string{" account = alpha ", "", " dc=test"},
			expected:    map[string]string{"account": "alpha", "dc": "test"},
		},
		{
			description: "value with equal sign",
			items:       []string{"filter=job=node"},
			expected:    map[string]string{"filter": "job=node"},
		},
		{
			description: "same label twice with the same value",
			items:       []string{"account=alpha", "account=alpha"},
			expected:    map[string]string{"account": "alpha"},
		},
		{
			description: "missing value",
			items:       []string{"account"},
			err:         `invalid external label "account", expected name=value, for example account=alpha`,
		},
		{
			description: "empty value",
			items:       []string{"account="},
			err:         `invalid external label "account=", expected name=value, for example account=alpha`,
		},
		{
			description: "empty name",
			items:       []string{"=alpha"},
			err:         `invalid external label "=alpha", expected name=value, for example account=alpha`,
		},
		{
			description: "invalid name",
			items:       []string{"bad-name=x"},
			err:         `invalid external label name "bad-name" in "bad-name=x"`,
		},
		{
			description: "same label twice with different values",
			items:       []string{"account=alpha", "account=beta"},
			err:         `external label "account" is set twice, to "alpha" and "beta"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			labels, err := config.ParseExternalLabels(tc.items)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, labels)
		})
	}
}

func TestSetExternalLabels(t *testing.T) {
	dir := t.TempDir()
	cfgPath := path.Join(dir, ".pint.hcl")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
prometheus "vm" {
  uri            = "http://vm:8428"
  backend        = "victoriametrics"
  externalLabels = { cluster = "demo", account = "from-config" }
}
prometheus "vm-empty" {
  uri     = "http://vm2:8428"
  backend = "victoriametrics"
}
prometheus "prom" {
  uri = "http://prometheus:9090"
}
discovery {
  filepath {
    directory = "/tmp"
    match     = "(?P<name>.+).yml"
    template {
      name    = "vm-{{ $name }}"
      uri     = "http://{{ $name }}:8428"
      backend = "victoriametrics"
    }
    template {
      name = "prom-{{ $name }}"
      uri  = "http://{{ $name }}:9090"
    }
  }
}
`), 0o644))

	cfg, _, err := config.Load(cfgPath, true)
	require.NoError(t, err)

	require.Equal(t, 0, cfg.SetExternalLabels(nil), "no labels must not change anything")
	require.Nil(t, cfg.Prometheus[1].ExternalLabels)

	updated := cfg.SetExternalLabels(map[string]string{"account": "alpha", "dc": "test"})
	require.Equal(t, 3, updated, "two VictoriaMetrics servers and one VictoriaMetrics discovery template")

	// Flag labels override labels with the same name from the config file.
	require.Equal(t, map[string]string{"cluster": "demo", "account": "alpha", "dc": "test"}, cfg.Prometheus[0].ExternalLabels)
	require.Equal(t, map[string]string{"account": "alpha", "dc": "test"}, cfg.Prometheus[1].ExternalLabels)
	// Prometheus servers expose their own external labels and are not changed.
	require.Nil(t, cfg.Prometheus[2].ExternalLabels)

	tmpls := cfg.Discovery.FilePath[0].Template
	require.Equal(t, map[string]string{"account": "alpha", "dc": "test"}, tmpls[0].ExternalLabels)
	require.Nil(t, tmpls[1].ExternalLabels)

	// Servers don't share label maps.
	cfg.Prometheus[1].ExternalLabels["only"] = "here"
	require.NotContains(t, cfg.Prometheus[0].ExternalLabels, "only")
	require.NotContains(t, tmpls[0].ExternalLabels, "only")
}

func TestDisableOnlineChecksWithVictoriaMetrics(t *testing.T) {
	dir := t.TempDir()
	cfgPath := path.Join(dir, ".pint.hcl")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
parser {
  query_language = "metricsql"
}
prometheus "vm" {
  uri     = "http://vm:8428"
  backend = "victoriametrics"
}
prometheus "vm2" {
  uri     = "http://vm2:8428"
  backend = "victoriametrics"
}
`), 0o644))

	cfg, _, err := config.Load(cfgPath, true)
	require.NoError(t, err)
	cfg.Prometheus = append(cfg.Prometheus, config.PrometheusConfig{Name: "prom", URI: "http://prometheus:9090"})

	cfg.DisableOnlineChecks()

	// External labels of VictoriaMetrics servers come from the config, these checks
	// still run for them offline and are only disabled for Prometheus servers.
	for _, name := range []string{checks.AlertsExternalLabelsCheckName, checks.LabelsConflictCheckName} {
		require.NotContains(t, cfg.Checks.Disabled, name)
		require.Contains(t, cfg.Checks.Disabled, name+"(prom)")
		require.NotContains(t, cfg.Checks.Disabled, name+"(vm)")
		require.NotContains(t, cfg.Checks.Disabled, name+"(vm2)")
	}
	for _, name := range checks.OnlineChecks {
		if checks.StaticOnVictoriaMetrics(name) {
			continue
		}
		require.Contains(t, cfg.Checks.Disabled, name)
	}
}
