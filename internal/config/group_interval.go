package config

import "github.com/cloudflare/pint/internal/checks"

// setStaticChecksOffline stops static checks from reading server flags in offline mode.
// group/interval reads VictoriaMetrics flags that change how far back vmalert queries look,
// rule/vmalert counts rule results to compare them with the group limit.
func (cfg *Config) setStaticChecksOffline() {
	for i, sr := range cfg.staticRules {
		if gc, ok := sr.checker.(checks.GroupIntervalCheck); ok {
			cfg.staticRules[i].checker = gc.Offline()
		}
		if vc, ok := sr.checker.(checks.RuleVMAlertCheck); ok {
			cfg.staticRules[i].checker = vc.Offline()
		}
	}
}
