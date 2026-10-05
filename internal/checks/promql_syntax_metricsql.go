package checks

import (
	"fmt"

	"github.com/cloudflare/pint/internal/diags"
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

// checkMisplacedWindows reports MetricsQL rollup functions without a lookbehind window
// that are followed by one, like rate(foo)[5m]. The window doesn't apply to rate(),
// it creates a subquery that MetricsQL wraps in default_rollup().
func (c SyntaxCheck) checkMisplacedWindows(expr *parser.PromQLExpr) (problems []Problem) {
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
	}
	return problems
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
