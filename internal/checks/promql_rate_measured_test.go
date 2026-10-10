package checks_test

import (
	"testing"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/cloudflare/pint/internal/parser"
)

// promql/rate on VictoriaMetrics measures intervals between samples and checks windows
// against them when they are longer than the configured scrape interval (1m by default).
func TestRateVMMeasuredInterval(t *testing.T) {
	counters := &prometheusMock{
		conds: []requestCondition{requireMetadataPath},
		resp:  metadataResponse{metadata: map[string][]v1.Metadata{"foo": {{Type: "counter"}}}},
	}
	record := func(expr string) string {
		return "- record: foo:rate\n  expr: " + expr + "\n"
	}
	tenMinutes := map[string]float64{"foo": 600}

	runTests(t, []checkTest{
		{
			description: "window shorter than the measured interval allows",
			content:     record("rate(foo[2m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counters, vmIntervals(tenMinutes), vmFlags(nil)},
			problems:    true,
		},
		{
			description: "window long enough for the measured interval",
			content:     record("rate(foo[15m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counters, vmIntervals(tenMinutes), vmFlags(nil)},
		},
		{
			description: "measured interval shorter than the configured one",
			content:     record("rate(foo[2m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counters, vmIntervals(map[string]float64{"foo": 15})},
		},
		{
			description: "deriv needs two measured intervals",
			content:     record("deriv(foo[15m] offset 1h)"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{vmIntervals(tenMinutes), vmFlags(nil)},
			problems:    true,
		},
		{
			// The configured 2x scrape interval rule passes, the measured one doesn't.
			description: "rate_prometheus with the measured interval",
			content:     record("rate_prometheus(foo[5m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counters, vmIntervals(tenMinutes), vmFlags(nil)},
			problems:    true,
		},
		{
			description: "window derived from the step with the measured interval",
			content:     record("rate(foo)"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counters, vmIntervals(tenMinutes), vmFlags(nil)},
			problems:    true,
		},
		{
			description: "PromQL rules are not measured",
			content:     record("rate(foo[2m])"),
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counters},
		},
		{
			description: "Prometheus servers are not measured",
			content:     record("rate(foo[2m])"),
			language:    parser.MetricsQL,
			checker:     newRateCheck,
			prometheus:  newSimpleProm,
			mocks: []*prometheusMock{
				counters,
				{conds: []requestCondition{requireConfigPath}, resp: configResponse{yaml: "global:\n  scrape_interval: 1m\n"}},
			},
		},
	})
}
