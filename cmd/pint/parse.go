package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
)

const (
	levelStep = 2

	queryLanguageFlag = "query-language"
)

var parseCmd = &cli.Command{
	Name:   "parse",
	Usage:  "Parse a query and print AST, use it for debugging or understanding query details.",
	Action: actionParse,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    queryLanguageFlag,
			Aliases: []string{"q"},
			Value:   "promql",
			Usage:   "Query language to use, one of: promql, metricsql.",
		},
	},
}

func printNode(w io.Writer, ident int, format string, a ...any) {
	prefix := strings.Repeat(" ", ident)
	_, _ = fmt.Fprintf(w, prefix+format+"\n", a...)
}

func expressionsString(nodes []ast.Node) string {
	s := make([]string, 0, len(nodes))
	for _, n := range nodes {
		s = append(s, n.String())
	}
	return strings.Join(s, ", ")
}

func parseExpressions(w io.Writer, nodes []ast.Node, level int) {
	printNode(w, level, "++ node: %v", expressionsString(nodes))
	level += levelStep
	printNode(w, level, "Expressions:")
	for _, e := range nodes {
		parseNode(w, e, level+levelStep)
	}
}

func parseNode(w io.Writer, node ast.Node, level int) {
	printNode(w, level, "++ node: %v", node)
	level += levelStep

	switch n := node.(type) {
	case *ast.AggregateExpr:
		printNode(w, level, "AggregateExpr:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Op: %v", n.Op)
		printNode(w, level, "* Expr: %v", n.Expr)
		printNode(w, level, "* Param: %v", n.Param)
		if len(n.Args) > 0 {
			printNode(w, level, "* Args: %v", expressionsString(n.Args))
		}
		printNode(w, level, "* Grouping: %v", n.Grouping)
		printNode(w, level, "* Without: %v", n.Without)
		if n.Limit > 0 {
			printNode(w, level, "* Limit: %v", n.Limit)
		}
		if len(n.Args) > 0 {
			parseExpressions(w, n.Args, level+levelStep)
		} else {
			parseNode(w, n.Expr, level+levelStep)
		}
	case *ast.BinaryExpr:
		printNode(w, level, "BinaryExpr:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Op: %v", n.Op)
		printNode(w, level, "* LHS: %v", n.LHS)
		printNode(w, level, "* RHS: %v", n.RHS)
		printNode(w, level, "* VectorMatching:")
		if n.VectorMatching != nil {
			printNode(w, level+levelStep, "* Card: %v", n.VectorMatching.Card)
			printNode(w, level+levelStep, "* MatchingLabels: %v", n.VectorMatching.MatchingLabels)
			printNode(w, level+levelStep, "* On: %v", n.VectorMatching.On)
			printNode(w, level+levelStep, "* Include: %v", n.VectorMatching.Include)
			if n.VectorMatching.Prefix != "" {
				printNode(w, level+levelStep, "* Prefix: %v", n.VectorMatching.Prefix)
			}
			if n.VectorMatching.FillValues.LHS != nil {
				printNode(w, level+levelStep, "* FillLHS: %v", *n.VectorMatching.FillValues.LHS)
			}
			if n.VectorMatching.FillValues.RHS != nil {
				printNode(w, level+levelStep, "* FillRHS: %v", *n.VectorMatching.FillValues.RHS)
			}
		}
		printNode(w, level, "* ReturnBool: %v", n.ReturnBool)
		if n.KeepMetricNames {
			printNode(w, level, "* KeepMetricNames: true")
		}
		parseNode(w, n.LHS, level+levelStep)
		parseNode(w, n.RHS, level+levelStep)
	case *ast.Call:
		printNode(w, level, "Call:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Func: %v", n.Func.Name)
		printNode(w, level, "* Args: %v", expressionsString(n.Args))
		if n.KeepMetricNames {
			printNode(w, level, "* KeepMetricNames: true")
		}
		parseExpressions(w, n.Args, level+levelStep)
	case *ast.ParenExpr:
		printNode(w, level, "ParenExpr:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Expr: %v", n.Expr)
		parseNode(w, n.Expr, level+levelStep)
	case *ast.SubqueryExpr:
		printNode(w, level, "SubqueryExpr:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Expr: %v", n.Expr)
		printNode(w, level, "* Step: %v", n.Step)
		if n.StepExpr != nil {
			printNode(w, level, "* StepExpr: %v", n.StepExpr)
			parseNode(w, n.StepExpr, level+levelStep)
		}
		printNode(w, level, "* Range: %v", n.Range)
		if n.RangeExpr != nil {
			printNode(w, level, "* RangeExpr: %v", n.RangeExpr)
			parseNode(w, n.RangeExpr, level+levelStep)
		}
		printNode(w, level, "* Offset: %v", n.Offset)
		if n.OriginalOffsetExpr != nil {
			printNode(w, level, "* OriginalOffsetExpr: %v", n.OriginalOffsetExpr)
			parseNode(w, n.OriginalOffsetExpr, level+levelStep)
		}
		printMetricsQLModifiers(w, level, n.At, n.Implicit, n.StepRelative)
		parseNode(w, n.Expr, level+levelStep)
	case *ast.OffsetExpr:
		printNode(w, level, "OffsetExpr:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Expr: %v", n.Expr)
		printNode(w, level, "* OriginalOffset: %v", n.OriginalOffset)
		printMetricsQLModifiers(w, level, n.At, false, false)
		parseNode(w, n.Expr, level+levelStep)
	case *ast.MatrixSelector:
		printNode(w, level, "MatrixSelector:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* VectorSelector: %v", n.VectorSelector)
		printNode(w, level, "* Range: %v", n.Range)
		if n.RangeExpr != nil {
			printNode(w, level, "* RangeExpr: %v", n.RangeExpr)
			parseNode(w, n.RangeExpr, level+levelStep)
		}
		printMetricsQLModifiers(w, level, nil, n.Implicit, n.StepRelative)
	case *ast.VectorSelector:
		printNode(w, level, "VectorSelector:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Name: %v", n.Name)
		printNode(w, level, "* Offset: %v", n.Offset)
		if n.OriginalOffsetExpr != nil {
			printNode(w, level, "* OriginalOffsetExpr: %v", n.OriginalOffsetExpr)
			parseNode(w, n.OriginalOffsetExpr, level+levelStep)
		}
		printNode(w, level, "* LabelMatchers: %v", n.LabelMatchers)
		for _, group := range n.OrMatchers {
			printNode(w, level, "* OrMatchers: %v", group)
		}
		if n.Anchored {
			printNode(w, level, "* Anchored: true")
		}
		if n.Smoothed {
			printNode(w, level, "* Smoothed: true")
		}
		printMetricsQLModifiers(w, level, n.At, false, false)
	case *ast.NumberLiteral:
		printNode(w, level, "NumberLiteral:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
	case *ast.StringLiteral:
		printNode(w, level, "StringLiteral:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
	case *ast.DurationExpr:
		printNode(w, level, "DurationExpr:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Op: %v", n.Op)
		printNode(w, level, "* LHS: %v", n.LHS)
		printNode(w, level, "* RHS: %v", n.RHS)
		printNode(w, level, "* Wrapped: %v", n.Wrapped)
	case *ast.UnaryExpr:
		printNode(w, level, "UnaryExpr:")
		level += levelStep
		printNode(w, level, "* Type: %v", n.Type())
		printNode(w, level, "* Op: %v", n.Op)
		printNode(w, level, "* Expr: %v", n.Expr)
	default:
		printNode(w, level, "! Unsupported node")
	}
}

// printMetricsQLModifiers prints fields that are only set when parsing MetricsQL.
func printMetricsQLModifiers(w io.Writer, level int, at ast.Node, implicit, stepRelative bool) {
	if at != nil {
		printNode(w, level, "* At: %v", at)
		parseNode(w, at, level+levelStep)
	}
	if implicit {
		printNode(w, level, "* Implicit: true")
	}
	if stepRelative {
		printNode(w, level, "* StepRelative: true")
	}
}

func parseQuery(w io.Writer, lang ast.Language, query string) error {
	expr, err := lang.ParseExpr(query)
	if perr, ok := errors.AsType[*ast.ParseError](err); ok {
		return perr.OriginalError()
	}
	if err != nil {
		return err
	}
	parseNode(w, expr, 0)
	return nil
}

func actionParse(_ context.Context, c *cli.Command) (err error) {
	err = initLogger(c.String(logLevelFlag), c.Bool(noColorFlag))
	if err != nil {
		return fmt.Errorf("failed to set log level: %w", err)
	}

	lang, err := parser.NewLanguage(c.String(queryLanguageFlag))
	if err != nil {
		return err
	}

	parts := c.Args().Slice()
	if len(parts) == 0 {
		return errors.New("a query string is required")
	}

	query := strings.Join(parts, " ")
	return parseQuery(os.Stdout, lang, query)
}
