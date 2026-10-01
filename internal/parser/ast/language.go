package ast

import (
	"github.com/prometheus/prometheus/model/labels"
)

// Language is a query language that can be parsed into the AST.
type Language interface {
	// Name returns the name of this query language, as used in pint config.
	Name() string
	// ParseExpr parses a query and returns its AST.
	// Syntax errors are returned as *ParseError.
	ParseExpr(query string) (Node, error)
	// ParseMetricSelector parses a single time series selector and returns label matchers from it.
	ParseMetricSelector(selector string) ([]*labels.Matcher, error)
}

// ParseError is a syntax error with the position of the invalid query fragment.
type ParseError struct {
	// Cause is the most relevant error returned by the parser.
	Cause error
	// Original is the unmodified error returned by the parser, if it's different from Cause.
	Original error
	// Message describes the problem without any position information.
	Message       string
	PositionRange PositionRange
}

func (pe *ParseError) Error() string {
	if pe.Cause != nil {
		return pe.Cause.Error()
	}
	return pe.Message
}

func (pe *ParseError) Unwrap() error {
	return pe.Cause
}

// OriginalError returns the unmodified error returned by the parser.
func (pe *ParseError) OriginalError() error {
	if pe.Original != nil {
		return pe.Original
	}
	return pe
}
