package ast

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
)

// The printer follows the formatting rules of the Prometheus PromQL printer,
// so PromQL queries are printed exactly as Prometheus would print them.
// MetricsQL specific syntax is only emitted when MetricsQL only fields are set.

func (n *VectorSelector) String() string {
	var b strings.Builder
	b.WriteString(n.Name)
	groups := make([]string, 0, len(n.OrMatchers)+1)
	for _, matchers := range n.MatcherGroups() {
		labelStrings := make([]string, 0, len(matchers))
		for _, matcher := range matchers {
			if matcher.Name == model.MetricNameLabel && matcher.Type == labels.MatchEqual && matcher.Value == n.Name && matcher.Value != "" {
				continue
			}
			labelStrings = append(labelStrings, matcher.String())
		}
		if len(labelStrings) > 0 {
			slices.Sort(labelStrings)
			groups = append(groups, strings.Join(labelStrings, ","))
		}
	}
	if len(groups) > 0 {
		b.WriteByte('{')
		b.WriteString(strings.Join(groups, " or "))
		b.WriteByte('}')
	}
	b.WriteString(formatAt(n.Timestamp, n.StartOrEnd, n.At))
	switch {
	case n.Anchored:
		b.WriteString(" anchored")
	case n.Smoothed:
		b.WriteString(" smoothed")
	}
	b.WriteString(formatOffset(n.OffsetText, n.OriginalOffsetExpr, n.OriginalOffset))
	return b.String()
}

func (n *MatrixSelector) String() string {
	vs, ok := n.VectorSelector.(*VectorSelector)
	if !ok {
		return n.VectorSelector.String()
	}
	at := formatAt(vs.Timestamp, vs.StartOrEnd, vs.At)
	offset := formatOffset(vs.OffsetText, vs.OriginalOffsetExpr, vs.OriginalOffset)

	// Copy the vector selector so we can modify it to not print @, offset, and other modifiers twice.
	vecSelector := *vs
	vecSelector.OriginalOffset = 0
	vecSelector.OriginalOffsetExpr = nil
	vecSelector.OffsetText = ""
	vecSelector.Timestamp = nil
	vecSelector.StartOrEnd = ""
	vecSelector.At = nil
	vecSelector.Anchored = false
	vecSelector.Smoothed = false

	var extendedAttribute string
	switch {
	case vs.Anchored:
		extendedAttribute = " anchored"
	case vs.Smoothed:
		extendedAttribute = " smoothed"
	}
	if n.Implicit {
		return vecSelector.String() + at + offset
	}
	return fmt.Sprintf("%s[%s]%s%s%s", vecSelector.String(), formatRange(n.RangeText, n.RangeExpr, n.Range), extendedAttribute, at, offset)
}

func (n *SubqueryExpr) String() string {
	var b strings.Builder
	b.WriteString(n.Expr.String())
	if !n.Implicit {
		var step string
		switch {
		case n.StepText != "":
			step = n.StepText
		case n.Step != 0:
			step = model.Duration(n.Step).String()
		case n.StepExpr != nil:
			step = n.StepExpr.String()
		}
		b.WriteByte('[')
		b.WriteString(formatRange(n.RangeText, n.RangeExpr, n.Range))
		b.WriteByte(':')
		b.WriteString(step)
		b.WriteByte(']')
	}
	b.WriteString(formatAt(n.Timestamp, n.StartOrEnd, n.At))
	b.WriteString(formatOffset(n.OffsetText, n.OriginalOffsetExpr, n.OriginalOffset))
	return b.String()
}

func (n *OffsetExpr) String() string {
	expr := n.Expr.String()
	switch n.Expr.(type) {
	case *BinaryExpr, *UnaryExpr:
		expr = "(" + expr + ")"
	}
	return expr + formatAt(n.Timestamp, n.StartOrEnd, n.At) + formatOffset(n.OffsetText, nil, n.OriginalOffset)
}

func (n *AggregateExpr) String() string {
	var b strings.Builder
	b.WriteString(n.Op.String())
	switch {
	case n.Without:
		b.WriteString(" without (")
		writeLabels(&b, n.Grouping)
		b.WriteString(") ")
	case len(n.Grouping) > 0:
		b.WriteString(" by (")
		writeLabels(&b, n.Grouping)
		b.WriteString(") ")
	}
	b.WriteByte('(')
	switch {
	case len(n.Args) > 0:
		writeNodes(&b, n.Args)
	case n.Param != nil:
		b.WriteString(n.Param.String())
		b.WriteString(", ")
		b.WriteString(n.Expr.String())
	case n.Expr != nil:
		b.WriteString(n.Expr.String())
	}
	b.WriteByte(')')
	if n.Limit > 0 {
		b.WriteString(" limit ")
		b.WriteString(strconv.Itoa(n.Limit))
	}
	return b.String()
}

func (n *BinaryExpr) String() string {
	var returnBool string
	if n.ReturnBool {
		returnBool = " bool"
	}
	s := n.LHS.String() + " " + n.Op.String() + returnBool + n.matchingString() + " " + n.RHS.String()
	if n.KeepMetricNames {
		return "(" + s + ") keep_metric_names"
	}
	return s
}

func (n *BinaryExpr) matchingString() string {
	vm := n.VectorMatching
	if vm == nil {
		return ""
	}

	var b strings.Builder
	if len(vm.MatchingLabels) > 0 || vm.On || vm.Card == CardManyToOne || vm.Card == CardOneToMany {
		if vm.On {
			b.WriteString(" on (")
		} else {
			b.WriteString(" ignoring (")
		}
		writeLabels(&b, vm.MatchingLabels)
		b.WriteByte(')')
	}

	if vm.Card == CardManyToOne || vm.Card == CardOneToMany {
		if vm.Card == CardManyToOne {
			b.WriteString(" group_left (")
		} else {
			b.WriteString(" group_right (")
		}
		writeLabels(&b, vm.Include)
		b.WriteByte(')')
		if vm.Prefix != "" {
			b.WriteString(" prefix ")
			b.WriteString(strconv.Quote(vm.Prefix))
		}
	}

	if vm.FillValues.LHS != nil || vm.FillValues.RHS != nil {
		if vm.FillValues.LHS != nil && vm.FillValues.RHS != nil && *vm.FillValues.LHS == *vm.FillValues.RHS {
			_, _ = fmt.Fprintf(&b, " fill (%v)", *vm.FillValues.LHS)
		} else {
			if vm.FillValues.LHS != nil {
				_, _ = fmt.Fprintf(&b, " fill_left (%v)", *vm.FillValues.LHS)
			}
			if vm.FillValues.RHS != nil {
				_, _ = fmt.Fprintf(&b, " fill_right (%v)", *vm.FillValues.RHS)
			}
		}
	}
	return b.String()
}

func (n *Call) String() string {
	var b strings.Builder
	b.WriteString(n.Func.Name)
	b.WriteByte('(')
	writeNodes(&b, n.Args)
	b.WriteByte(')')
	if n.KeepMetricNames {
		b.WriteString(" keep_metric_names")
	}
	return b.String()
}

func (n *NumberLiteral) String() string {
	if n.Duration {
		if n.Val < 0 {
			return "-" + model.Duration(-n.Val*1e9).String()
		}
		return model.Duration(n.Val * 1e9).String()
	}
	return strconv.FormatFloat(n.Val, 'f', -1, 64)
}

func (n *StringLiteral) String() string {
	return strconv.Quote(n.Val)
}

func (n *ParenExpr) String() string {
	return "(" + n.Expr.String() + ")"
}

func (n *UnaryExpr) String() string {
	return n.Op.String() + n.Expr.String()
}

func (n *DurationExpr) String() string {
	var b strings.Builder
	if n.Wrapped {
		b.WriteByte('(')
	}
	switch {
	case n.Op == OpStep:
		b.WriteString("step()")
	case n.Op == OpRange:
		b.WriteString("range()")
	case n.Op == OpMinOf, n.Op == OpMaxOf:
		b.WriteString(n.Op.String())
		b.WriteByte('(')
		b.WriteString(n.LHS.String())
		b.WriteString(", ")
		b.WriteString(n.RHS.String())
		b.WriteByte(')')
	case n.LHS == nil:
		if n.Op == OpSub {
			b.WriteString(n.Op.String())
		}
		b.WriteString(n.RHS.String())
	default:
		b.WriteString(n.LHS.String())
		b.WriteByte(' ')
		b.WriteString(n.Op.String())
		b.WriteByte(' ')
		b.WriteString(n.RHS.String())
	}
	if n.Wrapped {
		b.WriteByte(')')
	}
	return b.String()
}

func writeNodes(b *strings.Builder, nodes []Node) {
	for i, n := range nodes {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(n.String())
	}
}

func writeLabels(b *strings.Builder, ss []string) {
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		if !model.LegacyValidation.IsValidLabelName(s) {
			b.WriteString(strconv.Quote(s))
		} else {
			b.WriteString(s)
		}
	}
}

func formatRange(text string, expr *DurationExpr, d time.Duration) string {
	switch {
	case text != "":
		return text
	case expr != nil:
		return expr.String()
	default:
		return model.Duration(d).String()
	}
}

func formatAt(ts *int64, startOrEnd Op, at Node) string {
	switch {
	case ts != nil:
		return fmt.Sprintf(" @ %.3f", float64(*ts)/1000.0)
	case startOrEnd == OpStart:
		return " @ start()"
	case startOrEnd == OpEnd:
		return " @ end()"
	case at != nil:
		return " @ " + at.String()
	default:
		return ""
	}
}

func formatOffset(text string, expr *DurationExpr, d time.Duration) string {
	switch {
	case text != "":
		return " offset " + text
	case expr != nil:
		return " offset " + expr.String()
	case d > 0:
		return " offset " + model.Duration(d).String()
	case d < 0:
		return " offset -" + model.Duration(-d).String()
	default:
		return ""
	}
}
