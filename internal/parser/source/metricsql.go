package source

import (
	"fmt"

	mql "github.com/VictoriaMetrics/metricsql"
	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/parser/ast"
)

// parseMetricsQLFunc handles functions that only exist in MetricsQL.
// Functions not listed here don't change labels.
// See https://docs.victoriametrics.com/victoriametrics/metricsql/.
func parseMetricsQLFunc(s *Source, expr string, n *ast.Call) *Source {
	// (a, b) is a union of a and b.
	if n.Func.Name != "" && !mql.IsSupportedFunction(n.Func.Name) {
		// Unsupported function
		return new(Source)
	}

	s.Returns = n.Func.ReturnType
	if s.Returns == ast.ValueTypeNone {
		s.Returns = ast.ValueTypeVector
	}
	addedReason := fmt.Sprintf("This label will be added to the result by a %s() call.", n.Func.Name)

	switch n.Func.Name {
	case "alias":
		// alias(q, "name")
		s.guaranteeLabel(addedReason, n.PosRange, model.MetricNameLabel)

	case "label_set":
		// label_set(q, "label1", "value1", ...)
		for i := 1; i+1 < len(n.Args); i += 2 {
			name, ok := stringArg(n, i)
			if !ok {
				continue
			}
			if value, _ := stringArg(n, i+1); value == "" {
				s.excludeLabel(fmt.Sprintf("This label is removed by a %s() call.", n.Func.Name), n.PosRange, name)
			} else {
				s.guaranteeLabel(addedReason, n.PosRange, name)
			}
		}

	case "label_del":
		// label_del(q, "label1", ...)
		for i := 1; i < len(n.Args); i++ {
			if name, ok := stringArg(n, i); ok {
				s.excludeLabel(fmt.Sprintf("This label is removed by a %s() call.", n.Func.Name), n.PosRange, name)
			}
		}

	case "label_keep":
		// label_keep(q, "label1", ...)
		names := []string{model.MetricNameLabel}
		for i := 1; i < len(n.Args); i++ {
			if name, ok := stringArg(n, i); ok {
				names = append(names, name)
			}
		}
		funcNamePos := FindFuncNamePosition(expr, n.PosRange, n.Func.Name)
		s.excludeAllLabels(
			expr,
			fmt.Sprintf("Query is using %s(), only labels passed to it will be present on the results.", n.Func.Name),
			funcNamePos,
			funcNamePos,
			names,
		)

	case "label_copy", "label_move":
		// label_copy(q, "src_label1", "dst_label1", ...)
		for i := 1; i+1 < len(n.Args); i += 2 {
			src, ok := stringArg(n, i)
			if !ok {
				continue
			}
			dst, ok := stringArg(n, i+1)
			if !ok {
				continue
			}
			s.UsedLabels = appendToSlice(s.UsedLabels, src)
			if l, ok := s.Labels[src]; ok && l.Kind == GuaranteedLabel {
				s.guaranteeLabel(addedReason, n.PosRange, dst)
			} else if s.CanHaveLabel(src) {
				s.includeLabel(expr, addedReason, n.PosRange, dst)
			}
			if n.Func.Name == "label_move" && src != dst {
				s.excludeLabel(fmt.Sprintf("This label is moved to `%s` by a %s() call.", dst, n.Func.Name), n.PosRange, src)
			}
		}

	case "count_values_over_time", "quantiles_over_time", "histogram_quantiles":
		// count_values_over_time("label", m[d])
		// quantiles_over_time("phiLabel", phi1, ..., m[d])
		// histogram_quantiles("phiLabel", phi1, ..., buckets)
		if name, ok := stringArg(n, 0); ok {
			s.guaranteeLabel(addedReason, n.PosRange, name)
		}

	case "aggr_over_time":
		// aggr_over_time(("rollup_func1", ...), m[d])
		s.guaranteeLabel(addedReason, n.PosRange, "rollup")

	case "rollup", "rollup_candlestick", "rollup_delta", "rollup_deriv", "rollup_increase", "rollup_rate", "rollup_scrape_interval":
		// rollup(m[d]) returns min, max and avg series with a rollup label,
		// rollup(m[d], "max") returns only one series without it.
		if len(n.Args) == 1 {
			s.guaranteeLabel(addedReason, n.PosRange, "rollup")
		}

	case "histogram_over_time":
		s.guaranteeLabel(addedReason, n.PosRange, "vmrange")

	case "prometheus_buckets":
		s.guaranteeLabel(addedReason, n.PosRange, "le")
		s.excludeLabel(fmt.Sprintf("This label is converted to `le` by a %s() call.", n.Func.Name), n.PosRange, "vmrange")
	}

	if s.Returns == ast.ValueTypeScalar {
		s.ReturnInfo.AlwaysReturns = true
		s.ReturnInfo.ValuePosition = n.PosRange
		s.excludeAllLabels(
			expr,
			fmt.Sprintf("Calling `%s()` will return a scalar value with no labels.", n.Func.Name),
			n.PosRange,
			n.PosRange,
			nil,
		)
	}
	return s
}

func stringArg(n *ast.Call, idx int) (string, bool) {
	if idx >= len(n.Args) {
		return "", false
	}
	sl, ok := n.Args[idx].(*ast.StringLiteral)
	if !ok {
		return "", false
	}
	return sl.Val, true
}
