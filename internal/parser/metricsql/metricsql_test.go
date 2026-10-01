package metricsql_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/parser/promql"
)

// Queries valid in both PromQL and MetricsQL that must produce identical AST.
var sharedCorpus = []string{
	"foo",
	"{job=\"foo\"}",
	"foo{job=\"foo\", env=~\"prod|dev\", instance!=\"a\", path!~\"/api/.+\"}",
	"foo offset 5m",
	"foo offset -5m",
	"foo @ 1234",
	"foo @ start()",
	"foo[5m] @ end() offset 1h",
	"rate(foo[5m])",
	"rate(foo[1h30m])",
	"rate(foo[5m] offset 1h)",
	"rate(foo[5m:1m])",
	"max_over_time(rate(foo[5m])[1h:])",
	"max_over_time((foo > 0)[30m:5m] offset 1h)",
	"sum(foo)",
	"sum by (job) (foo)",
	"sum without (job, instance) (foo)",
	"sum(foo) by (job)",
	"topk(5, foo)",
	"count_values(\"version\", build_info)",
	"quantile(0.9, foo)",
	"foo + bar",
	"foo + on(job) bar",
	"foo + ignoring(job) bar",
	"foo * on(job) group_left(instance) bar",
	"foo * ignoring(job) group_right() bar",
	"foo > bool 5",
	"foo and bar",
	"foo or on() vector(1)",
	"foo unless ignoring(a) bar",
	"-foo",
	"-(foo + bar)",
	"(foo)",
	"((foo + bar)) / baz",
	"1e3",
	"\"a string\"",
	"label_replace(foo, \"dst\", \"$1\", \"src\", \"(.*)\")",
	"label_join(foo, \"dst\", \",\", \"a\", \"b\")",
	"histogram_quantile(0.99, sum by (le) (rate(foo_bucket[5m])))",
	"absent(foo{job=\"bar\"})",
	"absent_over_time(foo[5m])",
	"vector(1)",
	"scalar(foo)",
	"time() - foo",
	"round(foo, 5)",
	"clamp(foo, 0, 1)",
	"timestamp(foo)",
	"foo atan2 bar",
	"foo % 2 ^ bar",
	"sum(rate(foo[5m])) by (job) / on(job) group_left sum(rate(bar[5m])) by (job) > 0.5",
	"count(up == 0) > 0 and on() hour() > 8",
	"foo{job=\"a\"}\n  / \n  bar{job=\"b\"}",
}

type nodeShape struct {
	typ      string
	text     string
	value    ast.ValueType
	children []nodeShape
	start    ast.Pos
	end      ast.Pos
}

func shape(n ast.Node) nodeShape {
	s := nodeShape{
		typ:   fmt.Sprintf("%T", n),
		text:  n.String(),
		value: n.Type(),
		start: n.PositionRange().Start,
		end:   n.PositionRange().End,
	}
	for _, child := range ast.Children(n) {
		s.children = append(s.children, shape(child))
	}
	return s
}

func TestSharedCorpus(t *testing.T) {
	for _, q := range sharedCorpus {
		t.Run(q, func(t *testing.T) {
			expected, err := promql.Language{}.ParseExpr(q)
			require.NoError(t, err)
			got, err := metricsql.Language{}.ParseExpr(q)
			require.NoError(t, err)
			require.Equal(t, shape(expected), shape(got))
		})
	}
}

func TestSharedCorpusDetails(t *testing.T) {
	for _, q := range sharedCorpus {
		t.Run(q, func(t *testing.T) {
			expected, err := promql.Language{}.ParseExpr(q)
			require.NoError(t, err)
			got, err := metricsql.Language{}.ParseExpr(q)
			require.NoError(t, err)
			requireSameDetails(t, expected, got)
		})
	}
}

func requireSameDetails(t *testing.T, expected, got ast.Node) {
	t.Helper()
	switch e := expected.(type) {
	case *ast.BinaryExpr:
		g, ok := got.(*ast.BinaryExpr)
		require.True(t, ok)
		require.Equal(t, e.Op, g.Op)
		require.Equal(t, e.ReturnBool, g.ReturnBool)
		if e.VectorMatching == nil {
			require.Nil(t, g.VectorMatching)
		} else {
			require.NotNil(t, g.VectorMatching, "missing VectorMatching for %s", e)
			require.Equal(t, e.VectorMatching.Card, g.VectorMatching.Card)
			require.Equal(t, e.VectorMatching.On, g.VectorMatching.On)
			require.ElementsMatch(t, e.VectorMatching.MatchingLabels, g.VectorMatching.MatchingLabels)
			require.ElementsMatch(t, e.VectorMatching.Include, g.VectorMatching.Include)
		}
	case *ast.AggregateExpr:
		g, ok := got.(*ast.AggregateExpr)
		require.True(t, ok)
		require.Equal(t, e.Op, g.Op)
		require.Equal(t, e.Without, g.Without)
		require.ElementsMatch(t, e.Grouping, g.Grouping)
		require.Empty(t, g.Args)
	case *ast.Call:
		g, ok := got.(*ast.Call)
		require.True(t, ok)
		require.Equal(t, e.Func.Name, g.Func.Name)
		require.Equal(t, e.Func.ArgTypes, g.Func.ArgTypes)
		require.Equal(t, e.Func.ReturnType, g.Func.ReturnType)
	case *ast.VectorSelector:
		g, ok := got.(*ast.VectorSelector)
		require.True(t, ok)
		require.Equal(t, e.Name, g.Name)
		require.Equal(t, fmt.Sprint(e.LabelMatchers), fmt.Sprint(g.LabelMatchers))
		require.Equal(t, e.OriginalOffset, g.OriginalOffset)
		require.Equal(t, e.Timestamp, g.Timestamp)
		require.Equal(t, e.StartOrEnd, g.StartOrEnd)
	case *ast.MatrixSelector:
		g, ok := got.(*ast.MatrixSelector)
		require.True(t, ok)
		require.Equal(t, e.Range, g.Range)
		require.False(t, g.Implicit)
		require.False(t, g.StepRelative)
	case *ast.SubqueryExpr:
		g, ok := got.(*ast.SubqueryExpr)
		require.True(t, ok)
		require.Equal(t, e.Range, g.Range)
		require.Equal(t, e.Step, g.Step)
		require.Equal(t, e.OriginalOffset, g.OriginalOffset)
	}
	ec := ast.Children(expected)
	gc := ast.Children(got)
	require.Len(t, gc, len(ec))
	for i := range ec {
		requireSameDetails(t, ec[i], gc[i])
	}
}

func TestMetricsQLOnly(t *testing.T) {
	type testCaseT struct {
		check func(t *testing.T, node ast.Node)
		query string
		text  string
		// validPromQL is true for queries that are valid PromQL but parsed differently by MetricsQL.
		validPromQL bool
	}

	testCases := []testCaseT{
		{
			query: "rate(foo)",
			text:  "rate(foo)",
			check: func(t *testing.T, node ast.Node) {
				call := node.(*ast.Call)
				ms := call.Args[0].(*ast.MatrixSelector)
				require.True(t, ms.Implicit)
				require.True(t, ms.StepRelative)
				require.Equal(t, "foo", ms.VectorSelector.(*ast.VectorSelector).Name)
				require.Equal(t, ast.PositionRange{Start: 5, End: 8}, ms.PosRange)
			},
		},
		{
			query: "rate(foo offset 5m)",
			text:  "rate(foo offset 5m)",
			check: func(t *testing.T, node ast.Node) {
				ms := node.(*ast.Call).Args[0].(*ast.MatrixSelector)
				require.True(t, ms.Implicit)
				require.Equal(t, "5m0s", ms.VectorSelector.(*ast.VectorSelector).OriginalOffset.String())
			},
		},
		{
			query: "rate(sum(foo))",
			text:  "rate(sum(foo))",
			check: func(t *testing.T, node ast.Node) {
				sq := node.(*ast.Call).Args[0].(*ast.SubqueryExpr)
				require.True(t, sq.Implicit)
				require.IsType(t, &ast.AggregateExpr{}, sq.Expr)
			},
		},
		{
			query: "rate(foo[1i])",
			text:  "rate(foo[1i])",
			check: func(t *testing.T, node ast.Node) {
				ms := node.(*ast.Call).Args[0].(*ast.MatrixSelector)
				require.False(t, ms.Implicit)
				require.True(t, ms.StepRelative)
				require.Zero(t, ms.Range)
			},
		},
		{
			query:       "foo[5m]",
			text:        "foo[5m]",
			validPromQL: true,
			check: func(t *testing.T, node ast.Node) {
				require.IsType(t, &ast.MatrixSelector{}, node)
			},
		},
		{
			query: "sum(foo)[5m]",
			text:  "sum(foo)[5m:]",
			check: func(t *testing.T, node ast.Node) {
				sq := node.(*ast.SubqueryExpr)
				require.Equal(t, "5m0s", sq.Range.String())
			},
		},
		{
			query: "sum(foo) offset 1h",
			text:  "sum(foo) offset 1h",
			check: func(t *testing.T, node ast.Node) {
				oe := node.(*ast.OffsetExpr)
				require.Equal(t, "1h0m0s", oe.OriginalOffset.String())
				require.Equal(t, ast.ValueTypeVector, oe.Type())
			},
		},
		{
			query: "foo offset -1i",
			text:  "foo offset -1i",
			check: func(t *testing.T, node ast.Node) {
				vs := node.(*ast.VectorSelector)
				require.Equal(t, "-1i", vs.OffsetText)
			},
		},
		{
			query: "foo @ (time() - 60)",
			text:  "foo @ (time() - 60)",
			check: func(t *testing.T, node ast.Node) {
				vs := node.(*ast.VectorSelector)
				require.IsType(t, &ast.ParenExpr{}, vs.At)
				require.Len(t, ast.Children(vs), 1)
			},
		},
		{
			query: "foo{a=\"1\" or b=\"2\"}",
			text:  "foo{a=\"1\" or b=\"2\"}",
			check: func(t *testing.T, node ast.Node) {
				vs := node.(*ast.VectorSelector)
				require.Equal(t, "foo", vs.Name)
				require.Len(t, vs.MatcherGroups(), 2)
				require.Equal(t, "__name__", vs.OrMatchers[0][len(vs.OrMatchers[0])-1].Name)
			},
		},
		{
			query: "{__name__=\"foo\" or __name__=\"bar\"}",
			text:  "{__name__=\"foo\" or __name__=\"bar\"}",
			check: func(t *testing.T, node ast.Node) {
				require.Empty(t, node.(*ast.VectorSelector).Name)
			},
		},
		{
			query: "sum(foo) by (job) limit 5",
			text:  "sum by (job) (foo) limit 5",
			check: func(t *testing.T, node ast.Node) {
				require.Equal(t, 5, node.(*ast.AggregateExpr).Limit)
			},
		},
		{
			query: "median(foo)",
			text:  "median(foo)",
			check: func(t *testing.T, node ast.Node) {
				agg := node.(*ast.AggregateExpr)
				require.Equal(t, ast.Op("median"), agg.Op)
				require.Empty(t, agg.Args)
			},
		},
		{
			query: "topk_max(3, foo, \"other\")",
			text:  "topk_max(3, foo, \"other\")",
			check: func(t *testing.T, node ast.Node) {
				agg := node.(*ast.AggregateExpr)
				require.Equal(t, "foo", agg.Expr.String())
				require.Equal(t, "3", agg.Param.String())
				require.Len(t, agg.Args, 3)
				require.Len(t, ast.Children(agg), 3)
			},
		},
		{
			query: "quantiles(\"phi\", 0.5, 0.9, foo)",
			text:  "quantiles(\"phi\", 0.5, 0.9, foo)",
			check: func(t *testing.T, node ast.Node) {
				agg := node.(*ast.AggregateExpr)
				require.Equal(t, "foo", agg.Expr.String())
				require.Nil(t, agg.Param)
			},
		},
		{
			query: "sum(foo, bar)",
			text:  "sum(foo, bar)",
			check: func(t *testing.T, node ast.Node) {
				agg := node.(*ast.AggregateExpr)
				require.Equal(t, "foo", agg.Expr.String())
				require.Len(t, ast.Children(agg), 2)
			},
		},
		{
			query: "foo if bar",
			text:  "foo if bar",
			check: func(t *testing.T, node ast.Node) {
				bin := node.(*ast.BinaryExpr)
				require.Equal(t, ast.OpIf, bin.Op)
				require.Equal(t, ast.CardManyToMany, bin.VectorMatching.Card)
			},
		},
		{
			query: "foo ifnot bar",
			text:  "foo ifnot bar",
			check: func(t *testing.T, node ast.Node) {
				require.Equal(t, ast.OpIfNot, node.(*ast.BinaryExpr).Op)
			},
		},
		{
			query: "foo default 0",
			text:  "foo default 0",
			check: func(t *testing.T, node ast.Node) {
				bin := node.(*ast.BinaryExpr)
				require.Equal(t, ast.OpDefault, bin.Op)
				require.Equal(t, ast.CardManyToMany, bin.VectorMatching.Card)
			},
		},
		{
			query: "foo or 1",
			text:  "foo or 1",
			check: func(t *testing.T, node ast.Node) {
				require.Equal(t, ast.CardManyToMany, node.(*ast.BinaryExpr).VectorMatching.Card)
			},
		},
		{
			query: "foo * on(job) group_left(a) prefix \"bar_\" bar",
			text:  "foo * on (job) group_left (a) prefix \"bar_\" bar",
			check: func(t *testing.T, node ast.Node) {
				require.Equal(t, "bar_", node.(*ast.BinaryExpr).VectorMatching.Prefix)
			},
		},
		{
			query:       "foo + fill_left(1) fill_right(2) bar",
			text:        "foo + fill_left (1) fill_right (2) bar",
			validPromQL: true,
			check: func(t *testing.T, node ast.Node) {
				vm := node.(*ast.BinaryExpr).VectorMatching
				require.InDelta(t, 1.0, *vm.FillValues.LHS, 0)
				require.InDelta(t, 2.0, *vm.FillValues.RHS, 0)
			},
		},
		{
			query: "(foo + bar) keep_metric_names",
			text:  "(foo + bar) keep_metric_names",
			check: func(t *testing.T, node ast.Node) {
				require.True(t, node.(*ast.BinaryExpr).KeepMetricNames)
			},
		},
		{
			query: "abs(foo) keep_metric_names",
			text:  "abs(foo) keep_metric_names",
			check: func(t *testing.T, node ast.Node) {
				require.True(t, node.(*ast.Call).KeepMetricNames)
			},
		},
		{
			query: "Rate(Foo[5m])",
			text:  "rate(Foo[5m])",
			check: func(t *testing.T, node ast.Node) {
				require.Same(t, promql.Functions()["rate"], node.(*ast.Call).Func)
			},
		},
		{
			query: "(foo, bar)",
			text:  "(foo, bar)",
			check: func(t *testing.T, node ast.Node) {
				call := node.(*ast.Call)
				require.Empty(t, call.Func.Name)
				require.Equal(t, ast.ValueTypeVector, call.Type())
			},
		},
		{
			query: "aggr_over_time((\"min_over_time\", \"max_over_time\"), foo[5m])",
			text:  "aggr_over_time((\"min_over_time\", \"max_over_time\"), foo[5m])",
			check: func(t *testing.T, node ast.Node) {
				call := node.(*ast.Call)
				require.Equal(t, []ast.ValueType{ast.ValueTypeString, ast.ValueTypeMatrix}, call.Func.ArgTypes)
			},
		},
		{
			query: "label_set(foo, \"a\", \"b\")",
			text:  "label_set(foo, \"a\", \"b\")",
			check: func(t *testing.T, node ast.Node) {
				call := node.(*ast.Call)
				require.Equal(t, []ast.ValueType{ast.ValueTypeVector, ast.ValueTypeString, ast.ValueTypeString}, call.Func.ArgTypes)
				require.Equal(t, ast.ValueTypeVector, call.Func.ReturnType)
			},
		},
		{
			query: "limit_offset(1, 2, foo)",
			text:  "limit_offset(1, 2, foo)",
			check: func(t *testing.T, node ast.Node) {
				call := node.(*ast.Call)
				require.Equal(t, []ast.ValueType{ast.ValueTypeScalar, ast.ValueTypeScalar, ast.ValueTypeVector}, call.Func.ArgTypes)
			},
		},
		{
			query: "count_gt_over_time(foo[5m], 10)",
			text:  "count_gt_over_time(foo[5m], 10)",
			check: func(t *testing.T, node ast.Node) {
				call := node.(*ast.Call)
				require.Equal(t, []ast.ValueType{ast.ValueTypeMatrix, ast.ValueTypeScalar}, call.Func.ArgTypes)
			},
		},
		{
			query: "histogram_quantile(0.9, foo, \"le\")",
			text:  "histogram_quantile(0.9, foo, \"le\")",
			check: func(t *testing.T, node ast.Node) {
				call := node.(*ast.Call)
				require.Equal(t, []ast.ValueType{ast.ValueTypeScalar, ast.ValueTypeVector, ast.ValueTypeString}, call.Func.ArgTypes)
			},
		},
		{
			query: "foo > now() - 5m",
			text:  "foo > now() - 5m",
			check: func(t *testing.T, node ast.Node) {
				require.Nil(t, node.(*ast.BinaryExpr).VectorMatching)
			},
		},
		{
			query:       "1 + 2",
			text:        "3",
			validPromQL: true,
			check: func(t *testing.T, node ast.Node) {
				require.Equal(t, ast.PositionRange{Start: 0, End: 5}, node.PositionRange())
			},
		},
		{
			query: "alias(foo, \"bar\")",
			text:  "label_set(foo, \"__name__\", \"bar\")",
			check: func(t *testing.T, node ast.Node) {
				require.Equal(t, ast.PositionRange{Start: 0, End: 17}, node.PositionRange())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.query, func(t *testing.T) {
			_, err := promql.Language{}.ParseExpr(tc.query)
			if tc.validPromQL {
				require.NoError(t, err)
			} else {
				require.Error(t, err, "query is expected to be invalid PromQL")
			}

			node, err := metricsql.Language{}.ParseExpr(tc.query)
			require.NoError(t, err)
			require.Equal(t, tc.text, node.String())
			tc.check(t, node)

			// Printed query must be a valid MetricsQL query with the same meaning.
			reparsed, err := metricsql.Language{}.ParseExpr(node.String())
			require.NoError(t, err)
			require.Equal(t, node.String(), reparsed.String())
		})
	}
}

func TestWithTemplates(t *testing.T) {
	q := `WITH (
  commonFilters = {job="foo"},
  f(x) = rate(x[5m])
)
sum(f(up{commonFilters}))`
	node, err := metricsql.Language{}.ParseExpr(q)
	require.NoError(t, err)
	require.Equal(t, `sum(rate(up{job="foo"}[5m]))`, node.String())

	agg := node.(*ast.AggregateExpr)
	require.Equal(t, "sum(f(up{commonFilters}))", q[agg.PosRange.Start:agg.PosRange.End])
	call := agg.Expr.(*ast.Call)
	require.Equal(t, "f(up{commonFilters})", q[call.PosRange.Start:call.PosRange.End])

	var positions []ast.PositionRange
	ast.Inspect(node, func(n ast.Node) bool {
		pr := n.PositionRange()
		require.GreaterOrEqual(t, int(pr.Start), 0, "%s", n)
		require.LessOrEqual(t, int(pr.End), len(q), "%s", n)
		positions = append(positions, pr)
		return true
	})
	require.Len(t, positions, 4)
}

func TestParseErrors(t *testing.T) {
	testCases := []struct {
		query    string
		fragment string
	}{
		{query: "unknown_func(foo)", fragment: "unknown_func(foo)"},
		{query: "foo{bar=}", fragment: "}"},
		{query: "foo ) bar", fragment: ") bar"},
		{query: "sum(foo) by", fragment: ""},
		{query: "foo{a=~\"(\"}", fragment: "a=~\"(\""},
	}
	for _, tc := range testCases {
		t.Run(tc.query, func(t *testing.T) {
			_, err := metricsql.Language{}.ParseExpr(tc.query)
			require.Error(t, err)
			var perr *ast.ParseError
			require.ErrorAs(t, err, &perr)
			require.NotEmpty(t, perr.Message)
			require.Equal(t, tc.fragment, tc.query[perr.PositionRange.Start:perr.PositionRange.End])
		})
	}
}

func TestParseMetricSelector(t *testing.T) {
	lang := metricsql.Language{}
	require.Equal(t, metricsql.Name, lang.Name())

	m, err := lang.ParseMetricSelector("foo{job=\"bar\"}")
	require.NoError(t, err)
	require.Len(t, m, 2)

	_, err = lang.ParseMetricSelector("sum(foo)")
	require.Error(t, err)

	_, err = lang.ParseMetricSelector("foo{a=\"1\" or b=\"2\"}")
	require.Error(t, err)

	_, err = lang.ParseMetricSelector("foo{")
	require.Error(t, err)
}
