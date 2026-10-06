package checks

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	mql "github.com/VictoriaMetrics/metricsql"
	"github.com/stretchr/testify/require"
)

// implicitConversions() must find a conversion in exactly the queries
// metricsql.IsLikelyInvalid() reports, so problems point at the right place.
// After a metricsql upstream sync this fails if IsLikelyInvalid() changed.
func TestImplicitConversionsMatchIsLikelyInvalid(t *testing.T) {
	files, err := filepath.Glob("../parser/metricsql/testdata/corpus/*.txt")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	var checked, invalid int
	for _, path := range files {
		for _, q := range readCorpusQueries(t, path) {
			e, err := mql.Parse(q)
			if err != nil {
				continue
			}
			checked++
			want := mql.IsLikelyInvalid(e)
			got := implicitConversions(e, len(q))
			require.Equal(t, want, len(got) > 0, "IsLikelyInvalid() and implicitConversions() disagree on %q", q)
			if want {
				invalid++
			}
			for _, ic := range got {
				require.True(t, ic.span.Start >= 0 && ic.span.Start < ic.span.End && int(ic.span.End) <= len(q),
					"invalid position %v for %q", ic.span, q)
				require.NotEmpty(t, ic.message(q))
			}
		}
	}
	t.Logf("%d corpus queries, %d with implicit conversions", checked, invalid)
	require.Greater(t, checked, 1000, "too few corpus queries parsed")
	require.Greater(t, invalid, 10, "too few corpus queries with implicit conversions")
}

// readCorpusQueries reads quoted queries, one per line. Lines of upstream_verdicts.txt
// start with "A " or "R ".
func readCorpusQueries(t *testing.T, path string) (queries []string) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "A ") || strings.HasPrefix(line, "R ") {
			line = line[2:]
		}
		q, err := strconv.Unquote(line)
		require.NoError(t, err, line)
		queries = append(queries, q)
	}
	require.NoError(t, sc.Err())
	return queries
}
