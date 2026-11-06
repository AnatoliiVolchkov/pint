package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/promapi"
)

// rule/vmalert reports vmalert group and rule settings that are likely mistakes, for MetricsQL
// rules only. Most of it only looks at rule files and runs offline; the group limit check
// (rule_vmalert_limit.go) asks the servers of the rule file how many results a rule returns.
//
// `debug: true` on a rule or a group makes vmalert log every datasource request of the rule
// and every alert state change at INFO level (app/vmalert/rule/alerting.go and
// datasource/client.go, v1.153.0; vmalert docs "Debug mode"), meant for debugging a rule,
// not for permanent use.
//
// `latency_offset` in group params overrides -search.latencyOffset: VictoriaMetrics then
// moves the time of a query back to now - latency_offset when it's newer. vmalert asks for
// now - eval_delay (the group's eval_delay, else -rule.evalDelay, 30s by default) and stamps
// the results with that time, so with a longer latency_offset results are computed for an
// earlier time than they're stamped with. Checked with VictoriaMetrics v1.153.0 on
// 2026-10-10, a sample every 15s: queried at now-30s with latency_offset=3m the newest
// sample seen was 161s before the query time, queried at now-3m20s it was 6s before. vmalert
// docs: "If group has latency_offset set in params, then it is recommended to set eval_delay
// equal to latency_offset". eval_delay is ignored with eval_offset, such groups are skipped.

// VMAlertCheckNames are checks that only apply to rules evaluated by vmalert. They aren't in
// CheckNames, so configs for Prometheus list and enable exactly the checks upstream pint has.
var VMAlertCheckNames = []string{RuleVMAlertCheckName}

const (
	RuleVMAlertCheckName    = "rule/vmalert"
	RuleVMAlertDebugDetails = "With `debug: true` vmalert logs every datasource request of the rule and every alert state change at INFO level, " +
		"set on a group it applies to all rules of the group. " +
		"It's meant for debugging a rule, remove it once you're done. " +
		"See [vmalert debug mode](https://docs.victoriametrics.com/victoriametrics/vmalert/#debug-mode)."
	RuleVMAlertLatencyOffsetDetails = "`latency_offset` in group `params` overrides `-search.latencyOffset`: VictoriaMetrics doesn't return " +
		"samples newer than that and moves the time of a query back to `now - latency_offset` when it's newer. " +
		"vmalert evaluates rules `eval_delay` before now, `-rule.evalDelay` (30s by default) when the group doesn't set it, " +
		"and stamps the results with that time. vmalert docs recommend setting `eval_delay` equal to `latency_offset`, " +
		"see [eval_delay](https://docs.victoriametrics.com/victoriametrics/vmalert/#groups) and " +
		"[query latency](https://docs.victoriametrics.com/victoriametrics/keyconcepts/#query-latency). " +
		"If vmalert runs with a `-rule.evalDelay` longer than the default, set `eval_delay` in the group anyway."

	// vmalertDefaultEvalDelay is the default -rule.evalDelay of vmalert.
	vmalertDefaultEvalDelay = 30 * time.Second
)

func NewRuleVMAlertCheck() RuleVMAlertCheck {
	return RuleVMAlertCheck{servers: nil, offline: false}
}

type RuleVMAlertCheck struct {
	// servers are checked for rules producing more results than vmalert allows, see
	// rule_vmalert_limit.go.
	servers []*promapi.FailoverGroup
	offline bool
}

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

func (c RuleVMAlertCheck) Check(ctx context.Context, entry *discovery.Entry, _ []*discovery.Entry) (problems []Problem) {
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
	if entry.Group != nil {
		problems = append(problems, c.latencyOffsetProblems(entry.Group.VMAlert)...)
	}
	if entry.Group != nil && entry.Group.VMAlert != nil && entry.Group.VMAlert.DuplicateName != nil {
		problems = append(problems, c.duplicateGroupProblem(entry.Group.VMAlert.DuplicateName))
	}
	problems = append(problems, c.limitProblems(ctx, entry)...)
	return problems
}

// latencyOffsetProblems reports a latency_offset longer than the group's eval_delay.
func (c RuleVMAlertCheck) latencyOffsetProblems(vg *parser.VMAlertGroup) []Problem {
	if vg == nil || vg.LatencyOffset == nil || vg.EvalOffset != nil {
		return nil
	}
	offset := parseVMQueryDuration(vg.LatencyOffset.Value)
	delay := vmalertDefaultEvalDelay
	source := "`-rule.evalDelay`, 30s by default, as the group doesn't set `eval_delay`"
	if vg.EvalDelay != nil {
		delay = *vg.EvalDelay
		source = "`eval_delay` of this group"
	}
	if offset <= delay {
		return nil
	}
	return []Problem{{
		Anchor:   AnchorAfter,
		Lines:    vg.LatencyOffset.Pos.Lines(),
		Reporter: c.Reporter(),
		Summary:  "latency_offset longer than eval_delay",
		Details:  RuleVMAlertLatencyOffsetDetails,
		Severity: Warning,
		Diagnostics: []diags.Diagnostic{
			{
				Message: fmt.Sprintf(
					"VictoriaMetrics answers queries of this group with data at least %s old, "+
						"but vmalert evaluates them %s before now, %s, and stamps the results with that time, "+
						"so alerts and recording rule results lag %s behind. Set `eval_delay: %s`.",
					output.HumanizeDuration(offset), output.HumanizeDuration(delay), source,
					output.HumanizeDuration(offset-delay), vg.LatencyOffset.Value,
				),
				Pos:         vg.LatencyOffset.Pos,
				Expr:        nil,
				FirstColumn: 1,
				LastColumn:  len(vg.LatencyOffset.Value),
				Kind:        diags.Issue,
			},
		},
	}}
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

const RuleVMAlertDuplicateGroupDetails = "vmalert refuses to load a rule file with two groups of the same name, " +
	"so none of the rules in that file are evaluated. Group names must be unique within a file. " +
	"In a ConfigMap every data key is a separate file, and so is the spec of a VMRule."

// duplicateGroupProblem reports a group whose name is already used in the same groups list.
func (c RuleVMAlertCheck) duplicateGroupProblem(name *parser.YamlNode) Problem {
	return Problem{
		Anchor:   AnchorAfter,
		Lines:    name.Pos.Lines(),
		Reporter: c.Reporter(),
		Summary:  "duplicated group name",
		Details:  RuleVMAlertDuplicateGroupDetails,
		Severity: Warning,
		Diagnostics: []diags.Diagnostic{
			{
				Message: fmt.Sprintf("Another group in this file is also named `%s`: vmalert fails to load the whole file "+
					"with `group name %q duplicate in file`.", name.Value, name.Value),
				Pos:         name.Pos,
				Expr:        nil,
				FirstColumn: 1,
				LastColumn:  len(name.Value),
				Kind:        diags.Issue,
			},
		},
	}
}
