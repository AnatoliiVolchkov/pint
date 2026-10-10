package checks_test

import (
	"testing"

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
			description: "PromQL rules are not checked",
			content:     "- alert: Down\n  expr: up == 0\n",
			checker:     newRuleVMAlertCheck,
			prometheus:  noProm,
		},
	})
}
