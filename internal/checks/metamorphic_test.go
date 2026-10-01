package checks_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/parser"
)

// Metamorphic tests: semantically equivalent formatting of a MetricsQL query must not
// change what pint reports. Only offsets may move, the highlighted text must stay the
// same once whitespace and parentheses are ignored.

type exprVariant struct {
	apply func(string) string
	name  string
}

var exprVariants = []exprVariant{
	{name: "wide whitespace", apply: func(q string) string {
		return mapOutsideLiterals(q, func(r byte) string {
			if r == ' ' || r == '\t' {
				return "   "
			}
			return string([]byte{r})
		})
	}},
	{name: "newlines", apply: func(q string) string {
		return mapOutsideLiterals(q, func(r byte) string {
			if r == ' ' {
				return "\n  "
			}
			return string([]byte{r})
		})
	}},
	{name: "padded parens", apply: func(q string) string {
		return mapOutsideLiterals(q, func(r byte) string {
			switch r {
			case '(':
				return "( "
			case ')':
				return " )"
			}
			return string([]byte{r})
		})
	}},
	{name: "redundant parens", apply: func(q string) string {
		return "(" + q + "\n)"
	}},
	{name: "trailing comment", apply: func(q string) string {
		return q + "\n# metamorphic comment"
	}},
}

// mapOutsideLiterals rewrites every byte outside of string literals and comments.
func mapOutsideLiterals(q string, fn func(byte) string) string {
	var b strings.Builder
	var quote byte
	inComment := false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case inComment:
			b.WriteByte(c)
			if c == '\n' {
				inComment = false
			}
		case quote != 0:
			b.WriteByte(c)
			if c == '\\' && quote != '`' && i+1 < len(q) {
				i++
				b.WriteByte(q[i])
			} else if c == quote {
				quote = 0
			}
		case c == '\\' && i+1 < len(q):
			// Escaped character in an identifier, e.g. `foo\ bar`.
			b.WriteByte(c)
			i++
			b.WriteByte(q[i])
		case c == '"' || c == '\'' || c == '`':
			quote = c
			b.WriteByte(c)
		case c == '#':
			inComment = true
			b.WriteByte(c)
		default:
			b.WriteString(fn(c))
		}
	}
	return b.String()
}

// replaceExprs returns rules content with every expr value replaced by fn(expr).
func replaceExprs(t *testing.T, content string, fn func(string) string) string {
	t.Helper()
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(content), &doc))
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == "expr" && n.Content[i+1].Kind == yaml.ScalarNode {
					// Trailing whitespace would force a double-quoted scalar with escapes,
					// keep every variant representable as a block scalar.
					lines := strings.Split(fn(n.Content[i+1].Value), "\n")
					for j := range lines {
						lines[j] = strings.TrimRight(lines[j], " \t")
					}
					n.Content[i+1].Value = strings.Join(lines, "\n")
					n.Content[i+1].Style = yaml.LiteralStyle
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&doc)
	out, err := yaml.Marshal(&doc)
	require.NoError(t, err)
	return string(out)
}

func stripFormatting(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '(', ')':
			return -1
		}
		return r
	}, s)
}

func metamorphicKey(f finding) string {
	return strings.Join([]string{f.rule, f.reporter, f.severity, f.summary, stripFormatting(f.message), stripFormatting(f.fragment)}, "\x00")
}

func metamorphicFindings(t *testing.T, content, check string) (keys []string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v\n%s", check, r, content)
			keys = []string{"panic"}
		}
	}()
	findings := runDifferential(t, content, parser.MetricsQL, check)
	keys = make([]string, 0, len(findings))
	for _, f := range findings {
		keys = append(keys, metamorphicKey(f))
	}
	return keys
}

func TestMetricsQLMetamorphic(t *testing.T) {
	type input struct {
		name    string
		content string
		checks  []string
	}
	var inputs []input
	for _, f := range loadOracle(t) {
		if !slices.Contains(differentialChecks, f.Check) {
			continue
		}
		for _, c := range f.Cases {
			// Syntax error messages quote the unparsed rest of the query, so they change
			// with formatting by design, only valid queries are compared.
			if c.Data != nil || len(c.Others) > 0 || c.State != "" || f.Check == checks.SyntaxCheckName {
				continue
			}
			inputs = append(inputs, input{name: f.Check + "/" + c.Name, content: c.Rules, checks: []string{f.Check}})
		}
	}
	for _, path := range differentialCorpus {
		for _, q := range readQuotedLines(t, path) {
			if _, err := parser.MetricsQL.ParseExpr(q); err != nil || strings.Contains(q, "#") {
				continue
			}
			inputs = append(inputs, input{name: q, content: differentialRules(t, q), checks: differentialChecks})
		}
	}
	require.Greater(t, len(inputs), 500)

	var runs int
	for _, in := range inputs {
		for _, check := range in.checks {
			want := metamorphicFindings(t, in.content, check)
			for _, v := range exprVariants {
				// A block scalar starting with a line break maps offsets one byte off,
				// in PromQL and MetricsQL mode alike (upstream pint), so variants are
				// built from the trimmed query.
				content := replaceExprs(t, in.content, func(q string) string { return v.apply(strings.TrimSpace(q)) })
				opts := parser.DefaultOptions
				opts.Language = parser.MetricsQL
				entries, err := parseContent(content, opts, nil)
				require.NoError(t, err, "%s: %s", in.name, v.name)
				for _, e := range entries {
					if e.Rule.RecordingRule == nil && e.Rule.AlertingRule == nil {
						continue
					}
					if e.Rule.Expr().SyntaxError() != nil {
						t.Errorf("%s (%s): equivalent query doesn't parse: %s\n%s", in.name, v.name, e.Rule.Expr().SyntaxError(), e.Rule.Expr().Value.Value)
					}
				}
				got := metamorphicFindings(t, content, check)
				runs++
				if strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Errorf("%s: %s (%s) changed findings\noriginal: %q\nvariant:  %q\nquery: %s", check, in.name, v.name, want, got, content)
				}
			}
		}
	}
	t.Logf("%d inputs, %d variant runs", len(inputs), runs)
}
