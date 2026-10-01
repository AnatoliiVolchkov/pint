// Package metricsql converts queries parsed by the VictoriaMetrics MetricsQL parser into pint AST.
package metricsql

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	mql "github.com/VictoriaMetrics/metricsql"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/cloudflare/pint/internal/parser/ast"
)

const Name = "metricsql"

// Language implements ast.Language for MetricsQL.
type Language struct{}

func (Language) Name() string {
	return Name
}

func (Language) ParseExpr(query string) (node ast.Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unexpected error: %v", r)
		}
	}()

	expr, err := mql.Parse(query)
	if err != nil {
		return nil, convertError(err, len(query))
	}
	c := converter{query: query, err: nil}
	node = c.convert(expr)
	if c.err != nil {
		return nil, c.err
	}
	return node, nil
}

func (l Language) ParseMetricSelector(selector string) ([]*labels.Matcher, error) {
	node, err := l.ParseExpr(selector)
	if err != nil {
		return nil, err
	}
	vs, ok := node.(*ast.VectorSelector)
	if !ok {
		return nil, fmt.Errorf("expected a time series selector, got %q", selector)
	}
	if len(vs.OrMatchers) > 0 {
		return nil, fmt.Errorf("time series selectors with `or` filters are not supported here: %q", selector)
	}
	return vs.LabelMatchers, nil
}

func convertError(err error, queryLen int) error {
	perr, ok := errors.AsType[*mql.ParseError](err)
	if !ok {
		return err
	}
	pos := perr.Pos
	if pos.Start < 0 || pos.End < pos.Start || pos.End > queryLen {
		pos = mql.Span{Start: 0, End: queryLen}
	}
	return &ast.ParseError{
		Cause:         err,
		Message:       perr.Msg,
		PositionRange: toPosRange(pos),
	}
}

type converter struct {
	err   error
	query string
}

func (c *converter) fail(pos mql.Span, format string, args ...any) {
	if c.err != nil {
		return
	}
	c.err = &ast.ParseError{
		Cause:         nil,
		Message:       fmt.Sprintf(format, args...),
		PositionRange: toPosRange(pos),
	}
}

// convert converts an expression used in an instant vector context.
func (c *converter) convert(e mql.Expr) ast.Node {
	return c.wrapParens(e, c.convertInner(e))
}

// convertRollupArg converts an expression passed to a function that expects a range vector.
// MetricsQL allows to skip the lookbehind window, which is then derived from the query step.
func (c *converter) convertRollupArg(e mql.Expr) ast.Node {
	switch t := e.(type) {
	case *mql.MetricExpr:
		vs := c.convertMetricExpr(t)
		return &ast.MatrixSelector{
			VectorSelector: vs,
			PosRange:       vs.PosRange,
			Implicit:       true,
			StepRelative:   true,
		}
	case *mql.RollupExpr:
		if t.Window == nil && t.Step == nil && !t.InheritStep {
			if me, ok := t.Expr.(*mql.MetricExpr); ok {
				vs := c.convertMetricExpr(me)
				vs.PosRange = toPosRange(t.Pos)
				c.setModifiers(t, &vs.OriginalOffset, &vs.OffsetText, &vs.Timestamp, &vs.StartOrEnd, &vs.At)
				return c.wrapParens(e, &ast.MatrixSelector{
					VectorSelector: vs,
					PosRange:       toPosRange(t.Pos),
					Implicit:       true,
					StepRelative:   true,
				})
			}
		}
		return c.convert(e)
	case *mql.NumberExpr, *mql.StringExpr, *mql.DurationExpr:
		return c.convert(e)
	default:
		inner := c.convert(e)
		return &ast.SubqueryExpr{
			Expr:         inner,
			PosRange:     inner.PositionRange(),
			Implicit:     true,
			StepRelative: true,
		}
	}
}

func (c *converter) wrapParens(e mql.Expr, node ast.Node) ast.Node {
	si := mql.SourceOf(e)
	if si == nil {
		return node
	}
	parens := si.Parens
	if be, ok := e.(*mql.BinaryOpExpr); ok && be.KeepMetricNames && len(parens) > 0 {
		// (foo + bar) keep_metric_names - these parens are part of the modifier syntax.
		parens = parens[1:]
	}
	for _, p := range parens {
		node = &ast.ParenExpr{Expr: node, PosRange: toPosRange(p)}
	}
	return node
}

func (c *converter) convertInner(e mql.Expr) ast.Node {
	switch t := e.(type) {
	case *mql.MetricExpr:
		return c.convertMetricExpr(t)
	case *mql.RollupExpr:
		return c.convertRollupExpr(t)
	case *mql.FuncExpr:
		return c.convertFuncExpr(t)
	case *mql.AggrFuncExpr:
		return c.convertAggrFuncExpr(t)
	case *mql.BinaryOpExpr:
		return c.convertBinaryOpExpr(t)
	case *mql.NumberExpr:
		return &ast.NumberLiteral{Val: t.N, PosRange: toPosRange(t.Pos)}
	case *mql.StringExpr:
		return &ast.StringLiteral{Val: t.S, PosRange: toPosRange(t.Pos)}
	case *mql.DurationExpr:
		return &ast.NumberLiteral{
			Val:      float64(t.Duration(0)) / 1000,
			Duration: true,
			PosRange: toPosRange(t.Pos),
		}
	default:
		c.fail(mql.Span{Start: 0, End: len(c.query)}, "unsupported MetricsQL expression: %T", e)
		return &ast.VectorSelector{}
	}
}

func (c *converter) convertMetricExpr(me *mql.MetricExpr) *ast.VectorSelector {
	vs := &ast.VectorSelector{PosRange: toPosRange(me.Pos)}

	var names []string
	for i, lfs := range me.LabelFilterss {
		matchers := make([]*labels.Matcher, 0, len(lfs))
		var nameMatcher *labels.Matcher
		for _, lf := range lfs {
			m, err := labels.NewMatcher(matchType(lf), lf.Label, lf.Value)
			if err != nil {
				pos := lf.Pos
				if pos.IsZero() {
					pos = me.Pos
				}
				c.fail(pos, "invalid label matcher: %s", err)
				return vs
			}
			if lf.Label == model.MetricNameLabel && m.Type == labels.MatchEqual && nameMatcher == nil {
				nameMatcher = m
				continue
			}
			matchers = append(matchers, m)
		}
		// Prometheus puts the metric name matcher last.
		if nameMatcher != nil {
			matchers = append(matchers, nameMatcher)
			names = append(names, nameMatcher.Value)
		}
		if i == 0 {
			vs.LabelMatchers = matchers
		} else {
			vs.OrMatchers = append(vs.OrMatchers, matchers)
		}
	}
	if len(names) > 0 && len(names) == len(me.LabelFilterss) {
		vs.Name = names[0]
		for _, name := range names[1:] {
			if name != vs.Name {
				vs.Name = ""
				break
			}
		}
	}
	return vs
}

func matchType(lf mql.LabelFilter) labels.MatchType {
	switch {
	case lf.IsNegative && lf.IsRegexp:
		return labels.MatchNotRegexp
	case lf.IsNegative:
		return labels.MatchNotEqual
	case lf.IsRegexp:
		return labels.MatchRegexp
	default:
		return labels.MatchEqual
	}
}

func (c *converter) convertRollupExpr(re *mql.RollupExpr) ast.Node {
	pos := toPosRange(re.Pos)
	me, isMetric := re.Expr.(*mql.MetricExpr)

	switch {
	case re.Window == nil && re.Step == nil && !re.InheritStep:
		// foo offset 5m
		// sum(foo) offset 5m
		if isMetric {
			vs := c.convertMetricExpr(me)
			vs.PosRange = pos
			c.setModifiers(re, &vs.OriginalOffset, &vs.OffsetText, &vs.Timestamp, &vs.StartOrEnd, &vs.At)
			return vs
		}
		oe := &ast.OffsetExpr{Expr: c.convert(re.Expr), PosRange: pos}
		c.setModifiers(re, &oe.OriginalOffset, &oe.OffsetText, &oe.Timestamp, &oe.StartOrEnd, &oe.At)
		return oe
	case isMetric && re.Step == nil && !re.InheritStep:
		// foo[5m] offset 5m
		vs := c.convertMetricExpr(me)
		c.setModifiers(re, &vs.OriginalOffset, &vs.OffsetText, &vs.Timestamp, &vs.StartOrEnd, &vs.At)
		ms := &ast.MatrixSelector{VectorSelector: vs, PosRange: pos}
		ms.Range, ms.RangeText, ms.StepRelative = c.duration(re.Window)
		return ms
	default:
		// foo[5m:1m]
		// sum(foo)[5m:]
		// sum(foo)[5m]
		sq := &ast.SubqueryExpr{Expr: c.convert(re.Expr), PosRange: pos}
		if re.Window == nil {
			sq.Implicit = true
			sq.StepRelative = true
		} else {
			sq.Range, sq.RangeText, sq.StepRelative = c.duration(re.Window)
		}
		if re.Step != nil {
			var stepRelative bool
			sq.Step, sq.StepText, stepRelative = c.duration(re.Step)
			if stepRelative {
				sq.StepRelative = true
			}
		}
		c.setModifiers(re, &sq.OriginalOffset, &sq.OffsetText, &sq.Timestamp, &sq.StartOrEnd, &sq.At)
		return sq
	}
}

func (c *converter) setModifiers(re *mql.RollupExpr, offset *time.Duration, offsetText *string, ts **int64, startOrEnd *ast.Op, at *ast.Node) {
	if re.Offset != nil {
		var stepRelative bool
		*offset, *offsetText, stepRelative = c.duration(re.Offset)
		if !stepRelative {
			*offsetText = ""
		}
	}
	if re.At == nil {
		return
	}
	switch t := re.At.(type) {
	case *mql.NumberExpr:
		v := int64(math.Round(t.N * 1000))
		*ts = &v
		return
	case *mql.FuncExpr:
		if len(t.Args) == 0 && len(t.Parens) == 0 {
			switch strings.ToLower(t.Name) {
			case "start":
				*startOrEnd = ast.OpStart
				return
			case "end":
				*startOrEnd = ast.OpEnd
				return
			}
		}
	}
	*at = c.convert(re.At)
}

// duration returns the duration value, its text and true if the duration depends on the query step.
func (c *converter) duration(d *mql.DurationExpr) (time.Duration, string, bool) {
	text := string(d.AppendString(nil))
	if strings.Contains(text, "i") {
		return 0, text, true
	}
	return time.Duration(d.Duration(0)) * time.Millisecond, text, false
}

func (c *converter) convertFuncExpr(fe *mql.FuncExpr) ast.Node {
	args := make([]ast.Node, 0, len(fe.Args))
	for i, arg := range fe.Args {
		if isPromQLMatrixArg(fe.Name, i, len(fe.Args)) {
			args = append(args, c.convertRollupArg(arg))
		} else {
			args = append(args, c.convert(arg))
		}
	}
	return &ast.Call{
		Func:            function(fe.Name, args),
		Args:            args,
		KeepMetricNames: fe.KeepMetricNames,
		PosRange:        toPosRange(fe.Pos),
	}
}

func (c *converter) convertAggrFuncExpr(ae *mql.AggrFuncExpr) ast.Node {
	name := strings.ToLower(ae.Name)
	args := make([]ast.Node, 0, len(ae.Args))
	for _, arg := range ae.Args {
		args = append(args, c.convert(arg))
	}

	agg := &ast.AggregateExpr{
		Op:       ast.Op(name),
		Limit:    ae.Limit,
		PosRange: toPosRange(ae.Pos),
	}
	switch strings.ToLower(ae.Modifier.Op) {
	case "by":
		agg.Grouping = nonNilStrings(ae.Modifier.Args)
	case "without":
		agg.Grouping = nonNilStrings(ae.Modifier.Args)
		agg.Without = true
	}

	sig, ok := aggrFunctions[name]
	if !ok {
		sig = aggrSignature{exprIdx: 0, paramIdx: -1}
	}
	exprIdx := sig.exprIdx
	if exprIdx < 0 {
		exprIdx = len(args) + exprIdx
	}
	if exprIdx >= 0 && exprIdx < len(args) {
		agg.Expr = args[exprIdx]
	}
	if sig.paramIdx >= 0 && sig.paramIdx < len(args) && sig.paramIdx != exprIdx {
		agg.Param = args[sig.paramIdx]
	}
	if !sig.isPromQLShaped(len(args)) {
		agg.Args = args
	}
	if agg.Expr == nil {
		c.fail(ae.Pos, "missing series argument in call to %s()", name)
		agg.Expr = &ast.VectorSelector{PosRange: toPosRange(ae.Pos)}
	}
	return agg
}

func (c *converter) convertBinaryOpExpr(be *mql.BinaryOpExpr) ast.Node {
	if be.IsUnaryMinus() {
		return &ast.UnaryExpr{
			Op:       ast.OpSub,
			Expr:     c.convert(be.Right),
			PosRange: toPosRange(be.Pos),
		}
	}

	lhs := c.convert(be.Left)
	rhs := c.convert(be.Right)
	op := ast.Op(strings.ToLower(be.Op))
	bin := &ast.BinaryExpr{
		Op:              op,
		LHS:             lhs,
		RHS:             rhs,
		ReturnBool:      be.Bool,
		KeepMetricNames: be.KeepMetricNames,
		PosRange:        toPosRange(be.Pos),
	}

	isSetOp := op.IsSetOperator() || op == ast.OpIf || op == ast.OpIfNot || op == ast.OpDefault
	if !isSetOp && (lhs.Type() != ast.ValueTypeVector || rhs.Type() != ast.ValueTypeVector) {
		return bin
	}

	vm := &ast.VectorMatching{Card: ast.CardOneToOne}
	switch strings.ToLower(be.GroupModifier.Op) {
	case "on":
		vm.On = true
		vm.MatchingLabels = nonNilStrings(be.GroupModifier.Args)
	case "ignoring":
		vm.MatchingLabels = nonNilStrings(be.GroupModifier.Args)
	}
	switch strings.ToLower(be.JoinModifier.Op) {
	case "group_left":
		vm.Card = ast.CardManyToOne
		vm.Include = nonNilStrings(be.JoinModifier.Args)
	case "group_right":
		vm.Card = ast.CardOneToMany
		vm.Include = nonNilStrings(be.JoinModifier.Args)
	}
	if be.JoinModifierPrefix != nil {
		vm.Prefix = be.JoinModifierPrefix.S
	}
	if be.FillLeft != nil {
		v := be.FillLeft.N
		vm.FillValues.LHS = &v
	}
	if be.FillRight != nil {
		v := be.FillRight.N
		vm.FillValues.RHS = &v
	}
	if isSetOp {
		vm.Card = ast.CardManyToMany
	}
	bin.VectorMatching = vm
	return bin
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toPosRange(s mql.Span) ast.PositionRange {
	return ast.PositionRange{Start: ast.Pos(s.Start), End: ast.Pos(s.End)}
}
