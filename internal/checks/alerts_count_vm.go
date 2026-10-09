package checks

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser/ast"
)

// vmalert evaluates alerting rules with instant queries. For a selector without a lookbehind
// window VictoriaMetrics answers an instant query with the last sample within the query step,
// 5m by default (see group_interval_vmalert.go), while a range query, which this check uses,
// estimates the interval between samples and looks back about that far. Pauses between
// samples shorter than their usual interval are invisible to the range query. Checked with
// v1.153.0 and a metric with one sample every 10m: absent(x) was 1 on half of the instant
// queries, as a range query it returned no points at all, and `vmalert -replay`, which also
// uses range queries, recorded no firing. absent(last_over_time(x[5m])) as a range query was
// 1 on 181 of 361 points, matching vmalert. So on VictoriaMetrics the rule is counted with
// every selector without a window replaced by last_over_time(selector[W]), W being the
// lookback of vmalert instant queries. last_over_time() keeps the metric name in MetricsQL.
// Selectors inside lookbehind windows and subqueries are left alone, windows derived from the
// step, like rate(x) or [2i], are reported by promql/rate instead.

const alertsCheckVMDetails = "On VictoriaMetrics pint counts alerts the way vmalert evaluates the rule, with instant queries: " +
	"selectors without a lookbehind window were replaced by `last_over_time(selector[%s])`, the window being %s. " +
	"A range query of the rule itself looks back further and misses pauses between samples shorter than their usual interval. " +
	"The query pint counted:\n\n```\n%s\n```"

// instantSelectors returns selectors of node that have no lookbehind window and aren't
// inside a subquery, vmalert instant queries evaluate them with the query step as the window.
func instantSelectors(node ast.Node) (plain []*ast.VectorSelector) {
	var covered []ast.PositionRange
	var selectors []*ast.VectorSelector
	ast.Inspect(node, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.MatrixSelector:
			covered = append(covered, n.PosRange)
		case *ast.SubqueryExpr:
			covered = append(covered, n.PosRange)
		case *ast.VectorSelector:
			selectors = append(selectors, n)
		}
		return true
	})
	for _, vs := range selectors {
		inside := slices.ContainsFunc(covered, func(r ast.PositionRange) bool {
			return r.Start <= vs.PosRange.Start && vs.PosRange.End <= r.End
		})
		if !inside {
			plain = append(plain, vs)
		}
	}
	return plain
}

// withInstantWindows returns expr with the given selectors replaced by
// last_over_time(selector[w]), ok is false if their positions don't fit expr.
func withInstantWindows(expr string, selectors []*ast.VectorSelector, w time.Duration) (_ string, ok bool) {
	for _, vs := range selectors {
		if int(vs.PosRange.End) > len(expr) {
			return expr, false
		}
	}
	slices.SortFunc(selectors, func(a, b *ast.VectorSelector) int {
		return cmp.Compare(b.PosRange.Start, a.PosRange.Start)
	})
	for _, vs := range selectors {
		// MatrixSelector prints the offset and @ modifiers after the window.
		ms := ast.MatrixSelector{
			VectorSelector: vs,
			RangeExpr:      nil,
			RangeText:      "",
			PosRange:       vs.PosRange,
			Range:          w,
			Implicit:       false,
			StepRelative:   false,
		}
		expr = expr[:vs.PosRange.Start] + "last_over_time(" + ms.String() + ")" + expr[vs.PosRange.End:]
	}
	return expr, true
}

// vmInstantSemantics returns the query this check counts alerts with. For MetricsQL rules
// checked against VictoriaMetrics it's the rule query with selectors without a lookbehind
// window replaced by last_over_time(selector[W]), W being the lookback of vmalert instant
// queries, and details explaining that.
func (c AlertsCheck) vmInstantSemantics(ctx context.Context, entry *discovery.Entry) (query, details string) {
	query = entry.Rule.AlertingRule.Expr.Value.Value
	if !isVictoriaMetrics(c.prom) || !isVMAlertRule(entry) {
		return query, ""
	}
	selectors := instantSelectors(entry.Rule.AlertingRule.Expr.Query().Expr)
	if len(selectors) == 0 {
		return query, ""
	}
	settings, ok := vmLookbackSettingsFor(ctx, c.prom, true)
	if !ok {
		return query, ""
	}
	var step, maxLookback time.Duration
	if entry.Group != nil {
		step = parseVMQueryParam(entry, vmalertParamStep)
		maxLookback = parseVMQueryParam(entry, vmalertParamMaxLookback)
	}
	w := settings.window(step, maxLookback, true)
	rewritten, ok := withInstantWindows(query, selectors, w)
	if !ok {
		return query, ""
	}
	return rewritten, fmt.Sprintf(alertsCheckVMDetails,
		output.HumanizeDuration(w), settings.describe(step, maxLookback, w), rewritten)
}
