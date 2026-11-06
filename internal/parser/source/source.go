package source

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/cloudflare/pint/internal/parser/ast"
)

var guaranteedLabelsMatches = []labels.MatchType{labels.MatchEqual, labels.MatchRegexp}

type Type uint8

const (
	UnknownSource Type = iota
	NumberSource
	StringSource
	SelectorSource
	FuncSource
	AggregateSource
)

type RangeSelectorMode uint8

const (
	RangeSelectorDefault  RangeSelectorMode = iota
	RangeSelectorAnchored                   // VectorSelector uses the anchored modifier.
	RangeSelectorSmoothed                   // VectorSelector uses the smoothed modifier.
)

const (
	FeatureExperimentalFunctions  = "promql-experimental-functions"
	FeatureDurationExpr           = "promql-duration-expr"
	FeatureExtendedRangeSelectors = "promql-extended-range-selectors"
	FeatureBinopFillModifiers     = "promql-binop-fill-modifiers"
)

// Used for test snapshots.
func (st Type) MarshalYAML() (any, error) {
	var name string
	switch st { // nolint: exhaustive
	case NumberSource:
		name = "number"
	case StringSource:
		name = "string"
	case SelectorSource:
		name = "selector"
	case FuncSource:
		name = "function"
	case AggregateSource:
		name = "aggregation"
	}
	return name, nil
}

// Used for test snapshots.
func (rsm RangeSelectorMode) MarshalYAML() (any, error) {
	switch rsm { // nolint: exhaustive
	case RangeSelectorAnchored:
		return "anchored", nil
	case RangeSelectorSmoothed:
		return "smoothed", nil
	}
	return "default", nil
}

type LabelPromiseType uint8

const (
	ImpossibleLabel LabelPromiseType = iota
	PossibleLabel
	GuaranteedLabel
)

// Used for test snapshots.
func (lpt LabelPromiseType) MarshalYAML() (any, error) {
	var name string
	switch lpt {
	case ImpossibleLabel:
		name = "excluded"
	case PossibleLabel:
		name = "included"
	case GuaranteedLabel:
		name = "guaranteed"
	}
	return name, nil
}

type LabelTransform struct {
	Reason   string
	Kind     LabelPromiseType
	Fragment ast.PositionRange
}

type DeadInfo struct {
	Reason   string
	Fragment ast.PositionRange
}

type DeadLabelKind uint8

const (
	ImpossibleDeadLabel DeadLabelKind = iota
	OrphanedLabel
	DuplicatedJoin
	UnusedLabel
)

func (dlk DeadLabelKind) String() string {
	switch dlk {
	case ImpossibleDeadLabel:
		return "impossible label"
	case OrphanedLabel:
		return "orphaned label"
	case DuplicatedJoin:
		return "redundant label"
	case UnusedLabel:
		return "unused label"
	}
	return "unknown"
}

type DeadLabel struct {
	Name          string
	Reason        string
	LabelReason   string
	UsageFragment ast.PositionRange
	LabelFragment ast.PositionRange
	Kind          DeadLabelKind
}

type ReturnInfo struct {
	LogicalExpr    string
	ValuePosition  ast.PositionRange
	ReturnedNumber float64 // If AlwaysReturns=true this is the number that's returned
	AlwaysReturns  bool    // True if this source always returns results.
	KnownReturn    bool    // True if we always know the return value.
	IsReturnBool   bool    // True if this source uses the 'bool' modifier.
}

type Condition struct {
	Op         ast.Op  `yaml:"op,omitempty"`
	Value      float64 `yaml:"value,omitempty"`
	Present    bool    `yaml:"present,omitempty"`
	KnownValue bool    `yaml:"knownValue,omitempty"`
}

type Operation struct {
	Node      ast.Node
	Operation string
	Arguments []string
}

// Used for test snapshots.
func (so Operation) MarshalYAML() (any, error) {
	y := map[string]any{
		"op":   so.Operation,
		"node": fmt.Sprintf("[%T] %s", so.Node, so.Node.String()),
	}
	if so.Arguments != nil {
		y["args"] = so.Arguments
	}
	return y, nil
}

type Operations []Operation

func MostOuterOperation[T ast.Node](s *Source) (T, bool) {
	for _, op := range slices.Backward(s.Operations) {
		if o, ok := op.Node.(T); ok {
			return o, true
		}
	}
	return *new(T), false
}

type Fill struct {
	LHS float64
	RHS float64
}

type Join struct {
	Fill           *Fill // Fill values for unmatched series (binop fill modifiers).
	DeadInfo       *DeadInfo
	Src            *Source     // The source we're joining with.
	Op             ast.Op      // The binary operation used for this join.
	DeadLabels     []DeadLabel // Per-pairing dead labels from checkJoinedLabels/checkIncludedLabels.
	MatchingLabels []string
	AddedLabels    []string
	Depth          int // Zero if this is a direct join, non-zero otherwise. sum(foo * bar) would be in-direct join.
	IsOn           bool
}

// Used for test snapshots.
func (j Join) MarshalYAML() (any, error) {
	m := map[string]any{
		"matchinglabels": j.MatchingLabels,
		"addedlabels":    j.AddedLabels,
		"src":            j.Src,
		"op":             j.Op.String(),
		"depth":          j.Depth,
		"ison":           j.IsOn,
	}
	if j.Fill != nil {
		m["fill"] = j.Fill
	}
	if j.DeadInfo != nil {
		m["deadinfo"] = j.DeadInfo
	}
	if len(j.DeadLabels) > 0 {
		m["deadlabels"] = j.DeadLabels
	}
	return m, nil
}

// Used for test snapshots.
func (u Unless) MarshalYAML() (any, error) {
	m := map[string]any{
		"matchinglabels": u.MatchingLabels,
		"src":            u.Src,
		"ison":           u.IsOn,
	}
	if u.DeadInfo != nil {
		m["deadinfo"] = u.DeadInfo
	}
	return m, nil
}

type Unless struct {
	DeadInfo       *DeadInfo
	Src            *Source
	MatchingLabels []string
	IsOn           bool
}

type Side uint8

const (
	LHS Side = iota
	RHS
)

type Indirect struct {
	Src  *Source `yaml:"src"`
	Op   ast.Op  `yaml:"op"`
	Side Side    `yaml:"side"`
}

type FeatureRequirement struct {
	Feature   string              `yaml:"feature"`
	Name      string              `yaml:"name"`
	Fragments []ast.PositionRange `yaml:"fragments"`
}

type Source struct {
	Labels     map[string]LabelTransform `yaml:"labels,omitempty"`
	DeadInfo   *DeadInfo                 `yaml:"deadInfo,omitempty"`
	Returns    ast.ValueType             `yaml:"returns"`
	DeadLabels []DeadLabel               `yaml:"deadLabels,omitempty"`
	Operations Operations                `yaml:"operations,omitempty"`
	// Any other sources this source joins with.
	Joins []Join `yaml:"joins,omitempty"`
	// Any other sources this source is suppressed by.
	Unless []Unless `yaml:"unless,omitempty"`
	// Any other sources used indirectly, not contributing labels.
	Indirect      []Indirect           `yaml:"indirect,omitempty"`
	NeedsFeatures []FeatureRequirement `yaml:"needsFeatures,omitempty"`
	UsedLabels    []string             `yaml:"usedLabels,omitempty"`
	Condition     Condition            `yaml:"condition,omitempty"`
	ReturnInfo    ReturnInfo           `yaml:"returnInfo,omitempty"`
	Position      ast.PositionRange    `yaml:"position"`
	Type          Type                 `yaml:"type"`
	// Labels are fixed and only allowed labels can be present.
	FixedLabels       bool              `yaml:"fixedLabels,omitempty"`
	RangeSelectorMode RangeSelectorMode `yaml:"rangeSelectorMode,omitempty"`
}

// requireFeature is called for every PromQL function, aggregation, and modifier.
// It looks up the element name in the feature version map to determine whether
// a feature flag is needed. If the name is not in the map then it's a standard
// PromQL element that doesn't require any feature flag, so we skip it.
// When a match is found, the query position is recorded so that diagnostics
// can later highlight the exact fragment that requires the flag.
func (s *Source) requireFeature(name string, pos ast.PositionRange) {
	// Only elements listed in the feature version map require a feature flag.
	// Everything else is standard PromQL and can be ignored.
	fv, ok := LookupFeatureVersion(name)
	if !ok {
		return
	}
	// If we already recorded a requirement for the same feature+name pair,
	// just append the new position fragment to avoid duplicate entries.
	for i, req := range s.NeedsFeatures {
		if req.Feature == fv.Flag && req.Name == name {
			s.NeedsFeatures[i].Fragments = append(s.NeedsFeatures[i].Fragments, pos)
			return
		}
	}
	s.NeedsFeatures = append(s.NeedsFeatures, FeatureRequirement{
		Feature:   fv.Flag,
		Name:      name,
		Fragments: []ast.PositionRange{pos},
	})
}

func (s *Source) clone() *Source {
	c := new(Source)
	*c = *s
	return c
}

func (s Source) Operation() string {
	if len(s.Operations) > 0 {
		return s.Operations[len(s.Operations)-1].Operation
	}
	return ""
}

func (s Source) CanHaveLabel(name string) bool {
	if v, ok := s.Labels[name]; ok {
		if v.Kind == ImpossibleLabel {
			return false
		}
		if v.Kind == PossibleLabel || v.Kind == GuaranteedLabel {
			return true
		}
	}
	return !s.FixedLabels
}

func (s Source) TransformedLabels(kinds ...LabelPromiseType) []string {
	names := make([]string, 0, len(s.Labels))
	for name, l := range s.Labels {
		if slices.Contains(kinds, l.Kind) {
			names = append(names, name)
		}
	}
	return names
}

func (s Source) LabelExcludeReason(name string) (string, ast.PositionRange) {
	if l, ok := s.Labels[name]; ok && l.Kind == ImpossibleLabel {
		return l.Reason, l.Fragment
	}
	return s.Labels[""].Reason, s.Labels[""].Fragment
}

func (s *Source) excludeAllLabels(expr, reason string, fragment, allFragment ast.PositionRange, except []string) {
	// Everything that was included until now but will be removed needs an explicit stamp to mark it as gone.
	for name, l := range s.Labels {
		if slices.Contains(except, name) {
			continue
		}
		if l.Kind == PossibleLabel || l.Kind == GuaranteedLabel {
			s.Labels[name] = LabelTransform{
				Kind:     ImpossibleLabel,
				Reason:   reason,
				Fragment: fragment,
			}
		}
	}
	s.UsedLabels = slices.DeleteFunc(s.UsedLabels, func(name string) bool {
		return !slices.Contains(except, name)
	})
	// Mark except labels as possible, unless they are already guaranteed.
	s.UsedLabels = appendToSlice(s.UsedLabels, except...)
	for _, name := range except {
		if l, ok := s.Labels[name]; ok && l.Kind == GuaranteedLabel {
			continue
		}

		// We have grouping labels set, if they are possible mark them as such, if not mark as impossible.
		if s.CanHaveLabel(name) {
			s.Labels[name] = LabelTransform{
				Kind:     PossibleLabel,
				Reason:   reason,
				Fragment: findArgumentPosition(expr, fragment, name),
			}
		} else {
			r, f := s.LabelExcludeReason(name)
			s.Labels[name] = LabelTransform{
				Kind:     ImpossibleLabel,
				Reason:   r,
				Fragment: f,
			}
		}

	}
	s.Labels[""] = LabelTransform{
		Kind:     ImpossibleLabel,
		Reason:   reason,
		Fragment: allFragment,
	}
	s.FixedLabels = true
}

func (s *Source) excludeLabel(reason string, fragment ast.PositionRange, name string) {
	s.Labels[name] = LabelTransform{
		Kind:     ImpossibleLabel,
		Reason:   reason,
		Fragment: fragment,
	}
	s.UsedLabels = slices.DeleteFunc(s.UsedLabels, func(s string) bool {
		return s == name
	})
}

func (s *Source) joinLabels(expr string, within ast.PositionRange, op ast.Op, names []string, outside []ast.PositionRange) {
	for _, name := range names {
		if l, ok := s.Labels[name]; ok && l.Kind == GuaranteedLabel {
			s.DeadLabels = append(s.DeadLabels, DeadLabel{
				Kind:   DuplicatedJoin,
				Name:   name,
				Reason: "Query is trying to join the `" + name + "` label that is already present on the other side of the query.",
				UsageFragment: findArgumentPosition(
					expr,
					FindFuncPosition(expr, within, op.String(), outside),
					name,
				),
				LabelReason:   l.Reason,
				LabelFragment: l.Fragment,
			})
			return
		}
		s.Labels[name] = LabelTransform{
			Kind: PossibleLabel,
			Reason: fmt.Sprintf(
				"Query is using `%s(%s)`, all labels included inside `%s(...)` will be joined to the results on the other side of the query.",
				op.String(), strings.Join(names, ", "), op.String(),
			),
			Fragment: findArgumentPosition(
				expr,
				FindFuncPosition(expr, within, op.String(), outside),
				name,
			),
		}
	}
}

func (s *Source) includeLabel(expr, reason string, fragment ast.PositionRange, name string) {
	if l, ok := s.Labels[name]; ok && l.Kind == GuaranteedLabel {
		return
	}
	s.Labels[name] = LabelTransform{
		Kind:     PossibleLabel,
		Reason:   reason,
		Fragment: findArgumentPosition(expr, fragment, name),
	}
}

func (s *Source) guaranteeLabel(reason string, fragment ast.PositionRange, names ...string) {
	for _, name := range names {
		s.Labels[name] = LabelTransform{
			Kind:     GuaranteedLabel,
			Reason:   reason,
			Fragment: fragment,
		}
	}
}

func findDeadIncludedLabels(s *Source, expr string, pos ast.PositionRange, names []string) []DeadLabel {
	var dead []DeadLabel
	for _, name := range names {
		if !s.CanHaveLabel(name) {
			reason, fragment := s.LabelExcludeReason(name)
			dead = append(dead, DeadLabel{
				Kind:          ImpossibleDeadLabel,
				Name:          name,
				Reason:        "You can't use `" + name + "` because this label is not possible here.",
				UsageFragment: findArgumentPosition(expr, pos, name),
				LabelReason:   reason,
				LabelFragment: fragment,
			})
		}
	}
	return dead
}

func (s *Source) checkIncludedLabels(expr string, pos ast.PositionRange, names []string) {
	s.DeadLabels = append(s.DeadLabels, findDeadIncludedLabels(s, expr, pos, names)...)
}

func (s *Source) checkAggregationLabels(expr string, n *ast.AggregateExpr) {
	var pos ast.PositionRange
	switch {
	case len(n.Grouping) == 0:
		pos = FindFuncNamePosition(expr, n.PosRange, n.Op.String())
	case n.Without:
		pos = FindFuncPosition(expr, n.PosRange, ast.OpWithout.String(), nil)
	default:
		pos = FindFuncPosition(expr, n.PosRange, ast.OpBy.String(), nil)
	}

	for _, j := range s.Joins {
		for _, name := range j.AddedLabels {
			if slices.Contains(s.UsedLabels, name) {
				continue
			}
			if !n.Without && slices.Contains(n.Grouping, name) {
				continue
			}
			if n.Without && !slices.Contains(n.Grouping, name) {
				continue
			}
			var (
				labelPos    = pos
				labelReason string
			)
			if t := s.findLabelTransform(name); t != nil {
				labelPos = t.Fragment
				labelReason = t.Reason
			}
			s.DeadLabels = append(s.DeadLabels, DeadLabel{
				Kind:          UnusedLabel,
				Name:          name,
				Reason:        fmt.Sprintf("Previously joined label `%s` is being removed from the results.", name),
				UsageFragment: findArgumentPosition(expr, pos, name),
				LabelReason:   labelReason,
				LabelFragment: labelPos,
			})
		}
	}
}

func (s *Source) findLabelTransform(name string) *LabelTransform {
	if t, ok := s.Labels[name]; ok {
		return &t
	}
	for _, j := range s.Joins {
		if t := j.Src.findLabelTransform(name); t != nil {
			return t
		}
	}
	return nil
}

func (s *Source) checkJoinedLabels(expr string, n *ast.BinaryExpr, dst *Source) (dead []DeadLabel) {
	pos := findBinOpsOperatorPosition(expr, n, n.Op.String())
	for _, j := range s.Joins {
		for _, name := range j.AddedLabels {
			if slices.Contains(n.VectorMatching.Include, name) {
				// label is included in group_left or group_right
				continue
			}
			if n.VectorMatching.On && slices.Contains(n.VectorMatching.MatchingLabels, name) {
				// label is included inside on(...)
				continue
			}
			if !n.VectorMatching.On && !slices.Contains(n.VectorMatching.MatchingLabels, name) {
				// label is NOT included inside ignoring(...)
				continue
			}
			var (
				labelPos    = pos
				labelReason string
			)
			if t := dst.findLabelTransform(name); t != nil {
				labelPos = t.Fragment
				labelReason = t.Reason
			}
			dead = append(dead, DeadLabel{
				Kind:          OrphanedLabel,
				Name:          name,
				Reason:        fmt.Sprintf("This binary operation prevents previously joined label `%s` from being added to the results.", name),
				UsageFragment: pos,
				LabelReason:   labelReason,
				LabelFragment: labelPos,
			})
		}
	}
	return dead
}

func (s *Source) useLabelsNotExcluded(excluded []string) {
	// Iterating over a map can yield labels in different order each time
	// so append labels to an extra slice, sort it, and then append the
	// sorted results to UsedLabels.
	// Without this tests might show a diff sometimes.
	toAdd := make([]string, 0, len(s.Labels))
	for name, lt := range s.Labels {
		if lt.Kind == ImpossibleLabel {
			continue
		}
		if !slices.Contains(excluded, name) {
			toAdd = appendToSlice(toAdd, name)
		}
	}
	slices.Sort(toAdd)
	s.UsedLabels = appendToSlice(s.UsedLabels, toAdd...)
}

type Visitor func(s *Source, j *Join, u *Unless)

func innerWalk(fn Visitor, s *Source, j *Join, u *Unless) {
	fn(s, j, u)
	for i := range s.Joins {
		innerWalk(fn, s.Joins[i].Src, &s.Joins[i], nil)
	}
	for i := range s.Unless {
		innerWalk(fn, s.Unless[i].Src, nil, &s.Unless[i])
	}
	for _, ind := range s.Indirect {
		innerWalk(fn, ind.Src, nil, nil)
	}
}

func (s *Source) WalkSources(fn Visitor) {
	innerWalk(fn, s, nil, nil)
}

func LabelsSource(expr string, node ast.Node) (src []*Source) {
	return walkNode(expr, node)
}

func walkNode(expr string, node ast.Node) (src []*Source) {
	switch n := node.(type) {
	case *ast.AggregateExpr:
		src = append(src, walkAggregation(expr, n)...)

	case *ast.BinaryExpr:
		src = append(src, parseBinOps(expr, n)...)

	case *ast.Call:
		src = append(src, parseCall(expr, n)...)

	case *ast.MatrixSelector:
		for _, s := range walkNode(expr, n.VectorSelector) {
			s.Returns = ast.ValueTypeMatrix
			// Prepend Matrix operation
			s.Operations = append(
				Operations{
					{
						Operation: "",
						Node:      node,
						Arguments: nil,
					},
				},
				s.Operations...,
			)
			s.requireDurationExprFeatures(n.RangeExpr)
			src = append(src, s)
		}

	case *ast.SubqueryExpr:
		for _, s := range walkNode(expr, n.Expr) {
			// Prepend Subquery operation
			s.Operations = append(
				Operations{
					{
						Operation: "",
						Node:      node,
						Arguments: nil,
					},
				},
				s.Operations...,
			)
			s.requireDurationExprFeatures(n.RangeExpr)
			s.requireDurationExprFeatures(n.StepExpr)
			s.requireDurationExprFeatures(n.OriginalOffsetExpr)
			s.requireStartOrEndFeature(expr, n.StartOrEnd, n.PositionRange())
			src = append(src, s)
		}

	case *ast.NumberLiteral:
		s := new(Source)
		s.Labels = map[string]LabelTransform{}
		s.Type = NumberSource
		s.Returns = ast.ValueTypeScalar
		s.ReturnInfo.KnownReturn = true
		s.ReturnInfo.ReturnedNumber = n.Val
		s.ReturnInfo.AlwaysReturns = true
		s.ReturnInfo.ValuePosition = n.PosRange
		s.excludeAllLabels(expr, "This query returns a number value with no labels.", n.PosRange, n.PosRange, nil)
		s.Position = n.PosRange
		src = append(src, s)

	case *ast.ParenExpr:
		src = append(src, walkNode(expr, n.Expr)...)

	case *ast.OffsetExpr:
		// MetricsQL offset or @ modifier applied to an arbitrary expression.
		src = append(src, walkNode(expr, n.Expr)...)

	case *ast.StringLiteral:
		s := new(Source)
		s.Labels = map[string]LabelTransform{}
		s.Type = StringSource
		s.Returns = ast.ValueTypeString
		s.ReturnInfo.AlwaysReturns = true
		s.excludeAllLabels(expr, "This query returns a string value with no labels.", n.PosRange, n.PosRange, nil)
		s.Position = n.PosRange
		src = append(src, s)

	case *ast.UnaryExpr:
		src = append(src, walkNode(expr, n.Expr)...)

	// case *ast.StepInvariantExpr:
	// Not possible to get this from the parser.

	case *ast.VectorSelector:
		s := new(Source)
		s.Labels = map[string]LabelTransform{}
		s.Type = SelectorSource
		s.Returns = ast.ValueTypeVector
		switch {
		case n.Anchored:
			s.RangeSelectorMode = RangeSelectorAnchored
			s.requireFeature(ast.OpAnchored.String(), n.PosRange)
		case n.Smoothed:
			s.RangeSelectorMode = RangeSelectorSmoothed
			s.requireFeature(ast.OpSmoothed.String(), n.PosRange)
		}
		s.requireDurationExprFeatures(n.OriginalOffsetExpr)
		s.requireStartOrEndFeature(expr, n.StartOrEnd, n.PosRange)
		s.Operations = append(s.Operations, Operation{
			Operation: "",
			Node:      n,
			Arguments: nil,
		})
		s.guaranteeLabel(
			"Query will only return series where these labels are present.",
			n.PosRange,
			labelsFromSelectors(guaranteedLabelsMatches, n)...,
		)
		for _, name := range labelsWithEmptyValueSelector(n) {
			s.excludeLabel(
				"Query uses `{"+name+"=\"\"}` selector which will filter out any time series with the `"+name+"` label set.",
				n.PosRange,
				name,
			)
		}
		s.Position = n.PosRange
		src = append(src, s)
	}
	return src
}

func appendToSlice(dst []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}

func labelsFromSelectors(matches []labels.MatchType, selector *ast.VectorSelector) (names []string) {
	if selector == nil {
		return nil
	}
	// Any label used in positive filters is gurnateed to be present.
	// With MetricsQL `or` filters it must be used in all filter groups.
	return namesInAllGroups(selector, func(lm *labels.Matcher) bool {
		return slices.Contains(matches, lm.Type)
	})
}

func labelsWithEmptyValueSelector(selector *ast.VectorSelector) (names []string) {
	return namesInAllGroups(selector, func(lm *labels.Matcher) bool {
		return lm.Type == labels.MatchEqual && lm.Value == ""
	})
}

func namesInAllGroups(selector *ast.VectorSelector, match func(*labels.Matcher) bool) (names []string) {
	for i, group := range selector.MatcherGroups() {
		var groupNames []string
		for _, lm := range group {
			if lm.Name == model.MetricNameLabel {
				continue
			}
			if match(lm) {
				groupNames = appendToSlice(groupNames, lm.Name)
			}
		}
		if i == 0 {
			names = groupNames
			continue
		}
		names = slices.DeleteFunc(names, func(name string) bool {
			return !slices.Contains(groupNames, name)
		})
	}
	return names
}

// MetricsQL aggregations that return some of the input time series with all their labels.
var labelPreservingAggregations = []ast.Op{
	"any",
	"bottomk_avg", "bottomk_last", "bottomk_max", "bottomk_median", "bottomk_min",
	"outliers_iqr", "outliers_mad", "outliersk",
	"share",
	"topk_avg", "topk_last", "topk_max", "topk_median", "topk_min",
	"zscore",
}

// aggregationExprs returns all expressions aggregated by n.
// MetricsQL allows to pass multiple series arguments, for example sum(foo, bar).
func aggregationExprs(n *ast.AggregateExpr) []ast.Node {
	if len(n.Args) == 0 {
		return []ast.Node{n.Expr}
	}
	exprs := make([]ast.Node, 0, len(n.Args))
	for _, arg := range n.Args {
		if arg == n.Param {
			continue
		}
		if arg == n.Expr || arg.Type() == ast.ValueTypeVector || arg.Type() == ast.ValueTypeMatrix {
			exprs = append(exprs, arg)
		}
	}
	return exprs
}

func aggregationArgs(n *ast.AggregateExpr) (args []string) {
	if len(n.Args) == 0 {
		if n.Param != nil {
			args = append(args, n.Param.String())
		}
		return args
	}
	exprs := aggregationExprs(n)
	for _, arg := range n.Args {
		if !slices.Contains(exprs, arg) {
			args = append(args, arg.String())
		}
	}
	return args
}

func walkNodes(expr string, nodes []ast.Node) (src []*Source) {
	for _, node := range nodes {
		src = append(src, walkNode(expr, node)...)
	}
	return src
}

func walkAggregation(expr string, n *ast.AggregateExpr) (src []*Source) {
	var s *Source

	args := aggregationArgs(n)

	switch {
	case n.Op == ast.OpCountValues:
		for _, s = range parseAggregation(expr, n) {
			s.Operations = append(s.Operations, Operation{
				Operation: n.Op.String(),
				Node:      n,
				Arguments: args,
			})
			// Param is the label to store the count value in.
			if sl, ok := n.Param.(*ast.StringLiteral); ok {
				s.guaranteeLabel(
					"This label will be added to the results by the count_values() call.",
					n.PosRange,
					sl.Val,
				)
			}
			if n.Without || !slices.Contains(n.Grouping, model.MetricNameLabel) {
				s.excludeLabel("Aggregation removes metric name.", n.PosRange, model.MetricNameLabel)
			}
			src = append(src, s)
		}
	case n.Op == ast.OpTopK, n.Op == ast.OpBottomK, slices.Contains(labelPreservingAggregations, n.Op):
		for _, s = range walkNodes(expr, aggregationExprs(n)) {
			for i := range s.Joins {
				s.Joins[i].Depth++
			}
			s.Type = AggregateSource
			s.Operations = append(s.Operations, Operation{
				Operation: n.Op.String(),
				Node:      n,
				Arguments: args,
			})
			src = append(src, s)
		}
	default:
		for _, s = range parseAggregation(expr, n) {
			s.Operations = append(s.Operations, Operation{
				Operation: n.Op.String(),
				Node:      n,
				Arguments: args,
			})
			if n.Without || !slices.Contains(n.Grouping, model.MetricNameLabel) {
				s.excludeLabel("Aggregation removes metric name.", n.PosRange, model.MetricNameLabel)
			}
			{
				name := n.Op.String()
				s.requireFeature(name, FindFuncNamePosition(expr, n.PosRange, name))
			}
			switch n.Op { // nolint: exhaustive
			case "histogram":
				s.guaranteeLabel("This label will be added to the results by the histogram() call.", n.PosRange, "vmrange")
			case "quantiles":
				if len(n.Args) == 0 {
					break
				}
				if sl, ok := n.Args[0].(*ast.StringLiteral); ok {
					s.guaranteeLabel("This label will be added to the results by the quantiles() call.", n.PosRange, sl.Val)
				}
			}
			src = append(src, s)
		}
	}
	return src
}

func parseAggregation(expr string, n *ast.AggregateExpr) (src []*Source) {
	var s *Source
	for _, s = range walkNodes(expr, aggregationExprs(n)) {
		// If we have sum(foo * bar) then we start with:
		// - source: foo
		//   joins: bar
		// Then we parse aggregation and end up with:
		// - source: sum(foo)
		//   joins: bar
		// Which is misleading and wrong, so we bump depth value to know about it.
		for i := range s.Joins {
			s.Joins[i].Depth++
		}

		s.checkAggregationLabels(expr, n)
		if n.Without {
			for _, name := range n.Grouping {
				s.excludeLabel(
					fmt.Sprintf("Query is using aggregation with `%s(%s)`, all labels included inside `%s(...)` will be removed from the results.",
						ast.OpWithout.String(), strings.Join(n.Grouping, ", "), ast.OpWithout.String()),
					findArgumentPosition(
						expr,
						FindFuncPosition(expr, n.PosRange, ast.OpWithout.String(), nil),
						name,
					),
					name,
				)
			}
		} else {
			if len(n.Grouping) == 0 {
				funcNamePos := FindFuncNamePosition(expr, n.PosRange, n.Op.String())
				s.excludeAllLabels(
					expr,
					"Query is using aggregation that removes all labels.",
					funcNamePos,
					funcNamePos,
					nil,
				)
			} else {
				s.UsedLabels = appendToSlice(s.UsedLabels, n.Grouping...)
				s.checkIncludedLabels(
					expr,
					FindFuncPosition(expr, n.PosRange, ast.OpBy.String(), nil),
					n.Grouping,
				)
				s.excludeAllLabels(
					expr,
					fmt.Sprintf("Query is using aggregation with `%s(%s)`, only labels included inside `%s(...)` will be present on the results.",
						ast.OpBy.String(), strings.Join(n.Grouping, ", "), ast.OpBy.String()),
					FindFuncPosition(expr, n.PosRange, ast.OpBy.String(), nil),
					FindFuncNamePosition(expr, n.PosRange, ast.OpBy.String()),
					n.Grouping,
				)
			}
		}
		s.Type = AggregateSource
		s.Returns = ast.ValueTypeVector
		src = append(src, s)
	}
	return src
}

func parsePromQLFunc(s *Source, expr string, n *ast.Call) *Source {
	s.requireFeature(n.Func.Name, FindFuncNamePosition(expr, n.PositionRange(), n.Func.Name))
	switch n.Func.Name {
	case "abs", "sgn",
		"acos", "acosh", "asin", "asinh", "atan", "atanh",
		"cos", "cosh", "sin", "sinh", "tan", "tanh":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "ceil", "floor", "round":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "changes", "resets":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "clamp", "clamp_max", "clamp_min":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "absent", "absent_over_time":
		s.Returns = ast.ValueTypeVector
		vs, _ := MostOuterOperation[*ast.VectorSelector](s)
		names := labelsFromSelectors([]labels.MatchType{labels.MatchEqual}, vs)
		funcNamePos := FindFuncNamePosition(expr, n.PosRange, n.Func.Name)
		s.excludeAllLabels(
			expr,
			fmt.Sprintf(`The [%s()](https://prometheus.io/docs/prometheus/latest/querying/functions/#%s) function is used to check if the provided query doesn't match any time series.
You will only get any results back if the metric selector you pass doesn't match anything.
Since there are no matching time series there are also no labels. If some time series is missing you cannot read its labels.
This means that the only labels you can get back from absent call are the ones you pass to it.
If you're hoping to get instance specific labels this way and alert when some target is down then that won't work, use the `+"`up`"+` metric instead.`,
				n.Func.Name, n.Func.Name),
			funcNamePos,
			funcNamePos,
			names,
		)
		s.guaranteeLabel(
			fmt.Sprintf("All labels passed to a %s() call will be present on the results if the query doesn't match anything.", n.Func.Name),
			n.PosRange,
			names...,
		)

	case "avg_over_time",
		"count_over_time",
		"first_over_time",
		"last_over_time",
		"mad_over_time",
		"max_over_time",
		"min_over_time",
		"present_over_time",
		"quantile_over_time",
		"stddev_over_time",
		"stdvar_over_time",
		"sum_over_time":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "days_in_month", "day_of_month", "day_of_week", "day_of_year", "hour", "minute", "month", "year":
		s.Returns = ast.ValueTypeVector
		// No labels if we don't pass any arguments.
		// Otherwise no change to labels.
		if len(n.Args) == 0 {
			s.ReturnInfo.AlwaysReturns = true
			s.excludeAllLabels(
				expr,
				fmt.Sprintf("Calling `%s()` with no arguments will return an empty time series with no labels.",
					n.Func.Name),
				n.PosRange,
				n.PosRange,
				nil,
			)
		}

	case "deg", "rad", "ln", "log10", "log2", "sqrt", "exp":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "delta", "idelta", "increase", "deriv", "irate", "rate":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "histogram_avg",
		"histogram_count",
		"histogram_fraction",
		"histogram_quantile",
		"histogram_quantiles",
		"histogram_stddev",
		"histogram_stdvar",
		"histogram_sum":
		// No change to labels.
		s.Returns = ast.ValueTypeVector
		if _, ok := stringArg(n, 0); ok {
			// MetricsQL histogram_quantiles("phiLabel", phi1, ..., buckets).
			return parseMetricsQLFunc(s, expr, n)
		}

	case "double_exponential_smoothing", "holt_winters", "predict_linear":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "label_join":
		// One label added to the results.
		// label_join(v instant-vector, dst_label string, separator string, src_label_1 string, src_label_2 string, ...)
		// MetricsQL only validates arguments when executing the query, so they might
		// be missing or not be strings.
		s.Returns = ast.ValueTypeVector
		if dst, ok := stringArg(n, 1); ok {
			s.guaranteeLabel(
				fmt.Sprintf("This label will be added to the result by a %s() call.", n.Func.Name),
				n.PosRange,
				dst,
			)
		}
		for i := 3; i < len(n.Args); i++ {
			if src, ok := stringArg(n, i); ok {
				s.UsedLabels = appendToSlice(s.UsedLabels, src)
			}
		}

	case "label_replace":
		// One label added to the results.
		// label_replace(v instant-vector, dst_label string, replacement string, src_label string, regex string)
		s.Returns = ast.ValueTypeVector
		if dst, ok := stringArg(n, 1); ok {
			s.guaranteeLabel(
				fmt.Sprintf("This label will be added to the result by a %s() call.", n.Func.Name),
				n.PosRange,
				dst,
			)
		}
		if src, ok := stringArg(n, 3); ok {
			s.UsedLabels = appendToSlice(s.UsedLabels, src)
		}

	case "pi":
		s.Returns = ast.ValueTypeScalar
		s.ReturnInfo.AlwaysReturns = true
		s.ReturnInfo.KnownReturn = true
		s.ReturnInfo.ReturnedNumber = math.Pi
		s.ReturnInfo.ValuePosition = n.PosRange
		s.excludeAllLabels(
			expr,
			fmt.Sprintf("Calling `%s()` will return a scalar value with no labels.", n.Func.Name),
			n.PosRange,
			n.PosRange,
			nil,
		)

	case "min_of", "max_of", "scalar":
		s.Returns = ast.ValueTypeScalar
		s.ReturnInfo.AlwaysReturns = true
		funcPos := FindFuncPosition(expr, n.PositionRange(), n.Func.Name, nil)
		s.excludeAllLabels(
			expr,
			fmt.Sprintf("Calling `%s()` will return a scalar value with no labels.", n.Func.Name),
			funcPos,
			funcPos,
			nil,
		)

	case "sort", "sort_desc", "sort_by_label", "sort_by_label_desc":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "time", "start", "end", "range", "step":
		s.Returns = ast.ValueTypeScalar
		s.ReturnInfo.AlwaysReturns = true
		s.ReturnInfo.ValuePosition = n.PosRange
		s.excludeAllLabels(
			expr,
			fmt.Sprintf("Calling `%s()` will return a scalar value with no labels.", n.Func.Name),
			n.PosRange,
			n.PosRange,
			nil,
		)

	case "timestamp",
		"start_timestamp",
		"ts_of_first_over_time",
		"ts_of_last_over_time",
		"ts_of_max_over_time",
		"ts_of_min_over_time":
		// No change to labels.
		s.Returns = ast.ValueTypeVector

	case "info":
		// info() joins labels from an info-series onto the input vector.
		// The joined labels are dynamic so we treat this as no label change.
		s.Returns = ast.ValueTypeVector

	case "vector":
		s.Returns = ast.ValueTypeVector
		s.ReturnInfo.AlwaysReturns = true
		s.ReturnInfo.ValuePosition = n.PosRange
		// MetricsQL accepts vector() without an argument.
		if len(n.Args) > 0 {
			for _, vs := range walkNode(expr, n.Args[0]) {
				if vs.ReturnInfo.KnownReturn {
					s.ReturnInfo.ReturnedNumber = vs.ReturnInfo.ReturnedNumber
					s.ReturnInfo.KnownReturn = true
				}
			}
		}
		funcNamePos := FindFuncNamePosition(expr, n.PosRange, n.Func.Name)
		s.excludeAllLabels(
			expr,
			fmt.Sprintf("Calling `%s()` will return a vector value with no labels.", n.Func.Name),
			funcNamePos,
			funcNamePos,
			nil,
		)

	default:
		return parseMetricsQLFunc(s, expr, n)
	}
	return s
}

func parseCall(expr string, n *ast.Call) (src []*Source) {
	var args []string
	var exprs []ast.Node
	var indirect []Indirect

	var vt ast.ValueType
	for i, e := range n.Args {
		switch {
		case len(n.Func.ArgTypes) == 0:
			// MetricsQL accepts arguments to functions that take none, e.g. time(foo).
			vt = ast.ValueTypeNone
		case i >= len(n.Func.ArgTypes):
			vt = n.Func.ArgTypes[len(n.Func.ArgTypes)-1]
		default:
			vt = n.Func.ArgTypes[i]
		}

		switch vt {
		case ast.ValueTypeVector, ast.ValueTypeMatrix:
			exprs = append(exprs, e)
		case ast.ValueTypeNone, ast.ValueTypeScalar, ast.ValueTypeString:
			args = append(args, e.String())
			for _, s := range walkNode(expr, e) {
				indirect = append(indirect, Indirect{
					Src:  s,
					Op:   "",
					Side: LHS,
				})
			}
		}
	}

	for _, e := range exprs {
		for _, es := range walkNode(expr, e) {
			es.Type = FuncSource
			es.Operations = append(es.Operations, Operation{
				Operation: n.Func.Name,
				Node:      n,
				Arguments: args,
			})
			es.Position = e.PositionRange()
			src = append(src, parsePromQLFunc(es, expr, n))
		}
	}
	for i := range src {
		src[i].Indirect = append(src[i].Indirect, indirect...)
	}

	if len(src) == 0 {
		s := new(Source)
		s.Labels = map[string]LabelTransform{}
		s.Type = FuncSource
		s.Indirect = indirect
		s.Operations = Operations{
			{
				Operation: n.Func.Name,
				Node:      n,
				Arguments: args,
			},
		}
		s.Position = n.PosRange
		src = append(src, parsePromQLFunc(s, expr, n))
	}

	return src
}

func fillFromMatching(vm *ast.VectorMatching) *Fill {
	if vm.FillValues.LHS == nil && vm.FillValues.RHS == nil {
		return nil
	}
	var f Fill
	if vm.FillValues.LHS != nil {
		f.LHS = *vm.FillValues.LHS
	}
	if vm.FillValues.RHS != nil {
		f.RHS = *vm.FillValues.RHS
	}
	return &f
}

func (s *Source) requireDurationExprFeatures(de *ast.DurationExpr) {
	if de == nil {
		return
	}
	s.requireFeature("duration_expr", de.PositionRange())
	walkDurationExprFeatures(s, de)
}

func (s *Source) requireStartOrEndFeature(expr string, startOrEnd ast.Op, pos ast.PositionRange) {
	switch startOrEnd { // nolint: exhaustive
	case ast.OpStart:
		s.requireFeature("start", FindFuncPosition(expr, pos, "start", nil))
	case ast.OpEnd:
		s.requireFeature("end", FindFuncPosition(expr, pos, "end", nil))
	}
}

func walkDurationExprFeatures(s *Source, de *ast.DurationExpr) {
	switch de.Op { // nolint: exhaustive
	case ast.OpRange:
		s.requireFeature("range", de.PositionRange())
	case ast.OpStep:
		s.requireFeature("step", de.PositionRange())
	}
	if lhs, ok := de.LHS.(*ast.DurationExpr); ok {
		walkDurationExprFeatures(s, lhs)
	}
	if rhs, ok := de.RHS.(*ast.DurationExpr); ok {
		walkDurationExprFeatures(s, rhs)
	}
}

func parseBinOps(expr string, n *ast.BinaryExpr) (src []*Source) {
	pos := n.PositionRange()
	src = make([]*Source, 0, 2)
	switch {
	// foo{} + 1
	// 1 + foo{}
	// foo{} > 1
	// 1 > foo{}
	case n.VectorMatching == nil:
		lhs := walkNode(expr, n.LHS)
		rhs := walkNode(expr, n.RHS)
		for _, ls := range lhs {
			for _, rs := range rhs {
				var side, other *Source
				switch {
				case ls.Returns == ast.ValueTypeVector, ls.Returns == ast.ValueTypeMatrix:
					// Use labels from LHS
					side = ls
					other = rs
				case rs.Returns == ast.ValueTypeVector, rs.Returns == ast.ValueTypeMatrix:
					// Use labels from RHS
					side = rs
					other = ls
				default:
					side = ls
					other = rs
				}
				// We walk RHS multiple times because we might have query like:
				// foo or bar > 0
				// Where LHS is [foo, bar] while RHS is [0]
				// And we end up with multiple sources:
				// - foo > 0
				// - bar > 0
				// As we walk RHS multiple times we might end up mutating the same
				// Source, and so to avoid that we need to clone it first.
				result := side.clone()
				indSide := RHS
				if other == ls {
					indSide = LHS
				}
				result.Indirect = append(result.Indirect, Indirect{
					Src:  other,
					Op:   n.Op,
					Side: indSide,
				})
				result.Condition, result.ReturnInfo.IsReturnBool = checkConditions(result, n.Op, n.ReturnBool)
				if ls.ReturnInfo.AlwaysReturns && rs.ReturnInfo.AlwaysReturns && ls.ReturnInfo.KnownReturn && rs.ReturnInfo.KnownReturn {
					// Both sides always return something
					result.ReturnInfo, result.DeadInfo = calculateStaticReturn(expr, ls, rs, n)
				}
				src = append(src, result)
			}
		}

		// foo{} +               bar{}
		// foo{} + on(...)       bar{}
		// foo{} * ignoring(...) bar{}
		// foo{} /               bar{}
	case n.VectorMatching.Card == ast.CardOneToOne:
		rhs := walkNode(expr, n.RHS)
		for _, ls := range walkNode(expr, n.LHS) {
			if n.VectorMatching.On {
				ls.UsedLabels = appendToSlice(ls.UsedLabels, n.VectorMatching.MatchingLabels...)
				ls.checkIncludedLabels(expr, pos, n.VectorMatching.MatchingLabels)
				funcPos := FindFuncPosition(expr, pos, ast.OpOn.String(), []ast.PositionRange{
					n.LHS.PositionRange(), n.RHS.PositionRange(),
				})
				ls.excludeAllLabels(
					expr,
					fmt.Sprintf(
						"Query is using %s vector matching with `%s(%s)`, only labels included inside `%s(...)` will be present on the results.",
						n.VectorMatching.Card, ast.OpOn.String(), strings.Join(n.VectorMatching.MatchingLabels, ", "), ast.OpOn.String(),
					),
					funcPos,
					funcPos,
					n.VectorMatching.MatchingLabels,
				)
			} else {
				ls.useLabelsNotExcluded(n.VectorMatching.MatchingLabels)
				for _, name := range n.VectorMatching.MatchingLabels {
					ls.excludeLabel(
						fmt.Sprintf(
							"Query is using %s vector matching with `%s(%s)`, all labels included inside `%s(...)` will be removed on the results.",
							n.VectorMatching.Card, ast.OpIgnoring.String(), strings.Join(n.VectorMatching.MatchingLabels, ", "), ast.OpIgnoring.String(),
						),
						findArgumentPosition(
							expr,
							FindFuncPosition(expr, pos, ast.OpIgnoring.String(), []ast.PositionRange{
								n.LHS.PositionRange(), n.RHS.PositionRange(),
							}),
							name,
						),
						name,
					)
				}
				for _, rs := range rhs {
					if ls.ReturnInfo.AlwaysReturns && rs.ReturnInfo.AlwaysReturns && ls.ReturnInfo.KnownReturn && rs.ReturnInfo.KnownReturn {
						// Both sides always return something
						ls.ReturnInfo, ls.DeadInfo = calculateStaticReturn(expr, ls, rs, n)
					}
				}
			}
			for _, rs := range rhs {
				deadLabels := rs.checkJoinedLabels(expr, n, rs)
				var deadInfo *DeadInfo
				if ok, s, p := canJoin(ls, rs, n.VectorMatching); !ok {
					deadInfo = &DeadInfo{
						Reason:   s,
						Fragment: p,
					}
				}
				fill := fillFromMatching(n.VectorMatching)
				ls.Joins = append(ls.Joins, Join{
					Src:            rs,
					Fill:           fill,
					DeadInfo:       deadInfo,
					DeadLabels:     deadLabels,
					Op:             n.Op,
					Depth:          0,
					MatchingLabels: n.VectorMatching.MatchingLabels,
					AddedLabels:    nil,
					IsOn:           n.VectorMatching.On,
				})
				if fill != nil {
					name := ast.OpFill.String()
					ls.requireFeature(name, findBinOpsOperatorPosition(expr, n, name))
				}
			}
			ls.DeadLabels = append(ls.DeadLabels, ls.checkJoinedLabels(expr, n, ls)...)
			ls.excludeLabel("Binary operation between two vectors removes metric names.", pos, model.MetricNameLabel)
			ls.Condition, ls.ReturnInfo.IsReturnBool = checkConditions(ls, n.Op, n.ReturnBool)
			src = append(src, ls)
		}

		// foo{} + on(...)       group_right(...) bar{}
		// foo{} + ignoring(...) group_right(...) bar{}
	case n.VectorMatching.Card == ast.CardOneToMany:
		lhs := walkNode(expr, n.LHS)
		for _, rs := range walkNode(expr, n.RHS) {
			rs.joinLabels(expr, pos, ast.OpGroupRight, n.VectorMatching.Include, []ast.PositionRange{
				n.LHS.PositionRange(), n.RHS.PositionRange(),
			})
			// If we have:
			// foo * on(instance) group_left(a,b) bar{x="y"}
			// then only group_left() labels will be included.
			if n.VectorMatching.On {
				rs.UsedLabels = appendToSlice(rs.UsedLabels, n.VectorMatching.MatchingLabels...)
				rs.checkIncludedLabels(expr, pos, n.VectorMatching.MatchingLabels)
				for _, name := range n.VectorMatching.MatchingLabels {
					rs.includeLabel(
						expr,
						fmt.Sprintf(
							"Query is using %s vector matching with `on(%s)`, labels included inside `on(...)` will be present on the results.",
							n.VectorMatching.Card, strings.Join(n.VectorMatching.MatchingLabels, ", "),
						),
						findArgumentPosition(
							expr,
							FindFuncPosition(expr, pos, ast.OpOn.String(), []ast.PositionRange{
								n.LHS.PositionRange(), n.RHS.PositionRange(),
							}),
							name,
						),
						name,
					)
				}
			} else {
				rs.useLabelsNotExcluded(n.VectorMatching.MatchingLabels)
			}
			for _, ls := range lhs {
				deadLabels := findDeadIncludedLabels(
					ls,
					expr,
					FindFuncPosition(expr, pos, ast.OpGroupRight.String(), []ast.PositionRange{
						n.LHS.PositionRange(),
						n.RHS.PositionRange(),
					}),
					n.VectorMatching.Include,
				)
				rs.DeadLabels = append(rs.DeadLabels, ls.checkJoinedLabels(expr, n, rs)...)
				var deadInfo *DeadInfo
				if ok, s, p := canJoin(rs, ls, n.VectorMatching); !ok {
					deadInfo = &DeadInfo{
						Reason:   s,
						Fragment: p,
					}
				}
				fill := fillFromMatching(n.VectorMatching)
				rs.Joins = append(rs.Joins, Join{
					Src:            ls,
					Fill:           fill,
					DeadInfo:       deadInfo,
					DeadLabels:     deadLabels,
					Op:             n.Op,
					Depth:          0,
					MatchingLabels: n.VectorMatching.MatchingLabels,
					AddedLabels:    n.VectorMatching.Include,
					IsOn:           n.VectorMatching.On,
				})
				if fill != nil {
					name := ast.OpFill.String()
					rs.requireFeature(name, findBinOpsOperatorPosition(expr, n, name))
				}
			}
			rs.excludeLabel("Binary operation between two vectors removes metric names.", pos, model.MetricNameLabel)
			rs.Condition, rs.ReturnInfo.IsReturnBool = checkConditions(rs, n.Op, n.ReturnBool)
			src = append(src, rs)
		}

		// foo{} + on(...)       group_left(...) bar{}
		// foo{} + ignoring(...) group_left(...) bar{}
	case n.VectorMatching.Card == ast.CardManyToOne:
		rhs := walkNode(expr, n.RHS)
		for _, ls := range walkNode(expr, n.LHS) {
			ls.joinLabels(expr, pos, ast.OpGroupLeft, n.VectorMatching.Include, []ast.PositionRange{
				n.LHS.PositionRange(), n.RHS.PositionRange(),
			})
			if n.VectorMatching.On {
				ls.UsedLabels = appendToSlice(ls.UsedLabels, n.VectorMatching.MatchingLabels...)
				ls.checkIncludedLabels(expr, pos, n.VectorMatching.MatchingLabels)
				for _, name := range n.VectorMatching.MatchingLabels {
					ls.includeLabel(
						expr,
						fmt.Sprintf(
							"Query is using %s vector matching with `on(%s)`, labels included inside `on(...)` will be present on the results.",
							n.VectorMatching.Card, strings.Join(n.VectorMatching.MatchingLabels, ", "),
						),
						findArgumentPosition(
							expr,
							FindFuncPosition(expr, pos, ast.OpOn.String(), []ast.PositionRange{
								n.LHS.PositionRange(), n.RHS.PositionRange(),
							}),
							name,
						),
						name,
					)
				}
			} else {
				ls.useLabelsNotExcluded(n.VectorMatching.MatchingLabels)
			}
			for _, rs := range rhs {
				deadLabels := findDeadIncludedLabels(
					rs,
					expr,
					FindFuncPosition(expr, pos, ast.OpGroupLeft.String(), []ast.PositionRange{
						n.LHS.PositionRange(),
						n.RHS.PositionRange(),
					}),
					n.VectorMatching.Include,
				)
				ls.DeadLabels = append(ls.DeadLabels, rs.checkJoinedLabels(expr, n, ls)...)
				var deadInfo *DeadInfo
				if ok, s, p := canJoin(ls, rs, n.VectorMatching); !ok {
					deadInfo = &DeadInfo{
						Reason:   s,
						Fragment: p,
					}
				}
				fill := fillFromMatching(n.VectorMatching)
				ls.Joins = append(ls.Joins, Join{
					Src:            rs,
					Fill:           fill,
					DeadInfo:       deadInfo,
					DeadLabels:     deadLabels,
					Op:             n.Op,
					Depth:          0,
					MatchingLabels: n.VectorMatching.MatchingLabels,
					AddedLabels:    n.VectorMatching.Include,
					IsOn:           n.VectorMatching.On,
				})
				if fill != nil {
					name := ast.OpFill.String()
					ls.requireFeature(name, findBinOpsOperatorPosition(expr, n, name))
				}
			}
			ls.excludeLabel("Binary operation between two vectors removes metric names.", pos, model.MetricNameLabel)
			ls.Condition, ls.ReturnInfo.IsReturnBool = checkConditions(ls, n.Op, n.ReturnBool)
			src = append(src, ls)
		}

		// foo{} and on(...)       bar{}
		// foo{} and ignoring(...) bar{}
		// foo{} and bar{}
		// foo{} unless bar{}
		// foo{} or bar{}
		// foo{} or on(...) bar{}
		// foo{} or ignoring(...) bar{}
		//
		// 'foo or bar' means:
		// - take all foo{} results
		// - take all bar{} results
		// - remove any bar{} result if there's a foo{} result with same labels (except __name__)
		//
		// 'foo or on(a) bar' means we only compare 'a' label and remove any bar{} results with 'a' value
		// already provided by foo{}
		//
		// 'foo or ignoring(a) bar' means we ignore the 'a' label when comparing foo{} and bar{} results,
		// so (for example) bar{} results with 'a' labels present where foo{} doesn't have any 'a' label
		// will still be excluded if all other labels match.
	case n.VectorMatching.Card == ast.CardManyToMany:
		setOp := setOperator(n.Op)
		var lhsCanBeEmpty bool // true if any of the LHS query can produce empty results.
		rhs := walkNode(expr, n.RHS)
		for _, ls := range walkNode(expr, n.LHS) {
			var rhsConditional bool
			// With many-to-many on/ignore is only used for matching series, it doesn't impact
			// returned labels.
			if n.VectorMatching.On {
				ls.UsedLabels = appendToSlice(ls.UsedLabels, n.VectorMatching.MatchingLabels...)
				ls.checkIncludedLabels(expr, pos, n.VectorMatching.MatchingLabels)
				for _, name := range n.VectorMatching.MatchingLabels {
					ls.includeLabel(
						expr,
						fmt.Sprintf(
							"Query is using %s vector matching with `on(%s)`, labels included inside `on(...)` will be present on the results if matched time series have them.",
							n.VectorMatching.Card, strings.Join(n.VectorMatching.MatchingLabels, ", "),
						),
						findArgumentPosition(
							expr,
							FindFuncPosition(expr, pos, ast.OpOn.String(), []ast.PositionRange{
								n.LHS.PositionRange(), n.RHS.PositionRange(),
							}),
							name,
						),
						name,
					)
				}
			} else {
				ls.useLabelsNotExcluded(n.VectorMatching.MatchingLabels)
			}
			if !ls.ReturnInfo.AlwaysReturns || ls.Condition.Present {
				lhsCanBeEmpty = true
			}
			for _, rs := range rhs {
				condition, _ := checkConditions(rs, n.Op, n.ReturnBool)
				if condition.Present {
					rhsConditional = true
				}
				var deadInfo *DeadInfo
				if ok, s, p := canJoin(ls, rs, n.VectorMatching); !ok {
					deadInfo = &DeadInfo{
						Reason:   s,
						Fragment: p,
					}
				}
				switch {
				case setOp == ast.OpUnless:
					if n.VectorMatching.On && len(n.VectorMatching.MatchingLabels) == 0 && rs.ReturnInfo.AlwaysReturns && !rs.Condition.Present {
						ls.DeadInfo = &DeadInfo{
							Reason:   "This query will never return anything because the `" + n.Op.String() + "` query always returns something.",
							Fragment: rs.Position,
						}
					}
					ls.Unless = append(ls.Unless, Unless{
						DeadInfo:       deadInfo,
						Src:            rs,
						MatchingLabels: n.VectorMatching.MatchingLabels,
						IsOn:           n.VectorMatching.On,
					})
				case setOp != ast.OpOr:
					ls.Joins = append(ls.Joins, Join{
						Src:            rs,
						Fill:           nil,
						DeadInfo:       deadInfo,
						DeadLabels:     nil,
						Op:             n.Op,
						Depth:          0,
						MatchingLabels: n.VectorMatching.MatchingLabels,
						AddedLabels:    nil,
						IsOn:           n.VectorMatching.On,
					})
				}
				ls.DeadLabels = append(ls.DeadLabels, rs.checkJoinedLabels(expr, n, ls)...)
			}
			if setOp == ast.OpAnd && rhsConditional {
				ls.Condition = Condition{
					Present:    true,
					Op:         n.Op,
					Value:      0,
					KnownValue: false,
				}
			}
			src = append(src, ls)
		}
		if setOp == ast.OpOr {
			for _, rs := range rhs {
				// MetricsQL `default 0`: describe the fallback as `default 0`, not `0`, so dead
				// code messages read like the ones for `or vector(0)`.
				if n.Op == ast.OpDefault && rs.Type == NumberSource && rs.ReturnInfo.LogicalExpr == "" {
					rs.ReturnInfo.LogicalExpr = "default " + GetQueryFragment(expr, rs.ReturnInfo.ValuePosition)
				}
				// If LHS can NOT be empty then RHS is dead code.
				if !lhsCanBeEmpty {
					rs.DeadInfo = &DeadInfo{
						Reason:   "The left hand side always returns something and so the right hand side is never used.",
						Fragment: rs.Position,
					}
				}
				src = append(src, rs)
			}
		}
	}
	return src
}

// setOperator maps MetricsQL set-like operators to the PromQL operator with the same effect on labels.
func setOperator(op ast.Op) ast.Op {
	switch op { // nolint: exhaustive
	case ast.OpIf:
		return ast.OpAnd
	case ast.OpIfNot:
		return ast.OpUnless
	case ast.OpDefault:
		return ast.OpOr
	default:
		return op
	}
}

func checkConditions(s *Source, op ast.Op, isBool bool) (condition Condition, isReturnBool bool) {
	if !s.Condition.Present && isBool {
		isReturnBool = isBool
	}
	if s.Condition.Present {
		return s.Condition, isReturnBool
	}
	if !op.IsComparisonOperator() {
		return condition, isReturnBool
	}
	condition = Condition{
		Present:    true,
		Op:         op,
		Value:      0,
		KnownValue: false,
	}
	for _, j := range s.Joins {
		if j.Op != op || j.Src == nil || !j.Src.ReturnInfo.KnownReturn {
			continue
		}
		condition.Value = j.Src.ReturnInfo.ReturnedNumber
		condition.KnownValue = true
		return condition, isReturnBool
	}
	for _, ind := range s.Indirect {
		if ind.Op != op || ind.Src == nil || !ind.Src.ReturnInfo.KnownReturn {
			continue
		}
		condition.Value = ind.Src.ReturnInfo.ReturnedNumber
		condition.KnownValue = true
		return condition, isReturnBool
	}
	return condition, isReturnBool
}

func canJoin(ls, rs *Source, vm *ast.VectorMatching) (bool, string, ast.PositionRange) {
	var side string
	if vm.Card == ast.CardOneToMany {
		side = "left"
	} else {
		side = "right"
	}

	switch {
	case vm.On && len(vm.MatchingLabels) == 0: // ls on() unless rs
		return true, "", ast.PositionRange{}
	case vm.On: // ls on(...) unless rs
		for _, name := range vm.MatchingLabels {
			if ls.CanHaveLabel(name) && !rs.CanHaveLabel(name) {
				reason, fragment := rs.LabelExcludeReason(name)
				return false, fmt.Sprintf("The %s hand side will never be matched because it doesn't have the `%s` label from `on(...)`. %s",
					side, name, reason), fragment
			}
		}
	default: // ls unless rs
		// Sorted, so the same label is reported on every run.
		for _, name := range slices.Sorted(maps.Keys(ls.Labels)) {
			if ls.Labels[name].Kind != GuaranteedLabel {
				continue
			}
			if slices.Contains(vm.MatchingLabels, name) {
				continue
			}
			if ls.CanHaveLabel(name) && !rs.CanHaveLabel(name) {
				otherSide := "left"
				if side == "left" {
					otherSide = "right"
				}
				reason, fragment := rs.LabelExcludeReason(name)
				return false, fmt.Sprintf("The %s hand side will never be matched because it doesn't have the `%s` label while the %s hand side will. %s",
					side, name, otherSide, reason), fragment
			}
		}
	}
	return true, "", ast.PositionRange{}
}

func describeDeadCode(expr string, ls, rs *Source, op *ast.BinaryExpr, match string) *DeadInfo {
	var lse, rse string
	if ls.ReturnInfo.LogicalExpr != "" {
		lse = ls.ReturnInfo.LogicalExpr
	} else {
		lse = GetQueryFragment(expr, ls.ReturnInfo.ValuePosition)
	}
	if rs.ReturnInfo.LogicalExpr != "" {
		rse = rs.ReturnInfo.LogicalExpr
	} else {
		rse = GetQueryFragment(expr, rs.ReturnInfo.ValuePosition)
	}

	cmpPrefix := fmt.Sprintf("`%s %s %s` always evaluates to", lse, op.Op, rse)

	var cmpSuffix string
	if op.ReturnBool {
		cmpSuffix = "and uses the `bool` modifier which means it will always return 0"
	} else {
		cmpSuffix = "which is not possible, so it will never return anything."
	}
	return &DeadInfo{
		Reason: fmt.Sprintf(
			"%s `%s %s %s` %s",
			cmpPrefix,
			strconv.FormatFloat(ls.ReturnInfo.ReturnedNumber, 'f', -1, 64),
			match,
			strconv.FormatFloat(rs.ReturnInfo.ReturnedNumber, 'f', -1, 64),
			cmpSuffix,
		),
		Fragment: ls.Position,
	}
}

func calculateStaticReturn(expr string, ls, rs *Source, op *ast.BinaryExpr) (ret ReturnInfo, deadinfo *DeadInfo) {
	ret = ls.ReturnInfo
	switch op.Op { // nolint: exhaustive
	case ast.OpEql:
		if ls.ReturnInfo.ReturnedNumber != rs.ReturnInfo.ReturnedNumber {
			deadinfo = describeDeadCode(expr, ls, rs, op, "==")
		}
	case ast.OpNeq:
		if ls.ReturnInfo.ReturnedNumber == rs.ReturnInfo.ReturnedNumber {
			deadinfo = describeDeadCode(expr, ls, rs, op, "!=")
		}
	case ast.OpLte:
		if ls.ReturnInfo.ReturnedNumber > rs.ReturnInfo.ReturnedNumber {
			deadinfo = describeDeadCode(expr, ls, rs, op, "<=")
		}
	case ast.OpLss:
		if ls.ReturnInfo.ReturnedNumber >= rs.ReturnInfo.ReturnedNumber {
			deadinfo = describeDeadCode(expr, ls, rs, op, "<")
		}
	case ast.OpGte:
		if ls.ReturnInfo.ReturnedNumber < rs.ReturnInfo.ReturnedNumber {
			deadinfo = describeDeadCode(expr, ls, rs, op, ">=")
		}
	case ast.OpGtr:
		if ls.ReturnInfo.ReturnedNumber <= rs.ReturnInfo.ReturnedNumber {
			deadinfo = describeDeadCode(expr, ls, rs, op, ">")
		}
	case ast.OpAdd:
		ret.ReturnedNumber = ls.ReturnInfo.ReturnedNumber + rs.ReturnInfo.ReturnedNumber
		ret.LogicalExpr = formatDesc(expr, ls, rs, "+")
	case ast.OpSub:
		ret.ReturnedNumber = ls.ReturnInfo.ReturnedNumber - rs.ReturnInfo.ReturnedNumber
		ret.LogicalExpr = formatDesc(expr, ls, rs, "-")
	case ast.OpMul:
		ret.ReturnedNumber = ls.ReturnInfo.ReturnedNumber * rs.ReturnInfo.ReturnedNumber
		ret.LogicalExpr = formatDesc(expr, ls, rs, "*")
	case ast.OpDiv:
		ret.ReturnedNumber = ls.ReturnInfo.ReturnedNumber / rs.ReturnInfo.ReturnedNumber
		ret.LogicalExpr = formatDesc(expr, ls, rs, "/")
	case ast.OpMod:
		ret.ReturnedNumber = math.Mod(ls.ReturnInfo.ReturnedNumber, rs.ReturnInfo.ReturnedNumber)
		ret.LogicalExpr = formatDesc(expr, ls, rs, "%")
	case ast.OpPow:
		ret.ReturnedNumber = math.Pow(ls.ReturnInfo.ReturnedNumber, rs.ReturnInfo.ReturnedNumber)
		ret.LogicalExpr = formatDesc(expr, ls, rs, "^")
	}
	return ret, deadinfo
}

func formatDesc(expr string, ls, rs *Source, op string) string {
	var lse, rse string
	if ls.ReturnInfo.LogicalExpr != "" {
		lse = ls.ReturnInfo.LogicalExpr
	} else {
		lse = GetQueryFragment(expr, ls.ReturnInfo.ValuePosition)
	}
	if rs.ReturnInfo.LogicalExpr != "" {
		rse = rs.ReturnInfo.LogicalExpr
	} else {
		rse = GetQueryFragment(expr, rs.ReturnInfo.ValuePosition)
	}
	return lse + " " + op + " " + rse
}
