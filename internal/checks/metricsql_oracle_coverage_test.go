package checks_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/checks"
)

type oracleRequirement string

const (
	// Check looks at the query offline: needs positive and negative offline cases.
	reqQueryOffline oracleRequirement = "query, offline"
	// Check queries VictoriaMetrics: needs positive and negative cases with real data.
	reqQueryOnline oracleRequirement = "query, online"
	// Check queries VictoriaMetrics but VictoriaMetrics can never trigger it: needs
	// negative cases with real data proving there are no false positives.
	reqQueryOnlineNoPositive oracleRequirement = "query, online, no VictoriaMetrics positive"
	// Check doesn't depend on the query language: needs one MetricsQL case.
	reqLanguageAgnostic oracleRequirement = "language agnostic"
)

// oracleRequirements classifies every check. A check added upstream fails
// TestMetricsQLOracleCoverage until it is classified here and has oracle cases.
var oracleRequirements = map[string]struct {
	req    oracleRequirement
	reason string
}{
	checks.AlertsAbsentCheckName:         {req: reqQueryOnline},
	checks.AnnotationCheckName:           {req: reqLanguageAgnostic},
	checks.ComparisonCheckName:           {req: reqQueryOffline},
	checks.AlertsCheckName:               {req: reqQueryOnline},
	checks.AlertsExternalLabelsCheckName: {req: reqQueryOnline},
	checks.AlertForCheckName:             {req: reqLanguageAgnostic},
	checks.TemplateCheckName:             {req: reqQueryOffline},
	checks.LabelsConflictCheckName:       {req: reqQueryOnline},
	checks.AggregationCheckName:          {req: reqQueryOffline},
	checks.CounterCheckName: {
		req:    reqQueryOnlineNoPositive,
		reason: "needs metric metadata, the VictoriaMetrics v1.115.0 test images return none; positive cases use mocked metadata in metricsql_test.go",
	},
	checks.FeaturesCheckName: {
		req:    reqQueryOnlineNoPositive,
		reason: "VictoriaMetrics has no feature flags, every MetricsQL function is always available",
	},
	checks.FragileCheckName:        {req: reqQueryOffline},
	checks.GroupIntervalCheckName:  {req: reqLanguageAgnostic},
	checks.ImpossibleCheckName:     {req: reqQueryOffline},
	checks.NaNCheckName:            {req: reqQueryOffline},
	checks.OffsetCheckName:         {req: reqQueryOnline},
	checks.RangeQueryCheckName:     {req: reqQueryOnline},
	checks.RateCheckName:           {req: reqQueryOnline},
	checks.RegexpCheckName:         {req: reqQueryOffline},
	checks.SelectorCheckName:       {req: reqQueryOffline},
	checks.SeriesCheckName:         {req: reqQueryOnline},
	checks.SyntaxCheckName:         {req: reqQueryOffline},
	checks.VectorMatchingCheckName: {req: reqQueryOnline},
	checks.CostCheckName:           {req: reqQueryOnline},
	checks.RuleDependencyCheckName: {req: reqQueryOffline},
	checks.RuleDuplicateCheckName:  {req: reqQueryOffline},
	checks.RuleForCheckName:        {req: reqLanguageAgnostic},
	checks.LabelCheckName:          {req: reqLanguageAgnostic},
	checks.RuleLinkCheckName:       {req: reqLanguageAgnostic},
	checks.RuleNameCheckName:       {req: reqLanguageAgnostic},
	checks.RejectCheckName:         {req: reqLanguageAgnostic},
	checks.ReportCheckName:         {req: reqLanguageAgnostic},
}

type oracleCounts struct {
	positive, negative, boundary   int
	vmPositive, vmNegative, vmCase int
}

func countOracleCases(files []oracleFile) map[string]*oracleCounts {
	counts := map[string]*oracleCounts{}
	for _, f := range files {
		c, ok := counts[f.Check]
		if !ok {
			c = &oracleCounts{}
			counts[f.Check] = c
		}
		for _, oc := range f.Cases {
			hasData := oc.Data != nil
			if hasData {
				c.vmCase++
			}
			switch oc.Kind {
			case "positive":
				c.positive++
				if hasData {
					c.vmPositive++
				}
			case "negative":
				c.negative++
				if hasData {
					c.vmNegative++
				}
			case "boundary":
				c.boundary++
			}
		}
	}
	return counts
}

func TestMetricsQLOracleCoverage(t *testing.T) {
	files := loadOracle(t)
	counts := countOracleCases(files)

	for _, name := range checks.CheckNames {
		r, ok := oracleRequirements[name]
		if !assertTrue(t, ok, "check %s is not classified in oracleRequirements", name) {
			continue
		}
		c := counts[name]
		if c == nil {
			c = &oracleCounts{}
		}
		switch r.req {
		case reqQueryOffline:
			assertTrue(t, c.positive > 0, "%s needs a positive oracle case", name)
			assertTrue(t, c.negative > 0, "%s needs a negative oracle case", name)
		case reqQueryOnline:
			assertTrue(t, c.vmPositive > 0, "%s needs a positive oracle case with VictoriaMetrics data", name)
			assertTrue(t, c.vmNegative > 0, "%s needs a negative oracle case with VictoriaMetrics data", name)
		case reqQueryOnlineNoPositive:
			assertTrue(t, r.reason != "", "%s needs a reason why VictoriaMetrics can't trigger it", name)
			assertTrue(t, c.vmNegative > 0, "%s needs a negative oracle case with VictoriaMetrics data", name)
		case reqLanguageAgnostic:
			assertTrue(t, c.positive+c.negative+c.boundary > 0, "%s needs at least one MetricsQL oracle case", name)
		}
	}
	for name := range oracleRequirements {
		assertTrue(t, slices.Contains(checks.CheckNames, name), "oracleRequirements has unknown check %s", name)
	}

	checkOracleMatrixDoc(t, renderOracleMatrix(counts))
}

func assertTrue(t *testing.T, ok bool, format string, args ...any) bool {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
	return ok
}

func renderOracleMatrix(counts map[string]*oracleCounts) string {
	var b strings.Builder
	b.WriteString("| Check | Classification | Positive | Negative | Boundary | Real VictoriaMetrics cases | Notes |\n")
	b.WriteString("|-------|----------------|----------|----------|----------|----------------------------|-------|\n")
	names := slices.Clone(checks.CheckNames)
	slices.Sort(names)
	for _, name := range names {
		r := oracleRequirements[name]
		c := counts[name]
		if c == nil {
			c = &oracleCounts{}
		}
		fmt.Fprintf(&b, "| `%s` | %s | %d | %d | %d | %d | %s |\n", name, r.req, c.positive, c.negative, c.boundary, c.vmCase, r.reason)
	}
	return b.String()
}

const (
	oracleMatrixDoc   = "../../docs/METRICSQL_TESTING.md"
	oracleMatrixStart = "<!-- oracle-matrix:start -->\n"
	oracleMatrixEnd   = "<!-- oracle-matrix:end -->\n"
)

// checkOracleMatrixDoc fails if the coverage matrix in docs/METRICSQL_TESTING.md is stale.
// Regenerate it with UPDATE_ORACLE_MATRIX=1 go test ./internal/checks -run TestMetricsQLOracleCoverage.
func checkOracleMatrixDoc(t *testing.T, matrix string) {
	t.Helper()
	data, err := os.ReadFile(oracleMatrixDoc)
	require.NoError(t, err)
	doc := string(data)
	start := strings.Index(doc, oracleMatrixStart)
	end := strings.Index(doc, oracleMatrixEnd)
	require.True(t, start >= 0 && end > start, "%s has no oracle matrix markers", oracleMatrixDoc)
	current := doc[start+len(oracleMatrixStart) : end]
	if current == matrix {
		return
	}
	if os.Getenv("UPDATE_ORACLE_MATRIX") != "" {
		doc = doc[:start+len(oracleMatrixStart)] + matrix + doc[end:]
		require.NoError(t, os.WriteFile(oracleMatrixDoc, []byte(doc), 0o644))
		return
	}
	t.Errorf("%s coverage matrix is stale, run UPDATE_ORACLE_MATRIX=1 go test ./internal/checks -run TestMetricsQLOracleCoverage", oracleMatrixDoc)
}
