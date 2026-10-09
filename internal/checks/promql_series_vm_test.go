package checks_test

import (
	"slices"
	"testing"
	"time"

	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/parser"
)

func TestSeriesVMCheck(t *testing.T) {
	// Points of the mocked range responses must be aligned to their step, like real ones.
	now := time.Now().Truncate(time.Hour)
	// presence is count(count_over_time(x[step])) over the last week, the keep function
	// decides which points are present.
	presence := func(query string, step time.Duration, keep func(ts time.Time) bool) *prometheusMock {
		s := generateSampleStream(map[string]string{}, now.Add(-8*24*time.Hour), now.Add(2*time.Hour), step)
		s.Values = slices.DeleteFunc(s.Values, func(v model.SamplePair) bool { return !keep(v.Timestamp.Time()) })
		return &prometheusMock{
			conds: []requestCondition{requireRangeQueryPath, formCond{key: "query", value: query}},
			resp:  matrixResponse{samples: []*model.SampleStream{s}},
		}
	}
	// Every other 5m point is missing, a metric written every 10m.
	everyOther := func(ts time.Time) bool { return ts.Unix()/300%2 == 0 }
	always := func(time.Time) bool { return true }
	// A single 30m outage in the last hour.
	outage := func(ts time.Time) bool {
		return ts.Before(now.Add(-40*time.Minute)) || ts.After(now.Add(-10*time.Minute))
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
			mocks:       []*prometheusMock{found(`foo{job="a"}`), flags, presence(`count(count_over_time(foo{job="a"}[5m]))`, time.Minute*5, everyOther)},
			problems:    true,
		},
		{
			description: "metric without pauses",
			content:     recording(`sum(foo{job="a"})`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`foo{job="a"}`), flags, presence(`count(count_over_time(foo{job="a"}[5m]))`, time.Minute*5, always)},
		},
		{
			description: "a single outage is not reported",
			content:     recording(`sum(foo{job="a"})`),
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{found(`foo{job="a"}`), flags, presence(`count(count_over_time(foo{job="a"}[5m]))`, time.Minute*5, outage)},
		},
		{
			description: "step from group params",
			content:     "- name: g\n  params:\n    step: [\"10m\"]\n  rules:\n  - record: foo:sum\n    expr: sum(foo) offset 1h\n",
			language:    parser.MetricsQL,
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				found(`foo`), flags,
				presence(`count(count_over_time(foo[10m]))`, time.Minute*10, func(ts time.Time) bool { return ts.Unix()/600%3 != 0 }),
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
