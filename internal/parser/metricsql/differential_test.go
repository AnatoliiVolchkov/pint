package metricsql_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	mql "github.com/VictoriaMetrics/metricsql"
	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
)

// knownDifferences lists queries where pint's MetricsQL mode intentionally disagrees with
// the upstream parser about syntax validity, see testdata/corpus/KNOWN_DIFFERENCES.md.
var knownDifferences = map[string]string{}

type verdict struct {
	query    string
	accepted bool
}

func readVerdicts(t *testing.T) []verdict {
	t.Helper()
	lines := readCorpusLines(t, "upstream_verdicts.txt")
	out := make([]verdict, 0, len(lines))
	for _, line := range lines {
		v, quoted, ok := strings.Cut(line, " ")
		require.True(t, ok && (v == "A" || v == "R"), "invalid verdict line: %s", line)
		q, err := strconv.Unquote(quoted)
		require.NoError(t, err, line)
		out = append(out, verdict{query: q, accepted: v == "A"})
	}
	return out
}

func readCorpusLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(corpusDir, name))
	require.NoError(t, err)
	var out []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// TestParserDifferential checks that pint's MetricsQL mode agrees with the unmodified
// upstream parser on syntax validity for every corpus query, that our source-aware fork
// agrees too, and that rejected queries get a positioned diagnostic.
func TestParserDifferential(t *testing.T) {
	verdicts := readVerdicts(t)
	require.Greater(t, len(verdicts), 5000, "corpus is too small")

	// Every corpus query must have an upstream verdict.
	known := map[string]bool{}
	for _, v := range verdicts {
		known[v.query] = true
	}
	for _, name := range append(append([]string{}, corpusFiles...), "mutations.txt") {
		for _, q := range readCorpus(t, name) {
			require.True(t, known[q], "%s: no upstream verdict for %q, regenerate the corpus", name, q)
		}
	}

	var accepted, rejected int
	for _, v := range verdicts {
		_, forkErr := mql.Parse(v.query)
		if (forkErr == nil) != v.accepted {
			t.Errorf("fork disagrees with upstream for %q: upstream accepted=%v, fork error: %v", v.query, v.accepted, forkErr)
		}

		node, err := metricsql.Language{}.ParseExpr(v.query)
		pintAccepted := err == nil
		if reason, ok := knownDifferences[v.query]; ok {
			if pintAccepted == v.accepted {
				t.Errorf("%q is listed as a known difference (%s) but pint agrees with upstream", v.query, reason)
			}
			continue
		}
		if pintAccepted != v.accepted {
			t.Errorf("pint disagrees with upstream for %q: upstream accepted=%v, pint error: %v", v.query, v.accepted, err)
			continue
		}
		if pintAccepted {
			accepted++
			assertNodePositions(t, v.query, node)
			continue
		}
		rejected++
		var perr *ast.ParseError
		if !errors.As(err, &perr) {
			t.Errorf("%q: expected *ast.ParseError, got %T: %s", v.query, err, err)
			continue
		}
		if strings.TrimSpace(perr.Message) == "" && strings.TrimSpace(perr.Error()) == "" {
			t.Errorf("%q: empty error message", v.query)
		}
		if pr := perr.PositionRange; pr.Start < 0 || pr.End < pr.Start || int(pr.End) > len(v.query) {
			t.Errorf("%q: error position %v is out of range", v.query, pr)
		}
	}
	t.Logf("%d queries: %d accepted, %d rejected, %d known differences", len(verdicts), accepted, rejected, len(knownDifferences))
}

// assertNodePositions checks that every AST node points inside the query.
func assertNodePositions(t *testing.T, q string, node ast.Node) {
	t.Helper()
	ast.Inspect(node, func(n ast.Node) bool {
		pr := n.PositionRange()
		if pr.Start < 0 || pr.End < pr.Start || int(pr.End) > len(q) {
			t.Errorf("%q: %T %s has position %v outside the query", q, n, n, pr)
		}
		return true
	})
}
