package checks_test

import (
	"testing"
	"time"

	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/parser"
)

func TestAlertsCountVMCheck(t *testing.T) {
	alerting := func(expr string) string {
		return "- alert: Gone\n  expr: " + expr + "\n"
	}
	firing := func(query string) *prometheusMock {
		return &prometheusMock{
			conds: []requestCondition{requireRangeQueryPath, formCond{key: "query", value: query}},
			resp: matrixResponse{
				samples: []*model.SampleStream{
					generateSampleStream(map[string]string{}, time.Now().Add(time.Hour*-2), time.Now(), time.Minute),
				},
			},
		}
	}
	uptime := &prometheusMock{
		conds: []requestCondition{requireRangeQueryPath, formCond{key: "query", value: "count(\nup\n)"}},
		resp: matrixResponse{
			samples: []*model.SampleStream{
				generateSampleStream(map[string]string{}, time.Now().Add(time.Hour*-24), time.Now(), time.Minute),
			},
		},
	}
	flags := func(flags map[string]string) *prometheusMock {
		return &prometheusMock{conds: []requestCondition{requireVMFlagsPath}, resp: vmFlagsResponse{flags: flags}}
	}
	withStep := "- name: g\n  params:\n    step: [\"30s\"]\n  rules:\n  - alert: Gone\n    expr: absent(foo) or bar > 0\n"

	runTests(t, []checkTest{
		{
			description: "selectors without a window get the lookback of vmalert instant queries",
			content:     alerting(`absent(foo{job="bar"} offset 1h) and sum(rate(foo[5m])) > 0`),
			language:    parser.MetricsQL,
			checker:     newAlertsCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				flags(map[string]string{}),
				firing(`absent(last_over_time(foo{job="bar"}[5m] offset 1h)) and sum(rate(foo[5m])) > 0`),
				uptime,
			},
			problems: true,
		},
		{
			description: "step from group params",
			content:     withStep,
			language:    parser.MetricsQL,
			checker:     newAlertsCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				flags(map[string]string{}),
				firing(`absent(last_over_time(foo[30s])) or last_over_time(bar[30s]) > 0`),
				uptime,
			},
			problems: true,
		},
		{
			description: "-search.maxStalenessInterval limits the lookback",
			content:     alerting(`absent(foo)`),
			language:    parser.MetricsQL,
			checker:     newAlertsCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				flags(map[string]string{"search.maxStalenessInterval": "1m0s"}),
				firing(`absent(last_over_time(foo[1m]))`),
				uptime,
			},
			problems: true,
		},
		{
			description: "lookbehind windows and subqueries are not changed",
			content:     alerting(`max_over_time(foo[1h:1m]) > 0 or rate(bar) > 0 or increase(baz[2i]) > 0`),
			language:    parser.MetricsQL,
			checker:     newAlertsCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				firing(`max_over_time(foo[1h:1m]) > 0 or rate(bar) > 0 or increase(baz[2i]) > 0`),
				uptime,
			},
			problems: true,
		},
		{
			description: "PromQL rules are not changed",
			content:     alerting(`absent(foo)`),
			checker:     newAlertsCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				firing(`absent(foo)`),
				uptime,
			},
			problems: true,
		},
		{
			description: "Prometheus servers are not changed",
			content:     alerting(`absent(foo)`),
			language:    parser.MetricsQL,
			checker:     newAlertsCheck,
			prometheus:  newSimpleProm,
			mocks: []*prometheusMock{
				firing(`absent(foo)`),
				uptime,
			},
			problems: true,
		},
	})
}
