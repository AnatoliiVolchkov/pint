package checks_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/diags"
)

func TestVictoriaMetricsTexts(t *testing.T) {
	problem := func(summary, details, message string) checks.Problem {
		return checks.Problem{
			Anchor:   checks.AnchorAfter,
			Lines:    diags.LineRange{First: 1, Last: 1},
			Reporter: "promql/series",
			Summary:  summary,
			Details:  details,
			Severity: checks.Warning,
			Diagnostics: []diags.Diagnostic{
				{Message: message, Pos: nil, Expr: nil, FirstColumn: 1, LastColumn: 1, Kind: diags.Issue},
			},
		}
	}

	in := problem(
		"This rule is not a valid Prometheus rule: `boom`.",
		"When Prometheus restarts this alert rule might be evaluated before your service is scraped.",
		"`vm` Prometheus server at http://vm didn't have any series, `prom` Prometheus server at http://prom did. "+
			"Using `absent()` might cause false positive alerts when Prometheus restarts. "+
			"Prometheus regexp matchers are automatically fully anchored so a match for `x` will result in `^x$`.",
	)

	out := checks.VictoriaMetricsTexts(in, []string{"vm"}, true)
	require.Equal(t, "This rule is not a valid vmalert rule: `boom`.", out.Summary)
	require.Equal(t, "When vmagent or VictoriaMetrics restarts this alert rule might be evaluated before your service is scraped.", out.Details)
	require.Equal(t,
		"`vm` VictoriaMetrics server at http://vm didn't have any series, `prom` Prometheus server at http://prom did. "+
			"Using `absent()` might cause false positive alerts when vmagent or VictoriaMetrics restarts. "+
			"Regexp matchers are automatically fully anchored so a match for `x` will result in `^x$`.",
		out.Diagnostics[0].Message)
	require.Contains(t, in.Diagnostics[0].Message, "`vm` Prometheus server", "the original problem must not change")

	// PromQL rules checked against a VictoriaMetrics server only get the server text.
	out = checks.VictoriaMetricsTexts(in, []string{"vm"}, false)
	require.Equal(t, in.Summary, out.Summary)
	require.Contains(t, out.Diagnostics[0].Message, "`vm` VictoriaMetrics server at http://vm")
	require.Contains(t, out.Diagnostics[0].Message, "when Prometheus restarts")

	// Nothing to change without VictoriaMetrics servers and MetricsQL.
	require.Equal(t, in, checks.VictoriaMetricsTexts(in, nil, false))
}
