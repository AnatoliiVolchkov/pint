package diags

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// Block scalar indicators like `|-` and comments after them must not be matched as
// part of the value.
func TestNewPositionRangeBlockScalar(t *testing.T) {
	type testCaseT struct {
		content string
		want    string
	}
	testCases := []testCaseT{
		{content: "expr: |-\n  -some_metric\n", want: "-some_metric"},
		{content: "expr: |\n  -foo + bar\n", want: "-foo + bar\n"},
		{content: "expr: |+\n  +1\n", want: "+1\n"},
		{content: "expr: |2-\n   2 * foo\n", want: " 2 * foo"},
		{content: "expr: >-\n  -foo\n  + bar\n", want: "-foo + bar"},
		{content: "expr: |- # check foo\n  foo\n", want: "foo"},
		{content: "expr: |-\n  sum(\n    foo\n  )\n", want: "sum(\n  foo\n)"},
		{content: "expr: -foo\n", want: "-foo"},
	}
	for _, tc := range testCases {
		t.Run(tc.content, func(t *testing.T) {
			var doc yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tc.content), &doc))
			val := doc.Content[0].Content[1]
			require.Equal(t, tc.want, val.Value)

			lines := strings.Split(tc.content, "\n")
			prs := NewPositionRange(lines, val, 1)
			// The trailing line break kept by `|` and `|+` has no position, as upstream.
			require.Equal(t, len(strings.TrimSuffix(val.Value, "\n")), prs.Len(), "every byte of the value needs a position: %v", prs)

			// Rebuild the value from positions, line breaks are not on any line.
			var b strings.Builder
			var prevLine int
			for _, pr := range prs {
				for col := pr.FirstColumn; col <= pr.LastColumn; col++ {
					line := lines[pr.Line-1]
					if col-1 < len(line) {
						if prevLine != 0 && pr.Line != prevLine && b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
							b.WriteByte(' ')
						}
						b.WriteByte(line[col-1])
					} else {
						b.WriteByte('\n')
					}
					prevLine = pr.Line
				}
			}
			got := strings.ReplaceAll(b.String(), " \n", "\n")
			require.Equal(t, strings.ReplaceAll(strings.ReplaceAll(tc.want, "\n", ""), " ", ""),
				strings.ReplaceAll(strings.ReplaceAll(got, "\n", ""), " ", ""),
				"positions don't point at the value: %v", prs)
			require.NotContains(t, prs, PositionRange{Line: 1, FirstColumn: 7, LastColumn: 7}, "matched the block indicator")
		})
	}
}
