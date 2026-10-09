package checks_test

import (
	"slices"
	"testing"
	"time"

	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/parser"
)

func TestAlertsAbsentVMCheck(t *testing.T) {
	// Points of the mocked range responses must be aligned to their step, like real ones.
	now := time.Now().Truncate(time.Hour)
	alerting := func(expr, forDur string) string {
		content := "- alert: Gone\n  expr: " + expr + "\n"
		if forDur != "" {
			content += "  for: " + forDur + "\n"
		}
		return content
	}
	// presence is count(count_over_time(x[step])) over the last week with points step apart,
	// except for holes of the given length every `every`, starting three days ago and going
	// back. every = 0 makes a single hole.
	presence := func(query string, step, hole, every time.Duration) *prometheusMock {
		s := generateSampleStream(map[string]string{}, now.Add(-8*24*time.Hour), now.Add(time.Hour), step)
		from := now.Add(-3 * 24 * time.Hour)
		s.Values = slices.DeleteFunc(s.Values, func(v model.SamplePair) bool {
			ts := v.Timestamp.Time()
			if !ts.Before(from) {
				return ts.Before(from.Add(hole))
			}
			if every == 0 {
				return false
			}
			// Holes start at from - k*every.
			d := from.Sub(ts)
			return (every-d%every)%every < hole
		})
		return &prometheusMock{
			conds: []requestCondition{requireRangeQueryPath, formCond{key: "query", value: query}},
			resp:  matrixResponse{samples: []*model.SampleStream{s}},
		}
	}
	flags := &prometheusMock{conds: []requestCondition{requireVMFlagsPath}, resp: vmFlagsResponse{flags: map[string]string{}}}
	pauses := `count(count_over_time(foo{job="a"}[5m]))`

	runTests(t, []checkTest{
		{
			description: "absent on a metric with regular pauses longer than the lookback",
			content:     alerting(`absent(foo{job="a"})`, ""),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags, presence(pauses, time.Minute*5, time.Minute*30, time.Hour*12)},
			problems:    true,
		},
		{
			description: "a single outage is not reported",
			content:     alerting(`absent(foo{job="a"})`, "2m"),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags, presence(pauses, time.Minute*5, time.Minute*30, 0)},
		},
		{
			description: "for covers the pauses",
			content:     alerting(`absent(foo{job="a"})`, "30m"),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags, presence(pauses, time.Minute*5, time.Minute*30, time.Hour*12)},
		},
		{
			description: "for shorter than the pauses",
			content:     alerting(`absent(foo{job="a"})`, "20m"),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags, presence(pauses, time.Minute*5, time.Minute*30, time.Hour*12)},
			problems:    true,
		},
		{
			description: "offset and comparison are ignored",
			content:     alerting(`sum(rate(bar[5m])) > 0 and absent(foo{job="a"} offset 1h) == 1`, ""),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags, presence(pauses, time.Minute*5, time.Minute*30, time.Hour*12)},
			problems:    true,
		},
		{
			description: "absent_over_time with a long enough window",
			content:     alerting(`absent_over_time(foo{job="a"}[1h])`, ""),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags, presence(`count(count_over_time(foo{job="a"}[1h]))`, time.Hour, 0, 0)},
		},
		{
			description: "absent_over_time with a short window",
			content:     alerting(`absent_over_time(foo{job="a"}[10m])`, ""),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags, presence(`count(count_over_time(foo{job="a"}[10m]))`, time.Minute*10, time.Hour, time.Hour*12)},
			problems:    true,
		},
		{
			description: "no absent calls",
			content:     alerting(`foo{job="a"} == 0`, ""),
			language:    parser.MetricsQL,
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
		},
		{
			description: "PromQL rules are not measured",
			content:     alerting(`absent(foo{job="a"})`, "5m"),
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
		},
	})
}
