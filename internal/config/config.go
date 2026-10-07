package config

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/prometheus/common/model"
)

type staticRule struct {
	checker checks.RuleChecker
	name    string
}

type Config struct {
	CI         *CI                `hcl:"ci,block" json:"ci,omitzero"`
	Parser     *Parser            `hcl:"parser,block" json:"parser,omitzero"`
	Repository *Repository        `hcl:"repository,block" json:"repository,omitzero"`
	Discovery  *Discovery         `hcl:"discovery,block" json:"discovery,omitzero"`
	Checks     *Checks            `hcl:"checks,block" json:"checks,omitzero"`
	Owners     *Owners            `hcl:"owners,block" json:"owners,omitzero"`
	Prometheus []PrometheusConfig `hcl:"prometheus,block" json:"prometheus,omitempty"`
	Check      []Check            `hcl:"check,block" json:"check,omitempty"`
	Rules      []Rule             `hcl:"rule,block" json:"rules,omitempty"`

	staticRules []staticRule
}

func (cfg *Config) DisableOnlineChecks() {
	for _, name := range checks.OnlineChecks {
		var found bool
		if slices.Contains(cfg.Checks.Disabled, name) {
			found = true
		}
		if !found {
			cfg.Checks.Disabled = append(cfg.Checks.Disabled, name)
		}
	}
}

func (cfg *Config) SetDisabledChecks(l []string) {
	disabled := map[string]struct{}{}
	for _, s := range l {
		// add raw string: promql/series(prom)
		disabled[s] = struct{}{}
		// find any check name that matches string as regexp
		re := strictRegex(s)
		for _, name := range checks.CheckNames {
			if re.MatchString(name) {
				disabled[name] = struct{}{}
			}
		}
	}
	for name := range disabled {
		var found bool
		if slices.Contains(cfg.Checks.Disabled, name) {
			found = true
		}
		if !found {
			cfg.Checks.Disabled = append(cfg.Checks.Disabled, name)
		}
	}
}

func (cfg Config) String() string {
	content, _ := json.Marshal(
		cfg,
		jsontext.WithIndent("  "),
		jsontext.EscapeForHTML(true),
		json.Deterministic(true),
	)
	return string(content)
}

func (cfg *Config) GetChecksForEntry(ctx context.Context, gen *PrometheusGenerator, entry *discovery.Entry) []checks.RuleChecker {
	enabled := []checks.RuleChecker{}

	defaultStates := defaultMatchStates(commandFromContext(ctx))
	defaultMatch := []Match{{State: defaultStates}}
	proms := gen.ServersForPath(entry.Path.Name)

	parsedRules := make([]*parsedRule, 0, len(cfg.Rules))
	if entry.PathError != nil || entry.Rule.Error.Err != nil {
		check := checks.NewErrorCheck(entry)
		parsedRules = append(parsedRules, baseParsedRule(defaultMatch, check.Reporter(), check, nil))
	} else {
		parsedRules = append(parsedRules, baseRules(cfg.staticRules, proms, defaultMatch)...)
		for _, rule := range cfg.Rules {
			parsedRules = append(parsedRules, parseRule(rule, proms, defaultStates)...)
		}
	}
	for _, pr := range parsedRules {
		if !isMatch(ctx, entry, pr.ignore, pr.match) {
			continue
		}
		if pr.isEnabled(ctx, cfg.Checks.Enabled, cfg.Checks.Disabled, enabled, entry, cfg.Rules, pr.locked) {
			enabled = append(enabled, pr.check)
		}
	}

	slog.LogAttrs(
		ctx, slog.LevelDebug, "Configured checks for rule",
		slog.Any("enabled", func() []string {
			el := make([]string, 0, len(enabled))
			for _, e := range enabled {
				el = append(el, e.String())
			}
			return el
		}()),
		slog.String("path", entry.Path.Name),
		slog.String("rule", entry.Rule.Name()),
	)

	return enabled
}

func getContext() *hcl.EvalContext {
	vars := map[string]cty.Value{}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			vars["ENV_"+k] = cty.StringVal(v)
		}
	}
	return &hcl.EvalContext{Variables: vars}
}

func Load(path string, failOnMissing bool) (cfg Config, fromFile bool, err error) {
	cfg = Config{ // nolint: exhaustruct_v5
		CI: &CI{
			MaxCommits: 20,
			BaseBranch: "master",
		},
		Parser:     &Parser{},     // nolint: exhaustruct_v5
		Repository: &Repository{}, // nolint: exhaustruct_v5
		Checks: &Checks{
			Enabled:  checks.CheckNames,
			Disabled: []string{},
		},
		Rules: []Rule{},
		Owners: &Owners{
			Allowed: []string{},
		},
	}

	if _, err = os.Stat(path); err == nil || failOnMissing {
		fromFile = true
		slog.LogAttrs(context.Background(), slog.LevelInfo, "Loading configuration file", slog.String("path", path))
		ectx := getContext()
		err = hclsimple.DecodeFile(path, ectx, &cfg)
		if err != nil {
			return cfg, fromFile, err
		}
	}

	if cfg.CI != nil {
		if err = cfg.CI.validate(); err != nil {
			return cfg, fromFile, err
		}
	}

	if cfg.Owners != nil {
		if err = cfg.Owners.validate(); err != nil {
			return cfg, fromFile, err
		}
	}

	if err = cfg.Parser.validate(); err != nil {
		return cfg, fromFile, err
	}
	cfg.Parser.initOptions()

	if cfg.Repository != nil {
		if err = cfg.Repository.validate(); err != nil {
			return cfg, fromFile, err
		}
	}

	if cfg.Checks != nil {
		if err = cfg.Checks.validate(); err != nil {
			return cfg, fromFile, err
		}
	}

	for _, chk := range cfg.Check {
		if err = chk.validate(); err != nil {
			return cfg, fromFile, err
		}
	}

	promNames := make([]string, 0, len(cfg.Prometheus))
	for i, prom := range cfg.Prometheus {
		if err = prom.validate(); err != nil {
			return cfg, fromFile, err
		}

		if slices.Contains(promNames, prom.Name) {
			return cfg, fromFile, fmt.Errorf("prometheus server name must be unique, found two or more config blocks using %q name", prom.Name)
		}
		promNames = append(promNames, prom.Name)

		cfg.Prometheus[i].applyDefaults()

		if _, err = prom.TLS.toHTTPConfig(); err != nil {
			return cfg, fromFile, fmt.Errorf("invalid prometheus TLS configuration: %w", err)
		}
	}

	if cfg.Discovery != nil {
		if err = cfg.Discovery.validate(); err != nil {
			return cfg, fromFile, err
		}
	}

	if err = cfg.validateBackends(); err != nil {
		return cfg, fromFile, err
	}

	for i := range cfg.Rules {
		if err = cfg.Rules[i].validate(); err != nil {
			return cfg, fromFile, err
		}
	}

	cfg.staticRules = append(
		cfg.staticRules,
		staticRule{name: checks.SyntaxCheckName, checker: checks.NewSyntaxCheck()},
		staticRule{name: checks.AlertForCheckName, checker: checks.NewAlertsForCheck()},
		staticRule{name: checks.ComparisonCheckName, checker: checks.NewComparisonCheck()},
		staticRule{name: checks.TemplateCheckName, checker: checks.NewTemplateCheck()},
		staticRule{name: checks.FragileCheckName, checker: checks.NewFragileCheck()},
		staticRule{name: checks.RegexpCheckName, checker: checks.NewRegexpCheck()},
		staticRule{name: checks.RuleDependencyCheckName, checker: checks.NewRuleDependencyCheck()},
		staticRule{name: checks.ImpossibleCheckName, checker: checks.NewImpossibleCheck()},
		staticRule{name: checks.NaNCheckName, checker: checks.NewNaNCheck()},
		staticRule{name: checks.GroupIntervalCheckName, checker: checks.NewGroupIntervalCheck()},
	)

	return cfg, fromFile, nil
}

// validateBackends checks that the query language can be used with all configured servers.
func (cfg Config) validateBackends() error {
	type server struct {
		name, backend   string
		sampleRateLimit int
	}
	servers := make([]server, 0, len(cfg.Prometheus))
	for _, prom := range cfg.Prometheus {
		servers = append(servers, server{name: prom.Name, backend: prom.getBackend(), sampleRateLimit: prom.SampleRateLimit})
	}
	if cfg.Discovery != nil {
		for _, pt := range cfg.Discovery.templates() {
			servers = append(servers, server{name: pt.Name, backend: pt.getBackend(), sampleRateLimit: pt.SampleRateLimit})
		}
	}

	lang := cfg.Parser.getQueryLanguage()
	for _, s := range servers {
		switch {
		case lang == parser.MetricsQL.Name() && s.backend == promapi.BackendPrometheus:
			return fmt.Errorf(
				"prometheus server %q is using %q backend which doesn't support %s, set backend = %q or use query_language = %q",
				s.name, s.backend, lang, promapi.BackendVictoriaMetrics, parser.PromQL.Name(),
			)
		case lang == parser.PromQL.Name() && s.backend == promapi.BackendVictoriaMetrics:
			slog.LogAttrs(
				context.Background(), slog.LevelWarn,
				"VictoriaMetrics server is configured but rules will be parsed as PromQL, set query_language = \"metricsql\" in the parser block to allow MetricsQL syntax",
				slog.String("name", s.name),
			)
		}
		// sampleRateLimit needs the number of samples each query read, VictoriaMetrics
		// query statistics only have seriesFetched and executionTimeMsec.
		if s.backend == promapi.BackendVictoriaMetrics && s.sampleRateLimit > 0 {
			slog.LogAttrs(
				context.Background(), slog.LevelWarn,
				"sampleRateLimit has no effect on VictoriaMetrics servers, their query statistics don't include the number of samples read",
				slog.String("name", s.name),
			)
		}
	}
	return nil
}

func parseDuration(d string) (time.Duration, error) {
	mdur, err := model.ParseDuration(d)
	if err != nil {
		return 0, err
	}
	return time.Duration(mdur), nil
}

func commandFromContext(ctx context.Context) (cmd ContextCommandVal) {
	if val := ctx.Value(CommandKey); val != nil {
		cmd = val.(ContextCommandVal)
	}
	return cmd
}
