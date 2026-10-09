package checks

import (
	"fmt"
	"slices"
	"strings"

	mql "github.com/VictoriaMetrics/metricsql"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
)

const SyntaxCheckMisplacedWindowDetails = "In MetricsQL a lookbehind window placed after a function call, like `rate(foo)[5m]`, " +
	"is applied to the function results, not to the time series selector.\n" +
	"The function itself has no window, so MetricsQL picks one at query time from the query step, " +
	"vmalert sends `-datasource.queryStep` as the step, it's `5m` by default.\n" +
	"`[5m]` turns the function results into a subquery and a subquery used outside of rollup functions " +
	"is wrapped in `default_rollup()`, which returns the last value.\n" +
	"The result is the same as `rate(foo)`, to calculate it over `5m` put the window inside: `rate(foo[5m])`.\n" +
	SyntaxCheckMetricsQLDetails

const SyntaxCheckImplicitConversionDetails = "MetricsQL silently rewrites some queries before running them, see " +
	"[implicit query conversions](https://docs.victoriametrics.com/victoriametrics/metricsql/#implicit-query-conversions).\n" +
	"A rollup function like `rate()` called on anything other than a time series selector, and a lookbehind window like `[5m]` " +
	"placed after anything other than a time series selector, become subqueries that use the query step, " +
	"which returns unexpected results most of the time.\n" +
	"VictoriaMetrics runs such queries by default, rejects them when started with `-search.disableImplicitConversion` " +
	"and logs them with `-search.logImplicitConversion`. pint reports the same queries as these flags, " +
	"it uses the same `IsLikelyInvalid()` function from the MetricsQL parser."

// checkMetricsQL reports MetricsQL queries that are valid but don't do what they look like.
func (c SyntaxCheck) checkMetricsQL(entry *discovery.Entry, expr *parser.PromQLExpr) []Problem {
	problems, reported := c.checkMisplacedWindows(expr)
	problems = append(problems, c.checkImplicitConversions(expr, reported)...)
	return append(problems, c.checkSubqueriesWithoutStep(entry, expr)...)
}

// checkMisplacedWindows reports MetricsQL rollup functions without a lookbehind window
// that are followed by one, like rate(foo)[5m]. The window doesn't apply to rate(),
// it creates a subquery that MetricsQL wraps in default_rollup().
// It also returns positions of reported subqueries.
func (c SyntaxCheck) checkMisplacedWindows(expr *parser.PromQLExpr) (problems []Problem, spans []ast.PositionRange) {
	query := expr.Value.Value
	for _, node := range parser.WalkDownExpr[*ast.SubqueryExpr](expr.Query()) {
		sq := node.Expr.(*ast.SubqueryExpr)
		if sq.Implicit || isRollupFuncArg(node) {
			// max_over_time(rate(foo)[1h:1m]) is a regular subquery.
			continue
		}
		call, ok := withoutParens(sq.Expr).(*ast.Call)
		if !ok {
			continue
		}
		arg, ok := implicitWindowArg(call)
		if !ok {
			continue
		}

		msg := fmt.Sprintf(
			"`[%s]` is applied to the results of `%s()`, not to its argument, `%s()` has no window and MetricsQL picks one from the query step.",
			sq.RangeText, call.Func.Name, call.Func.Name,
		)
		if ms, ok := arg.(*ast.MatrixSelector); ok && !hasModifiers(ms.VectorSelector) {
			// Insert the window right after the selector: rate(foo{job="a"})[5m] -> rate(foo{job="a"}[5m]).
			end := ms.VectorSelector.PositionRange().End
			msg += fmt.Sprintf(" Did you mean `%s[%s]%s`?",
				query[call.PosRange.Start:end], sq.RangeText, query[end:call.PosRange.End])
		}

		problems = append(problems, Problem{
			Anchor:   AnchorAfter,
			Lines:    expr.Value.Pos.Lines(),
			Reporter: c.Reporter(),
			Summary:  "misplaced lookbehind window",
			Details:  SyntaxCheckMisplacedWindowDetails,
			Diagnostics: []diags.Diagnostic{
				{
					Message:     msg,
					Pos:         expr.Value.Pos,
					Expr:        expr.Query().Expr,
					FirstColumn: int(sq.PosRange.Start) + 1,
					LastColumn:  int(sq.PosRange.End),
					Kind:        diags.Issue,
				},
			},
			Severity: Warning,
		})
		spans = append(spans, sq.PosRange)
	}
	return problems, spans
}

// checkImplicitConversions reports queries that VictoriaMetrics rejects when started with
// -search.disableImplicitConversion. Subqueries at skip positions are already reported.
func (c SyntaxCheck) checkImplicitConversions(expr *parser.PromQLExpr, skip []ast.PositionRange) (problems []Problem) {
	query := expr.Value.Value
	e, err := mql.Parse(query)
	if err != nil || !mql.IsLikelyInvalid(e) {
		return nil
	}

	conversions := implicitConversions(e, len(query))
	if len(conversions) == 0 {
		// IsLikelyInvalid() found a conversion implicitConversions() doesn't know about.
		conversions = append(conversions, implicitConversion{
			node:  e,
			inner: nil,
			span:  ast.PositionRange{Start: 0, End: ast.Pos(len(query))},
		})
	}
	for _, ic := range conversions {
		if slices.Contains(skip, ic.span) {
			continue
		}
		problems = append(problems, Problem{
			Anchor:   AnchorAfter,
			Lines:    expr.Value.Pos.Lines(),
			Reporter: c.Reporter(),
			Summary:  "implicit subquery conversion",
			Details:  SyntaxCheckImplicitConversionDetails,
			Diagnostics: []diags.Diagnostic{
				{
					Message:     ic.message(query),
					Pos:         expr.Value.Pos,
					Expr:        expr.Query().Expr,
					FirstColumn: int(ic.span.Start) + 1,
					LastColumn:  int(ic.span.End),
					Kind:        diags.Issue,
				},
			},
			Severity: Warning,
		})
	}
	return problems
}

// implicitConversion is a part of a MetricsQL query that VictoriaMetrics converts into a subquery.
type implicitConversion struct {
	// node is a *mql.RollupExpr for a window after a non-selector, like sum(foo)[5m],
	// or a *mql.FuncExpr for a rollup function called on a non-selector, like rate(sum(foo)).
	node mql.Expr
	// inner is the expression turned into a subquery.
	inner mql.Expr
	span  ast.PositionRange
}

// implicitConversions returns all conversions metricsql.IsLikelyInvalid() reports.
// It must find something for every query IsLikelyInvalid() returns true for, which is
// verified against the parser test corpus.
func implicitConversions(e mql.Expr, queryLen int) (found []implicitConversion) {
	add := func(node, inner mql.Expr) {
		found = append(found, implicitConversion{node: node, inner: inner, span: sourceSpan(node, queryLen)})
	}
	mql.VisitAll(e, func(n mql.Expr) {
		switch t := n.(type) {
		case *mql.RollupExpr:
			// sum(foo)[5m] is sum(foo)[5m:1i], only foo[5m] is a range selector.
			if t.Window != nil && !t.ForSubquery() {
				if _, ok := t.Expr.(*mql.MetricExpr); !ok {
					add(t, t.Expr)
				}
			}
		case *mql.FuncExpr:
			// IsLikelyInvalid() treats timestamp() as a transform function, like Prometheus.
			if t.Name == "timestamp" {
				return
			}
			idx := mql.GetRollupArgIdx(t)
			if idx < 0 || idx >= len(t.Args) {
				return
			}
			// rate(sum(foo)) is rate((sum(foo))[1i:1i]).
			switch arg := t.Args[idx].(type) {
			case *mql.MetricExpr:
			case *mql.RollupExpr:
				if _, ok := arg.Expr.(*mql.MetricExpr); !ok && arg.Window == nil {
					add(t, arg)
				}
			default:
				add(t, arg)
			}
		}
	})
	return found
}

func (ic implicitConversion) message(query string) string {
	switch t := ic.node.(type) {
	case *mql.RollupExpr:
		window := string(t.Window.AppendString(nil))
		return fmt.Sprintf(
			"`[%s]` is placed after `%s`, which isn't a time series selector, so MetricsQL turns it into a subquery that uses the query step, as if it was written `%s[%s:]`.",
			window, sourceText(query, t.Expr), subqueryOperand(query, t.Expr), window,
		)
	case *mql.FuncExpr:
		return fmt.Sprintf(
			"`%s()` is called on `%s`, which isn't a time series selector, so MetricsQL turns it into the subquery `%s[1i:1i]` and `%s()` is calculated over the query step.",
			t.Name, sourceText(query, ic.inner), subqueryOperand(query, ic.inner), t.Name,
		)
	default:
		return "This query requires an implicit subquery conversion."
	}
}

// sourceSpan returns the position of e in the query, or the whole query if e
// was expanded from a WITH template or doesn't have a valid position.
func sourceSpan(e mql.Expr, queryLen int) ast.PositionRange {
	si := mql.SourceOf(e)
	if si == nil || si.Pos.Start < 0 || si.Pos.End > queryLen || si.Pos.Start >= si.Pos.End {
		return ast.PositionRange{Start: 0, End: ast.Pos(queryLen)}
	}
	return ast.PositionRange{Start: ast.Pos(si.Pos.Start), End: ast.Pos(si.Pos.End)}
}

// sourceText returns e as written in the query, including parentheses around it,
// or its string representation if it was expanded from a WITH template.
func sourceText(query string, e mql.Expr) string {
	si := mql.SourceOf(e)
	if si != nil && si.Origin.IsZero() {
		pos := si.OuterPos()
		if pos.Start >= 0 && pos.Start < pos.End && pos.End <= len(query) {
			return query[pos.Start:pos.End]
		}
	}
	return string(e.AppendString(nil))
}

// subqueryOperand returns e as written in the query, wrapped in parentheses if it
// needs them to be used as a subquery.
func subqueryOperand(query string, e mql.Expr) string {
	text := sourceText(query, e)
	switch e.(type) {
	case *mql.MetricExpr, *mql.FuncExpr, *mql.AggrFuncExpr:
		return text
	}
	if si := mql.SourceOf(e); si != nil && len(si.Parens) > 0 && si.Origin.IsZero() {
		return text
	}
	return "(" + text + ")"
}

// isRollupFuncArg returns true if the node is passed to a function that expects a range vector.
func isRollupFuncArg(node *parser.PromQLNode) bool {
	child := node
	parent := node.Parent
	for parent != nil {
		if _, ok := parent.Expr.(*ast.ParenExpr); !ok {
			break
		}
		child = parent
		parent = parent.Parent
	}
	if parent == nil {
		return false
	}
	call, ok := parent.Expr.(*ast.Call)
	if !ok {
		return false
	}
	for i, arg := range call.Args {
		if arg == child.Expr {
			return i < len(call.Func.ArgTypes) && call.Func.ArgTypes[i] == ast.ValueTypeMatrix
		}
	}
	return false
}

// implicitWindowArg returns the range vector argument of a function call written without a window.
func implicitWindowArg(call *ast.Call) (ast.Node, bool) {
	for i, arg := range call.Args {
		if i >= len(call.Func.ArgTypes) || call.Func.ArgTypes[i] != ast.ValueTypeMatrix {
			continue
		}
		switch n := withoutParens(arg).(type) {
		case *ast.MatrixSelector:
			if n.Implicit {
				return n, true
			}
		case *ast.SubqueryExpr:
			if n.Implicit {
				return n, true
			}
		}
	}
	return nil, false
}

func hasModifiers(node ast.Node) bool {
	vs, ok := node.(*ast.VectorSelector)
	if !ok {
		return true
	}
	return vs.OriginalOffset != 0 || vs.At != nil || vs.Timestamp != nil || vs.StartOrEnd != ""
}

func withoutParens(node ast.Node) ast.Node {
	for {
		pe, ok := node.(*ast.ParenExpr)
		if !ok {
			return node
		}
		node = pe.Expr
	}
}

// A subquery without a step, like foo[1h:], uses the step of the query, see "subqueries" in
// the MetricsQL docs. vmalert evaluates rules with instant queries and sends
// -datasource.queryStep, 5m by default, or `step` from the group params. Checked with
// VictoriaMetrics v1.153.0, a series with a sample every 15s:
// count_over_time(foo[1h:]) returned 12, 60 and 240 with step 300s, 60s and 15s, while
// count_over_time(foo[1h:1m]) always returned 60.

const SyntaxCheckSubqueryStepDetails = "A subquery without a step, like `foo[1h:]`, uses the step of the query, " +
	"see [subqueries](https://docs.victoriametrics.com/victoriametrics/metricsql/#subqueries).\n" +
	"vmalert evaluates rules with instant queries and sends `-datasource.queryStep` as the step, 5m by default, " +
	"or `step` from the group `params`. vmui and Grafana use the step of the graph instead, " +
	"so the same query returns different results there. Set the step explicitly, like `foo[1h:1m]`, " +
	"to get the same results everywhere.\n" +
	SyntaxCheckMetricsQLDetails

// subqueryWithoutStep returns the range text of a subquery written as `[range:]`, ok is false if
// the subquery has a step or no colon, like sum(foo)[5m], which is an implicit conversion.
func subqueryWithoutStep(query string, sq *ast.SubqueryExpr) (rangeText string, ok bool) {
	if sq.Implicit || sq.Step != 0 || sq.StepText != "" {
		return "", false
	}
	text := query[sq.PosRange.Start:sq.PosRange.End]
	end := strings.LastIndexByte(text, ']')
	if end < 0 {
		return "", false
	}
	start := strings.LastIndexByte(text[:end], '[')
	if start < 0 {
		return "", false
	}
	window, step, hasColon := strings.Cut(text[start+1:end], ":")
	if !hasColon || strings.TrimSpace(step) != "" {
		return "", false
	}
	return strings.TrimSpace(window), true
}

// checkSubqueriesWithoutStep reports subqueries without a step in rules evaluated by vmalert.
func (c SyntaxCheck) checkSubqueriesWithoutStep(entry *discovery.Entry, expr *parser.PromQLExpr) (problems []Problem) {
	query := expr.Value.Value
	stepText := "the vmalert query step, `-datasource.queryStep`, 5m by default"
	if entry.Group != nil {
		if step, ok := entry.Group.VMAlert.Param(vmalertParamStep); ok {
			stepText = fmt.Sprintf("`%s`, the `step` from `params` of this group", step)
		}
	}
	for _, node := range parser.WalkDownExpr[*ast.SubqueryExpr](expr.Query()) {
		sq := node.Expr.(*ast.SubqueryExpr)
		rangeText, ok := subqueryWithoutStep(query, sq)
		if !ok {
			continue
		}
		problems = append(problems, Problem{
			Anchor:   AnchorAfter,
			Lines:    expr.Value.Pos.Lines(),
			Reporter: c.Reporter(),
			Summary:  "subquery without a step",
			Details:  SyntaxCheckSubqueryStepDetails,
			Diagnostics: []diags.Diagnostic{
				{
					Message: fmt.Sprintf(
						"`[%s:]` has no step, so in vmalert the subquery uses %s, and the step of the graph in vmui or Grafana. "+
							"Set the step explicitly, like `[%s:1m]`.",
						rangeText, stepText, rangeText,
					),
					Pos:         expr.Value.Pos,
					Expr:        expr.Query().Expr,
					FirstColumn: int(sq.PosRange.Start) + 1,
					LastColumn:  int(sq.PosRange.End),
					Kind:        diags.Issue,
				},
			},
			Severity: Warning,
		})
	}
	return problems
}
