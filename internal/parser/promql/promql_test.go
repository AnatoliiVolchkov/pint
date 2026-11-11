package promql_test

import (
	"testing"

	promParser "github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/promql"
)

var promqlParser = promParser.NewParser(promParser.Options{
	EnableExperimentalFunctions: true,
	EnableBinopFillModifiers:    true,
	EnableUnquotedUTF8Names:     true,
})

var corpus = []string{
	"foo",
	"{job=\"foo\"}",
	"foo{job=\"foo\", env=~\"prod|dev\", instance!=\"a\", path!~\"/api/.+\"}",
	"{__name__=\"foo\", job=\"bar\"}",
	"{\"foo.bar\", job=\"bar\"}",
	"foo offset 5m",
	"foo offset -5m",
	"foo @ 1234",
	"foo @ start()",
	"foo[5m] @ end() offset 1h",
	"rate(foo[5m])",
	"rate(foo[1h30m])",
	"rate(foo[5m] offset 1h)",
	"rate(foo[5m] anchored)",
	"rate(foo[5m] smoothed)",
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
	"limitk(3, foo)",
	"limit_ratio(0.5, foo)",
	"foo + bar",
	"foo + on(job) bar",
	"foo + ignoring(job) bar",
	"foo * on(job) group_left(instance) bar",
	"foo * ignoring(job) group_right() bar",
	"foo > bool 5",
	"foo and bar",
	"foo or on() vector(1)",
	"foo unless ignoring(a) bar",
	"foo + fill(0) bar",
	"foo + fill_left(1) fill_right(2) bar",
	"-foo",
	"+foo",
	"-(foo + bar)",
	"(foo)",
	"((foo + bar)) / baz",
	"1 + 2",
	"0x1F",
	"1e3",
	"Inf",
	"-Inf",
	"NaN",
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
	"sort_by_label(foo, \"a\", \"b\")",
	"info(foo)",
	"foo[5m + 1m]",
	"foo[step()]",
	"foo[(5m * 2)]",
	"foo offset (1h - 5m)",
	"foo[max_of(5m, step())]",
	"rate(foo[5m])[10m:step() * 2]",
	"foo atan2 bar",
	"foo % 2 ^ 3",
	"sum(rate(foo[5m])) by (job) / on(job) group_left sum(rate(bar[5m])) by (job) > 0.5",
	"count(up == 0) > 0 and on() hour() > 8",
}

func requireEquivalent(t *testing.T, expected promParser.Node, got ast.Node) {
	t.Helper()
	require.Equal(t, expected.String(), got.String(), "String() mismatch for %T", expected)
	require.Equal(t, int(expected.PositionRange().Start), int(got.PositionRange().Start), "start mismatch for %s", expected)
	require.Equal(t, int(expected.PositionRange().End), int(got.PositionRange().End), "end mismatch for %s", expected)
	if e, ok := expected.(promParser.Expr); ok {
		require.Equal(t, string(e.Type()), string(got.Type()), "type mismatch for %s", expected)
	}
	echildren := promParser.Children(expected)
	gchildren := ast.Children(got)
	require.Len(t, gchildren, len(echildren), "children mismatch for %s", expected)
	for i := range echildren {
		requireEquivalent(t, echildren[i], gchildren[i])
	}
}

func TestConvert(t *testing.T) {
	lang := promql.Language{}
	for _, q := range corpus {
		t.Run(q, func(t *testing.T) {
			expected, err := promqlParser.ParseExpr(q)
			require.NoError(t, err)
			got, err := lang.ParseExpr(q)
			require.NoError(t, err)
			requireEquivalent(t, expected, got)
		})
	}
}

func TestConvertDetails(t *testing.T) {
	lang := promql.Language{}

	node, err := lang.ParseExpr("foo * on(job) group_left(a) fill(0) bar{x=\"y\"} offset 5m")
	require.NoError(t, err)
	bin, ok := node.(*ast.BinaryExpr)
	require.True(t, ok)
	require.Equal(t, ast.OpMul, bin.Op)
	require.Equal(t, ast.CardManyToOne, bin.VectorMatching.Card)
	require.True(t, bin.VectorMatching.On)
	require.Equal(t, []string{"job"}, bin.VectorMatching.MatchingLabels)
	require.Equal(t, []string{"a"}, bin.VectorMatching.Include)
	require.InDelta(t, 0.0, *bin.VectorMatching.FillValues.LHS, 0)
	rhs, ok := bin.RHS.(*ast.VectorSelector)
	require.True(t, ok)
	require.Equal(t, "bar", rhs.Name)
	require.Len(t, rhs.LabelMatchers, 2)
	require.Empty(t, rhs.OrMatchers)

	node, err = lang.ParseExpr("rate(foo[5m])")
	require.NoError(t, err)
	call, ok := node.(*ast.Call)
	require.True(t, ok)
	require.Equal(t, []ast.ValueType{ast.ValueTypeMatrix}, call.Func.ArgTypes)
	require.Equal(t, ast.ValueTypeVector, call.Func.ReturnType)
	require.Same(t, promql.Functions()["rate"], call.Func)

	node, err = lang.ParseExpr("foo @ end()")
	require.NoError(t, err)
	vs, ok := node.(*ast.VectorSelector)
	require.True(t, ok)
	require.Equal(t, ast.OpEnd, vs.StartOrEnd)

	node, err = lang.ParseExpr("-foo")
	require.NoError(t, err)
	un, ok := node.(*ast.UnaryExpr)
	require.True(t, ok)
	require.Equal(t, ast.OpSub, un.Op)

	node, err = lang.ParseExpr("sum by (job) (foo)")
	require.NoError(t, err)
	agg, ok := node.(*ast.AggregateExpr)
	require.True(t, ok)
	require.Equal(t, ast.OpSum, agg.Op)
	require.Nil(t, agg.Param)
}

func TestParseErrors(t *testing.T) {
	lang := promql.Language{}
	testCases := []struct {
		query   string
		message string
		start   int
		end     int
	}{
		{query: "sum(foo) by", message: "unexpected end of input in grouping opts, expected \"(\"", start: 11, end: 11},
		{query: "foo{", message: "unexpected end of input inside braces", start: 4, end: 4},
		{query: "rate(foo)", message: "expected type range vector in call to function \"rate\", got instant vector", start: 5, end: 8},
		{query: "foo +", message: "unexpected end of input", start: 5, end: 5},
		{query: "unknown_func(foo)", message: "unknown function with name \"unknown_func\"", start: 0, end: 12},
	}
	for _, tc := range testCases {
		t.Run(tc.query, func(t *testing.T) {
			_, err := lang.ParseExpr(tc.query)
			require.Error(t, err)
			var perr *ast.ParseError
			require.ErrorAs(t, err, &perr)
			require.Equal(t, tc.message, perr.Message)
			require.Equal(t, tc.start, int(perr.PositionRange.Start))
			require.Equal(t, tc.end, int(perr.PositionRange.End))
			_, promErr := promqlParser.ParseExpr(tc.query)
			require.Error(t, promErr)
		})
	}
}

func TestParseMetricSelector(t *testing.T) {
	lang := promql.Language{}
	require.Equal(t, promql.Name, lang.Name())

	m, err := lang.ParseMetricSelector("foo{job=\"bar\"}")
	require.NoError(t, err)
	require.Len(t, m, 2)

	_, err = lang.ParseMetricSelector("foo +")
	require.Error(t, err)
}
