package checks_test

import (
	"strings"
	"testing"

	"github.com/cloudflare/pint/internal/parser"
)

// literalBlockRules writes q as a literal block scalar, so every character of the query
// appears in the file as is. It returns false if q can't be written that way.
func literalBlockRules(q string) (string, bool) {
	lines := strings.Split(q, "\n")
	if strings.HasPrefix(lines[0], " ") || strings.HasPrefix(lines[0], "\t") {
		return "", false
	}
	var expr strings.Builder
	for _, l := range lines {
		if strings.TrimRight(l, " \t") != l {
			return "", false
		}
		expr.WriteString("    ")
		expr.WriteString(l)
		expr.WriteByte('\n')
	}
	return "- record: diff:rule\n  expr: |-\n" + expr.String() +
		"- alert: DiffAlert\n  expr: |-\n" + expr.String() +
		"  annotations:\n    summary: '{{ $labels.instance }} {{ $value }}'\n", true
}

// FuzzMetricsQLChecks runs every offline query check on arbitrary MetricsQL queries
// and checks that no check panics and that every diagnostic range fits inside the YAML
// value it points at. Run a long campaign with:
//
//	go test ./internal/checks -run '^$' -fuzz=FuzzMetricsQLChecks -fuzztime=1h
func FuzzMetricsQLChecks(f *testing.F) {
	for _, path := range differentialCorpus {
		for _, q := range readQuotedLines(f, path) {
			f.Add(q)
		}
	}
	f.Add(`WITH (f(x) = x + x) f(foo{job="a"}) > 1`)
	f.Add(`label_replace(foo)`)

	f.Fuzz(func(t *testing.T, q string) {
		// YAML values starting with whitespace and a line break map offsets wrongly in
		// PromQL and MetricsQL mode alike (upstream pint), see docs/METRICSQL_TESTING.md.
		q = strings.TrimSpace(q)
		// Control characters can only be written as escapes in double-quoted YAML, the
		// position mapping (upstream pint) expects the value to appear literally in the file.
		if strings.ContainsFunc(q, func(r rune) bool { return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f }) {
			return
		}
		if _, err := parser.MetricsQL.ParseExpr(q); err != nil {
			return
		}
		content, ok := literalBlockRules(q)
		if !ok {
			return
		}
		opts := parser.DefaultOptions
		opts.Language = parser.MetricsQL
		entries, err := parseContent(content, opts, nil)
		if err != nil {
			return
		}
		prom := oracleProm("http://127.0.0.1:9", nil)
		for _, check := range differentialChecks {
			checker := oracleCheckers[check](prom, oracleSettings{})
			for _, e := range entries {
				// Like pint, broken entries only get the error check.
				if e.PathError != nil || e.Rule.Error.Err != nil {
					continue
				}
				for _, p := range checker.Check(t.Context(), e, entries) {
					for _, d := range p.Diagnostics {
						if d.FirstColumn < 1 || d.LastColumn < d.FirstColumn-1 || d.LastColumn > d.Pos.Len() {
							t.Fatalf("%s: %q: diagnostic [%d:%d] outside its value of length %d: %s", check, q, d.FirstColumn, d.LastColumn, d.Pos.Len(), d.Message)
						}
					}
				}
			}
		}
	})
}
