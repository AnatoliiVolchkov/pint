package checks_test

import (
	"testing"

	"github.com/cloudflare/pint/internal/parser"
)

// Aggregations by __name__ over more than one metric, checked with vmalert v1.153.0.
func TestLabelsConflictVMNameAggregation(t *testing.T) {
	alert := func(expr string) string {
		return "- alert: Collide\n  expr: '" + expr + "'\n"
	}
	runTests(t, []checkTest{
		{
			description: "sum by __name__ in an alert",
			content:     alert(`sum({__name__=~"x|y"}) by (__name__) > 0`),
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
			problems:    true,
		},
		{
			description: "sum by __name__ in a recording rule",
			content:     "- record: xy:sum\n  expr: 'sum by (__name__, job) ({__name__=\"x\" or __name__=\"y\"})'\n",
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
			problems:    true,
		},
		{
			// VictoriaMetrics rejects the query: abs() drops the metric name.
			description: "function around sum by __name__",
			content:     alert(`abs(sum({__name__=~"x|y"}) by (__name__)) > 0`),
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
			problems:    true,
		},
		{
			description: "outer aggregation drops the metric name",
			content:     alert(`sum(sum({__name__=~"x|y"}) by (__name__)) > 0`),
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
		},
		{
			description: "metric name kept in another label",
			content:     alert(`label_replace(sum({__name__=~"x|y"}) by (__name__), "metric", "$1", "__name__", "(.+)") > 0`),
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
		},
		{
			description: "sum by another label",
			content:     alert(`sum({__name__=~"x|y"}) by (job) > 0`),
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
		},
		{
			description: "sum without labels drops the metric name",
			content:     alert(`sum({__name__=~"x|y"}) without (job) > 0`),
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
		},
		{
			description: "a regexp of one literal name",
			content:     alert(`sum({__name__=~"x"}) by (__name__) > 0 or {__name__=~"y"} > 0`),
			language:    parser.MetricsQL,
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
		},
		{
			description: "PromQL rules are not checked",
			content:     alert(`sum({__name__=~"x|y"}) by (__name__) > 0`),
			checker:     newLabelsConflict,
			prometheus:  newSimpleVM,
		},
	})
}
