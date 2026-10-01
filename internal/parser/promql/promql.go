// Package promql converts queries parsed by the Prometheus PromQL parser into pint AST.
package promql

import (
	"errors"
	"fmt"
	"slices"

	"github.com/prometheus/prometheus/model/labels"
	promParser "github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/promql/parser/posrange"

	"github.com/cloudflare/pint/internal/parser/ast"
)

const Name = "promql"

// parser is a shared, goroutine-safe PromQL parser with all feature flags enabled.
// Each method creates its own internal parser state, so it is safe to call concurrently.
var parser = promParser.NewParser(promParser.Options{
	EnableExperimentalFunctions:  true,
	ExperimentalDurationExpr:     true,
	EnableExtendedRangeSelectors: true,
	EnableBinopFillModifiers:     true,
})

var functions = convertFunctions(promParser.Functions)

// Language implements ast.Language for PromQL.
type Language struct{}

func (Language) Name() string {
	return Name
}

func (Language) ParseExpr(query string) (ast.Node, error) {
	node, err := parser.ParseExpr(query)
	if err != nil {
		return nil, convertError(err)
	}
	return Convert(node), nil
}

func (Language) ParseMetricSelector(selector string) ([]*labels.Matcher, error) {
	return parser.ParseMetricSelector(selector)
}

// Functions returns all PromQL functions.
func Functions() map[string]*ast.Function {
	return functions
}

// convertError returns the error pointing at the shortest query fragment.
func convertError(err error) error {
	errorList, ok := errors.AsType[promParser.ParseErrors](err)
	if !ok {
		return err
	}
	original := slices.Clone(errorList)
	// Find the error pointing at the shortest query fragment.
	slices.SortFunc(errorList, func(a, b promParser.ParseErr) int {
		ar := a.PositionRange.End - a.PositionRange.Start
		br := b.PositionRange.End - b.PositionRange.Start
		switch {
		case ar < br:
			return -1
		case ar > br:
			return 1
		default:
			return 0
		}
	})
	for _, el := range errorList {
		if el.PositionRange.Start > 0 && el.PositionRange.End > 0 {
			return &ast.ParseError{
				Cause:         promParser.ParseErrors{el},
				Original:      original,
				Message:       el.Err.Error(),
				PositionRange: convertPosRange(el.PositionRange),
			}
		}
	}
	if len(errorList) > 0 {
		el := errorList[len(errorList)-1]
		return &ast.ParseError{
			Cause:         err,
			Original:      original,
			Message:       el.Err.Error(),
			PositionRange: convertPosRange(el.PositionRange),
		}
	}
	return err
}

// Convert returns pint AST for given Prometheus PromQL node.
func Convert(node promParser.Node) ast.Node {
	switch n := node.(type) {
	case *promParser.VectorSelector:
		return convertVectorSelector(n)
	case *promParser.MatrixSelector:
		return &ast.MatrixSelector{
			VectorSelector: Convert(n.VectorSelector),
			Range:          n.Range,
			RangeExpr:      convertDuration(n.RangeExpr),
			PosRange:       convertPosRange(n.PositionRange()),
		}
	case *promParser.SubqueryExpr:
		return &ast.SubqueryExpr{
			Expr:               Convert(n.Expr),
			Range:              n.Range,
			RangeExpr:          convertDuration(n.RangeExpr),
			Step:               n.Step,
			StepExpr:           convertDuration(n.StepExpr),
			OriginalOffset:     n.OriginalOffset,
			OriginalOffsetExpr: convertDuration(n.OriginalOffsetExpr),
			Offset:             n.Offset,
			Timestamp:          n.Timestamp,
			StartOrEnd:         convertStartOrEnd(n.StartOrEnd),
			PosRange:           convertPosRange(n.PositionRange()),
		}
	case *promParser.AggregateExpr:
		agg := &ast.AggregateExpr{
			Op:       convertOp(n.Op),
			Expr:     Convert(n.Expr),
			Grouping: n.Grouping,
			Without:  n.Without,
			PosRange: convertPosRange(n.PosRange),
		}
		if n.Param != nil {
			agg.Param = Convert(n.Param)
		}
		return agg
	case *promParser.BinaryExpr:
		return &ast.BinaryExpr{
			Op:             convertOp(n.Op),
			LHS:            Convert(n.LHS),
			RHS:            Convert(n.RHS),
			VectorMatching: convertVectorMatching(n.VectorMatching),
			ReturnBool:     n.ReturnBool,
			PosRange:       convertPosRange(n.PositionRange()),
		}
	case *promParser.Call:
		args := make([]ast.Node, 0, len(n.Args))
		for _, arg := range n.Args {
			args = append(args, Convert(arg))
		}
		return &ast.Call{
			Func:     convertFunction(n.Func),
			Args:     args,
			PosRange: convertPosRange(n.PosRange),
		}
	case *promParser.NumberLiteral:
		return &ast.NumberLiteral{
			Val:      n.Val,
			Duration: n.Duration,
			PosRange: convertPosRange(n.PosRange),
		}
	case *promParser.StringLiteral:
		return &ast.StringLiteral{
			Val:      n.Val,
			PosRange: convertPosRange(n.PosRange),
		}
	case *promParser.ParenExpr:
		return &ast.ParenExpr{
			Expr:     Convert(n.Expr),
			PosRange: convertPosRange(n.PosRange),
		}
	case *promParser.UnaryExpr:
		return &ast.UnaryExpr{
			Op:       convertOp(n.Op),
			Expr:     Convert(n.Expr),
			PosRange: convertPosRange(n.PositionRange()),
		}
	case *promParser.DurationExpr:
		return convertDuration(n)
	case *promParser.StepInvariantExpr:
		return Convert(n.Expr)
	default:
		panic(fmt.Sprintf("unsupported PromQL node type: %T", node))
	}
}

func convertVectorSelector(n *promParser.VectorSelector) *ast.VectorSelector {
	return &ast.VectorSelector{
		Name:               n.Name,
		LabelMatchers:      n.LabelMatchers,
		OriginalOffset:     n.OriginalOffset,
		OriginalOffsetExpr: convertDuration(n.OriginalOffsetExpr),
		Offset:             n.Offset,
		Timestamp:          n.Timestamp,
		StartOrEnd:         convertStartOrEnd(n.StartOrEnd),
		Anchored:           n.Anchored,
		Smoothed:           n.Smoothed,
		PosRange:           convertPosRange(n.PosRange),
	}
}

func convertDuration(n *promParser.DurationExpr) *ast.DurationExpr {
	if n == nil {
		return nil
	}
	d := &ast.DurationExpr{
		Op:       convertOp(n.Op),
		Wrapped:  n.Wrapped,
		PosRange: convertPosRange(n.PositionRange()),
	}
	if n.LHS != nil {
		d.LHS = Convert(n.LHS)
	}
	if n.RHS != nil {
		d.RHS = Convert(n.RHS)
	}
	return d
}

func convertVectorMatching(vm *promParser.VectorMatching) *ast.VectorMatching {
	if vm == nil {
		return nil
	}
	return &ast.VectorMatching{
		Card:           convertCard(vm.Card),
		MatchingLabels: vm.MatchingLabels,
		On:             vm.On,
		Include:        vm.Include,
		FillValues: ast.FillValues{
			LHS: vm.FillValues.LHS,
			RHS: vm.FillValues.RHS,
		},
	}
}

func convertCard(c promParser.VectorMatchCardinality) ast.VectorMatchCardinality {
	switch c {
	case promParser.CardOneToOne:
		return ast.CardOneToOne
	case promParser.CardManyToOne:
		return ast.CardManyToOne
	case promParser.CardOneToMany:
		return ast.CardOneToMany
	case promParser.CardManyToMany:
		return ast.CardManyToMany
	}
	panic(fmt.Sprintf("unsupported vector matching cardinality: %s", c))
}

func convertStartOrEnd(it promParser.ItemType) ast.Op {
	switch it {
	case promParser.START:
		return ast.OpStart
	case promParser.END:
		return ast.OpEnd
	default:
		return ""
	}
}

func convertOp(it promParser.ItemType) ast.Op {
	if s, ok := promParser.ItemTypeStr[it]; ok {
		return ast.Op(s)
	}
	panic(fmt.Sprintf("unsupported PromQL operator: %s", it))
}

func convertPosRange(pr posrange.PositionRange) ast.PositionRange {
	return ast.PositionRange{
		Start: ast.Pos(pr.Start),
		End:   ast.Pos(pr.End),
	}
}

func convertFunction(f *promParser.Function) *ast.Function {
	if cf, ok := functions[f.Name]; ok {
		return cf
	}
	return newFunction(f)
}

func newFunction(f *promParser.Function) *ast.Function {
	argTypes := make([]ast.ValueType, 0, len(f.ArgTypes))
	for _, vt := range f.ArgTypes {
		argTypes = append(argTypes, ast.ValueType(vt))
	}
	return &ast.Function{
		Name:         f.Name,
		ArgTypes:     argTypes,
		Variadic:     f.Variadic,
		ReturnType:   ast.ValueType(f.ReturnType),
		Experimental: f.Experimental,
	}
}

func convertFunctions(src map[string]*promParser.Function) map[string]*ast.Function {
	dst := make(map[string]*ast.Function, len(src))
	for name, f := range src {
		dst[name] = newFunction(f)
	}
	return dst
}
