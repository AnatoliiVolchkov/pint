package checks_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/common/model"

	"github.com/cloudflare/pint/internal/checks"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"
)

func newRuleVMAlertCheck(_ *promapi.FailoverGroup) checks.RuleChecker {
	return checks.NewRuleVMAlertCheck()
}

func TestRuleVMAlertCheck(t *testing.T) {
	runTests(t, []checkTest{
		{
			description: "debug on a group",
			content:     "- name: g\n  debug: true\n  rules:\n  - alert: Down\n    expr: up == 0\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "debug on an alerting rule",
			content:     "- alert: Down\n  expr: up == 0\n  debug: true\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "debug on a recording rule",
			content:     "- record: up:sum\n  expr: sum(up)\n  debug: true\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			// YAML 1.2 `yes` is a string, vmalert rejects it as a bool.
			description: "debug with a string value",
			content:     "- record: up:sum\n  expr: sum(up)\n  debug: yes\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			description: "debug false",
			content:     "- name: g\n  debug: false\n  rules:\n  - alert: Down\n    expr: up == 0\n    debug: false\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			description: "no vmalert fields",
			content:     "- alert: Down\n  expr: up == 0\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			description: "latency_offset without eval_delay",
			content:     "- name: g\n  params:\n    latency_offset: [\"3m\"]\n  rules:\n  - alert: Down\n    expr: up == 0\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "latency_offset longer than eval_delay",
			content:     "- name: g\n  eval_delay: 1m\n  params:\n    latency_offset: [\"180\"]\n  rules:\n  - alert: Down\n    expr: up == 0\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
			problems:    true,
		},
		{
			description: "latency_offset equal to eval_delay",
			content:     "- name: g\n  eval_delay: 3m\n  params:\n    latency_offset: [\"3m\"]\n  rules:\n  - alert: Down\n    expr: up == 0\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			description: "latency_offset shorter than the default eval_delay",
			content:     "- name: g\n  params:\n    latency_offset: [\"10s\"]\n  rules:\n  - alert: Down\n    expr: up == 0\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			// eval_delay is ignored when eval_offset is set.
			description: "latency_offset with eval_offset",
			content:     "- name: g\n  interval: 5m\n  eval_offset: 1m\n  params:\n    latency_offset: [\"3m\"]\n  rules:\n  - alert: Down\n    expr: up == 0\n",
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			description: "PromQL rules are not checked",
			content:     "- alert: Down\n  expr: up == 0\n",
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
	})
}

func newRuleVMAlertCheckWithServer(prom *promapi.FailoverGroup) checks.RuleChecker {
	return checks.NewRuleVMAlertCheck().WithServers([]*promapi.FailoverGroup{prom})
}

func newRuleVMAlertCheckOffline(prom *promapi.FailoverGroup) checks.RuleChecker {
	return checks.NewRuleVMAlertCheck().WithServers([]*promapi.FailoverGroup{prom}).Offline()
}

// vmalert fails rules returning more results than the group limit.
func TestRuleVMAlertLimit(t *testing.T) {
	count := func(expr string, n float64) *prometheusMock {
		return &prometheusMock{
			conds: []requestCondition{requireQueryPath, formCond{key: "query", value: "count(\n" + expr + "\n)"}},
			resp:  vectorResponse{samples: []*model.Sample{generateSampleWithValue(map[string]string{}, n)}},
		}
	}
	group := func(limit, rule string) string {
		return "- name: g\n" + limit + "  rules:\n" + rule
	}
	recording := "  - record: foo:sum\n    expr: sum(foo) by (instance)\n"
	alerting := "  - alert: FooHigh\n    expr: foo > 1\n"

	runTests(t, []checkTest{
		{
			description: "recording rule returning more series than the group limit",
			content:     group("  limit: 2\n", recording),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheckWithServer,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{count("sum(foo) by (instance)", 10)},
			problems:    true,
		},
		{
			description: "alerting rule returning more alerts than the group limit",
			content:     group("  limit: 2\n", alerting),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheckWithServer,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{count("foo > 1", 3)},
			problems:    true,
		},
		{
			description: "rule returning as many results as the group limit",
			content:     group("  limit: 2\n", recording),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheckWithServer,
			prometheus:  newSimpleVM,
			mocks:       []*prometheusMock{count("sum(foo) by (instance)", 2)},
		},
		{
			description: "group without a limit is not queried",
			content:     group("", recording),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheckWithServer,
			prometheus:  newSimpleVM,
		},
		{
			description: "limit 0 means no limit",
			content:     group("  limit: 0\n", recording),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheckWithServer,
			prometheus:  newSimpleVM,
		},
		{
			description: "Prometheus servers are not queried",
			content:     group("  limit: 2\n", recording),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheckWithServer,
			prometheus:  newSimpleProm,
		},
		{
			description: "offline",
			content:     group("  limit: 2\n", recording),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheckOffline,
			prometheus:  newSimpleVM,
		},
	})
}

// vmalert refuses to load a rule file with two groups of the same name, in relaxed mode every
// groups list is such a file.
func TestRuleVMAlertDuplicateGroupName(t *testing.T) {
	configMap := func(keys ...string) string {
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: rules\ndata:\n" + strings.Join(keys, "")
	}
	key := func(name string, groups ...string) string {
		s := "  " + name + ": |\n    groups:\n"
		for i, g := range groups {
			s += fmt.Sprintf("      - name: %s\n        rules:\n          - record: r%d:sum\n            expr: sum(up)\n", g, i)
		}
		return s
	}

	runTests(t, []checkTest{
		{
			// Every rule entry must have a problem, so the first group has no rules.
			description: "the same group name twice in one data key",
			content: configMap("  a.yaml: |\n    groups:\n      - name: same\n        rules: []\n" +
				"      - name: same\n        rules:\n          - record: r:sum\n            expr: sum(up)\n"),
			language:   parser.MetricsQL,
			checker:    newRuleVMAlertCheck,
			prometheus: noProm,
			problems:   true,
		},
		{
			description: "the same group name in two data keys",
			content:     configMap(key("a.yaml", "same"), key("b.yaml", "same")),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			description: "different group names in one data key",
			content:     configMap(key("a.yaml", "first", "second")),
			language:    parser.MetricsQL,
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
		{
			description: "PromQL rules are not checked",
			content:     configMap(key("a.yaml", "same", "same")),
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
	})
}
