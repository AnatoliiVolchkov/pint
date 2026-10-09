package checks_test

import (
	"testing"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"
)

// Explicit windows of at least one and less than two scrape intervals, whether they have
// enough samples on VictoriaMetrics depends on the query step.
func TestRateVMShortWindow(t *testing.T) {
	counters := &prometheusMock{
		conds: []requestCondition{requireMetadataPath},
		resp:  metadataResponse{metadata: map[string][]v1.Metadata{"foo": {{Type: "counter"}}}},
	}
	scrape10m := newVMWithOptions(promapi.VictoriaMetricsOptions{ExternalLabels: nil, ScrapeInterval: time.Minute * 10, Retention: 0, QueryStep: 0})
	record := func(expr string) string {
		return "- record: foo:rate\n  expr: " + expr + "\n"
	}

	runTests(t, []checkTest{
		{
			description: "rate needs 2x scrape interval minus the step",
			content:     record("rate(foo[12m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{counters, vmFlags(nil)},
			problems:    true,
		},
		{
			description: "rate with a window covering 2x scrape interval minus the step",
			content:     record("rate(foo[15m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{counters, vmFlags(nil)},
		},
		{
			description: "increase with a window shorter than 2x scrape interval minus the step",
			content:     record("increase(foo[10m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{counters, vmFlags(nil)},
			problems:    true,
		},
		{
			description: "group step covering the scrape interval",
			content:     "- name: g\n  params:\n    step: [\"15m\"]\n  rules:\n  - record: foo:rate\n    expr: rate(foo[10m])\n",
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{counters, vmFlags(nil)},
		},
		{
			description: "-search.maxStalenessInterval limits how far back the sample before the window is used",
			content:     record("rate(foo[15m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{counters, vmFlags(map[string]string{"search.maxStalenessInterval": "2m0s"})},
			problems:    true,
		},
		{
			description: "deriv needs 2x scrape interval whatever the step",
			content:     "- name: g\n  params:\n    step: [\"15m\"]\n  rules:\n  - record: foo:deriv\n    expr: deriv(foo[15m])\n",
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{vmFlags(nil)},
			problems:    true,
		},
		{
			description: "rate_prometheus is only reported once, as a Bug",
			content:     record("rate_prometheus(foo[15m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{counters},
			problems:    true,
		},
		{
			description: "PromQL rules are not changed",
			content:     record("rate(foo[25m])"),
			checker:     newRateCheck,
			prometheus:  scrape10m,
			mocks:       []*prometheusMock{counters},
		},
	})
}
