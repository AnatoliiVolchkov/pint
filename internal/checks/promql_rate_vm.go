package checks

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/promapi"
)

// VictoriaMetrics returns one metadata entry per metric name and the type can come from any
// target exposing that name, so it's less reliable than Prometheus metadata. prom.example.com
// returned `type: gauge` for `process_cpu_seconds_total` on 2026-10-05 and
// 2026-10-08, which made pint report rate() on it as a Bug. For VictoriaMetrics servers
// "non-counter" is a Warning, and an Information when the name ends with `_total`, which is
// reserved for counters, since the metadata itself is then the likely mistake.

const nonCounterVMDetails = "VictoriaMetrics returns one metadata entry per metric name, " +
	"and the type can come from any target exposing a metric with that name. " +
	"A wrong `# TYPE` line on one target is enough to make a counter look like a gauge."

// vmNonCounter adjusts a "counter based function called on a non-counter" problem for
// VictoriaMetrics servers.
func (c RateCheck) vmNonCounter(problem Problem, fn, metric string, mtype v1.MetricType, uri string) Problem {
	if !isVictoriaMetrics(c.prom) {
		return problem
	}
	problem.Severity = Warning
	problem.Details += "\n\n" + nonCounterVMDetails
	problem.Diagnostics[0].Message = fmt.Sprintf(
		"`%s()` should only be used with counters but `%s` is a %s according to metrics metadata from %s.",
		fn, metric, mtype, vmServerText(c.prom, uri),
	)
	if mtype == v1.MetricTypeGauge && strings.HasSuffix(metric, "_total") {
		problem.Summary = "metric metadata contradicts the metric name"
		problem.Severity = Information
		problem.Diagnostics[0].Message = fmt.Sprintf(
			"`%s()` is used on `%s`, which is a %s according to metrics metadata from %s, "+
				"but its name ends with `_total`, which is reserved for counters. "+
				"The metadata is likely wrong, check the `# TYPE` line of targets exposing `%s`.",
			fn, metric, mtype, vmServerText(c.prom, uri), metric,
		)
	}
	return problem
}

// vmalert evaluates rules with instant queries. Without a lookbehind window, like rate(foo),
// VictoriaMetrics uses the query step as the window, and a window like rate(foo[2i]) is two
// query steps (rollupConfig.doInternal, v1.153.0). VictoriaMetrics also uses the last sample
// before the window, but only if it's within maxPrevInterval, which is the step for instant
// queries. Checked with v1.153.0, a counter scraped every 10m: rate(foo) with step 5m was
// always empty and increase(foo) returned 0, with step 15m both were correct;
// rate(foo[2i]) with step 5m, a 10m window, was empty for the second half of every
// interval.

const RateCheckRuntimeWindowDetails = "Without a lookbehind window, like `rate(foo)`, " +
	"or with a window relative to the step, like `rate(foo[2i])`, the window depends on the query step. " +
	"vmalert evaluates rules with instant queries and sends `-datasource.queryStep` as the step, 5m by default, " +
	"or `step` from the group `params`. VictoriaMetrics only uses the last sample before the window " +
	"if it's at most one step before the window, so with a scrape interval longer than the step " +
	"the window must cover two scrape intervals. Set the window explicitly to make the rule independent of the step."

// rateVMWindow holds what is needed to calculate windows VictoriaMetrics derives from the
// query step, for one rule.
type rateVMWindow struct {
	settings    vmLookbackSettings
	step        time.Duration // `step` from params of the rule's group, 0 if not set
	maxLookback time.Duration // `max_lookback` from params of the rule's group, 0 if not set
}

// hasRuntimeWindow returns true if the query has a lookbehind window derived from the step.
func hasRuntimeWindow(node ast.Node) (found bool) {
	ast.Inspect(node, func(n ast.Node) bool {
		if ms, ok := n.(*ast.MatrixSelector); ok && (ms.Implicit || ms.StepRelative) {
			found = true
		}
		return !found
	})
	return found
}

// withVMWindow returns a copy of the check that can calculate windows VictoriaMetrics
// derives from the query step, for MetricsQL rules checked against VictoriaMetrics.
func (c RateCheck) withVMWindow(ctx context.Context, entry *discovery.Entry) RateCheck {
	if !isVictoriaMetrics(c.prom) || !isVMAlertRule(entry) || entry.Group == nil {
		return c
	}
	if !hasRuntimeWindow(entry.Rule.Expr().Query().Expr) {
		return c
	}
	settings, ok := vmLookbackSettingsFor(ctx, c.prom, true)
	if !ok {
		return c
	}
	c.vm = &rateVMWindow{
		settings:    settings,
		step:        parseVMQueryParam(entry, vmalertParamStep),
		maxLookback: parseVMQueryParam(entry, vmalertParamMaxLookback),
	}
	return c
}

// runtimeWindowProblems reports windows derived from the query step that are too short
// for the scrape interval.
func (c RateCheck) runtimeWindowProblems(
	expr *parser.PromQLExpr,
	call *ast.Call,
	m *ast.MatrixSelector,
	cfg *promapi.ConfigResult,
	minIntervals int,
	details string,
) []Problem {
	if c.vm == nil {
		return nil
	}
	step, _, maxPrev := c.vm.settings.instantLimits(cmp.Or(c.vm.step, c.vm.settings.queryStep), c.vm.maxLookback)
	var window time.Duration
	var how string
	if m.Implicit {
		window = c.vm.settings.window(step, c.vm.maxLookback, false)
		how = fmt.Sprintf("`%s()` has no lookbehind window, so VictoriaMetrics uses a %s window, %s",
			call.Func.Name, output.HumanizeDuration(window), c.vm.settings.describe(c.vm.step, c.vm.maxLookback, window))
	} else {
		n, err := strconv.ParseFloat(strings.TrimSuffix(m.RangeText, "i"), 64)
		if err != nil || n <= 0 {
			return nil
		}
		window = time.Duration(n * float64(step))
		how = fmt.Sprintf("`%s()` uses a `[%s]` window, so VictoriaMetrics uses a %s window, %s x %s",
			call.Func.Name, m.RangeText, output.HumanizeDuration(window), m.RangeText[:len(m.RangeText)-1],
			c.vm.settings.describe(c.vm.step, c.vm.maxLookback, step))
	}

	scrape := cfg.Config.Global.ScrapeInterval
	need := scrape * time.Duration(minIntervals)
	if minIntervals == 1 && scrape > maxPrev {
		// The sample before the window is too far back to be used.
		need = 2 * scrape
	}
	if scrape <= 0 || window >= need {
		return nil
	}

	return []Problem{{
		Anchor:   AnchorAfter,
		Lines:    expr.Value.Pos.Lines(),
		Reporter: c.Reporter(),
		Summary:  "duration too small",
		Details:  details + "\n\n" + RateCheckRuntimeWindowDetails,
		Severity: Warning,
		Diagnostics: []diags.Diagnostic{
			{
				Message: fmt.Sprintf(
					"%s. `%s()` needs a window of at least %s with the `%s` scrape_interval of %s, "+
						"so it can return no results or wrong ones.",
					how, call.Func.Name, output.HumanizeDuration(need),
					output.HumanizeDuration(scrape), vmServerText(c.prom, cfg.URI),
				),
				Pos:         expr.Value.Pos,
				Expr:        expr.Query().Expr,
				FirstColumn: int(call.PosRange.Start) + 1,
				LastColumn:  int(call.PosRange.End),
				Kind:        diags.Issue,
			},
		},
	}}
}
