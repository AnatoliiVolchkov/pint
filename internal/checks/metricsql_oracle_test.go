package checks_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"
	"github.com/cloudflare/pint/internal/promapi/vmtest"
)

// MetricsQL test oracle.
//
// Every file in testdata/metricsql_oracle describes cases for one check. A case has rules
// parsed as MetricsQL and the exact set of problems the check must report for them:
// reporter, severity, summary, and for every diagnostic its message, the highlighted
// fragment, its byte offsets and its YAML line. An empty expect list means the check must
// not report anything, which guards against false positives.
//
// Cases with `data` run against real VictoriaMetrics (single-node and cluster, see
// package vmtest): the series are imported, `vm` queries confirm what VictoriaMetrics
// returns for them, and only then the check runs. Cases without data run offline.
//
// Set ORACLE_PRINT=1 to log the problems a check actually reports, in expect format.

const oracleDir = "testdata/metricsql_oracle"

type oracleFile struct {
	Check string `yaml:"check"`
	path  string
	Cases []oracleCase `yaml:"cases"`
}

type oracleCase struct {
	Settings map[string]string `yaml:"settings,omitempty"`
	Data     *oracleData       `yaml:"data,omitempty"`
	Name     string            `yaml:"name"`
	Kind     string            `yaml:"kind"`
	Rules    string            `yaml:"rules"`
	// State of the checked rules, as in pint ci, default noop.
	State   string `yaml:"state,omitempty"`
	Comment string `yaml:"comment,omitempty"`
	// Other rules visible to the check, e.g. for rule/dependency and rule/duplicate.
	Others []oracleOther   `yaml:"others,omitempty"`
	VM     []oracleVMQuery `yaml:"vm,omitempty"`
	Expect []oracleProblem `yaml:"expect"`
	// ExpectCluster replaces Expect on the VictoriaMetrics cluster. vmselect doesn't
	// report its retention, so pint assumes the VictoriaMetrics default of 31d there.
	ExpectCluster []oracleProblem `yaml:"expect_cluster,omitempty"`
}

// MarshalYAML writes case keys in a fixed, readable order.
func (c oracleCase) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, val any, skip bool) error {
		if skip {
			return nil
		}
		var v yaml.Node
		if err := v.Encode(val); err != nil {
			return err
		}
		if s, ok := val.(string); ok && strings.Contains(s, "\n") {
			v.Style = yaml.LiteralStyle
		}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &v)
		return nil
	}
	for _, kv := range []struct {
		val  any
		key  string
		skip bool
	}{
		{key: "name", val: c.Name},
		{key: "kind", val: c.Kind},
		{key: "comment", val: c.Comment, skip: c.Comment == ""},
		{key: "settings", val: c.Settings, skip: len(c.Settings) == 0},
		{key: "state", val: c.State, skip: c.State == ""},
		{key: "rules", val: c.Rules},
		{key: "others", val: c.Others, skip: len(c.Others) == 0},
		{key: "data", val: c.Data, skip: c.Data == nil},
		{key: "vm", val: c.VM, skip: len(c.VM) == 0},
		{key: "expect", val: c.Expect},
		{key: "expect_cluster", val: c.ExpectCluster, skip: c.ExpectCluster == nil},
	} {
		if err := add(kv.key, kv.val, kv.skip); err != nil {
			return nil, err
		}
	}
	return n, nil
}

type oracleOther struct {
	Path  string `yaml:"path"`
	State string `yaml:"state,omitempty"`
	Rules string `yaml:"rules"`
}

type oracleData struct {
	Step   string         `yaml:"step"`
	Series []oracleSeries `yaml:"series"`
}

type oracleSeries struct {
	Metric string `yaml:"metric"`
	Values string `yaml:"values"`
}

type oracleVMQuery struct {
	Query string `yaml:"query"`
	// Result is the expected instant query result, `empty` or `<labels> <value>` lines
	// sorted by labels, for example `{job="a"} 1`.
	Result string `yaml:"result"`
}

type oracleProblem struct {
	Rule        string             `yaml:"rule"`
	Reporter    string             `yaml:"reporter"`
	Severity    string             `yaml:"severity"`
	Summary     string             `yaml:"summary"`
	Lines       string             `yaml:"lines"`
	Diagnostics []oracleDiagnostic `yaml:"diagnostics"`
}

type oracleDiagnostic struct {
	Message  string `yaml:"message"`
	Fragment string `yaml:"fragment"`
	Start    int    `yaml:"start"`
	End      int    `yaml:"end"`
	Line     int    `yaml:"line"`
}

func loadOracle(t *testing.T) []oracleFile {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(oracleDir, "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no oracle files found")
	files := make([]oracleFile, 0, len(paths))
	metrics := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var f oracleFile
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		require.NoError(t, dec.Decode(&f), path)
		f.path = path
		require.Contains(t, checks.CheckNames, f.Check, "%s: unknown check", path)
		_, ok := oracleCheckers[f.Check]
		require.True(t, ok, "%s: no checker factory for %s", path, f.Check)
		for _, c := range f.Cases {
			require.NotEmpty(t, c.Name, "%s: case without a name", path)
			require.Contains(t, []string{"positive", "negative", "boundary"}, c.Kind, "%s: %s: invalid kind", path, c.Name)
			if c.Kind == "positive" && !oraclePrint() && !oracleWrite() {
				require.NotEmpty(t, c.Expect, "%s: %s: positive case without expected problems", path, c.Name)
			}
			if c.Kind == "negative" {
				require.Empty(t, c.Expect, "%s: %s: negative case with expected problems", path, c.Name)
			}
			if c.Data != nil {
				for _, s := range c.Data.Series {
					name, _, _ := strings.Cut(s.Metric, "{")
					if prev, ok := metrics[name]; ok && prev != f.Check+"/"+c.Name {
						t.Fatalf("%s: %s: metric %s is already used by %s, every case needs its own metrics", path, c.Name, name, prev)
					}
					metrics[name] = f.Check + "/" + c.Name
				}
			}
		}
		files = append(files, f)
	}
	return files
}

// oraclePrint is used when writing new cases: problems are logged instead of compared.
func oraclePrint() bool {
	return os.Getenv("ORACLE_PRINT") != ""
}

// oracleWrite stores reviewed results as expectations, use it only after checking the
// ORACLE_PRINT output. Positive cases must report and negative cases must not.
func oracleWrite() bool {
	return os.Getenv("ORACLE_WRITE") != ""
}

var oracleWritten = map[string][]oracleProblem{}

func writeOracleFiles(t *testing.T, files []oracleFile) {
	t.Helper()
	if !oracleWrite() || t.Failed() {
		return
	}
	for _, f := range files {
		var changed bool
		for i, c := range f.Cases {
			key := f.Check + "\x00" + c.Name + "\x00"
			if got, ok := oracleWritten[key]; ok {
				f.Cases[i].Expect = got
				changed = true
			}
			single, ok := oracleWritten[key+"single"]
			if !ok {
				continue
			}
			f.Cases[i].Expect = single
			f.Cases[i].ExpectCluster = nil
			if cluster, ok := oracleWritten[key+"cluster"]; ok && !reflect.DeepEqual(single, cluster) {
				f.Cases[i].ExpectCluster = cluster
			}
			changed = true
		}
		if !changed {
			continue
		}
		var b strings.Builder
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		require.NoError(t, enc.Encode(f))
		require.NoError(t, os.WriteFile(f.path, []byte(b.String()), 0o644))
	}
}

type oracleSettings map[string]string

func (s oracleSettings) str(key, def string) string {
	if v, ok := s[key]; ok {
		return v
	}
	return def
}

func (s oracleSettings) dur(key string, def time.Duration) time.Duration {
	if v, ok := s[key]; ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			panic(fmt.Sprintf("invalid %s duration %q: %s", key, v, err))
		}
		return d
	}
	return def
}

func (s oracleSettings) num(key string, def int) int {
	if v, ok := s[key]; ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			panic(fmt.Sprintf("invalid %s number %q: %s", key, v, err))
		}
		return n
	}
	return def
}

func (s oracleSettings) severity(def checks.Severity) checks.Severity {
	if v, ok := s["severity"]; ok {
		sev, err := checks.ParseSeverity(v)
		if err != nil {
			panic(err)
		}
		return sev
	}
	return def
}

func (s oracleSettings) re(key, def string) *checks.TemplatedRegexp {
	v := s.str(key, def)
	if v == "" {
		return nil
	}
	return checks.MustTemplatedRegexp(v)
}

// oracleCheckers creates every check with settings taken from the case.
var oracleCheckers = map[string]func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker{
	checks.AlertsAbsentCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewAlertsAbsentCheck(p)
	},
	checks.AnnotationCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewAnnotationCheck(s.re("key", "summary"), nil, s.re("value", ""), nil, s.str("required", "true") == "true", "", s.severity(checks.Bug))
	},
	checks.ComparisonCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewComparisonCheck()
	},
	checks.AlertsCheckName: func(p *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewAlertsCheck(p, s.dur("lookback", time.Hour), s.dur("step", time.Minute), s.dur("resolve", 5*time.Minute), s.num("minCount", 0), "", s.severity(checks.Information))
	},
	checks.AlertsExternalLabelsCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewAlertsExternalLabelsCheck(p)
	},
	checks.AlertForCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewAlertsForCheck()
	},
	checks.TemplateCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewTemplateCheck()
	},
	checks.LabelsConflictCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewLabelsConflictCheck(p)
	},
	checks.AggregationCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewAggregationCheck(s.re("name", ".+"), s.str("label", "job"), s.str("keep", "true") == "true", "", s.severity(checks.Warning))
	},
	checks.CounterCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewCounterCheck(p)
	},
	checks.FeaturesCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewFeaturesCheck(p)
	},
	checks.FragileCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewFragileCheck()
	},
	checks.GroupIntervalCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewGroupIntervalCheck()
	},
	checks.ImpossibleCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewImpossibleCheck()
	},
	checks.NaNCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewNaNCheck()
	},
	checks.OffsetCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewOffsetCheck(p)
	},
	checks.RangeQueryCheckName: func(p *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewRangeQueryCheck(p, s.dur("limit", 0), "", s.severity(checks.Warning))
	},
	checks.RateCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewRateCheck(p)
	},
	checks.RegexpCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewRegexpCheck()
	},
	checks.SelectorCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewSelectorCheck(s.re("key", ".+"), s.re("call", ".+"), s.str("label", "job"), "", s.severity(checks.Warning))
	},
	checks.SeriesCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewSeriesCheck(p)
	},
	checks.SyntaxCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewSyntaxCheck()
	},
	checks.VectorMatchingCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewVectorMatchingCheck(p)
	},
	checks.CostCheckName: func(p *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewCostCheck(p, s.num("maxSeries", 0), s.num("maxTotalSamples", 0), s.num("maxPeakSamples", 0), s.dur("maxEvaluationDuration", 0), "", s.severity(checks.Bug))
	},
	checks.RuleDependencyCheckName: func(*promapi.FailoverGroup, oracleSettings) checks.RuleChecker {
		return checks.NewRuleDependencyCheck()
	},
	checks.RuleDuplicateCheckName: func(p *promapi.FailoverGroup, _ oracleSettings) checks.RuleChecker {
		return checks.NewRuleDuplicateCheck(p)
	},
	checks.RuleForCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewRuleForCheck(checks.RuleForKey(s.str("key", "for")), s.dur("min", 0), s.dur("max", 0), "", s.severity(checks.Bug))
	},
	checks.LabelCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewLabelCheck(s.re("key", "severity"), nil, s.re("value", ""), nil, s.str("required", "true") == "true", "", s.severity(checks.Bug))
	},
	checks.RuleLinkCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewRuleLinkCheck(s.re("re", "^https?://.+$"), "", s.dur("timeout", time.Second), nil, "", s.severity(checks.Bug))
	},
	checks.RuleNameCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewRuleNameCheck(s.re("re", "^[a-z_:]+$"), "", s.severity(checks.Bug))
	},
	checks.RejectCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewRejectCheck(s.str("labels", "true") == "true", s.str("annotations", "false") == "true", s.re("key", ""), s.re("value", ""), s.severity(checks.Bug))
	},
	checks.ReportCheckName: func(_ *promapi.FailoverGroup, s oracleSettings) checks.RuleChecker {
		return checks.NewReportCheck(s.str("comment", "reported"), s.severity(checks.Bug))
	},
}

// parseOracleValues parses a space separated list of segments:
// `<v>x<n>` n samples of v, `<v>+<d>x<n>` / `<v>-<d>x<n>` n samples starting at v changing
// by d each, `_x<n>` n missing samples, or a single number.
func parseOracleValues(s string) ([]float64, error) {
	var vals []float64
	for seg := range strings.FieldsSeq(s) {
		head, count, ok := strings.Cut(seg, "x")
		n := 1
		if ok {
			var err error
			if n, err = strconv.Atoi(count); err != nil || n < 1 {
				return nil, fmt.Errorf("invalid count in %q", seg)
			}
		}
		if head == "_" {
			for range n {
				vals = append(vals, math.NaN())
			}
			continue
		}
		start, delta := head, "0"
		if i := strings.LastIndexAny(head, "+-"); i > 0 {
			start, delta = head[:i], head[i:]
		}
		v, err := strconv.ParseFloat(start, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value in %q: %w", seg, err)
		}
		d, err := strconv.ParseFloat(delta, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid delta in %q: %w", seg, err)
		}
		for i := range n {
			vals = append(vals, v+float64(i)*d)
		}
	}
	return vals, nil
}

func TestOracleValues(t *testing.T) {
	vals, err := parseOracleValues("1x2 0+10x3 5-1x2 _x1 7")
	require.NoError(t, err)
	require.Len(t, vals, 9)
	require.Equal(t, []float64{1, 1, 0, 10, 20, 5, 4}, vals[:7])
	require.True(t, math.IsNaN(vals[7]))
	require.InDelta(t, 7, vals[8], 0)
	for _, bad := range []string{"1x0", "ax2", "1+bx2", "1x"} {
		_, err = parseOracleValues(bad)
		require.Error(t, err, bad)
	}
}

func oracleEntries(t *testing.T, content string, state discovery.ChangeType, path string) []*discovery.Entry {
	t.Helper()
	opts := parser.DefaultOptions
	opts.Language = parser.MetricsQL
	entries, err := parseContent(content, opts, nil)
	require.NoError(t, err, "cannot parse rules")
	for _, e := range entries {
		e.State = state
		e.Path.Name = path
		e.Path.SymlinkTarget = path
	}
	return entries
}

func oracleState(t *testing.T, s string) discovery.ChangeType {
	t.Helper()
	switch s {
	case "", "noop":
		return discovery.Noop
	case "added":
		return discovery.Added
	case "modified":
		return discovery.Modified
	case "removed":
		return discovery.Removed
	}
	t.Fatalf("invalid state %q", s)
	return discovery.Noop
}

// diagnosticFragment returns the text highlighted by d in content and the line it starts on.
// An empty range (an error at the end of the query) is on the line of the preceding byte.
func diagnosticFragment(content string, d diags.Diagnostic) (string, int) {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	var index, firstLine, lastLine, prevLine int
	for _, pr := range d.Pos {
		for col := pr.FirstColumn; col <= pr.LastColumn; col++ {
			index++
			if index == d.FirstColumn-1 {
				prevLine = pr.Line
			}
			if index < d.FirstColumn || index > d.LastColumn {
				continue
			}
			if firstLine == 0 {
				firstLine = pr.Line
			} else if pr.Line != lastLine {
				b.WriteByte('\n')
			}
			lastLine = pr.Line
			if pr.Line-1 < len(lines) && col-1 < len(lines[pr.Line-1]) {
				b.WriteByte(lines[pr.Line-1][col-1])
			}
		}
	}
	if firstLine == 0 {
		firstLine = prevLine
	}
	return b.String(), firstLine
}

func oracleProblems(content string, entries []*discovery.Entry, problems [][]checks.Problem, uri string) []oracleProblem {
	var out []oracleProblem
	for i, ps := range problems {
		for _, p := range ps {
			op := oracleProblem{
				Rule:     entries[i].Rule.Name(),
				Reporter: p.Reporter,
				Severity: strings.ToLower(p.Severity.String()),
				Summary:  p.Summary,
				Lines:    p.Lines.String(),
			}
			for _, d := range p.Diagnostics {
				frag, line := diagnosticFragment(content, d)
				msg := d.Message
				if uri != "" {
					msg = strings.ReplaceAll(msg, uri, "$URI")
				}
				op.Diagnostics = append(op.Diagnostics, oracleDiagnostic{
					Message:  msg,
					Fragment: frag,
					Start:    d.FirstColumn - 1,
					End:      d.LastColumn,
					Line:     line,
				})
			}
			out = append(out, op)
		}
	}
	return out
}

var oracleWhitespace = regexp.MustCompile(`\s+`)

// normalizeMessage collapses whitespace so YAML folding in expectations doesn't matter.
func normalizeMessage(s string) string {
	return strings.TrimSpace(oracleWhitespace.ReplaceAllString(s, " "))
}

func runOracleCase(t *testing.T, check string, c oracleCase, prom *promapi.FailoverGroup, uri, target string) {
	t.Helper()
	entries := oracleEntries(t, c.Rules, oracleState(t, c.State), "rules.yaml")
	// Like pint, checks see every entry, including the one being checked.
	all := slices.Clone(entries)
	for _, o := range c.Others {
		all = append(all, oracleEntries(t, o.Rules, oracleState(t, o.State), o.Path)...)
	}
	checker := oracleCheckers[check](prom, oracleSettings(c.Settings))

	ctx := context.WithValue(t.Context(), promapi.AllPrometheusServers, []*promapi.FailoverGroup{prom})
	problems := make([][]checks.Problem, 0, len(entries))
	for _, e := range entries {
		var ps []checks.Problem
		for _, p := range checker.Check(ctx, e, all) {
			if p.Reporter == check {
				ps = append(ps, p)
			}
		}
		problems = append(problems, ps)
	}
	got := oracleProblems(c.Rules, entries, problems, uri)

	if oraclePrint() {
		out, _ := yaml.Marshal(map[string]any{"expect": got})
		t.Logf("%s / %s actual:\n%s", check, c.Name, out)
		return
	}
	if oracleWrite() {
		switch {
		case c.Kind == "positive" && len(got) == 0:
			t.Fatalf("positive case reported no problems, refusing to write it")
		case c.Kind == "negative" && len(got) > 0:
			t.Fatalf("negative case reported problems, refusing to write it: %v", got)
		}
		oracleWritten[check+"\x00"+c.Name+"\x00"+target] = got
		return
	}

	expect := c.Expect
	if target == "cluster" && c.ExpectCluster != nil {
		expect = c.ExpectCluster
	}
	require.Len(t, got, len(expect), "number of problems reported by %s", check)
	for i, want := range expect {
		g := got[i]
		require.Equal(t, want.Rule, g.Rule, "problem %d rule", i)
		require.Equal(t, want.Reporter, g.Reporter, "problem %d reporter", i)
		require.Equal(t, want.Severity, g.Severity, "problem %d severity", i)
		require.Equal(t, want.Summary, g.Summary, "problem %d summary", i)
		if want.Lines != "" {
			require.Equal(t, want.Lines, g.Lines, "problem %d lines", i)
		}
		require.Len(t, g.Diagnostics, len(want.Diagnostics), "problem %d diagnostics", i)
		for j, wd := range want.Diagnostics {
			gd := g.Diagnostics[j]
			require.Equal(t, normalizeMessage(wd.Message), normalizeMessage(gd.Message), "problem %d diagnostic %d message", i, j)
			require.Equal(t, wd.Fragment, gd.Fragment, "problem %d diagnostic %d fragment", i, j)
			require.Equal(t, wd.Start, gd.Start, "problem %d diagnostic %d start", i, j)
			require.Equal(t, wd.End, gd.End, "problem %d diagnostic %d end", i, j)
			require.Equal(t, wd.Line, gd.Line, "problem %d diagnostic %d line", i, j)
		}
	}
}

func oracleProm(uri string, settings oracleSettings) *promapi.FailoverGroup {
	var extLabels map[string]string
	if v, ok := settings["externalLabels"]; ok {
		extLabels = map[string]string{}
		for kv := range strings.SplitSeq(v, ",") {
			k, val, _ := strings.Cut(kv, "=")
			extLabels[k] = val
		}
	}
	return promapi.NewFailoverGroup("vm", uri, []*promapi.Prometheus{
		promapi.NewVictoriaMetrics("vm", uri, "", nil, 30*time.Second, 4, 1000, nil, promapi.VictoriaMetricsOptions{
			ExternalLabels: extLabels,
			ScrapeInterval: settings.dur("scrapeInterval", 0),
		}),
	}, true, settings.str("uptime", "up"), nil, nil, nil)
}

// TestMetricsQLOracle runs all offline oracle cases.
func TestMetricsQLOracle(t *testing.T) {
	files := loadOracle(t)
	defer writeOracleFiles(t, files)
	for _, f := range files {
		for _, c := range f.Cases {
			if c.Data != nil {
				continue
			}
			t.Run(f.Check+"/"+c.Name, func(t *testing.T) {
				// Offline checks must never query the server, an unroutable URI makes
				// any query fail loudly.
				prom := oracleProm("http://127.0.0.1:9", oracleSettings(c.Settings))
				reg := prometheus.NewRegistry()
				prom.StartWorkers(reg)
				defer prom.Close(reg)
				runOracleCase(t, f.Check, c, prom, "", "")
			})
		}
	}
}

// TestVictoriaMetricsSemantic runs oracle cases with data against real VictoriaMetrics.
func TestVictoriaMetricsSemantic(t *testing.T) {
	files := loadOracle(t)
	env := vmtest.Start(t)
	defer writeOracleFiles(t, files)
	for _, tg := range env.Targets() {
		t.Run(tg.Name, func(t *testing.T) {
			for _, f := range files {
				for _, c := range f.Cases {
					if c.Data == nil {
						continue
					}
					t.Run(f.Check+"/"+c.Name, func(t *testing.T) {
						runOracleDataCase(t, tg, f.Check, c)
					})
				}
			}
		})
	}
}

func runOracleDataCase(t *testing.T, tg vmtest.Target, check string, c oracleCase) {
	step, err := time.ParseDuration(c.Data.Step)
	require.NoError(t, err, "invalid step")
	series := make([]vmtest.Series, 0, len(c.Data.Series))
	for _, s := range c.Data.Series {
		vals, err := parseOracleValues(s.Values)
		require.NoError(t, err, s.Metric)
		series = append(series, vmtest.Series{Metric: s.Metric, Values: vals})
	}
	if len(series) > 0 {
		tg.Import(t, step, series...)
	}

	for _, q := range c.VM {
		res := tg.MustQuery(t, q.Query)
		require.Equal(t, strings.TrimSpace(q.Result), formatVMResult(res), "VictoriaMetrics result for %s", q.Query)
	}

	prom := oracleProm(tg.QueryURL, oracleSettings(c.Settings))
	reg := prometheus.NewRegistry()
	prom.StartWorkers(reg)
	defer prom.Close(reg)
	runOracleCase(t, check, c, prom, tg.QueryURL, tg.Name)
}

func formatVMResult(res []vmtest.Sample) string {
	if len(res) == 0 {
		return "empty"
	}
	lines := make([]string, 0, len(res))
	for _, s := range res {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, fmt.Sprintf("%s=%q", k, s.Labels[k]))
		}
		lines = append(lines, "{"+strings.Join(pairs, ", ")+"} "+strconv.FormatFloat(s.Value, 'g', -1, 64))
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}
