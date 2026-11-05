package checks_test

import (
	"slices"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/parser"
)

func TestSeriesVMCheck(t *testing.T) {
	// Points of the mocked range responses must be aligned to their step, like real ones.
	now := time.Now().Truncate(time.Hour)
	start := time.Now()
	// presence is count(count_over_time(x[step])) over the last week with points step apart,
	// except for holes of the given length every 6h between 5 and 2 days ago, far from the ends
	// of the queried range, so the number of pauses doesn't depend on the time the test runs.
	// hole = 0 makes no such holes. recent adds a hole shorter than the others in the last
	// hour, pint measures only selectors with a pause there.
	presence := func(query string, step, hole time.Duration, recent bool) *prometheusMock {
		s := generateSampleStream(map[string]string{}, now.Add(-8*24*time.Hour), now.Add(2*time.Hour), step)
		first, last := now.Add(-5*24*time.Hour), now.Add(-2*24*time.Hour)
		s.Values = slices.DeleteFunc(s.Values, func(v model.SamplePair) bool {
			ts := v.Timestamp.Time()
			if recent && !ts.Before(start.Add(-45*time.Minute)) && ts.Before(start.Add(-25*time.Minute)) {
				return true
			}
			for h := last; hole > 0 && !h.Before(first); h = h.Add(-6 * time.Hour) {
				if !ts.Before(h) && ts.Before(h.Add(hole)) {
					return true
				}
			}
			return false
		})
		return &prometheusMock{
			conds: []requestCondition{requireRangeQueryPath, formCond{key: "query", value: query}},
			resp:  matrixResponse{samples: []*model.SampleStream{s}},
		}
	}
	found := func(selector string) *prometheusMock {
		return &prometheusMock{
			conds: []requestCondition{requireQueryPath, formCond{key: "query", value: "count(\n" + selector + "\n)"}},
			resp:  respondWithSingleInstantVector(),
		}
	}
	flags := &prometheusMock{conds: []requestCondition{requireVMFlagsPath}, resp: vmFlagsResponse{flags: map[string]string{}}}
	recording := func(expr string) string {
		return "- record: foo:sum\n  expr: " + expr + "\n"
	}

	runTests(t, []checkTest{
		{
			description: "metric with regular pauses longer than the lookback",
			content:     recording(`sum(foo{job="a"})`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`foo{job="a"}`), flags, presence(`count(count_over_time(foo{job="a"}[5m]))`, time.Minute*5, time.Minute*30, true)},
			problems:    true,
		},
		{
			description: "metric without pauses",
			content:     recording(`sum(foo{job="a"})`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`foo{job="a"}`), flags, presence(`count(count_over_time(foo{job="a"}[5m]))`, time.Minute*5, 0, false)},
		},
		{
			description: "a single outage is not reported",
			content:     recording(`sum(foo{job="a"})`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`foo{job="a"}`), flags, presence(`count(count_over_time(foo{job="a"}[5m]))`, time.Minute*5, 0, true)},
		},
		{
			description: "step from group params",
			content:     "- name: g\n  params:\n    step: [\"10m\"]\n  rules:\n  - record: foo:sum\n    expr: sum(foo) offset 1h\n",
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				found(`foo`), flags,
				presence(`count(count_over_time(foo[10m]))`, time.Minute*10, time.Hour, true),
			},
			problems: true,
		},
		{
			description: "selectors inside absent() are left to alerts/absent",
			content:     "- alert: Gone\n  expr: absent(foo)\n  for: 30m\n",
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`foo`), flags},
		},
		{
			description: "selectors with lookbehind windows and fallbacks are skipped",
			content:     recording(`(sum(foo) or vector(0)) + sum(rate(bar[5m]))`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`bar`), flags},
		},
		{
			description: "results of other rules are skipped",
			content:     recording(`sum(job:foo:sum)`),
			language:    parser.MetricsQL,
			entries:     mustParseMetricsQL("- record: job:foo:sum\n  expr: sum(foo) by (job)\n"),
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`job:foo:sum`), flags},
		},
		{
			description: "disabled by comment",
			content:     "# pint disable promql/series(foo)\n" + recording(`sum(foo)`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags},
		},
		{
			description: "PromQL rules are not measured",
			content:     recording(`sum(foo)`),
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`foo`)},
		},
		{
			description: "Prometheus servers are not measured",
			content:     recording(`sum(foo)`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleProm,
			mocks:       []*prometheusMock{found(`foo`)},
		},
	})
}

// Selectors in promql/series settings and comments can use MetricsQL-only syntax, such as
// metric names with dots. PromQL selectors are parsed and reported as before.
func TestSeriesSettingsMetricsQLSelectors(t *testing.T) {
	valid := checks.PromqlSeriesSettings{
		IgnoreLabelsValue:       map[string][]string{`app.example.requests{env="dev"}`: {"code"}},
		IgnoreMatchingElsewhere: []string{`app.example.requests{env="dev"}`, `foo{job="a"}`},
	}
	require.NoError(t, valid.Validate())

	invalid := checks.PromqlSeriesSettings{IgnoreMatchingElsewhere: []string{`foo{`}}
	err := invalid.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), `"foo{" is not a valid PromQL metric selector`)
}

func TestSeriesCheckMetricsQLDisableComment(t *testing.T) {
	runTests(t, []checkTest{
		{
			description: "disable comment with a metric name with dots",
			content: `
# pint disable promql/series(app.example.requests{env="dev"})
- record: foo
  expr: sum(app.example.requests{env="dev", code="500"})
`,
			language:   parser.MetricsQL,
			checker:    newSeriesCheck,
			prometheus: newSimpleVM,
		},
	})
}
