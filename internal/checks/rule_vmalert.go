package checks

import (
	"context"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
)

// rule/vmalert reports vmalert group and rule settings that are likely mistakes. It only
// looks at rule files, so it runs offline and for MetricsQL rules only.
//
// `debug: true` on a rule or a group makes vmalert log every datasource request of the rule
// and every alert state change at INFO level (app/vmalert/rule/alerting.go and
// datasource/client.go, v1.153.0; vmalert docs "Debug mode"), meant for debugging a rule,
// not for permanent use.

// VMAlertCheckNames are checks that only apply to rules evaluated by vmalert. They aren't in
// CheckNames, so configs for Prometheus list and enable exactly the checks upstream pint has.
var VMAlertCheckNames = []string{RuleVMAlertCheckName}

const (
	RuleVMAlertCheckName    = "rule/vmalert"
	RuleVMAlertDebugDetails = "With `debug: true` vmalert logs every datasource request of the rule and every alert state change at INFO level, " +
		"set on a group it applies to all rules of the group. " +
		"It's meant for debugging a rule, remove it once you're done. " +
		"See [vmalert debug mode](https://docs.victoriametrics.com/victoriametrics/vmalert/#debug-mode)."
)

func NewRuleVMAlertCheck() RuleVMAlertCheck {
	return RuleVMAlertCheck{}
}

type RuleVMAlertCheck struct{}

func (c RuleVMAlertCheck) Meta() CheckMeta {
	return CheckMeta{
		States: []discovery.ChangeType{
			discovery.Noop,
			discovery.Added,
			discovery.Modified,
			discovery.Moved,
		},
		Online:        false,
		AlwaysEnabled: false,
	}
}

func (c RuleVMAlertCheck) String() string {
	return RuleVMAlertCheckName
}

func (c RuleVMAlertCheck) Reporter() string {
	return RuleVMAlertCheckName
}

func (c RuleVMAlertCheck) Check(_ context.Context, entry *discovery.Entry, _ []*discovery.Entry) (problems []Problem) {
	if !isVMAlertRule(entry) {
		return nil
	}
	if entry.Rule.VMAlert != nil && entry.Rule.VMAlert.Debug != nil {
		msg := "`debug: true` makes vmalert log every query of this rule."
		if entry.Rule.AlertingRule != nil {
			msg = "`debug: true` makes vmalert log every query of this rule and every change of its alerts' state."
		}
		problems = append(problems, c.debugProblem(entry.Rule.VMAlert.Debug, msg))
	}
	if entry.Group != nil && entry.Group.VMAlert != nil && entry.Group.VMAlert.Debug != nil {
		problems = append(problems, c.debugProblem(entry.Group.VMAlert.Debug,
			"`debug: true` makes vmalert log every query of every rule in this group and every change of their alerts' state."))
	}
	return problems
}

func (c RuleVMAlertCheck) debugProblem(debug *parser.YamlNode, msg string) Problem {
	return Problem{
		Anchor:   AnchorAfter,
		Lines:    debug.Pos.Lines(),
		Reporter: c.Reporter(),
		Summary:  "debug mode enabled",
		Details:  RuleVMAlertDebugDetails,
		Severity: Warning,
		Diagnostics: []diags.Diagnostic{
			{
				Message:     msg,
				Pos:         debug.Pos,
				Expr:        nil,
				FirstColumn: 1,
				LastColumn:  len(debug.Value),
				Kind:        diags.Issue,
			},
		},
	}
}
