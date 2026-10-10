package checks

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/promapi"
)

// vmalert evaluates absent(x) with instant queries, so it's 1 whenever x has no sample within
// the query step before the evaluation time, 5m by default (see group_interval_vmalert.go).
// A metric written less often than that, like results of stream aggregation or of batch
// jobs, makes the alert fire during every pause between samples, and every new sample resets
// `for`. Checked with v1.153.0 and a metric with one sample every 10m: absent(x) was empty
// 2m after a sample and 1 at 7m, and vmalert fired the alert on 5006 of 10080 evaluations
// in a week. Range queries hide this (see alerts_count_vm.go), but not with an explicit
// window: count_over_time(x[5m]) as a range query with step 5m had no points for 180 of
// 361 windows. This check runs that query for the selector of every absent() and
// absent_over_time() call and reports pauses at least as long as the window that `for`
// doesn't cover, if they happen at least once a day on average. The pause after the last
// sample isn't counted, that's the absence the rule is for, and neither is a single outage,
// absent() rightly fires during it.

const alertsAbsentVMDetails = "vmalert evaluates alerting rules with instant queries. " +
	"`absent(selector)` returns 1 when the selector has no sample within the query step before the evaluation time, " +
	"`-datasource.queryStep` of vmalert (5m by default) or `step` from the group `params`, " +
	"and `absent_over_time(selector[window])` when it has none within the window. " +
	"A metric written less often than that, for example results of stream aggregation or of batch jobs, " +
	"makes the alert fire during every pause between samples, and every new sample resets `for`. " +
	"To alert only when the metric stops, use `absent_over_time(selector[window])` with a window longer than the longest normal pause, " +
	"or set `for` to at least that long. " +
	"pint measured the pauses with `count_over_time(selector[window])` over the last %s, the pause after the last sample isn't counted. " +
	"It reports pauses that happen at least once a day on average, a single outage isn't reported."

// vmAbsentCall is an absent() call on a plain selector or an absent_over_time() call on a
// selector with a fixed window.
type vmAbsentCall struct {
	call   *ast.Call
	vs     *ast.VectorSelector
	window time.Duration // 0 for absent(), the instant query lookback applies
}

// vmAbsentCalls returns absent() calls on a plain selector and absent_over_time() calls on
// a selector with a fixed window, outside subqueries.
func vmAbsentCalls(node ast.Node) (calls []vmAbsentCall) {
	var subqueries []ast.PositionRange
	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SubqueryExpr:
			subqueries = append(subqueries, n.PosRange)
		case *ast.Call:
			if len(n.Args) == 0 {
				return true
			}
			arg := n.Args[0]
			for {
				p, ok := arg.(*ast.ParenExpr)
				if !ok {
					break
				}
				arg = p.Expr
			}
			switch n.Func.Name {
			case "absent":
				if vs, ok := arg.(*ast.VectorSelector); ok {
					calls = append(calls, vmAbsentCall{call: n, vs: vs, window: 0})
				}
			case "absent_over_time":
				ms, ok := arg.(*ast.MatrixSelector)
				if !ok || ms.Implicit || ms.StepRelative || ms.Range <= 0 {
					return true
				}
				if vs, ok := ms.VectorSelector.(*ast.VectorSelector); ok {
					calls = append(calls, vmAbsentCall{call: n, vs: vs, window: ms.Range})
				}
			}
		}
		return true
	})
	return slices.DeleteFunc(calls, func(c vmAbsentCall) bool {
		return slices.ContainsFunc(subqueries, func(r ast.PositionRange) bool {
			return r.Start <= c.call.PosRange.Start && c.call.PosRange.End <= r.End
		})
	})
}

// vmPresenceQuery returns the query that has a point at t if the selector had a sample in
// (t-window, t]. Offsets and @ modifiers shift the whole series, pauses stay the same, so
// they are dropped.
func vmPresenceQuery(selector *ast.VectorSelector, window time.Duration) string {
	ms := ast.MatrixSelector{
		VectorSelector: withoutModifiers(selector),
		RangeExpr:      nil,
		RangeText:      "",
		PosRange:       selector.PosRange,
		Range:          window,
		Implicit:       false,
		StepRelative:   false,
	}
	return fmt.Sprintf("count(count_over_time(%s))", ms.String())
}

// withoutModifiers returns a copy of the selector without offset and @ modifiers, which
// shift the whole series and don't change intervals between its samples.
func withoutModifiers(selector *ast.VectorSelector) *ast.VectorSelector {
	vs := *selector
	vs.OriginalOffset, vs.OriginalOffsetExpr, vs.OffsetText = 0, nil, ""
	vs.Timestamp, vs.StartOrEnd, vs.At = nil, "", nil
	vs.Anchored, vs.Smoothed = false, false
	return &vs
}

// vmPauseStats describes pauses between samples found by a presence query.
type vmPauseStats struct {
	count   int           // pauses at least step long that `for` doesn't cover
	longest time.Duration // the longest of them
	span    time.Duration // from the first to the last point of the query
}

// regular returns true if the pauses are part of how the metric is written, at least once a
// day on average over the time the metric was there, rather than one-off outages. A dense
// metric with a single 12m outage in a week has one pause, and absent() on it rightly fired
// during that outage (v1.153.0, 2026-10-09).
func (s vmPauseStats) regular() bool {
	return s.count >= 2 && time.Duration(s.count)*24*time.Hour >= s.span
}

// vmPauses returns pauses between samples the ranges of a presence query with given step
// show, counting those that `for` doesn't cover. Ranges hold consecutive points step apart,
// and RangeQuery extends their end by step-1s (ExpandRangesEnd), so the time between two
// ranges, rounded to the step, is the missing points, step each. A point at t means a sample
// in (t-step, t], so the metric had no sample for at least that long: checked with v1.153.0,
// samples 40m apart gave 7 missing 5m points. vmalert sees absent() as 1 for at least the
// pause minus one step, where exactly the samples fall within their windows is unknown.
func vmPauses(ranges promapi.MetricTimeRanges, step, forDur time.Duration) (stats vmPauseStats) {
	if len(ranges) == 0 {
		return stats
	}
	ranges = slices.Clone(ranges)
	slices.SortFunc(ranges, func(a, b promapi.MetricTimeRange) int { return a.Start.Compare(b.Start) })
	for i := 1; i < len(ranges); i++ {
		pause := ranges[i].Start.Sub(ranges[i-1].End).Round(step)
		if pause <= 0 || pause-step < forDur {
			continue
		}
		stats.count++
		stats.longest = max(stats.longest, pause)
	}
	stats.span = ranges[len(ranges)-1].End.Sub(ranges[0].Start)
	return stats
}

// vmAbsentLookback returns how far back pauses are measured, the lookbackRange of
// promql/series.
func vmAbsentLookback(ctx context.Context) time.Duration {
	if s, ok := ctx.Value(SettingsKey(SeriesCheckName)).(*PromqlSeriesSettings); ok && s.lookbackRangeDuration > 0 {
		return s.lookbackRangeDuration
	}
	return time.Hour * 24 * 7
}

// vmSparseAbsent reports absent() and absent_over_time() calls of MetricsQL alerting rules
// checked against VictoriaMetrics whose window is shorter than pauses between samples of
// the metric, so the alert fires during normal pauses.
func (c AlertsAbsentCheck) vmSparseAbsent(ctx context.Context, entry *discovery.Entry) (problems []Problem) {
	if !isVictoriaMetrics(c.prom) || !isVMAlertRule(entry) {
		return nil
	}
	expr := entry.Rule.AlertingRule.Expr
	calls := vmAbsentCalls(expr.Query().Expr)
	if len(calls) == 0 {
		return nil
	}
	settings, ok := vmLookbackSettingsFor(ctx, c.prom, true)
	if !ok {
		return nil
	}
	var step, maxLookback time.Duration
	if entry.Group != nil {
		step = parseVMQueryParam(entry, vmalertParamStep)
		maxLookback = parseVMQueryParam(entry, vmalertParamMaxLookback)
	}
	lookback := vmAbsentLookback(ctx)
	var forDur time.Duration
	if entry.Rule.AlertingRule.For != nil && entry.Rule.AlertingRule.For.ParseError == nil {
		forDur = entry.Rule.AlertingRule.For.Value
	}

	done := map[string]struct{}{}
	for _, ac := range calls {
		window := ac.window
		var source string
		if window == 0 {
			window = settings.window(step, maxLookback, true)
			source = ", the lookback of vmalert instant queries: " + settings.describe(step, maxLookback, window)
		}
		query := vmPresenceQuery(ac.vs, window)
		if _, ok := done[query]; ok {
			continue
		}
		done[query] = struct{}{}

		qr, err := c.prom.RangeQuery(ctx, query, promapi.NewRelativeRange(lookback, window)).Wait()
		if err != nil {
			problems = append(problems, problemFromError(err, entry.Rule, c.Reporter(), c.prom.Name(), Warning))
			continue
		}
		pauses := vmPauses(qr.Series.Ranges, window, forDur)
		if !pauses.regular() {
			continue
		}

		call := expr.Value.Value[ac.call.PosRange.Start:ac.call.PosRange.End]
		selector := expr.Value.Value[ac.vs.PosRange.Start:ac.vs.PosRange.End]
		msg := fmt.Sprintf(
			"`%s` returns 1 whenever `%s` has no sample in the last %s%s. "+
				"In the last %s `%s` had %d pauses of at least %s on %s, the longest at least %s, "+
				"while samples kept arriving, so this alert fires during normal pauses, not only when `%s` disappears.",
			call, selector, output.HumanizeDuration(window), source,
			output.HumanizeDuration(lookback), selector, pauses.count, output.HumanizeDuration(window), vmServerText(c.prom, qr.URI),
			output.HumanizeDuration(pauses.longest), selector,
		)
		if forDur > 0 {
			msg += fmt.Sprintf(" `for: %s` covers pauses up to %s only.",
				output.HumanizeDuration(forDur), output.HumanizeDuration(forDur+window))
		}
		problems = append(problems, Problem{
			Anchor:   AnchorAfter,
			Lines:    expr.Value.Pos.Lines(),
			Reporter: c.Reporter(),
			Summary:  ac.call.Func.Name + "() window shorter than pauses between samples",
			Details:  fmt.Sprintf(alertsAbsentVMDetails, output.HumanizeDuration(lookback)),
			Severity: Warning,
			Diagnostics: []diags.Diagnostic{
				{
					Message:     msg,
					Pos:         expr.Value.Pos,
					Expr:        expr.Query().Expr,
					FirstColumn: int(ac.call.PosRange.Start) + 1,
					LastColumn:  int(ac.call.PosRange.End),
					Kind:        diags.Issue,
				},
			},
		})
	}
	return problems
}
