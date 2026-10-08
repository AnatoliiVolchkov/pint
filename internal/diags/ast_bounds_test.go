package diags

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser/promql"
)

// Positions of queries inside double-quoted YAML strings, like a rule file stored in
// a Kubernetes ConfigMap value, don't map to source lines: the query can point at a
// different, shorter line. Trimming that line used to panic with "slice bounds out of
// range", now the line is printed as it is.
func TestInjectDiagnosticsExprOutsideLine(t *testing.T) {
	query := `sum(foo_requests_total_with_a_long_name{job="a"}) / sum(bar_with_a_very_long_metric_name_that_does_not_fit{job="b"})`
	expr, err := (promql.Language{}).ParseExpr(query)
	require.NoError(t, err)

	other := "x: " + strings.Repeat("v", 100)
	input := "expr: |\n  short\n" + other + "\n"
	diags := []Diagnostic{
		{
			Message:     "unsafe division in aggregation",
			Pos:         PositionRanges{{Line: 3, FirstColumn: 3, LastColumn: 2 + len(query)}},
			Expr:        expr,
			FirstColumn: 53,
			LastColumn:  len(query),
			Kind:        Issue,
		},
	}

	var out string
	require.NotPanics(t, func() {
		out = InjectDiagnostics(input, diags, output.None)
	})
	require.Contains(t, out, other)
	require.Contains(t, out, "unsafe division in aggregation")
}
