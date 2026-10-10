package checks

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/source"
)

// vmalert drops the metric name from alerts and replaces it with the record name for
// recording rules. If the query returns series of more than one metric, series that only
// differ by the metric name get the same labels and vmalert fails the whole rule with
// "result contains metrics with the same labelset during evaluation" (errDuplicate in
// app/vmalert/rule, v1.153.0). Checked with vmalert v1.153.0 in Docker: an alert on
// {__name__=~"dup_x|dup_y"} > 0 and a recording rule on {__name__=~"dup_x|dup_y"} failed on
// every evaluation once both metrics had a series with the same other labels, while
// `dup_x > 0 or dup_y > 0` worked, `or` drops right hand series with the same labels.
// rate({__name__=~"dup_x|dup_y"}[1m]) failed earlier: rate() drops the metric name and
// VictoriaMetrics rejected the query with "duplicate output timeseries". Wrapping the query
// in label_replace(..., "metric", "$1", "__name__", "(.+)") fixed the alert.
// Rule labels that override query labels don't cause it, vmalert keeps the query value as
// exported_<name>.

const LabelsConflictDuplicateDetails = "If the query returns series of more than one metric, " +
	"series that only differ by the metric name get the same labels once the name is dropped, " +
	"and the rule fails on every evaluation:\n\n" +
	"- vmalert drops the metric name from alert labels and replaces it with the record name for recording rules, " +
	"then fails with `result contains metrics with the same labelset during evaluation`, " +
	"see [series with the same labelset](https://docs.victoriametrics.com/victoriametrics/vmalert/#series-with-the-same-labelset);\n" +
	"- functions like `rate()` drop the metric name in VictoriaMetrics, " +
	"which then rejects the query with `duplicate output timeseries`.\n\n" +
	"To fix it keep the metric name in another label with `label_replace()`, " +
	"for example `label_replace(<query>, \"metric\", \"$1\", \"__name__\", \"(.+)\")`, " +
	"or select a single metric."

// selectorMetricNames returns names of metrics a selector can match, ok is false if it can
// match any metric name. A regexp of literal names, like `foo|bar`, matches those names.
func selectorMetricNames(vs *ast.VectorSelector) (names []string, ok bool) {
	for _, group := range vs.MatcherGroups() {
		var groupNames []string
		for _, m := range group {
			if m.Name != model.MetricNameLabel {
				continue
			}
			switch m.Type {
			case labels.MatchEqual:
				groupNames = []string{m.Value}
			case labels.MatchRegexp:
				if literals, ok := literalAlternatives(m.Value); ok && groupNames == nil {
					groupNames = literals
				}
			case labels.MatchNotEqual, labels.MatchNotRegexp:
			}
		}
		if len(groupNames) == 0 {
			return nil, false
		}
		for _, name := range groupNames {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names, true
}

// literalAlternatives returns the alternatives of a regexp made only of literal strings
// separated by `|`, ok is false for any other regexp.
func literalAlternatives(re string) (alternatives []string, ok bool) {
	for part := range strings.SplitSeq(re, "|") {
		if part == "" || regexp.QuoteMeta(part) != part {
			return nil, false
		}
		alternatives = append(alternatives, part)
	}
	return alternatives, true
}

// copiesLabels returns true if the source uses a function that can copy the metric name
// into another label, which keeps series of different metrics apart.
func copiesLabels(s *source.Source) bool {
	for _, op := range s.Operations {
		if strings.HasPrefix(op.Operation, "label_") || op.Operation == "alias" {
			return true
		}
	}
	return false
}

// vmDuplicateLabelsets reports MetricsQL rules returning series of more than one metric,
// which vmalert can't evaluate when such series have the same other labels.
func (c LabelsConflictCheck) vmDuplicateLabelsets(entry *discovery.Entry) (problems []Problem) {
	if !isVictoriaMetrics(c.prom) || !isVMAlertRule(entry) {
		return nil
	}
	expr := entry.Rule.Expr()
	if expr.SyntaxError() != nil {
		return nil
	}
	reported := map[ast.PositionRange]struct{}{}
	for _, src := range expr.Source() {
		src.WalkSources(func(s *source.Source, _ *source.Join, _ *source.Unless) {
			if !s.CanHaveLabel(model.MetricNameLabel) || copiesLabels(s) {
				return
			}
			vs, ok := source.MostOuterOperation[*ast.VectorSelector](s)
			if !ok {
				return
			}
			if names, ok := selectorMetricNames(vs); ok && len(names) < 2 {
				return
			}
			if _, ok := reported[vs.PosRange]; ok {
				return
			}
			reported[vs.PosRange] = struct{}{}

			problems = append(problems, Problem{
				Anchor:   AnchorAfter,
				Lines:    expr.Value.Pos.Lines(),
				Reporter: c.Reporter(),
				Summary:  "rule results can have the same labels",
				Details:  LabelsConflictDuplicateDetails,
				Severity: Warning,
				Diagnostics: []diags.Diagnostic{
					{
						Message: fmt.Sprintf(
							"`%s` can return series of more than one metric, "+
								"series that only differ by the metric name get the same labels once the name is dropped "+
								"and the whole rule fails.",
							expr.Value.Value[vs.PosRange.Start:vs.PosRange.End],
						),
						Pos:         expr.Value.Pos,
						Expr:        expr.Query().Expr,
						FirstColumn: int(vs.PosRange.Start) + 1,
						LastColumn:  int(vs.PosRange.End),
						Kind:        diags.Issue,
					},
				},
			})
		})
	}
	return problems
}
