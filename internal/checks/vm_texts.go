package checks

import (
	"fmt"
	"strings"

	"github.com/cloudflare/pint/internal/diags"
)

// Upstream checks describe every server as "`name` Prometheus server at <uri>" (promText) and
// talk about Prometheus restarts and Prometheus rules. VictoriaMetricsTexts rewrites these
// texts once problems are collected, instead of changing each of the upstream checks:
// servers with backend = "victoriametrics" become "`name` VictoriaMetrics server at <uri>",
// and with MetricsQL rules, which vmalert evaluates, the Prometheus wording becomes vmalert,
// vmagent and VictoriaMetrics wording. Server names are unique in pint config, so the
// server text only matches the server the problem is about.

// vmalertPhrases are replaced in problems of MetricsQL rules. VictoriaMetrics anchors regexp
// label filters too (`^(?:` + regexp + `)$`).
var vmalertPhrases = strings.NewReplacer(
	"when Prometheus restarts", "when vmagent or VictoriaMetrics restarts",
	"When Prometheus restarts", "When vmagent or VictoriaMetrics restarts",
	"Prometheus regexp matchers are automatically fully anchored", "Regexp matchers are automatically fully anchored",
	"is not a valid Prometheus rule", "is not a valid vmalert rule",
)

// VictoriaMetricsTexts returns the problem with texts for VictoriaMetrics servers and, when
// metricsQL is true, for rules evaluated by vmalert.
func VictoriaMetricsTexts(problem Problem, vmServers []string, metricsQL bool) Problem {
	pairs := make([]string, 0, len(vmServers)*2)
	for _, name := range vmServers {
		pairs = append(pairs, promText(name, ""), fmt.Sprintf("`%s` VictoriaMetrics server at ", name))
	}
	if len(pairs) == 0 && !metricsQL {
		return problem
	}
	servers := strings.NewReplacer(pairs...)
	rewrite := func(s string) string {
		s = servers.Replace(s)
		if metricsQL {
			s = vmalertPhrases.Replace(s)
		}
		return s
	}
	problem.Summary = rewrite(problem.Summary)
	problem.Details = rewrite(problem.Details)
	if len(problem.Diagnostics) > 0 {
		dgs := make([]diags.Diagnostic, len(problem.Diagnostics))
		copy(dgs, problem.Diagnostics)
		for i := range dgs {
			dgs[i].Message = rewrite(dgs[i].Message)
		}
		problem.Diagnostics = dgs
	}
	return problem
}
