package checks_test

import (
	"regexp"
	"testing"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"
)

// Tests in this file run every check with rules parsed as MetricsQL
// and, for online checks, against a mocked VictoriaMetrics server.

func newVMWithOptions(opts promapi.VictoriaMetricsOptions) newPrometheusFn {
	return func(uri string) *promapi.FailoverGroup {
		return promapi.NewFailoverGroup(
			"prom",
			uri,
			[]*promapi.Prometheus{
				promapi.NewVictoriaMetrics("prom", uri, simplePromPublicURI, nil, time.Second*5, 16, 1000, nil, opts),
			},
			true,
			"up",
			[]*regexp.Regexp{},
			[]*regexp.Regexp{},
			[]string{"mytag"},
		)
	}
}

func parseMetricsQLWithStatePath(content string, state discovery.ChangeType, name string) []*discovery.Entry {
	entries := mustParseMetricsQL(content)
	for _, e := range entries {
		e.State = state
		e.Path.Name = name
		e.Path.SymlinkTarget = name
	}
	return entries
}

func withMetricsQL(testCases []checkTest) []checkTest {
	for i := range testCases {
		testCases[i].language = parser.MetricsQL
	}
	return testCases
}

func TestMetricsQLSyntaxCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "rollup without lookbehind window",
			content:     "- record: foo\n  expr: sum(rate(foo))\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "WITH template",
			content:     "- record: foo\n  expr: 'WITH (f(x) = sum(rate(x[5m])) by (job)) f(foo) / f(bar)'\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "or label filters",
			content:     "- record: foo\n  expr: 'foo{job=\"a\" or instance=\"b\"}'\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "keep_metric_names and default",
			content:     "- record: foo\n  expr: 'rate(foo[5m]) keep_metric_names default 0'\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "step relative window and histogram_quantiles",
			content:     "- record: foo\n  expr: 'histogram_quantiles(\"phi\", 0.5, 0.9, rate(foo_bucket[5i]))'\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "syntax error",
			content:     "- record: foo\n  expr: sum(foo) by (\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "unknown function",
			content:     "- record: foo\n  expr: not_a_function(foo)\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "window after rate",
			content:     "- alert: foo\n  expr: 'sum by (shard) (rate(requests_total{job=\"api\"})[1m]) < 2'\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "window with step after rate",
			content:     "- record: foo\n  expr: rate(foo)[5m:1m]\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "window after rate in parens",
			content:     "- record: foo\n  expr: sum((rate(foo))[5m])\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "window after delta with offset",
			content:     "- record: foo\n  expr: delta(foo offset 1h)[10m] > 0\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "window after rate of an expression",
			content:     "- record: foo\n  expr: rate(sum(foo))[5m]\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "subquery inside a rollup function",
			content:     "- record: foo\n  expr: max_over_time(rate(foo)[1h:1m])\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "subquery of rate with a window",
			content:     "- record: foo\n  expr: rate(foo[5m])[1h:1m]\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			// sum(foo)[5m] is an implicit subquery, sum(foo)[5m:1i].
			description: "subquery of an aggregation",
			content:     "- record: foo\n  expr: sum(foo)[5m]\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "explicit subquery of an aggregation",
			content:     "- record: foo\n  expr: max_over_time(sum(foo)[5m:]) + max_over_time(sum(foo)[5m:1m])\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "implicit subquery step inside a rollup function",
			content:     "- record: foo\n  expr: avg_over_time(rate(http_requests_total[5m])[1h])\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "implicit subquery of a binary operation",
			content:     "- record: foo\n  expr: max_over_time((foo + bar)[5m])\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "rate of an aggregation",
			content:     "- alert: foo\n  expr: 'rate(sum(requests_total{job=\"api\"})) > 0'\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "rollup functions of expressions",
			content:     "- record: foo\n  expr: delta(abs(foo)) + increase(foo > 10) + rate(foo + bar)\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "rollup function of an expression with offset",
			content:     "- record: foo\n  expr: rate(sum(foo) offset 5m)\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "rollup function of an expression from a WITH template",
			content:     "- record: foo\n  expr: 'WITH (f(x) = rate(sum(x))) f(foo)'\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			// IsLikelyInvalid() treats timestamp() as a transform function, like Prometheus.
			description: "timestamp of an aggregation",
			content:     "- record: foo\n  expr: timestamp(sum(foo))\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
		{
			description: "rollup functions of selectors",
			content:     "- record: foo\n  expr: rate(foo) + rate(foo[5m]) + rate(foo offset 5m) + max_over_time(rate(foo[5m])[1h:1m])\n",
			checker:     newSyntaxCheck,
			prometheus:  noProm,
		},
	}))
}

func TestMetricsQLAlertsForCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "valid for",
			content:     "- alert: foo\n  expr: rate(foo) > 0\n  for: 5m\n",
			checker:     newAlertsForCheck,
			prometheus:  noProm,
		},
		{
			description: "negative for",
			content:     "- alert: foo\n  expr: rate(foo) > 0\n  for: -5m\n",
			checker:     newAlertsForCheck,
			prometheus:  noProm,
			problems:    true,
		},
	}))
}

func TestMetricsQLComparisonCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "comparison inside WITH",
			content:     "- alert: foo\n  expr: 'WITH (x = rate(foo)) x > 0'\n",
			checker:     newComparisonCheck,
			prometheus:  noProm,
		},
		{
			description: "comparison with if",
			content:     "- alert: foo\n  expr: 'rate(foo) > 0 if bar'\n",
			checker:     newComparisonCheck,
			prometheus:  noProm,
		},
		{
			description: "no comparison",
			content:     "- alert: foo\n  expr: rate(foo)\n",
			checker:     newComparisonCheck,
			prometheus:  noProm,
			problems:    true,
		},
	}))
}

func newTemplateCheckWithExternalLabels(names ...string) newCheckFn {
	return func(_ *promapi.FailoverGroup) checks.RuleChecker {
		return checks.NewTemplateCheck().WithExternalLabels(names)
	}
}

func TestMetricsQLTemplateCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			// vmalert merges external labels into rule labels, annotations can use them.
			description: "external label in annotation",
			content: `
- alert: foo
  expr: sum(up{job="api"}) == 0
  annotations:
    summary: 'API is down in {{ $labels.account }} {{ $labels.dc }}'
`,
			checker:    newTemplateCheckWithExternalLabels("account", "dc"),
			prometheus: noProm,
		},
		{
			description: "label missing from external labels in annotation",
			content: `
- alert: foo
  expr: sum(up{job="api"}) == 0
  annotations:
    summary: 'API is down in {{ $labels.account }} {{ $labels.region }}'
`,
			checker:    newTemplateCheckWithExternalLabels("account"),
			prometheus: noProm,
			problems:   true,
		},
		{
			// Label templates are expanded with query labels only.
			description: "external label in label template",
			content: `
- alert: foo
  expr: sum(up{job="api"}) == 0
  labels:
    copy: '{{ $labels.account }}'
`,
			checker:    newTemplateCheckWithExternalLabels("account"),
			prometheus: noProm,
			problems:   true,
		},
		{
			// vmalert adds alertname, alertgroup and rule labels before expanding annotations.
			description: "vmalert labels in annotations",
			content: `
- alert: foo
  expr: sum(rate(foo)) by (job) > 0
  labels:
    severity: warning
  annotations:
    summary: '{{ $labels.alertname }} {{ $labels.alertgroup }} {{ $labels.severity }} {{ $labels.job }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
		},
		{
			// Labels returned by the query are still validated.
			description: "vmalert labels in annotations with missing query label",
			content: `
- alert: foo
  expr: sum(rate(foo)) by (job) > 0
  labels:
    severity: warning
  annotations:
    summary: '{{ $labels.alertname }} {{ $labels.instance }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			// Label templates are expanded before vmalert adds alertname.
			description: "alertname in label template",
			content: `
- alert: foo
  expr: sum(rate(foo)) by (job) > 0
  labels:
    copy: '{{ $labels.alertname }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "vmalert template variables",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: '{{ $expr }} {{ $type }} {{ $alertID }} {{ $groupID }} {{ $activeAt.Unix }} {{ $for }} {{ $isPartial }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
		},
		{
			description: "vmalert template functions",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: '{{ $labels.instance | quotesEscape }} {{ $labels.instance | jsonEscape }} {{ $labels.instance | htmlEscape }} {{ $labels.instance | crlfEscape }}'
    graph: 'https://vm/vmui/#/?g0.expr={{ $expr | queryEscape }}&path={{ "a b" | pathEscape }}'
    since: '{{ (parseDurationTime "1h").Seconds }} {{ formatTime "2006" $activeAt }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
		},
		{
			// $interval was added to vmalert templates after v1.145.0.
			description: "vmalert interval template variable",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: 'evaluated every {{ $interval }} {{ .Interval.Seconds }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
		},
		{
			// vmalert v1.153.0 doesn't have toDuration, urlQueryEscape, graphLink, tableLink and tmpl.
			description: "Prometheus only template function toDuration",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: 'down for {{ $value | toDuration }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "Prometheus only template function urlQueryEscape",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    link: 'https://vm/vmui/#/?g0.expr={{ $expr | urlQueryEscape }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "Prometheus only template function in a branch that never runs",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    link: '{{ if false }}{{ graphLink "foo" }}{{ tableLink "foo" }}{{ end }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "Prometheus only template function in a label",
			content: `
- alert: foo
  expr: foo > 0
  labels:
    since: '{{ $value | toDuration }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "vmalert template variable with bad field",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: 'x {{ $activeAt.Foo }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "unknown template function",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: '{{ $labels.instance | fooEscape }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "vmalert template variable position in label check",
			content: `
- alert: foo
  expr: sum(foo) by (job) > 0
  annotations:
    summary: '{{ $expr }} {{ $labels.instance }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "template query position with vmalert header",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: '{{ $expr }} {{ with query "sum(foo) by (job)" }}{{ . | first | label "instance" }}{{ end }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "label removed by aggregation",
			content: `
- alert: foo
  expr: sum(rate(foo)) by (job) > 0
  annotations:
    summary: '{{ $labels.instance }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "label added by label_set",
			content: `
- alert: foo
  expr: label_set(sum(rate(foo)) by (job), "instance", "x") > 0
  annotations:
    summary: '{{ $labels.instance }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
		},
		{
			description: "label removed by label_del",
			content: `
- alert: foo
  expr: label_del(foo, "instance") > 0
  annotations:
    summary: '{{ $labels.instance }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "MetricsQL template query",
			content: `
- alert: foo
  expr: up == 0
  annotations:
    summary: '{{ with query "sum(rate(foo{job=\"a\" or job=\"b\"}))" }}{{ . | first | value }}{{ end }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
		},
		{
			description: "invalid template query",
			content: `
- alert: foo
  expr: up == 0
  annotations:
    summary: '{{ with query "sum(foo" }}{{ . | first | value }}{{ end }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
	}))
}

// PromQL rules are expanded by Prometheus, vmalert template differences must not apply to them.
func TestMetricsQLTemplateCheckPromQLRules(t *testing.T) {
	runTests(t, []checkTest{
		{
			description: "Prometheus template functions vmalert doesn't have",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: 'down for {{ $value | toDuration }}'
    link: '{{ $labels.job | urlQueryEscape }} {{ if false }}{{ graphLink "foo" }}{{ tableLink "foo" }}{{ end }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
		},
		{
			description: "vmalert interval template variable",
			content: `
- alert: foo
  expr: foo > 0
  annotations:
    summary: 'evaluated every {{ $interval }}'
`,
			checker:    newTemplateCheck,
			prometheus: noProm,
			problems:   true,
		},
	})
}

func TestMetricsQLFragileCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "topk_max in alert",
			content:     "- alert: foo\n  expr: topk_max(10, rate(foo)) > 0\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "outliersk in alert",
			content:     "- alert: foo\n  expr: outliersk(3, rate(foo)) > 0\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "topk in recording rule",
			content:     "- record: foo\n  expr: topk_max(10, rate(foo))\n",
			checker:     newFragileCheck,
			prometheus:  noProm,
		},
	}))
}

func TestMetricsQLRegexpCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "static regexp in or filter",
			content:     "- record: foo\n  expr: 'foo{job=~\"bar\" or instance=~\"a.+\"}'\n",
			checker:     newRegexpCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "valid regexps in or filter",
			content:     "- record: foo\n  expr: 'foo{job=~\"bar.+\" or instance=~\"a.+\"}'\n",
			checker:     newRegexpCheck,
			prometheus:  noProm,
		},
	}))
}

func TestMetricsQLImpossibleCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "vector(0) > 0 inside WITH",
			content:     "- alert: foo\n  expr: 'WITH (z = vector(0)) z > 0'\n",
			checker:     newImpossibleCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "possible query",
			content:     "- alert: foo\n  expr: 'rate(foo) > 0'\n",
			checker:     newImpossibleCheck,
			prometheus:  noProm,
		},
	}))
}

func TestMetricsQLNaNCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "division",
			content:     "- record: foo\n  expr: sum(foo / bar)\n",
			checker:     newNaNCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			// MetricsQL treats NaN as missing value, only Inf is a problem.
			description: "modulo",
			content:     "- record: foo\n  expr: sum(foo % bar)\n",
			checker:     newNaNCheck,
			prometheus:  noProm,
		},
	}))
}

func TestMetricsQLGroupIntervalCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			// vmalert keeps firing alerts active between evaluations, they don't flap.
			description: "alerting rule with interval above 5m",
			content: `
- name: test
  interval: 10m
  rules:
  - alert: foo
    expr: rate(foo) > 0
`,
			checker:    newGroupIntervalCheck,
			prometheus: noProm,
		},
		{
			description: "recording rule with interval above 5m",
			content: `
- name: test
  interval: 10m
  rules:
  - record: foo
    expr: sum(rate(foo))
`,
			checker:    newGroupIntervalCheck,
			prometheus: noProm,
			problems:   true,
		},
	}))
}

func TestMetricsQLAggregationCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "job removed inside WITH",
			content:     "- record: foo\n  expr: 'WITH (x = sum(rate(foo)) without(job)) x'\n",
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewAggregationCheck(checks.MustTemplatedRegexp(".+"), "job", true, "", checks.Warning)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "job kept by limit",
			content:     "- record: foo\n  expr: 'sum(rate(foo)) by (job) limit 10'\n",
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewAggregationCheck(checks.MustTemplatedRegexp(".+"), "job", true, "", checks.Warning)
			},
			prometheus: noProm,
		},
	}))
}

func TestMetricsQLSelectorCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "or filter without required label",
			content:     "- record: foo\n  expr: 'foo{job=\"a\" or instance=\"b\"}'\n",
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewSelectorCheck(checks.MustTemplatedRegexp(".+"), nil, "job", "comment", checks.Warning)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "or filter with required label",
			content:     "- record: foo\n  expr: 'foo{job=\"a\" or job=\"b\", instance=\"b\"}'\n",
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewSelectorCheck(checks.MustTemplatedRegexp(".+"), nil, "job", "comment", checks.Warning)
			},
			prometheus: noProm,
		},
	}))
}

func TestMetricsQLRuleChecks(t *testing.T) {
	content := "- alert: foo\n  expr: 'WITH (x = rate(foo)) x > 0'\n  for: 1m\n  labels:\n    severity: warning\n  annotations:\n    link: http://127.0.0.1:1\n"
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "annotation",
			content:     content,
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewAnnotationCheck(checks.MustTemplatedRegexp("summary"), nil, nil, nil, true, "", checks.Warning)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "label",
			content:     content,
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewLabelCheck(checks.MustTemplatedRegexp("severity"), nil, checks.MustTemplatedRegexp("critical"), nil, true, "", checks.Warning)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "reject",
			content:     content,
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewRejectCheck(true, false, checks.MustTemplatedRegexp("severity"), nil, checks.Bug)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "report",
			content:     content,
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewReportCheck("problem reported", checks.Warning)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "for",
			content:     content,
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewRuleForCheck(checks.RuleForFor, time.Minute*5, 0, "", checks.Bug)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "name",
			content:     content,
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewRuleNameCheck(checks.MustTemplatedRegexp("total:.+"), "", checks.Warning)
			},
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "link",
			content:     content,
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewRuleLinkCheck(checks.MustTemplatedRegexp("http://.+"), "", time.Second, nil, "", checks.Bug)
			},
			prometheus: noProm,
			problems:   true,
		},
	}))
}

func TestMetricsQLRuleDependencyCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "removed rule used in or filter",
			content:     "- record: foo\n  expr: sum(rate(bar))\n",
			checker: func(_ *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewRuleDependencyCheck()
			},
			prometheus: newSimpleVM,
			problems:   true,
			entries: []*discovery.Entry{
				parseMetricsQLWithStatePath("- record: foo\n  expr: sum(rate(bar))\n", discovery.Removed, "foo.yaml")[0],
				parseMetricsQLWithStatePath("- alert: alert\n  expr: 'WITH (x = {__name__=\"foo\" or __name__=\"baz\"}) x == 0'\n", discovery.Noop, "alerts.yaml")[0],
			},
		},
	}))
}

func TestMetricsQLRuleDuplicateCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "duplicated rule",
			content:     "- record: foo\n  expr: rollup_rate(foo[5m])\n",
			checker: func(prom *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewRuleDuplicateCheck(prom)
			},
			prometheus: newSimpleVM,
			problems:   true,
			entries: mustParseMetricsQL(`
- record: foo
  expr: rollup_rate(foo[5m])
- record: foo
  expr: rollup_rate(foo[5m])
`),
		},
		{
			description: "or filters never overlap",
			content:     "- record: foo\n  expr: 'foo{a=\"1\" or b=\"2\"}'\n",
			checker: func(prom *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewRuleDuplicateCheck(prom)
			},
			prometheus: newSimpleVM,
			entries: mustParseMetricsQL(`
- record: foo
  expr: foo
`),
		},
	}))
}

func TestMetricsQLAlertsAbsentCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			// VictoriaMetrics config is synthesized, no API calls expected.
			description: "absent() without for",
			content:     "- alert: foo\n  expr: 'absent(foo{job=\"a\" or job=\"b\"})'\n",
			checker:     newAlertsAbsentCheck,
			prometheus:  newSimpleVM,
			problems:    true,
		},
		{
			description: "absent() with for and custom scrape interval",
			content:     "- alert: foo\n  expr: absent(foo)\n  for: 1m\n",
			checker:     newAlertsAbsentCheck,
			prometheus:  newVMWithOptions(promapi.VictoriaMetricsOptions{ExternalLabels: nil, ScrapeInterval: time.Second * 15}),
		},
	}))
}

func TestMetricsQLAlertsCountCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "alerts count",
			content:     "- alert: foo\n  expr: 'rate(foo) > 0'\n",
			checker:     newAlertsCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{
						requireRangeQueryPath,
						formCond{key: "query", value: "rate(foo) > 0"},
					},
					resp: respondWithSingleRangeVector1D(),
				},
				{
					conds: []requestCondition{
						requireRangeQueryPath,
						formCond{key: "query", value: "count(\nup\n)"},
					},
					resp: respondWithSingleRangeVector1D(),
				},
			},
		},
	}))
}

func TestMetricsQLAlertsExternalLabelsCheck(t *testing.T) {
	vm := newVMWithOptions(promapi.VictoriaMetricsOptions{ExternalLabels: map[string]string{"cluster": "demo"}, ScrapeInterval: 0})
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "configured external label",
			content:     "- alert: foo\n  expr: rate(foo) > 0\n  annotations:\n    summary: '{{ $externalLabels.cluster }}'\n",
			checker:     newAlertsExternalLabelsCheck,
			prometheus:  vm,
		},
		{
			description: "missing external label",
			content:     "- alert: foo\n  expr: rate(foo) > 0\n  annotations:\n    summary: '{{ $externalLabels.region }}'\n",
			checker:     newAlertsExternalLabelsCheck,
			prometheus:  vm,
			problems:    true,
		},
	}))
}

func TestMetricsQLLabelsConflictCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "conflict with external labels",
			content:     "- record: foo\n  expr: rate(foo)\n  labels:\n    cluster: dev\n",
			checker:     newLabelsConflict,
			prometheus:  newVMWithOptions(promapi.VictoriaMetricsOptions{ExternalLabels: map[string]string{"cluster": "demo"}, ScrapeInterval: 0}),
			problems:    true,
		},
	}))
}

func TestMetricsQLCounterCheck(t *testing.T) {
	counterMetadata := func() *prometheusMock {
		return &prometheusMock{
			conds: []requestCondition{requireMetadataPath},
			resp: metadataResponse{metadata: map[string][]v1.Metadata{
				"foo": {{Type: "counter"}},
			}},
		}
	}
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "counter without rate",
			content:     "- record: foo\n  expr: 'WITH (x = foo) x > 1'\n",
			checker:     newCounterCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{counterMetadata()},
		},
		{
			description: "counter with rollup_rate",
			content:     "- record: foo\n  expr: rollup_rate(foo[5m])\n",
			checker:     newCounterCheck,
			prometheus:  newSimpleVM,
		},
		{
			description: "metadata placeholder response",
			content:     "- record: foo\n  expr: foo > 1\n",
			checker:     newCounterCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireMetadataPath},
					resp:  metadataResponse{metadata: map[string][]v1.Metadata{}},
				},
			},
		},
	}))
}

func TestMetricsQLFeaturesCheck(t *testing.T) {
	runTests(t, []checkTest{
		{
			// VictoriaMetrics has no feature flags, no API calls expected.
			description: "PromQL experimental function with VictoriaMetrics",
			content:     "- record: foo\n  expr: limitk(10, foo)\n",
			checker:     newFeaturesCheck,
			prometheus:  newSimpleVM,
		},
		{
			description: "MetricsQL function",
			content:     "- record: foo\n  expr: limitk(10, rate(foo))\n",
			checker:     newFeaturesCheck,
			prometheus:  newSimpleVM,
			language:    parser.MetricsQL,
		},
	})
}

func TestMetricsQLOffsetCheck(t *testing.T) {
	flags := func(retention string) *prometheusMock {
		return &prometheusMock{
			conds: []requestCondition{requireVMFlagsPath},
			resp:  vmFlagsResponse{flags: map[string]string{"retentionPeriod": retention}},
		}
	}
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "offset within retention",
			content:     "- record: foo\n  expr: rate(foo) offset 20d\n",
			checker:     newOffsetCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{flags("1")},
		},
		{
			description: "offset beyond retention",
			content:     "- record: foo\n  expr: rate(foo) offset 40d\n",
			checker:     newOffsetCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{flags("1")},
		},
		{
			description: "offset beyond retention in days",
			content:     "- record: foo\n  expr: 'WITH (x = foo offset 14d) x'\n",
			checker:     newOffsetCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{flags("7d")},
		},
		{
			// vmselect doesn't know the retention of vmstorage nodes, pint doesn't guess it.
			description: "vmselect without configured retention",
			content:     "- record: foo\n  expr: foo offset 40d\n",
			checker:     newOffsetCheck,
			prometheus: func(uri string) *promapi.FailoverGroup {
				return newSimpleVM(uri + "/select/0/prometheus")
			},
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireVMFlagsPath},
					resp:  vmFlagsResponse{flags: map[string]string{"storageNode": "vmstorage:8401"}},
				},
			},
		},
		{
			description: "vmselect with configured retention",
			content:     "- record: foo\n  expr: foo offset 40d\n",
			checker:     newOffsetCheck,
			prometheus: func(uri string) *promapi.FailoverGroup {
				return newVMWithOptions(promapi.VictoriaMetricsOptions{ExternalLabels: nil, ScrapeInterval: 0, Retention: time.Hour * 24 * 30})(uri + "/select/0/prometheus")
			},
			problems: true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireVMFlagsPath},
					resp:  vmFlagsResponse{flags: map[string]string{"storageNode": "vmstorage:8401"}},
				},
			},
		},
	}))
}

func TestMetricsQLRangeQueryCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			// vmselect doesn't know the retention of vmstorage nodes, pint doesn't guess it.
			description: "vmselect without configured retention",
			content:     "- record: foo\n  expr: rate(foo[90d])\n",
			checker:     newRangeQueryCheck,
			prometheus: func(uri string) *promapi.FailoverGroup {
				return newSimpleVM(uri + "/select/0/prometheus")
			},
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireVMFlagsPath},
					resp:  vmFlagsResponse{flags: map[string]string{"storageNode": "vmstorage:8401"}},
				},
			},
		},
		{
			description: "vmselect with configured retention",
			content:     "- record: foo\n  expr: rate(foo[90d])\n",
			checker:     newRangeQueryCheck,
			prometheus: func(uri string) *promapi.FailoverGroup {
				return newVMWithOptions(promapi.VictoriaMetricsOptions{ExternalLabels: nil, ScrapeInterval: 0, Retention: time.Hour * 24 * 60})(uri + "/select/0/prometheus")
			},
			problems: true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireVMFlagsPath},
					resp:  vmFlagsResponse{flags: map[string]string{"storageNode": "vmstorage:8401"}},
				},
			},
		},
		{
			description: "range beyond retention",
			content:     "- record: foo\n  expr: rate(foo[30d])\n",
			checker:     newRangeQueryCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireVMFlagsPath},
					resp:  vmFlagsResponse{flags: map[string]string{"retentionPeriod": "7d"}},
				},
			},
		},
		{
			description: "range within retention",
			content:     "- record: foo\n  expr: rate(foo[3d])\n",
			checker:     newRangeQueryCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireVMFlagsPath},
					resp:  vmFlagsResponse{flags: map[string]string{"retentionPeriod": "7d"}},
				},
			},
		},
	}))
}

func TestMetricsQLRateCheck(t *testing.T) {
	counterMetadata := &prometheusMock{
		conds: []requestCondition{requireMetadataPath},
		resp: metadataResponse{metadata: map[string][]v1.Metadata{
			"foo": {{Type: "counter"}},
		}},
	}
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "rate without lookbehind window",
			content:     "- record: foo\n  expr: rate(foo)\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "rate with step relative window",
			content:     "- record: foo\n  expr: rate(foo[1i])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			// VictoriaMetrics uses the last sample before the window, one scrape interval is enough.
			description: "rate = 1x default scrape interval",
			content:     "- record: foo\n  expr: rate(foo[1m])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "rate < 1x default scrape interval",
			content:     "- record: foo\n  expr: rate(foo[30s])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "irate and deriv = 1x default scrape interval",
			content:     "- record: foo\n  expr: irate(foo[1m]) + deriv(bar[1m])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "deriv < 1x default scrape interval",
			content:     "- record: foo\n  expr: deriv(bar[45s])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			// rate_prometheus() has Prometheus semantics and needs two samples inside the window.
			description: "rate_prometheus < 2x default scrape interval",
			content:     "- record: foo\n  expr: rate_prometheus(foo[1m])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "rate_prometheus = 2x default scrape interval",
			content:     "- record: foo\n  expr: rate_prometheus(foo[2m])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			// VictoriaMetrics' increase() uses the last sample before the window, like rate().
			description: "increase = 1x default scrape interval",
			content:     "- record: foo\n  expr: increase(foo[1m])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "increase < 1x default scrape interval",
			content:     "- record: foo\n  expr: increase(foo[30s])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			// increase_prometheus() has Prometheus semantics and needs two samples inside the window.
			description: "increase_prometheus < 2x default scrape interval",
			content:     "- record: foo\n  expr: increase_prometheus(foo[1m])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "increase_prometheus = 2x default scrape interval",
			content:     "- record: foo\n  expr: increase_prometheus(foo[2m])\n",
			checker:     newRateCheck,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{counterMetadata},
		},
		{
			description: "rate with configured scrape interval",
			content:     "- record: foo\n  expr: rate(foo[1m])\n",
			checker:     newRateCheck,
			prometheus:  newVMWithOptions(promapi.VictoriaMetricsOptions{ExternalLabels: nil, ScrapeInterval: time.Second * 15}),
			mocks:       []*prometheusMock{counterMetadata},
		},
	}))
}

func TestMetricsQLSeriesCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "missing series in WITH template",
			content:     "- record: foo\n  expr: 'WITH (x = notfound) sum(x)'\n",
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireQueryPath},
					resp:  respondWithEmptyVector(),
				},
				{
					conds: []requestCondition{requireRangeQueryPath},
					resp:  respondWithEmptyMatrix(),
				},
			},
		},
		{
			description: "or filter is split into selectors",
			content:     "- record: foo\n  expr: 'sum(found{job=\"a\" or job=\"b\"})'\n",
			checker:     newSeriesCheck,
			prometheus:  newSimpleVM,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{
						requireQueryPath,
						formCond{key: "query", value: "count(\nfound{job=\"a\"}\n)"},
					},
					resp: respondWithSingleInstantVector(),
				},
				{
					conds: []requestCondition{
						requireQueryPath,
						formCond{key: "query", value: "count(\nfound{job=\"b\"}\n)"},
					},
					resp: respondWithSingleInstantVector(),
				},
			},
		},
	}))
}

func TestMetricsQLVectorMatchingCheck(t *testing.T) {
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "one to one matching",
			content:     "- record: foo\n  expr: foo_with_notfound / bar\n",
			checker:     newVectorMatchingCheck,
			prometheus:  newSimpleVM,
			problems:    true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{
						requireQueryPath,
						formCond{key: "query", value: "count(\nfoo_with_notfound / bar\n)"},
					},
					resp: respondWithEmptyVector(),
				},
				{
					conds: []requestCondition{
						requireQueryPath,
						formCond{key: "query", value: "count(\nfoo_with_notfound\n) without(__name__)"},
					},
					resp: vectorResponse{
						samples: []*model.Sample{
							generateSample(map[string]string{"instance": "aaa", "notfound": "xxx"}),
						},
					},
				},
				{
					conds: []requestCondition{
						requireQueryPath,
						formCond{key: "query", value: "count(\nbar\n) without(__name__)"},
					},
					resp: vectorResponse{
						samples: []*model.Sample{
							generateSample(map[string]string{"instance": "aaa"}),
						},
					},
				},
			},
		},
		{
			// default works like or and doesn't need matching labels.
			description: "default operator",
			content:     "- record: foo\n  expr: foo default bar\n",
			checker:     newVectorMatchingCheck,
			prometheus:  newSimpleVM,
		},
	}))
}

func TestMetricsQLCostCheck(t *testing.T) {
	// The memory estimate query (checks.BytesPerSampleQuery) is never sent to VictoriaMetrics,
	// there is no mock for it and an unexpected request fails the test.
	runTests(t, withMetricsQL([]checkTest{
		{
			description: "series count",
			content:     "- record: foo\n  expr: sum(rate(foo))\n",
			checker: func(prom *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewCostCheck(prom, 0, 0, 0, 0, "", checks.Bug)
			},
			prometheus: newSimpleVM,
			problems:   true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireQueryPath, formCond{key: "query", value: "count(\nsum(rate(foo))\n)"}},
					resp: vmVectorResponse{
						samples:       []*model.Sample{generateSampleWithValue(map[string]string{}, 7)},
						seriesFetched: 7,
						execTimeMsec:  10,
					},
				},
			},
		},
		{
			description: "sample limits are not supported",
			content:     "- record: foo\n  expr: sum(rate(foo))\n",
			checker: func(prom *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewCostCheck(prom, 0, 100, 100, 0, "", checks.Bug)
			},
			prometheus: newSimpleVM,
			problems:   true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireQueryPath, formCond{key: "query", value: "count(\nsum(rate(foo))\n)"}},
					resp: vmVectorResponse{
						samples:       []*model.Sample{generateSampleWithValue(map[string]string{}, 7)},
						seriesFetched: 7,
						execTimeMsec:  10,
					},
				},
			},
		},
		{
			description: "evaluation duration",
			content:     "- record: foo\n  expr: sum(rate(foo))\n",
			checker: func(prom *promapi.FailoverGroup) checks.RuleChecker {
				return checks.NewCostCheck(prom, 0, 0, 0, time.Second, "", checks.Bug)
			},
			prometheus: newSimpleVM,
			problems:   true,
			mocks: []*prometheusMock{
				{
					conds: []requestCondition{requireQueryPath, formCond{key: "query", value: "count(\nsum(rate(foo))\n)"}},
					resp: vmVectorResponse{
						samples:       []*model.Sample{generateSampleWithValue(map[string]string{}, 7)},
						seriesFetched: 7,
						execTimeMsec:  2500,
					},
				},
			},
		},
	}))
}
