package checks

import (
	"context"
	"errors"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
)

const (
	SyntaxCheckName    = "promql/syntax"
	SyntaxCheckDetails = "[Click here](https://prometheus.io/docs/prometheus/latest/querying/basics/) for PromQL documentation."

	SyntaxCheckMetricsQLDetails = "[Click here](https://docs.victoriametrics.com/victoriametrics/metricsql/) for MetricsQL documentation."
)

// queryLanguageName returns the name of the query language used in messages.
func queryLanguageName(lang ast.Language) string {
	if lang.Name() == metricsql.Name {
		return "MetricsQL"
	}
	return "PromQL"
}

func NewSyntaxCheck() SyntaxCheck {
	return SyntaxCheck{}
}

type SyntaxCheck struct{}

func (c SyntaxCheck) Meta() CheckMeta {
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

func (c SyntaxCheck) String() string {
	return SyntaxCheckName
}

func (c SyntaxCheck) Reporter() string {
	return SyntaxCheckName
}

func (c SyntaxCheck) Check(_ context.Context, entry *discovery.Entry, _ []*discovery.Entry) (problems []Problem) {
	expr := entry.Rule.Expr()
	if err := expr.SyntaxError(); err != nil {
		// This is pretty much dead code because PromQL always returns ParseErrors.
		// The only exception is if it panics and recover gives as a generic error,
		// which would require a bug in the parser itself.
		diag := diags.Diagnostic{
			Message:     err.Error(),
			Pos:         expr.Value.Pos,
			Expr:        nil,
			FirstColumn: 1,
			LastColumn:  len(expr.Value.Value),
			Kind:        diags.Issue,
		}

		if perr, ok := errors.AsType[*ast.ParseError](err); ok {
			diag = diags.Diagnostic{
				Message:     perr.Message,
				Pos:         expr.Value.Pos,
				Expr:        nil,
				FirstColumn: int(perr.PositionRange.Start) + 1,
				LastColumn:  int(perr.PositionRange.End),
				Kind:        diags.Issue,
			}
		}

		details := SyntaxCheckDetails
		if expr.Language().Name() == metricsql.Name {
			details = SyntaxCheckMetricsQLDetails
		}

		problems = append(problems, Problem{
			Anchor:      AnchorAfter,
			Lines:       expr.Value.Pos.Lines(),
			Reporter:    c.Reporter(),
			Summary:     queryLanguageName(expr.Language()) + " syntax error",
			Details:     details,
			Diagnostics: []diags.Diagnostic{diag},
			Severity:    Fatal,
		})
	} else if expr.Language().Name() == metricsql.Name {
		problems = append(problems, c.checkMisplacedWindows(expr)...)
	}
	return problems
}
