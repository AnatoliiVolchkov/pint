package config

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
)

// validateVMAlertURI checks the vmalertURI option of a rendered server config.
func validateVMAlertURI(uri string) error {
	if uri == "" {
		return nil
	}
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("vmalertURI %q is invalid: %w", uri, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("vmalertURI %q is invalid, expected an http or https URI like http://vmalert:8880", uri)
	}
	return nil
}

// LoadVMAlert reads flags and rule files of vmalert for every VictoriaMetrics server with
// vmalertURI, so checks use its query step and external labels. Servers whose vmalert can't
// be queried keep the settings from pint config, checks that query the server configuration
// report the error.
func (pg *PrometheusGenerator) LoadVMAlert(ctx context.Context) {
	for _, server := range pg.servers {
		if err := server.LoadVMAlert(ctx); err != nil {
			slog.LogAttrs(ctx, slog.LevelWarn, "Cannot read vmalert flags and rules, using settings from pint config",
				slog.String("name", server.Name()), slog.String("uri", server.VMAlertURI()), slog.Any("err", err))
		}
	}
}
