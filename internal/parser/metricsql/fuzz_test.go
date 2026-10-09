package metricsql_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	mql "github.com/VictoriaMetrics/metricsql"

	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/parser/source"
)

// Fuzz targets for the MetricsQL adapter, run a long campaign with:
//
//	go test ./internal/parser/metricsql -run '^$' -fuzz=FuzzMetricsQLParse -fuzztime=1h
//	go test ./internal/parser/metricsql -run '^$' -fuzz=FuzzMetricsQLPositions -fuzztime=1h

func addFuzzSeeds(f *testing.F) {
	for _, name := range []string{"upstream_parser_success.txt", "upstream_parser_error.txt", "metricsql_only.txt", "pint_testscripts.txt"} {
		for _, q := range readCorpusTB(f, name) {
			f.Add(q)
		}
	}
	f.Add("foo + foo{a=\"b\"}")
	f.Add("WITH (x = foo) x + x")
}

// knownRegexpDifference returns true if the query has a regexp label filter that is only
// valid inside the `^(?:...)$` anchors VictoriaMetrics adds before compiling it, like `)(`.
// VictoriaMetrics accepts such queries, pint rejects them, see
// testdata/corpus/KNOWN_DIFFERENCES.md.
func knownRegexpDifference(q string) (found bool) {
	e, err := mql.Parse(q)
	if err != nil {
		return false
	}
	mql.VisitAll(e, func(e mql.Expr) {
		me, ok := e.(*mql.MetricExpr)
		if !ok {
			return
		}
		for _, lfs := range me.LabelFilterss {
			for _, lf := range lfs {
				if !lf.IsRegexp {
					continue
				}
				if _, err := regexp.Compile(lf.Value); err == nil {
					continue
				}
				if _, err := regexp.Compile("^(?:" + lf.Value + ")$"); err == nil {
					found = true
				}
			}
		}
	})
	return found
}

// FuzzMetricsQLParse checks that the adapter never panics, accepts exactly what the
// MetricsQL parser accepts and returns positioned errors and nodes.
func FuzzMetricsQLParse(f *testing.F) {
	addFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, q string) {
		_, mqlErr := mql.Parse(q)
		node, err := metricsql.Language{}.ParseExpr(q)
		if err != nil && strings.HasPrefix(err.Error(), "unexpected error:") {
			t.Fatalf("%q: adapter panicked: %s", q, err)
		}
		if mqlErr == nil && err != nil && knownRegexpDifference(q) {
			return
		}
		if (mqlErr == nil) != (err == nil) {
			t.Fatalf("%q: metricsql error: %v, pint error: %v", q, mqlErr, err)
		}
		if err != nil {
			var perr *ast.ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("%q: expected *ast.ParseError, got %T: %s", q, err, err)
			}
			if pr := perr.PositionRange; pr.Start < 0 || pr.End < pr.Start || int(pr.End) > len(q) {
				t.Fatalf("%q: error position %v out of range", q, pr)
			}
			return
		}
		ast.Inspect(node, func(n ast.Node) bool {
			if pr := n.PositionRange(); pr.Start < 0 || pr.End < pr.Start || int(pr.End) > len(q) {
				t.Fatalf("%q: %T %s at %v out of range", q, n, n, pr)
			}
			return true
		})
	})
}

// FuzzMetricsQLPositions checks that source analysis never panics, that every source
// position is inside the query and that, for queries without templates, every selector
// position points at the text of that selector, not at another occurrence of the same
// metric name.
func FuzzMetricsQLPositions(f *testing.F) {
	addFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, q string) {
		node, err := metricsql.Language{}.ParseExpr(q)
		if err != nil {
			return
		}
		for _, s := range source.LabelsSource(q, node) {
			s.WalkSources(func(s *source.Source, _ *source.Join, _ *source.Unless) {
				if pr := s.Position; pr.Start < 0 || pr.End < pr.Start || int(pr.End) > len(q) {
					t.Fatalf("%q: source position %v out of range", q, pr)
				}
			})
		}
		if hasTemplates(q) {
			return
		}
		ast.Inspect(node, func(n ast.Node) bool {
			vs, ok := n.(*ast.VectorSelector)
			if !ok {
				return true
			}
			pr := vs.PositionRange()
			frag := q[pr.Start:pr.End]
			got, err := metricsql.Language{}.ParseExpr(frag)
			if err != nil {
				t.Fatalf("%q: selector %s fragment %q doesn't parse: %s", q, vs, frag, err)
			}
			gvs, ok := unwrapParens(got).(*ast.VectorSelector)
			if !ok {
				t.Fatalf("%q: selector %s fragment %q parses as %T", q, vs, frag, got)
			}
			if selectorKey(gvs) != selectorKey(vs) {
				t.Fatalf("%q: selector %s at %v points at %q which is %s", q, selectorKey(vs), pr, frag, selectorKey(gvs))
			}
			return true
		})
	})
}

// hasTemplates returns true if q may use WITH or built-in templates, which relocate
// expanded expressions to the template use-site.
func hasTemplates(q string) bool {
	l := strings.ToLower(q)
	for _, w := range []string{"with", "ru", "ttf", "alias", "range_median"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

func unwrapParens(n ast.Node) ast.Node {
	for {
		p, ok := n.(*ast.ParenExpr)
		if !ok {
			return n
		}
		n = p.Expr
	}
}

func selectorKey(vs *ast.VectorSelector) string {
	var b strings.Builder
	b.WriteString(vs.Name)
	for _, g := range vs.MatcherGroups() {
		b.WriteString("{")
		for _, m := range g {
			b.WriteString(m.String())
			b.WriteString(",")
		}
		b.WriteString("}")
	}
	return b.String()
}
