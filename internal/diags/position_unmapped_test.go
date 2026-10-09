package diags

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/cloudflare/pint/internal/output"
)

// A double-quoted value starting with a \u escape has no byte of its decoded value in the
// source, it's placed on its first line instead of getting no position at all.
func TestNewPositionRangeUnmapped(t *testing.T) {
	content := "a: 1\nexpr: \"\\u10e2\\u10e2 foo{\\u03c0=\\\"a\\\"} +\"\nb: 2\n"
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(content), &doc))
	val := doc.Content[0].Content[3]
	require.Equal(t, "ტტ foo{π=\"a\"} +", val.Value)

	lines := strings.Split(content, "\n")
	prs := NewPositionRange(lines, val, 1)
	require.Equal(t, PositionRanges{{Line: 2, FirstColumn: 7, LastColumn: len(lines[1])}}, prs)

	// The console reporter panicked with `index out of range [0] with length 0` here.
	out := InjectDiagnostics(content, []Diagnostic{{
		Message:     "syntax error",
		Pos:         prs,
		Expr:        nil,
		FirstColumn: 1,
		LastColumn:  3,
		Kind:        Issue,
	}}, output.None)
	require.Contains(t, out, "2 | expr:")
	require.Contains(t, out, "syntax error")
}
