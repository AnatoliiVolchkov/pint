package checks

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/source"
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

// How long a window rate(), irate(), increase() and deriv() need on VictoriaMetrics, checked
// with v1.153.0 on 2026-10-10 with a counter written every 10m and instant queries every
// 30s for an hour: VictoriaMetrics uses the last sample before the window only if it's at
// most maxPrevInterval (the step of an instant query) before the window, so rate() needs
// two samples within the window plus that: window >= max(S, 2S - step) for a scrape
// interval S. With step 5m rate(x[12m]) was empty or zero 36 times of 120, rate(x[15m])
// never; with step 2m rate(x[15m]) failed 36 times, rate(x[18m]) never. increase() returned
// 0 instead of nothing. deriv() only uses samples inside the window: deriv(x[15m]) was 0 on
// half of the queries with step 5m and with step 15m, deriv(x[20m]) never, so it needs 2S.

// vmWindowNeed returns the shortest window that always has enough samples for fn on
// VictoriaMetrics, for a scrape interval and the maxPrevInterval of the query.
func vmWindowNeed(fn string, scrape, maxPrev time.Duration, minIntervals int) time.Duration {
	switch {
	case minIntervals != 1:
		return scrape * time.Duration(minIntervals)
	case fn == "deriv":
		return 2 * scrape
	default:
		return max(scrape, 2*scrape-maxPrev)
	}
}

// hasShortWindow returns true if the query has a rate(), irate(), increase() or deriv() call
// with an explicit window of at least one and less than two scrape intervals, whether it
// has enough samples on VictoriaMetrics depends on the query step.
func hasShortWindow(node ast.Node, scrape time.Duration) (found bool) {
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.Call)
		if !ok {
			return !found
		}
		switch call.Func.Name {
		case "rate", "irate", "increase", "deriv":
		default:
			return !found
		}
		for _, arg := range call.Args {
			if ms, ok := arg.(*ast.MatrixSelector); ok && !ms.Implicit && !ms.StepRelative && ms.Range >= scrape && ms.Range < 2*scrape {
				found = true
			}
		}
		return !found
	})
	return found
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
	"the window must be at least two scrape intervals minus the step. Set the window explicitly to make the rule independent of the step."

const RateCheckShortWindowDetails = "vmalert evaluates rules with instant queries and sends `-datasource.queryStep` as the step, 5m by default, " +
	"or `step` from the group `params`. VictoriaMetrics only uses the last sample before the window " +
	"if it's at most one step before the window. With a scrape interval longer than the step a window shorter than " +
	"two scrape intervals minus the step can have a single sample: `rate()` and `irate()` return nothing and `increase()` returns 0. " +
	"`deriv()` only uses samples inside the window and always needs two scrape intervals."

// rateVMWindow holds what is needed to calculate windows VictoriaMetrics derives from the
// query step, for one rule.
type rateVMWindow struct {
	// intervals are measured intervals between samples longer than the configured scrape
	// interval, by selector without modifiers.
	intervals   map[string]vmInterval
	settings    vmLookbackSettings
	step        time.Duration // `step` from params of the rule's group, 0 if not set
	maxLookback time.Duration // `max_lookback` from params of the rule's group, 0 if not set
}

// pint takes the scrape interval of VictoriaMetrics servers from scrapeInterval in its config,
// 1m by default, but metrics written by stream aggregation, recording rules of other systems
// or targets with their own scrape interval can have longer intervals between samples.
// scrape_interval(m[d]) returns the average interval between samples of every series
// (VictoriaMetrics v1.153.0 on 2026-10-10: 600 for a series written every 10m, 15 for every
// 15s, max() over series written every 60s and 300s gave 300) and needs two samples in the
// window, so pint asks for the last 15m first and only for metrics without a result there,
// written less often than every 7m30s or missing, for the last 2h.
const (
	vmIntervalShortWindow = 15 * time.Minute
	vmIntervalLongWindow  = 2 * time.Hour
)

// vmInterval is the longest average interval between samples of series of a selector.
type vmInterval struct {
	interval time.Duration
	window   time.Duration // how far back it was measured
}

// measureInterval returns the longest average interval between samples of series matching
// the selector, zero if it can't be measured.
func (c RateCheck) measureInterval(ctx context.Context, selector string) vmInterval {
	for _, window := range []time.Duration{vmIntervalShortWindow, vmIntervalLongWindow} {
		query := fmt.Sprintf("max(scrape_interval(%s[%s]))", selector, output.HumanizeDuration(window))
		qr, err := c.prom.Query(ctx, query).Wait()
		if err != nil {
			slog.LogAttrs(ctx, slog.LevelDebug, "Cannot measure intervals between samples", slog.String("query", query), slog.Any("err", err))
			return vmInterval{interval: 0, window: 0}
		}
		if len(qr.Series) > 0 && qr.Series[0].Value > 0 && !math.IsInf(qr.Series[0].Value, 0) {
			return vmInterval{interval: time.Duration(qr.Series[0].Value * float64(time.Second)).Round(time.Second), window: window}
		}
	}
	return vmInterval{interval: 0, window: 0}
}

// measureIntervals returns intervals between samples of selectors of rate(), irate(),
// increase() and deriv() calls that are longer than the configured scrape interval.
func (c RateCheck) measureIntervals(ctx context.Context, expr *parser.PromQLExpr, scrape time.Duration) map[string]vmInterval {
	intervals := map[string]vmInterval{}
	done := map[string]struct{}{}
	for _, src := range expr.Source() {
		src.WalkSources(func(s *source.Source, _ *source.Join, _ *source.Unless) {
			if _, ok := findRateCall(s, true); !ok {
				return
			}
			if _, ok := source.MostOuterOperation[*ast.MatrixSelector](s); !ok {
				return
			}
			vs, ok := source.MostOuterOperation[*ast.VectorSelector](s)
			if !ok {
				return
			}
			key := withoutModifiers(vs).String()
			if _, ok := done[key]; ok {
				return
			}
			done[key] = struct{}{}
			if measured := c.measureInterval(ctx, key); measured.interval > scrape {
				intervals[key] = measured
			}
		})
	}
	return intervals
}

// scrapeFor returns the interval between samples to check the window of a call against, the
// measured one when it's longer than the configured scrape interval, and how to describe it.
func (c RateCheck) scrapeFor(m *ast.MatrixSelector, cfg *promapi.ConfigResult) (time.Duration, string) {
	scrape := cfg.Config.Global.ScrapeInterval
	text := fmt.Sprintf("with the `%s` scrape_interval of %s", output.HumanizeDuration(scrape), vmServerText(c.prom, cfg.URI))
	vs, ok := m.VectorSelector.(*ast.VectorSelector)
	if !ok || c.vm == nil {
		return scrape, text
	}
	key := withoutModifiers(vs).String()
	if measured, ok := c.vm.intervals[key]; ok {
		return measured.interval, fmt.Sprintf(
			"with samples of `%s` every %s on %s (its slowest series in the last %s, measured with `scrape_interval()`; "+
				"`scrapeInterval` in pint config is %s)",
			key, output.HumanizeDuration(measured.interval), vmServerText(c.prom, cfg.URI),
			output.HumanizeDuration(measured.window), output.HumanizeDuration(scrape),
		)
	}
	return scrape, text
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
func (c RateCheck) withVMWindow(ctx context.Context, entry *discovery.Entry, scrape time.Duration) RateCheck {
	if !isVictoriaMetrics(c.prom) || !isVMAlertRule(entry) || entry.Group == nil {
		return c
	}
	intervals := c.measureIntervals(ctx, entry.Rule.Expr(), scrape)
	if node := entry.Rule.Expr().Query().Expr; !hasRuntimeWindow(node) && !hasShortWindow(node, scrape) && len(intervals) == 0 {
		return c
	}
	settings, ok := vmLookbackSettingsFor(ctx, c.prom, true)
	if !ok {
		return c
	}
	c.vm = &rateVMWindow{
		intervals:   intervals,
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

	scrape, scrapeText := c.scrapeFor(m, cfg)
	need := vmWindowNeed(call.Func.Name, scrape, maxPrev, minIntervals)
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
					"%s. `%s()` needs a window of at least %s %s, "+
						"so it can return no results or wrong ones.",
					how, call.Func.Name, output.HumanizeDuration(need), scrapeText,
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

// shortWindowProblems reports explicit windows of at least one scrape interval, shorter ones
// are reported as a Bug by the check itself, that can still have a single sample on
// VictoriaMetrics because of the query step.
func (c RateCheck) shortWindowProblems(
	expr *parser.PromQLExpr,
	call *ast.Call,
	m *ast.MatrixSelector,
	cfg *promapi.ConfigResult,
	minIntervals int,
) []Problem {
	if c.vm == nil || m.Implicit || m.StepRelative {
		return nil
	}
	// Windows shorter than minIntervals x the configured scrape interval are reported as a Bug
	// by the check.
	if configured := cfg.Config.Global.ScrapeInterval; configured <= 0 || m.Range < configured*time.Duration(minIntervals) {
		return nil
	}
	scrape, scrapeText := c.scrapeFor(m, cfg)
	step, _, maxPrev := c.vm.settings.instantLimits(cmp.Or(c.vm.step, c.vm.settings.queryStep), c.vm.maxLookback)
	need := vmWindowNeed(call.Func.Name, scrape, maxPrev, minIntervals)
	if m.Range >= need {
		return nil
	}

	var why string
	switch {
	case minIntervals != 1:
		why = fmt.Sprintf("`%s()` follows Prometheus semantics and only uses samples inside the window, so it needs %d scrape intervals",
			call.Func.Name, minIntervals)
	case call.Func.Name == "deriv":
		why = "`deriv()` only uses samples inside the window, so it needs two scrape intervals"
	default:
		why = fmt.Sprintf("vmalert evaluates the rule with instant queries with a %s step, %s, "+
			"and VictoriaMetrics uses the last sample before the window only if it's at most %s before the window",
			output.HumanizeDuration(step), c.vm.settings.describe(c.vm.step, c.vm.maxLookback, maxPrev), output.HumanizeDuration(maxPrev))
	}
	return []Problem{{
		Anchor:   AnchorAfter,
		Lines:    expr.Value.Pos.Lines(),
		Reporter: c.Reporter(),
		Summary:  "duration too small",
		Details:  RateCheckShortWindowDetails,
		Severity: Warning,
		Diagnostics: []diags.Diagnostic{
			{
				Message: fmt.Sprintf(
					"`%s()` needs a window of at least %s %s: %s. "+
						"A `[%s]` window can have a single sample, so it can return no results or wrong ones.",
					call.Func.Name, output.HumanizeDuration(need), scrapeText, why, output.HumanizeDuration(m.Range),
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
