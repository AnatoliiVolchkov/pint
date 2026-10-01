package ast_test

import (
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser/ast"
)

func selector(name string, groups ...[]*labels.Matcher) *ast.VectorSelector {
	vs := &ast.VectorSelector{Name: name}
	for i, g := range groups {
		if name != "" {
			g = append(g, labels.MustNewMatcher(labels.MatchEqual, model.MetricNameLabel, name))
		}
		if i == 0 {
			vs.LabelMatchers = g
		} else {
			vs.OrMatchers = append(vs.OrMatchers, g)
		}
	}
	if len(groups) == 0 && name != "" {
		vs.LabelMatchers = []*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, model.MetricNameLabel, name)}
	}
	return vs
}

func eq(name, value string) *labels.Matcher {
	return labels.MustNewMatcher(labels.MatchEqual, name, value)
}

func TestPrinter(t *testing.T) {
	fill := 5.0
	fill2 := 1.0
	ts := int64(1234000)

	testCases := []struct {
		node     ast.Node
		expected string
	}{
		{node: selector("foo"), expected: "foo"},
		{node: selector("foo", []*labels.Matcher{eq("job", "a"), eq("env", "b")}), expected: `foo{env="b",job="a"}`},
		{node: selector("", []*labels.Matcher{eq("job", "a")}), expected: `{job="a"}`},
		{
			node:     selector("foo", []*labels.Matcher{eq("a", "1")}, []*labels.Matcher{eq("b", "2")}),
			expected: `foo{a="1" or b="2"}`,
		},
		{
			node: &ast.MatrixSelector{
				VectorSelector: &ast.VectorSelector{
					Name:           "foo",
					OriginalOffset: time.Hour,
					Timestamp:      &ts,
				},
				Range: time.Minute * 5,
			},
			expected: "foo[5m] @ 1234.000 offset 1h",
		},
		{
			node: &ast.MatrixSelector{
				VectorSelector: &ast.VectorSelector{Name: "foo", OffsetText: "1i"},
				Implicit:       true,
				StepRelative:   true,
			},
			expected: "foo offset 1i",
		},
		{
			node: &ast.MatrixSelector{
				VectorSelector: &ast.VectorSelector{Name: "foo"},
				RangeText:      "2i",
				StepRelative:   true,
			},
			expected: "foo[2i]",
		},
		{
			node: &ast.SubqueryExpr{
				Expr:       selector("foo"),
				Range:      time.Hour,
				StartOrEnd: ast.OpEnd,
			},
			expected: "foo[1h:] @ end()",
		},
		{
			node: &ast.OffsetExpr{
				Expr:           &ast.BinaryExpr{Op: ast.OpAdd, LHS: selector("foo"), RHS: selector("bar")},
				OriginalOffset: -time.Minute,
			},
			expected: "(foo + bar) offset -1m",
		},
		{
			node: &ast.AggregateExpr{
				Op:       ast.OpTopK,
				Param:    &ast.NumberLiteral{Val: 5},
				Expr:     selector("foo"),
				Grouping: []string{"job", "a.b"},
			},
			expected: `topk by (job, "a.b") (5, foo)`,
		},
		{
			node: &ast.AggregateExpr{
				Op:      ast.OpSum,
				Expr:    selector("foo"),
				Without: true,
				Limit:   10,
			},
			expected: "sum without () (foo) limit 10",
		},
		{
			node: &ast.AggregateExpr{
				Op:   "quantiles",
				Args: []ast.Node{&ast.StringLiteral{Val: "phi"}, &ast.NumberLiteral{Val: 0.5}, selector("foo")},
			},
			expected: `quantiles("phi", 0.5, foo)`,
		},
		{
			node: &ast.BinaryExpr{
				Op:  ast.OpMul,
				LHS: selector("foo"),
				RHS: selector("bar"),
				VectorMatching: &ast.VectorMatching{
					Card:           ast.CardManyToOne,
					On:             true,
					MatchingLabels: []string{"job"},
					Include:        []string{"a"},
					Prefix:         "bar_",
					FillValues:     ast.FillValues{LHS: &fill, RHS: &fill2},
				},
			},
			expected: `foo * on (job) group_left (a) prefix "bar_" fill_left (5) fill_right (1) bar`,
		},
		{
			node: &ast.BinaryExpr{
				Op:              ast.OpDefault,
				LHS:             selector("foo"),
				RHS:             &ast.NumberLiteral{Val: 0},
				KeepMetricNames: true,
			},
			expected: "(foo default 0) keep_metric_names",
		},
		{
			node: &ast.BinaryExpr{
				Op:         ast.OpGtr,
				LHS:        selector("foo"),
				RHS:        &ast.NumberLiteral{Val: 1},
				ReturnBool: true,
			},
			expected: "foo > bool 1",
		},
		{
			node: &ast.Call{
				Func:            &ast.Function{Name: "abs", ReturnType: ast.ValueTypeVector},
				Args:            []ast.Node{&ast.UnaryExpr{Op: ast.OpSub, Expr: selector("foo")}},
				KeepMetricNames: true,
			},
			expected: "abs(-foo) keep_metric_names",
		},
		{node: &ast.NumberLiteral{Val: 300, Duration: true}, expected: "5m"},
		{node: &ast.StringLiteral{Val: `a"b`}, expected: `"a\"b"`},
		{node: &ast.ParenExpr{Expr: selector("foo")}, expected: "(foo)"},
		{
			node: &ast.DurationExpr{
				Op:      ast.OpAdd,
				LHS:     &ast.NumberLiteral{Val: 60, Duration: true},
				RHS:     &ast.DurationExpr{Op: ast.OpStep},
				Wrapped: true,
			},
			expected: "(1m + step())",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.node.String())
		})
	}
}

func TestChildren(t *testing.T) {
	foo := selector("foo")
	bar := selector("bar")
	param := &ast.NumberLiteral{Val: 3}

	agg := &ast.AggregateExpr{Op: ast.OpTopK, Param: param, Expr: foo}
	require.Equal(t, []ast.Node{foo, param}, ast.Children(agg))

	multi := &ast.AggregateExpr{Op: ast.OpSum, Expr: foo, Args: []ast.Node{foo, bar}}
	require.Equal(t, []ast.Node{foo, bar}, ast.Children(multi))

	at := &ast.Call{Func: &ast.Function{Name: "end", ReturnType: ast.ValueTypeScalar}}
	off := &ast.OffsetExpr{Expr: foo, At: at}
	require.Equal(t, []ast.Node{foo, at}, ast.Children(off))

	ms := &ast.MatrixSelector{VectorSelector: foo}
	require.Equal(t, []ast.Node{foo}, ast.Children(ms))
	require.Empty(t, ast.Children(foo))

	var visited []string
	ast.Inspect(&ast.BinaryExpr{Op: ast.OpAdd, LHS: agg, RHS: bar}, func(n ast.Node) bool {
		visited = append(visited, n.String())
		_, isAgg := n.(*ast.AggregateExpr)
		return !isAgg
	})
	require.Equal(t, []string{"topk(3, foo) + bar", "topk(3, foo)", "bar"}, visited)
}

func TestMatcherGroups(t *testing.T) {
	vs := selector("foo", []*labels.Matcher{eq("a", "1")}, []*labels.Matcher{eq("b", "2")})
	require.Len(t, vs.MatcherGroups(), 2)
	require.Len(t, vs.AllMatchers(), 4)

	single := selector("foo", []*labels.Matcher{eq("a", "1")})
	require.Equal(t, single.LabelMatchers, single.AllMatchers())
}

func TestOps(t *testing.T) {
	require.True(t, ast.OpGte.IsComparisonOperator())
	require.False(t, ast.OpAnd.IsComparisonOperator())
	require.True(t, ast.OpUnless.IsSetOperator())
	require.False(t, ast.OpIf.IsSetOperator())
	require.True(t, ast.OpLimitRatio.IsAggregatorWithParam())
	require.False(t, ast.OpSum.IsAggregatorWithParam())
	require.Equal(t, "many-to-one", ast.CardManyToOne.String())
}

func TestFunctionArgType(t *testing.T) {
	f := &ast.Function{Name: "f", ArgTypes: []ast.ValueType{ast.ValueTypeMatrix, ast.ValueTypeScalar}}
	require.Equal(t, ast.ValueTypeMatrix, f.ArgType(0))
	require.Equal(t, ast.ValueTypeScalar, f.ArgType(5))
	require.Equal(t, ast.ValueTypeNone, (&ast.Function{}).ArgType(0))
}

func TestParseError(t *testing.T) {
	pe := &ast.ParseError{Message: "bad"}
	require.EqualError(t, pe, "bad")
}
