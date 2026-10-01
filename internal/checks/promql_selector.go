package checks

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/source"
)

const (
	SelectorCheckName = "promql/selector"
)

func NewSelectorCheck(keyRe, callRe *TemplatedRegexp, requiredName, comment string, severity Severity) SelectorCheck {
	return SelectorCheck{
		keyRe:        keyRe,
		callRe:       callRe,
		requiredName: requiredName,
		comment:      comment,
		severity:     severity,
	}
}

type SelectorCheck struct {
	keyRe        *TemplatedRegexp
	callRe       *TemplatedRegexp
	comment      string
	requiredName string
	severity     Severity
}

func (c SelectorCheck) Meta() CheckMeta {
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

func (c SelectorCheck) String() string {
	if c.callRe != nil {
		return fmt.Sprintf("%s(%s:%s:%s)", SelectorCheckName, c.callRe.anchored, c.keyRe.anchored, c.requiredName)
	}
	return fmt.Sprintf("%s(%s:%s)", SelectorCheckName, c.keyRe.anchored, c.requiredName)
}

func (c SelectorCheck) Reporter() string {
	return SelectorCheckName
}

func (c SelectorCheck) Check(_ context.Context, entry *discovery.Entry, _ []*discovery.Entry) (problems []Problem) {
	expr := entry.Rule.Expr()
	if expr.SyntaxError() != nil {
		return problems
	}

	keyRe := c.keyRe.MustExpand(entry.Rule)

	var callRe *regexp.Regexp
	if c.callRe != nil {
		callRe = c.callRe.MustExpand(entry.Rule)
	}

	for _, src := range expr.Source() {
		src.WalkSources(func(s *source.Source, _ *source.Join, _ *source.Unless) {
			problems = append(problems, c.checkSource(keyRe, callRe, s, expr.Value.Pos)...)
		})
	}

	return problems
}

func (c SelectorCheck) findSelector(callRe *regexp.Regexp, s *source.Source) (*ast.VectorSelector, *ast.Call) {
	var call *ast.Call
	for _, op := range slices.Backward(s.Operations) {
		if callRe != nil && call == nil {
			if cl, ok := op.Node.(*ast.Call); ok {
				if callRe.MatchString(cl.Func.Name) {
					call = cl
					continue
				}
			}
		}
		if vs, ok := op.Node.(*ast.VectorSelector); ok {
			if callRe == nil || call != nil {
				return vs, call
			}
		}
	}
	return nil, nil
}

func (c SelectorCheck) checkSource(keyRe, callRe *regexp.Regexp, s *source.Source, pos diags.PositionRanges) (problems []Problem) {
	vs, call := c.findSelector(callRe, s)
	if vs == nil {
		return problems
	}

	if vs.Name != "" && !keyRe.MatchString(vs.Name) {
		return problems
	}
	for _, lm := range vs.AllMatchers() {
		if lm.Name == model.MetricNameLabel && !keyRe.MatchString(lm.Value) {
			return problems
		}
	}

	// With MetricsQL `or` filters every group must use the required matcher.
	if slices.ContainsFunc(vs.MatcherGroups(), func(group []*labels.Matcher) bool {
		return !slices.ContainsFunc(group, func(lm *labels.Matcher) bool {
			return lm.Name == c.requiredName
		})
	}) {
		prefix := "This vector selector"
		if call != nil {
			prefix = fmt.Sprintf("Vector selectors inside the `%s()` function", call.Func.Name)
		}
		problems = append(problems, Problem{
			Anchor:   AnchorAfter,
			Lines:    pos.Lines(),
			Reporter: c.Reporter(),
			Summary:  "required matcher missing",
			Details:  maybeComment(c.comment),
			Diagnostics: []diags.Diagnostic{
				{
					Pos:         pos,
					Expr:        nil,
					FirstColumn: int(vs.PosRange.Start) + 1,
					LastColumn:  int(vs.PosRange.End),
					Message: fmt.Sprintf(
						"%s must specify `%s` label. Please add a `{%s=\"...\"} matcher.",
						prefix, c.requiredName, c.requiredName,
					),
					Kind: diags.Issue,
				},
			},
			Severity: c.severity,
		})
	}
	return problems
}
