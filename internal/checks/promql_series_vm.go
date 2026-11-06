package checks

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/promapi"

	"github.com/prometheus/prometheus/model/labels"
)

// promql/series asks if a selector returns anything now, and for missing metrics looks at
// count(selector) as a range query. VictoriaMetrics range queries look back about the usual
// interval between samples, so a metric written every 10m is continuous there, while
// vmalert evaluates rules with instant queries that look back one query step, 5m by
// default, and see nothing during every other evaluation. Checked with v1.153.0 on
// 2026-10-09: count(x) over 6h at 1m returned 361 of 361 points for such a metric,
// count(count_over_time(x[5m])) 180 (see alerts_absent_vm.go and alerts_count_vm.go).
//
// For MetricsQL rules checked against VictoriaMetrics this check measures pauses of every
// selector vmalert evaluates with the instant query lookback W, with the presence query of
// alerts/absent. Selectors inside absent() are left to alerts/absent, selectors with a
// fallback like `or vector(0)` and selectors reading results of other rules (group/interval)
// are skipped. Measuring a week of samples of every selector would be too much load, so the
// last hour is checked first, one request, and only selectors with a pause in it are measured
// over lookbackRange.

const SeriesCheckVMPausesDetails = "vmalert evaluates rules with instant queries. " +
	"For a selector without a lookbehind window VictoriaMetrics returns the last sample within the query step before the evaluation time, " +
	"`-datasource.queryStep` of vmalert (5m by default) or `step` from the group `params`. " +
	"During pauses between samples longer than that the selector returns nothing: a firing alert resolves and its `for` starts again " +
	"when the metric is back, a recording rule writes no sample. " +
	"Graphs and pint range queries don't show such pauses, VictoriaMetrics range queries look back about the usual interval between samples.\n\n" +
	"Pauses at least once a day on average are usually how the metric is written: stream aggregation outputs, batch jobs, " +
	"or a scrape interval longer than the query step. To use the last sample of such a metric read it with " +
	"`last_over_time(selector[window])` and a window longer than the longest normal pause. " +
	"pint measured the pauses with `count(count_over_time(selector[step]))` over the last %s."

// vmRecentWindow is how far back pint looks for a pause before measuring a selector over
// lookbackRange.
const vmRecentWindow = time.Hour

// insideCalls returns positions of calls to functions with given names.
func insideCalls(node ast.Node, names ...string) (ranges []ast.PositionRange) {
	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.Call); ok && slices.Contains(names, call.Func.Name) {
			ranges = append(ranges, call.PosRange)
		}
		return true
	})
	return ranges
}

func containedIn(r ast.PositionRange, ranges []ast.PositionRange) bool {
	return slices.ContainsFunc(ranges, func(o ast.PositionRange) bool {
		return o.Start <= r.Start && r.End <= o.End
	})
}

// selectorMetricName returns the metric name of a selector, empty if it can match any.
func selectorMetricName(vs *ast.VectorSelector) string {
	if vs.Name != "" {
		return vs.Name
	}
	if names, ok := selectorMetricNames(vs); ok && len(names) == 1 {
		return names[0]
	}
	return ""
}

// hasRecentPause returns true if the presence query over the last window has missing points.
func hasRecentPause(qr *promapi.RangeQueryResult, step time.Duration) bool {
	if len(qr.Series.Ranges) != 1 {
		return true
	}
	r := qr.Series.Ranges[0]
	// Range points are aligned to the step, allow one step at both ends.
	return r.Start.Sub(qr.Series.From) > 2*step || qr.Series.Until.Sub(r.End) > 2*step
}

// vmSparseSelectors reports selectors of MetricsQL rules checked against VictoriaMetrics
// whose metric regularly has pauses longer than the lookback of vmalert instant queries.
func (c SeriesCheck) vmSparseSelectors(
	ctx context.Context,
	entry *discovery.Entry,
	entries []*discovery.Entry,
	settings *PromqlSeriesSettings,
) (problems []Problem) {
	if !isVictoriaMetrics(c.prom) || !isVMAlertRule(entry) {
		return nil
	}
	expr := entry.Rule.Expr()
	node := expr.Query().Expr
	selectors := instantSelectors(node)
	if len(selectors) == 0 {
		return nil
	}
	lookbackSettings, ok := vmLookbackSettingsFor(ctx, c.prom, true)
	if !ok {
		return nil
	}
	var step, maxLookback time.Duration
	if entry.Group != nil {
		step = parseVMQueryParam(entry, vmalertParamStep)
		maxLookback = parseVMQueryParam(entry, vmalertParamMaxLookback)
	}
	window := lookbackSettings.window(step, maxLookback, true)

	absent := insideCalls(node, "absent", "absent_over_time")
	fallback := map[ast.PositionRange]bool{}
	for _, si := range getSelectors(expr) {
		fallback[si.selector.PosRange] = fallback[si.selector.PosRange] || si.hasFallback
	}

	done := map[string]struct{}{}
	for _, vs := range selectors {
		if containedIn(vs.PosRange, absent) || fallback[vs.PosRange] {
			continue
		}
		if name := selectorMetricName(vs); name == "ALERTS" || name == "ALERTS_FOR_STATE" {
			continue
		}
		if slices.ContainsFunc(entries, func(e *discovery.Entry) bool {
			return e.State != discovery.Removed && e.Rule.Error.Err == nil && readsResultsOf(e, vs)
		}) {
			continue
		}
		if isDisabled(entry.Rule, vs) || isSnoozed(entry.Rule, vs) {
			continue
		}
		if name := selectorMetricName(vs); slices.ContainsFunc(settings.ignoreMetricsRe, func(re *regexp.Regexp) bool {
			return name != "" && re.MatchString(name)
		}) {
			continue
		}
		query := vmPresenceQuery(vs, window)
		if _, ok := done[query]; ok {
			continue
		}
		done[query] = struct{}{}

		recent, err := c.prom.RangeQuery(ctx, query, newStepAlignedRange(max(vmRecentWindow, 4*window), window)).Wait()
		if err != nil {
			slog.LogAttrs(ctx, slog.LevelDebug, "Cannot check recent pauses", slog.String("query", query), slog.Any("err", err))
			continue
		}
		if !hasRecentPause(recent, window) {
			continue
		}
		qr, err := c.prom.RangeQuery(ctx, query, promapi.NewRelativeRange(settings.lookbackRangeDuration, window)).Wait()
		if err != nil {
			slog.LogAttrs(ctx, slog.LevelDebug, "Cannot measure pauses", slog.String("query", query), slog.Any("err", err))
			continue
		}
		pauses := vmPauses(qr.Series.Ranges, window, 0)
		if !pauses.regular() {
			continue
		}

		selector := expr.Value.Value[vs.PosRange.Start:vs.PosRange.End]
		problems = append(problems, Problem{
			Anchor:   AnchorAfter,
			Lines:    expr.Value.Pos.Lines(),
			Reporter: c.Reporter(),
			Summary:  "pauses between samples longer than the query lookback",
			Details:  fmt.Sprintf(SeriesCheckVMPausesDetails, output.HumanizeDuration(settings.lookbackRangeDuration)),
			Severity: Warning,
			Diagnostics: []diags.Diagnostic{
				{
					Message: fmt.Sprintf(
						"`%s` had %d pauses of at least %s in the last %s on %s, the longest at least %s. "+
							"vmalert evaluates this rule with instant queries that look back %s, %s, "+
							"so evaluations during these pauses see no `%s`.",
						selector, pauses.count, output.HumanizeDuration(window),
						output.HumanizeDuration(settings.lookbackRangeDuration), vmServerText(c.prom, qr.URI),
						output.HumanizeDuration(pauses.longest), output.HumanizeDuration(window),
						lookbackSettings.describe(step, maxLookback, window), selector,
					),
					Pos:         expr.Value.Pos,
					Expr:        node,
					FirstColumn: int(vs.PosRange.Start) + 1,
					LastColumn:  int(vs.PosRange.End),
					Kind:        diags.Issue,
				},
			},
		})
	}
	return problems
}

// parseSeriesSelector parses a selector from promql/series settings and from
// `# pint disable promql/series(...)` and `# pint rule/set promql/series(...)` comments.
// PromQL comes first, so every selector PromQL accepts behaves like upstream. Selectors only
// MetricsQL accepts, such as metric names with dots, are parsed with MetricsQL. When both
// fail the PromQL error is returned. MetricsQL `or` filters stay unsupported here.
func parseSeriesSelector(selector string) ([]*labels.Matcher, error) {
	m, err := parser.PromQL.ParseMetricSelector(selector)
	if err == nil {
		return m, nil
	}
	if mm, merr := parser.MetricsQL.ParseMetricSelector(selector); merr == nil {
		return mm, nil
	}
	return nil, err
}

// stepAlignedRange is a relative range whose start is a multiple of its step. RangeQuery
// caches results under the start and end rounded to the step, and splits ranges longer than
// 2h into 2h slices starting at even hours. The recent window and the slice of the lookback
// range starting at the same even hour then share a cache key: when the recent window
// started at 16:01:30 the 16:00 slice got its result, without the 16:00 sample, and pint
// measured a pause at 16:00 that wasn't there. It happened in the first minutes of every
// other hour (found 2026-11-06 through a test failing at those times). Starting the recent
// window on the step makes both queries return the same samples.
type stepAlignedRange struct {
	start time.Time
	promapi.RelativeRange
}

func (r stepAlignedRange) Start() time.Time {
	return r.start
}

func newStepAlignedRange(lookback, step time.Duration) stepAlignedRange {
	rr := promapi.NewRelativeRange(lookback, step)
	return stepAlignedRange{RelativeRange: rr, start: rr.Start().Truncate(step)}
}
