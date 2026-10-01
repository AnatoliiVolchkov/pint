package source_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/parser/source"
)

// MetricsQL only validates function arguments when executing a query, source analysis
// must handle calls the parser accepts with missing or non-string arguments.
func TestLabelsSourceMetricsQLUnvalidatedArguments(t *testing.T) {
	for _, q := range []string{
		`label_replace(foo)`,
		`label_replace(foo, bar, "a", "b", "c")`,
		`label_replace(foo, "a", "b", 1, "c")`,
		`label_join(foo)`,
		`label_join(foo, "a", ",", bar)`,
		`quantiles()`,
		`quantiles(foo)`,
		`sum() by (job)`,
		`topk(3)`,
		`vector()`,
		`(veCtor())`,
		`time(foo)`,
		`absent()`,
		`lABel_CopY(A,"","")`,
		`label_move(foo, "", "")`,
	} {
		t.Run(q, func(t *testing.T) {
			node, err := metricsql.Language{}.ParseExpr(q)
			require.NoError(t, err)
			require.NotPanics(t, func() { source.LabelsSource(q, node) })
		})
	}
}

// Both sides of a binary operation expanded from a WITH template can point at the same
// template argument, source analysis must not slice the query with such positions.
func TestLabelsSourceMetricsQLRepeatedTemplateArgument(t *testing.T) {
	for _, q := range []string{
		`with (foo\-bar(baz) = baz + baz) foo\-bar((x,y))`,
		`WITH (f(x) = x / x) f(foo)`,
		`WITH (f(x) = x * on(job) group_left() x) f(rate(foo[5m]))`,
		`WITH (f(x) = x and x unless x) sum(f(foo{job="a"})) by (job)`,
		`WITH(A000000000000={A,"",A,""},s=(0)%((0%A000000000001)%(0,A000000000001)))(s)`,
	} {
		t.Run(q, func(t *testing.T) {
			node, err := metricsql.Language{}.ParseExpr(q)
			require.NoError(t, err)
			srcs := source.LabelsSource(q, node)
			require.NotEmpty(t, srcs)
			for _, s := range srcs {
				pr := s.Position
				require.True(t, pr.Start >= 0 && pr.Start <= pr.End && int(pr.End) <= len(q), "source position %v out of range", pr)
			}
			ast.Inspect(node, func(n ast.Node) bool {
				pr := n.PositionRange()
				require.True(t, pr.Start >= 0 && pr.Start <= pr.End && int(pr.End) <= len(q), "%s at %v", n, pr)
				return true
			})
		})
	}
}
