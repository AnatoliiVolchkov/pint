package checks

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/promapi"
)

// vmalert fails a rule that produces more results than `limit` of its group, or
// -rule.resultsLimit when the group doesn't set one, and drops all its results. Checked with
// vmalert v1.153.0 on 2026-11-06, a group with `limit: 2`: a recording rule returning 10
// series failed with "exec exceeded limit of 2 with 10 series" and wrote nothing, an alerting
// rule returning 10 series failed with "exec exceeded limit of 2 with 10 alerts" and fired
// nothing, a rule returning 2 series worked. vmalert -help: -rule.resultsLimit "can be
// overridden by the limit option under group if specified".
//
// rule/vmalert sends count(expr) for rules with a limit to the VictoriaMetrics servers of
// their file. It's one instant query per rule, only for groups with a limit or when vmalert
// runs with -rule.resultsLimit (read with vmalertURI). The rule is reported if it returns
// more results now; how many it returns later depends on the data.

const RuleVMAlertLimitDetails = "vmalert fails a rule that returns more results than `limit` of its group, " +
	"or `-rule.resultsLimit` when the group doesn't set one: the rule is marked with an error and all its results are dropped, " +
	"so a recording rule writes nothing and an alerting rule fires nothing. " +
	"Make the query return fewer series, or raise the limit. " +
	"See [vmalert groups](https://docs.victoriametrics.com/victoriametrics/vmalert/#groups)."

// WithServers returns a copy of the check that compares the number of results of rules with
// the vmalert limit on these servers.
func (c RuleVMAlertCheck) WithServers(servers []*promapi.FailoverGroup) RuleVMAlertCheck {
	c.servers = slices.Clone(servers)
	return c
}

// Offline returns a copy of the check that doesn't query servers.
func (c RuleVMAlertCheck) Offline() RuleVMAlertCheck {
	c.offline = true
	return c
}

// vmalertLimit returns the limit vmalert applies to results of rules in the group of the
// entry and how to describe it, 0 when there's none.
func vmalertLimit(entry *discovery.Entry, prom *promapi.FailoverGroup) (int, string) {
	if entry.Group != nil && entry.Group.Limit != nil {
		if entry.Group.Limit.ParseError != nil {
			return 0, ""
		}
		return entry.Group.Limit.Value, fmt.Sprintf("`limit: %d` of the group", entry.Group.Limit.Value)
	}
	if limit, uri := prom.VMAlertResultsLimit(); limit > 0 {
		return limit, fmt.Sprintf("`-rule.resultsLimit=%d` of vmalert at %s", limit, uri)
	}
	return 0, ""
}

// limitProblems reports rules that return more results than vmalert allows.
func (c RuleVMAlertCheck) limitProblems(ctx context.Context, entry *discovery.Entry) (problems []Problem) {
	if c.offline || entry.Rule.AlertingRule == nil && entry.Rule.RecordingRule == nil {
		return nil
	}
	expr := entry.Rule.Expr()
	if expr.SyntaxError() != nil {
		return nil
	}
	for _, prom := range c.servers {
		if !isVictoriaMetrics(prom) {
			continue
		}
		limit, limitText := vmalertLimit(entry, prom)
		if limit <= 0 {
			continue
		}
		qr, err := prom.Query(ctx, wrapExpr(expr.Value.Value, "count")).Wait()
		if err != nil {
			// Other online checks report servers that can't run the query.
			slog.LogAttrs(ctx, slog.LevelDebug, "Cannot count rule results",
				slog.String("name", prom.Name()), slog.String("rule", entry.Rule.Name()), slog.Any("err", err))
			continue
		}
		var results int
		for _, s := range qr.Series {
			results += int(s.Value)
		}
		if results <= limit {
			continue
		}
		kind := "series"
		if entry.Rule.AlertingRule != nil {
			kind = "alerts"
		}
		problems = append(problems, Problem{
			Anchor:   AnchorAfter,
			Lines:    expr.Value.Pos.Lines(),
			Reporter: c.Reporter(),
			Summary:  "rule returns more results than vmalert allows",
			Details:  RuleVMAlertLimitDetails,
			Severity: Warning,
			Diagnostics: []diags.Diagnostic{
				{
					Message: fmt.Sprintf(
						"%s returned %d result(s) for this query, more than %s: vmalert would fail this rule "+
							"with `exec exceeded limit of %d with %d %s` and drop all its results.",
						vmServerText(prom, qr.URI), results, limitText, limit, results, kind,
					),
					Pos:         expr.Value.Pos,
					Expr:        expr.Query().Expr,
					FirstColumn: 1,
					LastColumn:  len(expr.Value.Value),
					Kind:        diags.Issue,
				},
			},
		})
	}
	return problems
}
