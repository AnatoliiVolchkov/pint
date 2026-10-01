package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
)

type unsupportedNode struct {
	ast.Node
}

func (unsupportedNode) String() string {
	return "unsupported"
}

func TestParseUnsupportedNode(t *testing.T) {
	var stdout bytes.Buffer
	parseNode(&stdout, unsupportedNode{}, 0)

	require.Equal(t, "++ node: unsupported\n  ! Unsupported node\n", stdout.String())
}

func TestParseQueryLanguage(t *testing.T) {
	var stdout bytes.Buffer
	require.Error(t, parseQuery(&stdout, parser.PromQL, `rate(foo)`))

	stdout.Reset()
	require.NoError(t, parseQuery(&stdout, parser.MetricsQL, `rate(foo{a="1" or b="2"}) offset 5m`))
	require.Equal(t, `++ node: rate(foo{a="1" or b="2"}) offset 5m
  OffsetExpr:
    * Type: vector
    * Expr: rate(foo{a="1" or b="2"})
    * OriginalOffset: 5m0s
      ++ node: rate(foo{a="1" or b="2"})
        Call:
          * Type: vector
          * Func: rate
          * Args: foo{a="1" or b="2"}
            ++ node: foo{a="1" or b="2"}
              Expressions:
                ++ node: foo{a="1" or b="2"}
                  MatrixSelector:
                    * Type: matrix
                    * VectorSelector: foo{a="1" or b="2"}
                    * Range: 0s
                    * Implicit: true
                    * StepRelative: true
`, stdout.String())
}
