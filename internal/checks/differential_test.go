package checks_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
)

// PromQL vs MetricsQL differential test.
//
// MetricsQL is largely PromQL compatible. For every corpus expression accepted by both
// languages, offline query checks must report the same problems in both modes: same
// reporter, severity, summary, message, fragment and offsets. Intentional differences are
// listed in languageDifferences with the reason.

var differentialCorpus = []string{
	"../parser/metricsql/testdata/corpus/prometheus_parser.txt",
	"../parser/metricsql/testdata/corpus/pint_testscripts.txt",
	"../parser/metricsql/testdata/corpus/upstream_parser_success.txt",
	"../parser/metricsql/testdata/corpus/metricsql_docs.txt",
}

// differentialChecks are offline checks inspecting the query.
var differentialChecks = []string{
	checks.ComparisonCheckName,
	checks.TemplateCheckName,
	checks.AggregationCheckName,
	checks.FragileCheckName,
	checks.ImpossibleCheckName,
	checks.NaNCheckName,
	checks.RegexpCheckName,
	checks.SelectorCheckName,
	checks.SyntaxCheckName,
}

// differentialExtra are common real-world rule expressions added to the corpus.
var differentialExtra = []string{
	`sum(rate(http_requests_total{job="api"}[5m])) by (job)`,
	`sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))`,
	`histogram_quantile(0.9, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, job))`,
	`foo{job=~"bar"}`,
	`foo{job=~"^bar$"}`,
	`topk(3, foo) > 1`,
	`sum(foo % bar)`,
	`sum(foo / bar)`,
	`foo{job="a"} unless sum(foo)`,
	`absent(up{job="a"})`,
	`label_replace(up, "a", "$1", "instance", "(.+):.+")`,
	`up{job="a"} == 0`,
	`count(up) by (job) < 2`,
	`max_over_time(foo[1h]) > 5 and on(job) bar`,
	`rate(foo[5m]) > 1 or vector(0)`,
}

// languageDifferences explains every allowed difference, a predicate on the expression and
// the problem reported only in PromQL mode or only in MetricsQL mode.
var languageDifferences = []struct {
	match  func(check, expr string, f finding) bool
	reason string
	onlyIn string
}{
	{
		onlyIn: "promql",
		reason: "modulo by zero returns NaN which VictoriaMetrics aggregations skip, only division can produce Inf",
		match: func(check, _ string, f finding) bool {
			return check == checks.NaNCheckName && strings.Contains(f.fragmentContext, "%")
		},
	},
	{
		onlyIn: "both",
		reason: "NaN from a division is skipped by VictoriaMetrics aggregations, so the MetricsQL message " +
			"only mentions Inf",
		match: func(check, _ string, f finding) bool {
			return check == checks.NaNCheckName && strings.HasPrefix(f.message, "This aggregation can return ")
		},
	},
	{
		onlyIn: "both",
		reason: "constant-only expressions are represented differently: the MetricsQL parser folds numbers " +
			"while parsing (upstream behavior) and keeps unary minus on durations as a separate node, so " +
			"diagnostics highlight the folded expression or the literal without its sign, constant comparisons " +
			"leave no comparison for promql/impossible, and VictoriaMetrics evaluates a number as a single " +
			"series without labels, so alerts/comparison and alerts/template report it",
		match: func(_, expr string, _ finding) bool {
			return isConstantExpr(expr)
		},
	},
}

// isConstantExpr returns true if expr only has numbers, operators and parentheses.
func isConstantExpr(expr string) bool {
	node, err := parser.MetricsQL.ParseExpr(expr)
	if err != nil {
		return false
	}
	constant := true
	ast.Inspect(node, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.NumberLiteral, *ast.UnaryExpr, *ast.ParenExpr, *ast.BinaryExpr:
		default:
			constant = false
		}
		return constant
	})
	return constant
}

type finding struct {
	rule            string
	reporter        string
	severity        string
	summary         string
	message         string
	fragment        string
	fragmentContext string
	start, end      int
}

func (f finding) String() string {
	return fmt.Sprintf("%s %s %s %q %q [%d:%d] %q", f.rule, f.reporter, f.severity, f.summary, f.message, f.start, f.end, f.fragment)
}

func readQuotedLines(t testing.TB, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		q, err := strconv.Unquote(line)
		require.NoError(t, err, line)
		out = append(out, q)
	}
	return out
}

func differentialRules(t testing.TB, expr string) string {
	t.Helper()
	rules := []map[string]any{
		{"record": "diff:rule", "expr": expr},
		{"alert": "DiffAlert", "expr": expr, "annotations": map[string]string{"summary": "{{ $labels.instance }} {{ $value }}"}},
	}
	out, err := yaml.Marshal(rules)
	require.NoError(t, err)
	return string(out)
}

func runDifferential(t *testing.T, content string, lang ast.Language, check string) []finding {
	t.Helper()
	opts := parser.DefaultOptions
	opts.Language = lang
	entries, err := parseContent(content, opts, nil)
	require.NoError(t, err)
	prom := oracleProm("http://127.0.0.1:9", nil)
	reg := prometheus.NewRegistry()
	prom.StartWorkers(reg)
	defer prom.Close(reg)
	checker := oracleCheckers[check](prom, oracleSettings{})
	ctx := context.WithValue(t.Context(), checks.SettingsKey(check), nil)
	var out []finding
	for _, e := range entries {
		// Like pint, broken entries only get the error check.
		if e.PathError != nil || e.Rule.Error.Err != nil || (e.Rule.RecordingRule == nil && e.Rule.AlertingRule == nil) {
			continue
		}
		expr := e.Rule.Expr().Value.Value
		for _, p := range checker.Check(ctx, e, entries) {
			for _, d := range p.Diagnostics {
				frag, _ := diagnosticFragment(content, d)
				var exprCtx string
				if d.FirstColumn >= 1 && d.LastColumn <= len(expr) && d.FirstColumn <= d.LastColumn+1 {
					exprCtx = expr
				}
				out = append(out, finding{
					rule:            e.Rule.Name(),
					reporter:        p.Reporter,
					severity:        p.Severity.String(),
					summary:         p.Summary,
					message:         normalizeMessage(d.Message),
					fragment:        frag,
					fragmentContext: exprCtx,
					start:           d.FirstColumn - 1,
					end:             d.LastColumn,
				})
			}
		}
	}
	return out
}

func TestPromQLMetricsQLDifferential(t *testing.T) {
	seen := map[string]bool{}
	var exprs []string
	corpus := slices.Clone(differentialExtra)
	for _, path := range differentialCorpus {
		corpus = append(corpus, readQuotedLines(t, path)...)
	}
	{
		for _, q := range corpus {
			if seen[q] {
				continue
			}
			seen[q] = true
			if _, err := parser.PromQL.ParseExpr(q); err != nil {
				continue
			}
			if _, err := parser.MetricsQL.ParseExpr(q); err != nil {
				continue
			}
			exprs = append(exprs, q)
		}
	}
	require.Greater(t, len(exprs), 300, "too few expressions valid in both languages")

	var compared, allowed int
	for _, expr := range exprs {
		content := differentialRules(t, expr)
		for _, check := range differentialChecks {
			prom := runDifferential(t, content, parser.PromQL, check)
			mql := runDifferential(t, content, parser.MetricsQL, check)
			compared++
			onlyProm, onlyMQL := diffFindings(prom, mql)
			for _, f := range onlyProm {
				if differenceAllowed(check, expr, "promql", f) {
					allowed++
					continue
				}
				t.Errorf("%s: %q: only PromQL reports: %s", check, expr, f)
			}
			for _, f := range onlyMQL {
				if differenceAllowed(check, expr, "metricsql", f) {
					allowed++
					continue
				}
				t.Errorf("%s: %q: only MetricsQL reports: %s", check, expr, f)
			}
		}
	}
	t.Logf("%d expressions, %d check runs per language, %d allowed differences", len(exprs), compared, allowed)
	for i, d := range languageDifferences {
		if languageDifferencesUsed[i] == 0 {
			t.Errorf("allowed difference is never used, remove it: %s", d.reason)
		}
	}
}

func diffFindings(a, b []finding) (onlyA, onlyB []finding) {
	key := func(f finding) string { return f.String() }
	inB := map[string]int{}
	for _, f := range b {
		inB[key(f)]++
	}
	for _, f := range a {
		if inB[key(f)] > 0 {
			inB[key(f)]--
			continue
		}
		onlyA = append(onlyA, f)
	}
	inA := map[string]int{}
	for _, f := range a {
		inA[key(f)]++
	}
	for _, f := range b {
		if inA[key(f)] > 0 {
			inA[key(f)]--
			continue
		}
		onlyB = append(onlyB, f)
	}
	return onlyA, onlyB
}

var languageDifferencesUsed = map[int]int{}

func differenceAllowed(check, expr, lang string, f finding) bool {
	for i, d := range languageDifferences {
		if (d.onlyIn == lang || d.onlyIn == "both") && d.match(check, expr, f) {
			languageDifferencesUsed[i]++
			return true
		}
	}
	return false
}
