package parser

import (
	"fmt"
	"sync"

	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/parser/promql"
	"github.com/cloudflare/pint/internal/parser/source"
)

// PromQL is the default query language.
var PromQL ast.Language = promql.Language{}

// MetricsQL is the VictoriaMetrics query language.
var MetricsQL ast.Language = metricsql.Language{}

// NewLanguage returns the query language with given name.
func NewLanguage(name string) (ast.Language, error) {
	switch name {
	case "", promql.Name:
		return PromQL, nil
	case metricsql.Name:
		return MetricsQL, nil
	default:
		return nil, fmt.Errorf("unsupported query language %q, supported languages are: %s, %s", name, promql.Name, metricsql.Name)
	}
}

// PromQLExpr is a query expression, it's parsed using the configured query language.
type PromQLExpr struct {
	syntaxError error
	lang        ast.Language
	Value       *YamlNode
	query       *PromQLNode
	mu          *sync.Mutex
	source      []*source.Source
	hasSource   bool
}

func (pn *PromQLExpr) parse() {
	if pn.query == nil {
		pn.query, pn.syntaxError = DecodeExpr(pn.Language(), pn.Value.Value)
	}
}

// Language returns the query language used to parse this expression.
func (pn *PromQLExpr) Language() ast.Language {
	if pn.lang == nil {
		return PromQL
	}
	return pn.lang
}

func (pn *PromQLExpr) Source() []*source.Source {
	pn.mu.Lock()
	defer pn.mu.Unlock()
	pn.parse()
	if !pn.hasSource {
		pn.source = markMetricsQLStrings(pn.Language(), source.LabelsSource(pn.Value.Value, pn.query.Expr))
		pn.hasSource = true
	}
	return pn.source
}

func (pn *PromQLExpr) Query() *PromQLNode {
	pn.mu.Lock()
	defer pn.mu.Unlock()
	pn.parse()
	return pn.query
}

func (pn *PromQLExpr) SyntaxError() error {
	pn.mu.Lock()
	defer pn.mu.Unlock()
	pn.parse()
	return pn.syntaxError
}

// PromQLNode is used to turn the parsed query expression into a tree.
// This allows us to walk the tree up & down and look for either parents
// or children of specific type. Which is useful if you, for example,
// want to check if all vector selectors are wrapped inside function
// calls etc.
type PromQLNode struct {
	Parent   *PromQLNode
	Expr     ast.Node
	Children []*PromQLNode
}

// Tree takes a parsed query node and turns it into a Node
// instance with parent and children populated.
func tree(expr ast.Node, parent *PromQLNode) *PromQLNode {
	n := PromQLNode{
		Parent:   parent,
		Expr:     expr,
		Children: nil,
	}
	for _, child := range ast.Children(expr) {
		n.Children = append(n.Children, tree(child, &n))
	}

	return &n
}

// WalkUpExpr allows to iterate a promQLNode node looking for
// parents of specific type.
// The parser returns interfaces which makes it more difficult
// to figure out what kind of node we're dealing with, hence this
// helper takes a type parameter it tries to cast.
// It starts by checking the node passed to it and then walks
// up by visiting all parent nodes.
func WalkUpExpr[T ast.Node](node *PromQLNode) (nodes []*PromQLNode) {
	if node == nil {
		return nodes
	}
	if _, ok := node.Expr.(T); ok {
		nodes = append(nodes, node)
	}
	if node.Parent != nil {
		nodes = append(nodes, WalkUpExpr[T](node.Parent)...)
	}
	return nodes
}

// WalkDownExpr works just like WalkUpExpr but it walks the tree
// down, visiting all children.
// It also starts by checking the node passed to it before walking
// down the tree.
func WalkDownExpr[T ast.Node](node *PromQLNode) (nodes []*PromQLNode) {
	if _, ok := node.Expr.(T); ok {
		nodes = append(nodes, node)
	}
	for _, child := range node.Children {
		nodes = append(nodes, WalkDownExpr[T](child)...)
	}
	return nodes
}

// WalkUpParent works like WalkUpExpr but checks the parent
// (if present) instead of the node itself.
// It returns the nodes where the parent is of given type.
func WalkUpParent[T ast.Node](node *PromQLNode) (nodes []*PromQLNode) {
	if node == nil || node.Parent == nil {
		return nodes
	}
	if _, ok := node.Parent.Expr.(T); ok {
		nodes = append(nodes, node)
	}
	if node.Parent != nil {
		nodes = append(nodes, WalkUpParent[T](node.Parent)...)
	}
	return nodes
}

// DecodeExpr parses a query using given query language.
// Syntax errors are returned as *ast.ParseError.
func DecodeExpr(lang ast.Language, expr string) (*PromQLNode, error) {
	node, err := lang.ParseExpr(expr)
	if err != nil {
		return nil, err
	}
	return tree(node, nil), nil
}
