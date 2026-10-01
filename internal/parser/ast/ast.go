// Package ast defines a query language neutral syntax tree used by pint.
// Both PromQL and MetricsQL queries are converted into this representation,
// so checks can analyse queries without depending on a specific parser.
// Node types intentionally mirror the Prometheus PromQL AST, with extra fields
// for MetricsQL-only features.
package ast

import (
	"time"

	"github.com/prometheus/prometheus/model/labels"
)

// Pos is a byte offset within the query text.
type Pos int

// PositionRange describes a [Start, End) range of byte offsets within the query text.
type PositionRange struct {
	Start Pos
	End   Pos
}

type ValueType string

const (
	ValueTypeNone   ValueType = "none"
	ValueTypeVector ValueType = "vector"
	ValueTypeScalar ValueType = "scalar"
	ValueTypeMatrix ValueType = "matrix"
	ValueTypeString ValueType = "string"
)

// Node is a single element of the parsed query.
type Node interface {
	// String returns the query text for this node.
	String() string
	// PositionRange returns the position of this node within the original query.
	PositionRange() PositionRange
	// Type returns the type of value this node evaluates to.
	Type() ValueType
	node()
}

type VectorMatchCardinality int

const (
	CardOneToOne VectorMatchCardinality = iota
	CardManyToOne
	CardOneToMany
	CardManyToMany
)

func (vmc VectorMatchCardinality) String() string {
	switch vmc {
	case CardOneToOne:
		return "one-to-one"
	case CardManyToOne:
		return "many-to-one"
	case CardOneToMany:
		return "one-to-many"
	case CardManyToMany:
		return "many-to-many"
	}
	panic("promql.VectorMatchCardinality.String: unknown match cardinality")
}

// FillValues are the values used by fill(), fill_left() and fill_right() modifiers.
type FillValues struct {
	LHS *float64
	RHS *float64
}

// VectorMatching describes how elements from two vectors in a binary operation are matched.
type VectorMatching struct {
	FillValues FillValues
	// Prefix is the MetricsQL group_left(...) prefix "..." value.
	Prefix         string
	MatchingLabels []string
	Include        []string
	Card           VectorMatchCardinality
	On             bool
}

// Function describes a function that can be called in a query.
type Function struct {
	Name         string
	ReturnType   ValueType
	ArgTypes     []ValueType
	Variadic     int
	Experimental bool
}

// ArgType returns the type of the argument at given index.
func (f *Function) ArgType(idx int) ValueType {
	if len(f.ArgTypes) == 0 {
		return ValueTypeNone
	}
	if idx >= len(f.ArgTypes) {
		return f.ArgTypes[len(f.ArgTypes)-1]
	}
	return f.ArgTypes[idx]
}

// VectorSelector selects time series by metric name and label matchers.
type VectorSelector struct {
	// At is the MetricsQL `@` modifier value when it's not a number or start()/end().
	At        Node
	Timestamp *int64
	// OriginalOffsetExpr is set for PromQL duration expressions used as offsets.
	OriginalOffsetExpr *DurationExpr
	Name               string
	// OffsetText is the MetricsQL offset text, used when the offset is relative to the query step.
	OffsetText string
	StartOrEnd Op
	// LabelMatchers is the first (and for PromQL the only) group of label matchers.
	LabelMatchers []*labels.Matcher
	// OrMatchers are additional groups of MetricsQL `{a="1" or b="2"}` label matchers.
	OrMatchers     [][]*labels.Matcher
	PosRange       PositionRange
	OriginalOffset time.Duration
	Offset         time.Duration
	Anchored       bool
	Smoothed       bool
}

// MatcherGroups returns all groups of label matchers used by this selector.
// A time series is selected if it matches any group.
func (vs *VectorSelector) MatcherGroups() [][]*labels.Matcher {
	groups := make([][]*labels.Matcher, 0, len(vs.OrMatchers)+1)
	groups = append(groups, vs.LabelMatchers)
	return append(groups, vs.OrMatchers...)
}

// AllMatchers returns label matchers from all groups.
func (vs *VectorSelector) AllMatchers() []*labels.Matcher {
	if len(vs.OrMatchers) == 0 {
		return vs.LabelMatchers
	}
	all := make([]*labels.Matcher, 0, len(vs.LabelMatchers)*(len(vs.OrMatchers)+1))
	for _, g := range vs.MatcherGroups() {
		all = append(all, g...)
	}
	return all
}

// MatrixSelector selects a range of samples for each time series.
type MatrixSelector struct {
	VectorSelector Node
	RangeExpr      *DurationExpr
	// RangeText is the MetricsQL window text, used when the window is relative to the query step.
	RangeText string
	PosRange  PositionRange
	Range     time.Duration
	// Implicit is true for MetricsQL rollups without a lookbehind window, like rate(foo).
	Implicit bool
	// StepRelative is true when the range depends on the query step and so it's unknown.
	StepRelative bool
}

// SubqueryExpr evaluates an expression over a range of time.
type SubqueryExpr struct {
	Expr               Node
	At                 Node
	Timestamp          *int64
	RangeExpr          *DurationExpr
	StepExpr           *DurationExpr
	OriginalOffsetExpr *DurationExpr
	StartOrEnd         Op
	RangeText          string
	StepText           string
	OffsetText         string
	PosRange           PositionRange
	Range              time.Duration
	Step               time.Duration
	OriginalOffset     time.Duration
	Offset             time.Duration
	// Implicit is true for MetricsQL rollups over expressions without a lookbehind window, like rate(sum(foo)).
	Implicit bool
	// StepRelative is true when the range depends on the query step and so it's unknown.
	StepRelative bool
}

// OffsetExpr is a MetricsQL offset or `@` modifier applied to an arbitrary expression,
// for example `sum(foo) offset 5m`.
type OffsetExpr struct {
	Expr           Node
	At             Node
	Timestamp      *int64
	StartOrEnd     Op
	OffsetText     string
	PosRange       PositionRange
	OriginalOffset time.Duration
	Offset         time.Duration
}

// AggregateExpr is an aggregation operation on a vector.
type AggregateExpr struct {
	Expr  Node
	Param Node
	// Args lists all MetricsQL aggregation arguments in the order they were written.
	// It's only set for aggregations that don't map onto the PromQL (param, expr) form.
	Args     []Node
	Op       Op
	Grouping []string
	PosRange PositionRange
	// Limit is the MetricsQL `limit N` modifier value.
	Limit   int
	Without bool
}

// BinaryExpr is a binary operation between two expressions.
type BinaryExpr struct {
	LHS            Node
	RHS            Node
	VectorMatching *VectorMatching
	Op             Op
	PosRange       PositionRange
	ReturnBool     bool
	// KeepMetricNames is the MetricsQL keep_metric_names modifier.
	KeepMetricNames bool
}

// Call is a function call.
type Call struct {
	Func     *Function
	Args     []Node
	PosRange PositionRange
	// KeepMetricNames is the MetricsQL keep_metric_names modifier.
	KeepMetricNames bool
}

type NumberLiteral struct {
	PosRange PositionRange
	Val      float64
	// Duration is set when the number was written as a duration.
	Duration bool
}

type StringLiteral struct {
	Val      string
	PosRange PositionRange
}

type ParenExpr struct {
	Expr     Node
	PosRange PositionRange
}

type UnaryExpr struct {
	Expr     Node
	Op       Op
	PosRange PositionRange
}

// DurationExpr is a PromQL duration expression, like `foo[5m + 1m]`.
type DurationExpr struct {
	LHS      Node
	RHS      Node
	Op       Op
	PosRange PositionRange
	Wrapped  bool
}

func (*VectorSelector) node() {}
func (*MatrixSelector) node() {}
func (*SubqueryExpr) node()   {}
func (*OffsetExpr) node()     {}
func (*AggregateExpr) node()  {}
func (*BinaryExpr) node()     {}
func (*Call) node()           {}
func (*NumberLiteral) node()  {}
func (*StringLiteral) node()  {}
func (*ParenExpr) node()      {}
func (*UnaryExpr) node()      {}
func (*DurationExpr) node()   {}

func (vs *VectorSelector) PositionRange() PositionRange { return vs.PosRange }
func (n *MatrixSelector) PositionRange() PositionRange  { return n.PosRange }
func (n *SubqueryExpr) PositionRange() PositionRange    { return n.PosRange }
func (n *OffsetExpr) PositionRange() PositionRange      { return n.PosRange }
func (n *AggregateExpr) PositionRange() PositionRange   { return n.PosRange }
func (n *BinaryExpr) PositionRange() PositionRange      { return n.PosRange }
func (n *Call) PositionRange() PositionRange            { return n.PosRange }
func (n *NumberLiteral) PositionRange() PositionRange   { return n.PosRange }
func (n *StringLiteral) PositionRange() PositionRange   { return n.PosRange }
func (n *ParenExpr) PositionRange() PositionRange       { return n.PosRange }
func (n *UnaryExpr) PositionRange() PositionRange       { return n.PosRange }
func (n *DurationExpr) PositionRange() PositionRange    { return n.PosRange }

func (*VectorSelector) Type() ValueType { return ValueTypeVector }
func (*MatrixSelector) Type() ValueType { return ValueTypeMatrix }
func (*SubqueryExpr) Type() ValueType   { return ValueTypeMatrix }
func (n *OffsetExpr) Type() ValueType   { return n.Expr.Type() }
func (*AggregateExpr) Type() ValueType  { return ValueTypeVector }
func (n *BinaryExpr) Type() ValueType {
	if n.LHS.Type() == ValueTypeScalar && n.RHS.Type() == ValueTypeScalar {
		return ValueTypeScalar
	}
	return ValueTypeVector
}
func (n *Call) Type() ValueType        { return n.Func.ReturnType }
func (*NumberLiteral) Type() ValueType { return ValueTypeScalar }
func (*StringLiteral) Type() ValueType { return ValueTypeString }
func (n *ParenExpr) Type() ValueType   { return n.Expr.Type() }
func (n *UnaryExpr) Type() ValueType   { return n.Expr.Type() }
func (*DurationExpr) Type() ValueType  { return ValueTypeScalar }

// Children returns all child nodes of given node, in the same order
// Prometheus PromQL parser would return them.
// Duration expressions are not included.
func Children(node Node) []Node {
	switch n := node.(type) {
	case *AggregateExpr:
		nodes := make([]Node, 0, 2+len(n.Args))
		if n.Expr != nil {
			nodes = append(nodes, n.Expr)
		}
		if n.Param != nil {
			nodes = append(nodes, n.Param)
		}
		for _, arg := range n.Args {
			if arg != n.Expr && arg != n.Param {
				nodes = append(nodes, arg)
			}
		}
		return nodes
	case *BinaryExpr:
		return []Node{n.LHS, n.RHS}
	case *Call:
		return n.Args
	case *SubqueryExpr:
		return appendAt([]Node{n.Expr}, n.At)
	case *OffsetExpr:
		return appendAt([]Node{n.Expr}, n.At)
	case *ParenExpr:
		return []Node{n.Expr}
	case *UnaryExpr:
		return []Node{n.Expr}
	case *MatrixSelector:
		return []Node{n.VectorSelector}
	case *VectorSelector:
		return appendAt(nil, n.At)
	default:
		return nil
	}
}

func appendAt(nodes []Node, at Node) []Node {
	if at != nil {
		return append(nodes, at)
	}
	return nodes
}

// Inspect traverses the tree in depth-first order, calling fn for each node.
// Children of a node are not visited if fn returns false.
func Inspect(node Node, fn func(Node) bool) {
	if node == nil || !fn(node) {
		return
	}
	for _, child := range Children(node) {
		Inspect(child, fn)
	}
}
