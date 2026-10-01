package source_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/source"
)

func TestLabelsSourceMetricsQL(t *testing.T) {
	type testCaseT struct {
		expr       string
		returns    ast.ValueType
		guaranteed []string
		impossible []string
		possible   []string
		sources    int
		joins      int
		unless     int
	}

	testCases := []testCaseT{
		{
			expr:       `foo{a="1", c="3" or b="2", c="4"}`,
			guaranteed: []string{"c"},
			possible:   []string{"a", "b"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `foo{a="" or a="", b=""}`,
			impossible: []string{"a"},
			possible:   []string{"b"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:    `rate(foo)`,
			returns: ast.ValueTypeVector,
			sources: 1,
		},
		{
			expr:       `rate(foo{job="a"}[$__interval])`,
			guaranteed: []string{"job"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `sum(foo{job="a"}) by (job) offset 5m`,
			guaranteed: []string{"job"},
			impossible: []string{"instance"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `label_set(foo, "x", "y", "z", "")`,
			guaranteed: []string{"x"},
			impossible: []string{"z"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `alias(foo, "bar")`,
			guaranteed: []string{"__name__"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `label_del(foo{x="1"}, "x")`,
			impossible: []string{"x"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `label_keep(foo{a="1", b="2"}, "a")`,
			guaranteed: []string{"a"},
			impossible: []string{"b", "c"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `label_copy(foo{a="1"}, "a", "b", "c", "d")`,
			guaranteed: []string{"a", "b"},
			possible:   []string{"c", "d"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `label_move(foo{a="1"}, "a", "b")`,
			guaranteed: []string{"b"},
			impossible: []string{"a"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `histogram_over_time(foo[5m])`,
			guaranteed: []string{"vmrange"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `prometheus_buckets(foo)`,
			guaranteed: []string{"le"},
			impossible: []string{"vmrange"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `rollup(foo[5m])`,
			guaranteed: []string{"rollup"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:     `rollup(foo[5m], "max")`,
			possible: []string{"rollup"},
			returns:  ast.ValueTypeVector,
			sources:  1,
		},
		{
			expr:       `aggr_over_time(("min_over_time", "max_over_time"), foo[5m])`,
			guaranteed: []string{"rollup"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `quantiles_over_time("phi", 0.5, 0.9, foo[5m])`,
			guaranteed: []string{"phi"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `count_values_over_time("v", foo[5m])`,
			guaranteed: []string{"v"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `histogram_quantiles("phi", 0.5, 0.9, sum(rate(foo[5m])) by (le))`,
			guaranteed: []string{"phi"},
			impossible: []string{"job"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `histogram(foo)`,
			guaranteed: []string{"vmrange"},
			impossible: []string{"job"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `quantiles("phi", 0.5, 0.9, foo) by (job)`,
			guaranteed: []string{"phi"},
			possible:   []string{"job"},
			impossible: []string{"instance"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `sum(foo, bar) by (job)`,
			impossible: []string{"instance"},
			returns:    ast.ValueTypeVector,
			sources:    2,
		},
		{
			expr:       `topk_max(3, foo{instance="a"})`,
			guaranteed: []string{"instance"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `share(foo{instance="a"}) by (job)`,
			guaranteed: []string{"instance"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:    `(foo, bar)`,
			returns: ast.ValueTypeVector,
			sources: 2,
		},
		{
			expr:    `union(foo, bar, baz)`,
			returns: ast.ValueTypeVector,
			sources: 3,
		},
		{
			expr:    `foo if bar`,
			returns: ast.ValueTypeVector,
			sources: 1,
			joins:   1,
		},
		{
			expr:    `foo ifnot bar`,
			returns: ast.ValueTypeVector,
			sources: 1,
			unless:  1,
		},
		{
			expr:    `foo default bar`,
			returns: ast.ValueTypeVector,
			sources: 2,
		},
		{
			expr:       `now()`,
			impossible: []string{"job"},
			returns:    ast.ValueTypeScalar,
			sources:    1,
		},
		{
			expr:       `WITH (f = foo{job="a"}) label_set(f, "b", "c")`,
			guaranteed: []string{"job", "b"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
		{
			expr:       `range_avg(foo{job="a"})`,
			guaranteed: []string{"job"},
			returns:    ast.ValueTypeVector,
			sources:    1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.expr, func(t *testing.T) {
			n, err := parser.DecodeExpr(parser.MetricsQL, tc.expr)
			require.NoError(t, err)

			output := source.LabelsSource(tc.expr, n.Expr)
			require.Len(t, output, tc.sources)
			for _, src := range output {
				src.WalkSources(func(s *source.Source, _ *source.Join, _ *source.Unless) {
					require.Positive(t, s.Position.End, "empty position %+v", s)
				})
			}

			src := output[0]
			require.Equal(t, tc.returns, src.Returns)
			require.Len(t, src.Joins, tc.joins)
			require.Len(t, src.Unless, tc.unless)
			for _, name := range tc.guaranteed {
				require.Equal(t, source.GuaranteedLabel, src.Labels[name].Kind, "label %q should be guaranteed", name)
			}
			for _, name := range tc.impossible {
				require.False(t, src.CanHaveLabel(name), "label %q should be impossible", name)
			}
			for _, name := range tc.possible {
				require.True(t, src.CanHaveLabel(name), "label %q should be possible", name)
				require.NotEqual(t, source.GuaranteedLabel, src.Labels[name].Kind, "label %q shouldn't be guaranteed", name)
			}
		})
	}
}

func TestLabelsSourceMetricsQLUnknownFunction(t *testing.T) {
	n := &ast.Call{
		Func:     &ast.Function{Name: "not_a_metricsql_func"},
		PosRange: ast.PositionRange{Start: 0, End: 22},
	}
	output := source.LabelsSource("not_a_metricsql_func()", n)
	require.Len(t, output, 1)
	_, ok := source.MostOuterOperation[*ast.Call](output[0])
	require.False(t, ok)
}
