package checks

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/output"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/promapi"
)

// How vmalert and VictoriaMetrics read rule results, verified against v1.153.0:
//
//   - vmalert evaluates rules with instant queries and sends -datasource.queryStep (5m by
//     default) as the step, unless the group sets `step` in `params`
//     (setPrometheusInstantReqParams and setReqParams in app/vmalert/datasource).
//   - VictoriaMetrics answers an instant query with the newest sample inside the lookback
//     window, which is the step. -search.maxStalenessInterval and -search.maxLookback cap
//     it, -search.minStalenessInterval raises it and -search.setLookbackToStep pins it to
//     the step (QueryHandler and getMaxLookback in app/vmselect/prometheus,
//     rollupConfig.doInternal in app/vmselect/promql/rollup.go).
//   - Range queries, used by graphs and subqueries, look back as far as the interval
//     detected from the samples, so they have no gaps.
//   - vmalert aligns the timestamp of rule results to the group interval unless the group
//     sets eval_offset or eval_alignment: false (adjustReqTimestamp in
//     app/vmalert/rule/group.go). A result appears in VictoriaMetrics up to one interval
//     after its timestamp, depending on when vmalert evaluates the group.
//   - vmalert sends firing alerts with EndsAt set to 4 x max(interval, -rule.resendDelay)
//     and resends them on every evaluation, so a long interval doesn't make notifications
//     flap (getResolveDuration in app/vmalert/rule/group.go). The ALERTS series written
//     on every evaluation do get gaps.
//
// So for vmalert a long interval is a problem when other rules read the results with a
// lookback shorter than the interval, plus up to one more interval when timestamps are
// aligned.

const (
	vmFlagMaxLookback          = "search.maxLookback"
	vmFlagMaxStalenessInterval = "search.maxStalenessInterval"
	vmFlagMinStalenessInterval = "search.minStalenessInterval"
	vmFlagSetLookbackToStep    = "search.setLookbackToStep"

	vmalertParamStep        = "step"
	vmalertParamMaxLookback = "max_lookback"

	// VictoriaMetrics uses this step for instant queries without one, unless the lookback
	// is limited with -search.maxLookback or -search.maxStalenessInterval.
	vmDefaultInstantStep = 5 * time.Minute
)

func isVMAlertRule(entry *discovery.Entry) bool {
	expr := entry.Rule.Expr()
	return expr != nil && expr.Language().Name() == metricsql.Name
}

// WithServers returns a copy of the check that knows the servers vmalert queries when it
// evaluates rules of the checked file. They decide how far back rules read results.
func (c GroupIntervalCheck) WithServers(servers []*promapi.FailoverGroup) GroupIntervalCheck {
	c.servers = slices.Clone(servers)
	return c
}

// Offline returns a copy of the check that doesn't read server flags.
func (c GroupIntervalCheck) Offline() GroupIntervalCheck {
	c.offline = true
	return c
}

// vmLookbackSettings are vmalert and VictoriaMetrics settings that decide how far back
// instant queries look for samples.
type vmLookbackSettings struct {
	server         string
	maxLookbackSrc string
	queryStep      time.Duration
	maxLookback    time.Duration
	minStaleness   time.Duration
	lookbackToStep bool
	queryStepSet   bool
}

func (c GroupIntervalCheck) lookbackSettings(ctx context.Context) []vmLookbackSettings {
	settings := make([]vmLookbackSettings, 0, len(c.servers))
	for _, prom := range c.servers {
		step, ok := prom.VMAlertQueryStep()
		if !ok {
			continue
		}
		s := vmLookbackSettings{
			server:         prom.Name(),
			queryStep:      step,
			queryStepSet:   step != promapi.VMAlertDefaultQueryStep,
			maxLookback:    0,
			minStaleness:   0,
			maxLookbackSrc: "",
			lookbackToStep: false,
		}
		if !c.offline {
			flags, err := prom.Flags(ctx).Wait()
			if err != nil {
				slog.LogAttrs(ctx, slog.LevelDebug, "Cannot read VictoriaMetrics flags, using defaults for group/interval",
					slog.String("name", prom.Name()), slog.Any("err", err))
			} else {
				s.applyFlags(flags.Flags)
			}
		}
		settings = append(settings, s)
	}
	if len(settings) == 0 {
		settings = append(settings, vmLookbackSettings{
			server:         "",
			queryStep:      promapi.VMAlertDefaultQueryStep,
			queryStepSet:   false,
			maxLookback:    0,
			minStaleness:   0,
			maxLookbackSrc: "",
			lookbackToStep: false,
		})
	}
	return settings
}

func (s *vmLookbackSettings) applyFlags(flags map[string]string) {
	for _, name := range []string{vmFlagMaxLookback, vmFlagMaxStalenessInterval} {
		if d := parseVMFlagDuration(flags[name]); d > 0 {
			s.maxLookback = d
			s.maxLookbackSrc = fmt.Sprintf("-%s=%s", name, output.HumanizeDuration(d))
			break
		}
	}
	s.minStaleness = parseVMFlagDuration(flags[vmFlagMinStalenessInterval])
	s.lookbackToStep, _ = strconv.ParseBool(flags[vmFlagSetLookbackToStep])
}

// parseVMFlagDuration parses durations printed by /flags, like 5m0s.
func parseVMFlagDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	if d, err := model.ParseDuration(s); err == nil {
		return time.Duration(d)
	}
	return 0
}

// parseVMQueryDuration parses step and max_lookback query arguments: seconds or a duration.
func parseVMQueryDuration(s string) time.Duration {
	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}
	if d, err := model.ParseDuration(s); err == nil {
		return time.Duration(d)
	}
	return 0
}

// window returns how far back an instant query looks for samples with given step and
// max_lookback query arguments, zero means the argument isn't set. It mirrors
// getMaxLookback, QueryHandler and rollupConfig.doInternal from VictoriaMetrics.
// Without an explicit window plain selectors use default_rollup, which can't look back
// further than the max lookback. Other rollup functions are assumed to adjust their window.
func (s vmLookbackSettings) window(step, maxLookback time.Duration, plainSelector bool) time.Duration {
	ld := s.maxLookback
	if maxLookback > 0 {
		ld = maxLookback
	}
	if s.lookbackToStep && step > 0 {
		ld = step
	}
	if step <= 0 {
		step = ld
	}
	if step <= 0 {
		step = vmDefaultInstantStep
	}
	maxPrev := step
	if ld > 0 && maxPrev > ld {
		maxPrev = ld
	}
	if s.minStaleness > maxPrev {
		maxPrev = s.minStaleness
	}
	w := max(step, maxPrev)
	if plainSelector && ld > 0 && w > ld {
		w = ld
	}
	return w
}

// groupIntervalRead is a rule that reads results of the checked rule.
type groupIntervalRead struct {
	entry     *discovery.Entry
	how       string
	lookback  time.Duration
	sameGroup bool
	// noGaps is true for reads that don't depend on the lookback, like subqueries.
	noGaps bool
}

// readsResultsOf returns true if the selector reads results of given rule: the recording
// rule metric, or ALERTS and ALERTS_FOR_STATE series of the alerting rule.
func readsResultsOf(entry *discovery.Entry, vs *ast.VectorSelector) bool {
	if entry.Rule.RecordingRule != nil {
		name := entry.Rule.RecordingRule.Record.Value
		return vs.Name == name || selectsName(vs, name)
	}
	if entry.Rule.AlertingRule == nil || (vs.Name != "ALERTS" && vs.Name != "ALERTS_FOR_STATE") {
		return false
	}
	for _, lm := range vs.AllMatchers() {
		if lm.Name == "alertname" && lm.Type == labels.MatchEqual && lm.Value == entry.Rule.AlertingRule.Alert.Value {
			return true
		}
	}
	return false
}

// walkReads calls fn for every selector with its explicit or implicit window. Selectors
// inside subqueries are evaluated with range queries, inSubquery is true for them.
func walkReads(node ast.Node, inSubquery bool, fn func(vs *ast.VectorSelector, ms *ast.MatrixSelector, inSubquery bool)) {
	switch n := node.(type) {
	case *ast.SubqueryExpr:
		inSubquery = true
	case *ast.MatrixSelector:
		if vs, ok := n.VectorSelector.(*ast.VectorSelector); ok {
			fn(vs, n, inSubquery)
		}
		return
	case *ast.VectorSelector:
		fn(n, nil, inSubquery)
		return
	}
	for _, child := range ast.Children(node) {
		walkReads(child, inSubquery, fn)
	}
}

func groupIntervalReads(entry *discovery.Entry, entries []*discovery.Entry, settings []vmLookbackSettings) (reads []groupIntervalRead) {
	name := entry.Rule.Name()
	for _, other := range nonRemovedEntries(entries) {
		if other.Group == nil || other.Path.Name == entry.Path.Name && other.Rule.Lines == entry.Rule.Lines {
			continue
		}
		expr := other.Rule.Expr()
		if expr == nil || !isVMAlertRule(other) || !strings.Contains(expr.Value.Value, name) || expr.SyntaxError() != nil {
			continue
		}
		step := parseVMQueryParam(other, vmalertParamStep)
		maxLookback := parseVMQueryParam(other, vmalertParamMaxLookback)
		var best *groupIntervalRead
		walkReads(expr.Query().Expr, false, func(vs *ast.VectorSelector, ms *ast.MatrixSelector, inSubquery bool) {
			if !readsResultsOf(entry, vs) {
				return
			}
			read := groupIntervalRead{
				entry:     other,
				sameGroup: isSameGroup(entry, other),
				lookback:  0,
				how:       "",
				// Subqueries are range queries, they look back as far as the interval
				// between samples. Windows relative to the step are unknown.
				noGaps: inSubquery || (ms != nil && !ms.Implicit && ms.StepRelative),
			}
			switch {
			case read.noGaps:
			case ms != nil && !ms.Implicit:
				read.lookback = ms.Range
				read.how = fmt.Sprintf("the `[%s]` window", output.HumanizeDuration(ms.Range))
			default:
				// Use the longest lookback of all servers, so it's only reported if
				// it's too short everywhere. vmalert always sends a step.
				for _, s := range settings {
					w := s.window(cmp.Or(step, s.queryStep), maxLookback, ms == nil)
					if w > read.lookback {
						read.lookback = w
						read.how = s.describe(step, maxLookback, w)
					}
				}
			}
			// Report each reader once, using its longest lookback.
			if best == nil || (!best.noGaps && (read.noGaps || read.lookback > best.lookback)) {
				best = &read
			}
		})
		if best != nil {
			reads = append(reads, *best)
		}
	}
	slices.SortStableFunc(reads, func(a, b groupIntervalRead) int {
		return cmp.Or(
			cmp.Compare(a.entry.Path.Name, b.entry.Path.Name),
			cmp.Compare(a.entry.Rule.Lines.First, b.entry.Rule.Lines.First),
		)
	})
	return reads
}

// isSameGroup compares group names and where they are, relaxed files like Kubernetes
// ConfigMaps can have more groups with the same name.
func isSameGroup(a, b *discovery.Entry) bool {
	return a.Path.Name == b.Path.Name &&
		a.Group.Name.Value == b.Group.Name.Value &&
		a.Group.Name.Pos.Lines().First == b.Group.Name.Pos.Lines().First
}

func parseVMQueryParam(entry *discovery.Entry, name string) time.Duration {
	if v, ok := entry.Group.VMAlert.Param(name); ok {
		return parseVMQueryDuration(v)
	}
	return 0
}

// describe explains where the lookback w of a query comes from.
func (s vmLookbackSettings) describe(step, maxLookback, w time.Duration) string {
	var src string
	switch {
	case step > 0:
		src = fmt.Sprintf("the query step from `params` of its group (%s)", output.HumanizeDuration(step))
	case s.queryStepSet:
		src = fmt.Sprintf("the query step, vmalert -datasource.queryStep set in pint config (%s)", output.HumanizeDuration(s.queryStep))
		step = s.queryStep
	default:
		src = fmt.Sprintf("the query step, default vmalert -datasource.queryStep (%s)", output.HumanizeDuration(s.queryStep))
		step = s.queryStep
	}
	switch {
	case w < step && maxLookback > 0:
		src += fmt.Sprintf(", limited by `max_lookback` from `params` of its group (%s)", output.HumanizeDuration(maxLookback))
	case w < step:
		src += fmt.Sprintf(", limited by %s on `%s`", s.maxLookbackSrc, s.server)
	case w > step:
		src += fmt.Sprintf(", raised by -search.minStalenessInterval=%s on `%s`", output.HumanizeDuration(s.minStaleness), s.server)
	}
	return src
}

// isAligned returns true if vmalert aligns result timestamps of the group to its interval.
func isAligned(entry *discovery.Entry) bool {
	vg := entry.Group.VMAlert
	if vg == nil {
		return true
	}
	return vg.EvalOffset == nil && (vg.EvalAlignment == nil || *vg.EvalAlignment)
}

func (c GroupIntervalCheck) checkVMAlert(ctx context.Context, entry *discovery.Entry, entries []*discovery.Entry) (problems []Problem) {
	if entry.Group.Interval.ParseError != nil || entry.Rule.Expr().SyntaxError() != nil {
		return nil
	}
	// Alerting rules are only reported when other rules read their ALERTS series,
	// don't read server flags for the rest.
	if entry.Rule.AlertingRule != nil && !slices.ContainsFunc(entries, func(e *discovery.Entry) bool {
		expr := e.Rule.Expr()
		return expr != nil && strings.Contains(expr.Value.Value, "ALERTS") && strings.Contains(expr.Value.Value, entry.Rule.Name())
	}) {
		return nil
	}
	interval := entry.Group.Interval.Value
	settings := c.lookbackSettings(ctx)
	reads := groupIntervalReads(entry, entries, settings)
	aligned := isAligned(entry)

	target := "`" + entry.Rule.Name() + "`"
	if entry.Rule.AlertingRule != nil {
		target = fmt.Sprintf("`ALERTS{alertname=%q}`", entry.Rule.Name())
	}

	if len(reads) == 0 {
		return c.unreadVMAlertResults(entry, settings, interval)
	}

	var details strings.Builder
	details.WriteString(vmalertLookbackIntro)
	fmt.Fprintf(&details, "This group writes %s every %s.", target, output.HumanizeDuration(interval))
	if aligned {
		fmt.Fprintf(&details, " vmalert aligns the timestamp of results to the group interval, "+
			"so a new result appears up to %s after its timestamp, depending on when vmalert evaluates the group.",
			output.HumanizeDuration(interval))
	}
	fmt.Fprintf(&details, "\n\nRules reading %s:\n\n", target)

	var certain, possible []groupIntervalRead
	for _, read := range reads {
		var gap string
		switch {
		case read.noGaps:
			continue
		case read.sameGroup && read.lookback <= interval:
			gap = fmt.Sprintf("it runs in the same group, results of the previous evaluation are already %s old, so it gets no data",
				output.HumanizeDuration(interval))
			certain = append(certain, read)
		case read.sameGroup:
			continue
		case read.lookback < interval:
			gap = fmt.Sprintf("no data for at least %s of every %s",
				output.HumanizeDuration(interval-read.lookback), output.HumanizeDuration(interval))
			certain = append(certain, read)
		case aligned && read.lookback < 2*interval:
			gap = fmt.Sprintf("no data for up to %s of every %s, depending on when vmalert evaluates this group",
				output.HumanizeDuration(2*interval-read.lookback), output.HumanizeDuration(interval))
			possible = append(possible, read)
		default:
			continue
		}
		fmt.Fprintf(&details, "- `%s` at `%s:%d` looks back %s, %s: %s.\n",
			read.entry.Rule.Name(), read.entry.Path.SymlinkTarget, read.entry.Rule.Lines.First,
			output.HumanizeDuration(read.lookback), read.how, gap)
	}
	if len(certain) == 0 && len(possible) == 0 {
		return nil
	}

	details.WriteString("\nTo fix it ")
	if aligned {
		fmt.Fprintf(&details, "read %s with a lookback of at least %s, for example with `params: {step: [\"%s\"]}` on the reading groups, "+
			"or set `eval_offset` on this group and read it with a lookback of at least %s, ",
			target, output.HumanizeDuration(2*interval), output.HumanizeDuration(2*interval), output.HumanizeDuration(interval))
	} else {
		fmt.Fprintf(&details, "read %s with a lookback of at least %s, for example with `params: {step: [\"%s\"]}` on the reading groups, ",
			target, output.HumanizeDuration(interval), output.HumanizeDuration(interval))
	}
	details.WriteString("or lower the interval of this group.")
	if slices.ContainsFunc(certain, func(r groupIntervalRead) bool { return r.sameGroup }) {
		fmt.Fprintf(&details, " Rules in the same group need a lookback longer than %s, or move them to another group.", output.HumanizeDuration(interval))
	}

	severity := Information
	var msg string
	if len(certain) > 0 {
		severity = Warning
		msg = fmt.Sprintf("`%s` reads %s with a %s lookback, %s, so it gets empty results between evaluations of this group.",
			certain[0].entry.Rule.Name(), target, output.HumanizeDuration(certain[0].lookback), shorterThan(certain[0], interval))
	} else {
		msg = fmt.Sprintf("`%s` reads %s with a %s lookback, less than 2 x this interval, "+
			"so it can get empty results: vmalert aligns result timestamps to the interval.",
			possible[0].entry.Rule.Name(), target, output.HumanizeDuration(possible[0].lookback))
	}
	if n := len(certain) + len(possible) - 1; n > 0 {
		msg += fmt.Sprintf(" %d more rule(s) are listed in the details.", n)
	}

	problems = append(problems, Problem{
		Anchor:   AnchorAfter,
		Lines:    entry.Group.Interval.Pos.Lines(),
		Reporter: c.Reporter(),
		Summary:  "interval too long",
		Details:  details.String(),
		Severity: severity,
		Diagnostics: []diags.Diagnostic{
			{
				Message:     msg,
				Pos:         entry.Group.Interval.Pos,
				Expr:        nil,
				FirstColumn: 1,
				LastColumn:  entry.Group.Interval.Pos.Len(),
				Kind:        diags.Issue,
			},
		},
	})
	return problems
}

func shorterThan(read groupIntervalRead, interval time.Duration) string {
	if read.sameGroup {
		return "in the same group with this " + output.HumanizeDuration(interval) + " interval"
	}
	return "shorter than this " + output.HumanizeDuration(interval) + " interval"
}

const vmalertLookbackIntro = "vmalert evaluates rules with instant queries. VictoriaMetrics answers an instant query with " +
	"the newest sample in the lookback window, which is the query step: vmalert sends `-datasource.queryStep`, " +
	"5m by default, or `step` from the group `params`. Range queries used by graphs are not affected, " +
	"VictoriaMetrics looks back as far as the interval between samples.\n\n"

// unreadVMAlertResults returns information about recording rules no linted rule reads,
// when instant queries with the default step can't see their results between evaluations.
func (c GroupIntervalCheck) unreadVMAlertResults(entry *discovery.Entry, settings []vmLookbackSettings, interval time.Duration) (problems []Problem) {
	if entry.Rule.RecordingRule == nil {
		return nil
	}
	var lookback time.Duration
	for _, s := range settings {
		lookback = max(lookback, s.window(0, 0, true))
	}
	if interval <= lookback {
		return nil
	}
	problems = append(problems, Problem{
		Anchor:   AnchorAfter,
		Lines:    entry.Group.Interval.Pos.Lines(),
		Reporter: c.Reporter(),
		Summary:  "interval too long",
		Details: vmalertLookbackIntro + fmt.Sprintf("No checked rule reads `%s`. Instant queries of it, like stat panels in Grafana, "+
			"use the default step (%s) when they don't set one.", entry.Rule.Name(), output.HumanizeDuration(lookback)),
		Severity: Information,
		Diagnostics: []diags.Diagnostic{
			{
				Message: fmt.Sprintf("Instant queries of `%s` with the default %s step find no data for at least %s of every %s, graphs are not affected.",
					entry.Rule.Name(), output.HumanizeDuration(lookback), output.HumanizeDuration(interval-lookback), output.HumanizeDuration(interval)),
				Pos:         entry.Group.Interval.Pos,
				Expr:        nil,
				FirstColumn: 1,
				LastColumn:  entry.Group.Interval.Pos.Len(),
				Kind:        diags.Issue,
			},
		},
	})
	return problems
}
