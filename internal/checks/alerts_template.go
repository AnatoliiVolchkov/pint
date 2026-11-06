package checks

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	textTemplate "text/template"
	"text/template/parse"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/timestamp"
	"github.com/prometheus/prometheus/promql"
	promTemplate "github.com/prometheus/prometheus/template"

	"github.com/cloudflare/pint/internal/diags"
	"github.com/cloudflare/pint/internal/discovery"
	"github.com/cloudflare/pint/internal/parser"
	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/metricsql"
	"github.com/cloudflare/pint/internal/parser/source"
)

const (
	TemplateCheckName             = "alerts/template"
	TemplateCheckSyntaxDetails    = `Supported template syntax is documented [here](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/#templating).`
	TemplateCheckReferenceDetails = "[Click here](https://prometheus.io/docs/prometheus/latest/configuration/template_reference/) for a full list of all available template functions."
)

var (
	templateDefs = []string{
		"{{$labels := .Labels}}",
		"{{$externalLabels := .ExternalLabels}}",
		"{{$externalURL := .ExternalURL}}",
		"{{$value := .Value}}",
	}
	templateDefsString = strings.Join(templateDefs, "")
	templateDefsLen    = len(templateDefsString)

	// vmalert defines more variables than Prometheus, see tplHeaders in
	// https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.153.0/app/vmalert/notifier/alert.go
	vmalertTemplateDefs = []string{
		"{{$type := .Type}}",
		"{{$expr := .Expr}}",
		"{{$interval := .Interval}}",
		"{{$alertID := .AlertID}}",
		"{{$groupID := .GroupID}}",
		"{{$activeAt := .ActiveAt}}",
		"{{$for := .For}}",
		"{{$isPartial := .IsPartial}}",
	}
	// Positions inside templates are calculated by subtracting templateDefsLen,
	// templateDialect.shift() corrects them for the longer vmalert header.
	vmalertTemplateDefsString = templateDefsString + strings.Join(vmalertTemplateDefs, "")

	templateFuncMap = textTemplate.FuncMap{
		"args":               dummyFuncMap,
		"externalURL":        dummyFuncMap,
		"first":              dummyFuncMap,
		"graphLink":          dummyFuncMap,
		"humanize":           dummyFuncMap,
		"humanize1024":       dummyFuncMap,
		"humanizeDuration":   dummyFuncMap,
		"humanizePercentage": dummyFuncMap,
		"humanizeTimestamp":  dummyFuncMap,
		"label":              dummyFuncMap,
		"match":              dummyFuncMap,
		"now":                dummyFuncMap,
		"parseDuration":      dummyFuncMap,
		"pathPrefix":         dummyFuncMap,
		"query":              dummyFuncMap,
		"reReplaceAll":       dummyFuncMap,
		"safeHtml":           dummyFuncMap,
		"sortByLabel":        dummyFuncMap,
		"stripDomain":        dummyFuncMap,
		"stripPort":          dummyFuncMap,
		"strvalue":           dummyFuncMap,
		"tableLink":          dummyFuncMap,
		"title":              dummyFuncMap,
		"tmpl":               dummyFuncMap,
		"toDuration":         dummyFuncMap,
		"toLower":            dummyFuncMap,
		"toTime":             dummyFuncMap,
		"toUpper":            dummyFuncMap,
		"urlQueryEscape":     dummyFuncMap,
		"value":              dummyFuncMap,
	}

	// vmalertMissingTemplateFuncs are Prometheus template functions that vmalert doesn't have,
	// templates using them fail to load in vmalert, see
	// https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.153.0/app/vmalert/templates/template.go
	vmalertMissingTemplateFuncs = []string{
		"graphLink",
		"tableLink",
		"tmpl",
		"toDuration",
		"urlQueryEscape",
	}

	// vmalertTemplateFuncs are template functions that vmalert provides on top of the
	// functions it shares with Prometheus, see
	// https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.153.0/app/vmalert/templates/template.go
	vmalertTemplateFuncs = textTemplate.FuncMap{
		"crlfEscape": func(s string) string {
			return strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(s)
		},
		"formatTime": func(layout string, _ any) (string, error) {
			return layout, nil
		},
		"htmlEscape": textTemplate.HTMLEscapeString,
		"jsonEscape": strconv.Quote,
		"parseDurationTime": func(s string) (time.Duration, error) {
			d, err := model.ParseDuration(s)
			return time.Duration(d), err
		},
		"pathEscape":  url.PathEscape,
		"queryEscape": url.QueryEscape,
		"quotesEscape": func(s string) string {
			return strings.ReplaceAll(s, `"`, `\"`)
		},
	}

	vmalertTemplateFuncMap = vmalertFuncMap()

	templatePool        = newTemplatePool(templateFuncMap)
	vmalertTemplatePool = newTemplatePool(vmalertTemplateFuncMap)
)

func newTemplatePool(funcs textTemplate.FuncMap) *sync.Pool {
	return &sync.Pool{
		New: func() any {
			t := textTemplate.
				New(TemplateCheckName).
				Funcs(funcs).
				Option(dummyFuncMap("missingkey=zero")) // this is only so that dummyFuncMap is called at least once
			return t
		},
	}
}

func vmalertFuncMap() textTemplate.FuncMap {
	funcs := maps.Clone(templateFuncMap)
	for _, name := range vmalertMissingTemplateFuncs {
		delete(funcs, name)
	}
	maps.Copy(funcs, vmalertTemplateFuncs)
	return funcs
}

// templateDialect selects template variables and functions available to rule templates.
// Prometheus is used for PromQL rules, vmalert for MetricsQL rules.
type templateDialect struct {
	pool    *sync.Pool
	defs    string
	vmalert bool
}

var (
	prometheusTemplates = templateDialect{pool: templatePool, defs: templateDefsString, vmalert: false}
	vmalertTemplates    = templateDialect{pool: vmalertTemplatePool, defs: vmalertTemplateDefsString, vmalert: true}
)

// shift returns how many more bytes this dialect header has compared to templateDefsString.
func (d templateDialect) shift() int {
	return len(d.defs) - templateDefsLen
}

func templateDialectFor(lang ast.Language) templateDialect {
	if lang != nil && lang.Name() == metricsql.Name {
		return vmalertTemplates
	}
	return prometheusTemplates
}

// vmalertTemplateData mirrors the data vmalert passes to templates (notifier.AlertTplData).
type vmalertTemplateData struct {
	ActiveAt       time.Time
	Labels         map[string]string
	ExternalLabels map[string]string
	ExternalURL    string
	Type           string
	Expr           string
	Value          float64
	AlertID        uint64
	GroupID        uint64
	Interval       time.Duration
	For            time.Duration
	IsPartial      bool
}

func dummyFuncMap(q string) string {
	return q
}

func NewTemplateCheck() TemplateCheck {
	return TemplateCheck{externalLabels: nil}
}

// WithExternalLabels returns a copy of the check that knows the external labels vmalert
// adds to all rules. vmalert merges them into rule labels, so annotation templates of
// MetricsQL rules can read them as $labels.<name>.
func (c TemplateCheck) WithExternalLabels(names []string) TemplateCheck {
	c.externalLabels = slices.Clone(names)
	return c
}

type TemplateCheck struct {
	externalLabels []string
}

func (c TemplateCheck) Meta() CheckMeta {
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

func (c TemplateCheck) String() string {
	return TemplateCheckName
}

func (c TemplateCheck) Reporter() string {
	return TemplateCheckName
}

func (c TemplateCheck) Check(ctx context.Context, entry *discovery.Entry, _ []*discovery.Entry) (problems []Problem) {
	if entry.Rule.AlertingRule == nil {
		return nil
	}

	if entry.Rule.AlertingRule.Expr.SyntaxError() != nil {
		return nil
	}

	src := entry.Rule.AlertingRule.Expr.Source()
	lang := entry.Rule.AlertingRule.Expr.Language()
	dialect := templateDialectFor(lang)
	var data any
	if dialect.vmalert {
		data = vmalertTemplateData{} // nolint: exhaustruct_v5
	} else {
		data = promTemplate.AlertTemplateData(map[string]string{}, map[string]string{}, "", promql.Sample{})
	}

	for _, label := range entry.Labels().Items {
		meta := analyzeTemplate(dialect, label.Value.Value)
		if syntaxErr, ok := c.checkSyntax(ctx, lang, label, data); ok {
			problems = append(problems, syntaxErr)
		} else {
			problems = append(problems, c.checkTemplateQueries(lang, entry.Rule, label, meta)...)
		}
		problems = append(problems, c.checkValueInLabels(label, meta)...)
		problems = append(problems, c.checkQueryLabels(entry.Group, entry.Rule, label, src, meta, nil)...)
	}

	// vmalert adds alertname, alertgroup and all rule labels to the labels available
	// in annotation templates, Prometheus only passes labels returned by the query.
	var annotationLabels map[string]struct{}
	if dialect.vmalert {
		annotationLabels = map[string]struct{}{
			model.AlertNameLabel: {},
			"alertgroup":         {},
		}
		for _, label := range entry.Labels().Items {
			annotationLabels[label.Key.Value] = struct{}{}
		}
		for _, name := range c.externalLabels {
			annotationLabels[name] = struct{}{}
		}
	}

	if entry.Rule.AlertingRule.Annotations != nil {
		for _, annotation := range entry.Rule.AlertingRule.Annotations.Items {
			meta := analyzeTemplate(dialect, annotation.Value.Value)
			if syntaxErr, ok := c.checkSyntax(ctx, lang, annotation, data); ok {
				problems = append(problems, syntaxErr)
			} else {
				problems = append(problems, c.checkTemplateQueries(lang, entry.Rule, annotation, meta)...)
			}
			problems = append(problems, c.checkQueryLabels(entry.Group, entry.Rule, annotation, src, meta, annotationLabels)...)
			problems = append(problems, c.checkHumanizeIsNeeded(entry.Rule.AlertingRule.Expr, annotation, meta)...)
		}
	}

	return problems
}

// checkSyntax validates that a label/annotation template parses and expands.
// It returns a Fatal problem and true when parsing fails, or false when the
// template is valid.
func (c TemplateCheck) checkSyntax(ctx context.Context, lang ast.Language, kv *parser.YamlKeyValue, data any) (problem Problem, found bool) {
	err := checkTemplateSyntax(ctx, lang, kv.Key.Value, kv.Value.Value, data)
	if err == nil {
		return problem, false
	}

	firstCol, lastCol := 1, len(kv.Value.Value)
	if terr, ok := errors.AsType[templateError](err); ok {
		firstCol, lastCol = terr.firstCol, terr.lastCol
	}
	return Problem{
		Anchor: AnchorAfter,
		Lines: diags.LineRange{
			First: kv.Key.Pos.Lines().First,
			Last:  kv.Value.Pos.Lines().Last,
		},
		Reporter: c.Reporter(),
		Summary:  "template syntax error",
		Details:  TemplateCheckSyntaxDetails,
		Severity: Fatal,
		Diagnostics: []diags.Diagnostic{
			{
				Message:     fmt.Sprintf("Template failed to parse with this error: `%s`.", err),
				Pos:         kv.Value.Pos,
				Expr:        nil,
				FirstColumn: firstCol,
				LastColumn:  lastCol,
				Kind:        diags.Issue,
			},
		},
	}, true
}

// checkValueInLabels reports labels whose value depends on the query result,
// which would create a new alert identity on every value change.
func (c TemplateCheck) checkValueInLabels(label *parser.YamlKeyValue, meta templateMeta) (problems []Problem) {
	for _, msg := range checkForValueInLabels(meta) {
		problems = append(problems, Problem{
			Anchor: AnchorAfter,
			Lines: diags.LineRange{
				First: label.Key.Pos.Lines().First,
				Last:  label.Value.Pos.Lines().Last,
			},
			Reporter: c.Reporter(),
			Summary:  "value used in labels",
			Details:  "",
			Severity: Bug,
			Diagnostics: []diags.Diagnostic{
				{
					Message:     msg,
					Pos:         label.Value.Pos,
					Expr:        nil,
					FirstColumn: 1,
					LastColumn:  len(label.Value.Value),
					Kind:        diags.Issue,
				},
			},
		})
	}
	return problems
}

func (c TemplateCheck) checkHumanizeIsNeeded(expr parser.PromQLExpr, ann *parser.YamlKeyValue, a templateMeta) (problems []Problem) {
	if !a.isValid {
		return problems
	}
	if !a.hasValue {
		return problems
	}
	if a.hasHumanize {
		return problems
	}

	valAliases := a.aliases.varAliases(".Value")
	for _, src := range expr.Source() {
		call := isRateResult(src)
		if call != nil {
			dgs := []diags.Diagnostic{
				{
					Message:     fmt.Sprintf("`%s()` will produce results that are hard for humans to read.", call.Func.Name),
					Pos:         expr.Value.Pos,
					Expr:        expr.Query().Expr,
					FirstColumn: int(call.PosRange.Start) + 1,
					LastColumn:  int(call.PosRange.End),
					Kind:        diags.Context,
				},
			}
			for _, v := range a.vars {
				for _, va := range valAliases {
					if v.value[0] == va {
						dgs = append(dgs, diags.Diagnostic{
							Message:     "Use one of the humanize template functions to make the result more readable.",
							Pos:         ann.Value.Pos,
							Expr:        nil,
							FirstColumn: v.column,
							LastColumn:  v.column + len(v.value[0]) - 1,
							Kind:        diags.Issue,
						})
					}
				}
			}

			problems = append(problems, Problem{
				Anchor: AnchorAfter,
				Lines: diags.LineRange{
					First: min(expr.Value.Pos.Lines().First, ann.Value.Pos.Lines().First),
					Last:  max(expr.Value.Pos.Lines().Last, ann.Value.Pos.Lines().Last),
				},
				Reporter:    c.Reporter(),
				Summary:     "use humanize filters for the results",
				Details:     TemplateCheckReferenceDetails,
				Diagnostics: dgs,
				Severity:    Information,
			})
		}
	}
	return problems
}

func isRateResult(src *source.Source) *ast.Call {
	if src.Type == source.AggregateSource {
		switch src.Operation() {
		case "count", "count_values", "group":
			return nil
		}
	}

	call, ok := source.MostOuterOperation[*ast.Call](src)
	if !ok {
		return nil
	}

	switch call.Func.Name {
	case "rate", "irate", "deriv":
		return call
	default:
		return nil
	}
}

func queryFunc(lang ast.Language) promTemplate.QueryFunc {
	return func(_ context.Context, expr string, _ time.Time) (promql.Vector, error) {
		_, err := lang.ParseExpr(expr)
		if perr, ok := errors.AsType[*ast.ParseError](err); ok {
			return nil, perr.OriginalError()
		}
		if err != nil {
			return nil, err
		}
		// return a single sample so template using `... | first` don't fail
		return promql.Vector{{}}, nil
	}
}

func normalizeTemplateError(name string, err error) error {
	e := strings.TrimPrefix(err.Error(), "template: "+name+":")
	if v := strings.SplitN(e, ":", 2); len(v) > 1 {
		e = strings.TrimPrefix(v[1], " ")
	}
	return errors.New(e)
}

func maybeExpandError(err error) error {
	if e := errors.Unwrap(err); e != nil {
		return e
	}
	return err
}

// templateError wraps a template parse or execution error with the exact
// column range in the source text where the error occurred.
type templateError struct {
	msg      string
	firstCol int
	lastCol  int
}

func (e templateError) Error() string {
	return e.msg
}

var (
	templateErrorPrefixRe = regexp.MustCompile(`^template: ([^:]+):(\d+):(\d+):`)
	templateErrorTokenRe  = regexp.MustCompile(`at <([^>]+)>`)
)

// newTemplateError builds a templateError from a raw template error,
// normalizing the message and extracting column positions when possible.
func newTemplateError(rawErr error, name, text string, defsLen int) templateError {
	te := templateError{
		msg:      normalizeTemplateError(name, rawErr).Error(),
		firstCol: 1,
		lastCol:  len(text),
	}

	msg := rawErr.Error()
	m := templateErrorPrefixRe.FindStringSubmatch(msg)
	if m == nil || m[1] != name {
		return te
	}
	if defsLen != templateDefsLen {
		te.msg = vmalertTemplateErrorMessage(te.msg)
	}

	lineNum, _ := strconv.Atoi(m[2])
	offset, _ := strconv.Atoi(m[3])

	// The template header is prepended to text and has no newlines,
	// so only line 1 offsets need adjustment.
	if lineNum == 1 {
		offset -= defsLen
	}

	// Find the byte position of the target line in user text.
	var pos int
	for range lineNum - 2 {
		pos += strings.IndexByte(text[pos:], '\n') + 1
	}
	pos += offset

	te.firstCol = pos + 1
	te.lastCol = pos + 1

	if tokenM := templateErrorTokenRe.FindStringSubmatch(msg); tokenM != nil {
		if idx := strings.Index(text[pos:], tokenM[1]); idx >= 0 {
			te.firstCol = pos + idx + 1
			te.lastCol = pos + idx + len(tokenM[1])
		}
	}

	return te
}

// checkTemplateSyntax parses and expands a template value. If parsing or
// execution fails it returns a templateError with the normalized message
// and, when possible, the exact column range where the error occurred.
func checkTemplateSyntax(ctx context.Context, lang ast.Language, name, text string, data any) error {
	dialect := templateDialectFor(lang)
	tmpl := promTemplate.NewTemplateExpander(
		ctx,
		dialect.defs+text,
		name,
		data,
		model.Time(timestamp.FromTime(time.Now())),
		queryFunc(lang),
		nil,
		nil,
	)
	if dialect.vmalert {
		// The Prometheus expander also knows functions vmalert doesn't have,
		// parse with the vmalert function set first to report them.
		_, err := textTemplate.New(name).Funcs(vmalertTemplateFuncMap).Option("missingkey=zero").Parse(dialect.defs + text)
		if err != nil {
			return newTemplateError(maybeExpandError(err), name, text, len(dialect.defs))
		}
		tmpl.Funcs(vmalertTemplateFuncs)
	}

	if err := tmpl.ParseTest(); err != nil {
		return newTemplateError(maybeExpandError(err), name, text, len(dialect.defs))
	}

	if _, err := tmpl.Expand(); err != nil {
		return newTemplateError(maybeExpandError(err), name, text, len(dialect.defs))
	}

	return nil
}

type templateMeta struct {
	aliases     aliasMap
	vars        []tmplVar
	uses        []templateQueryUse
	hasValue    bool
	hasHumanize bool
	isValid     bool
}

// parseTemplate parses a label or annotation value and calls fn with the
// result. ok is false if it can't be parsed.
func parseTemplate(dialect templateDialect, text string, fn func(*textTemplate.Template)) (ok bool) {
	t := dialect.pool.Get().(*textTemplate.Template)
	defer dialect.pool.Put(t)

	tt, err := t.Parse(dialect.defs + text)
	if err != nil {
		return false
	}
	fn(tt)
	return true
}

func analyzeTemplate(dialect templateDialect, text string) templateMeta {
	var meta templateMeta
	ok := parseTemplate(dialect, text, func(tt *textTemplate.Template) {
		aliases := aliasesForTemplate(tt)
		vars := make([]tmplVar, 0, len(tt.Root.Nodes))
		for _, node := range tt.Root.Nodes {
			vars = append(vars, getVariables(node)...)
		}
		meta = templateMeta{
			aliases: aliases,
			vars:    vars,
			uses: walkTemplateNode(tt.Root, queryScope{
				aliases: map[string]templateQuery{},
				dot:     nil,
			}),
			hasValue:    hasValue(tt, aliases),
			hasHumanize: hasHumanize(tt, aliases),
			isValid:     true,
		}
		if shift := dialect.shift(); shift != 0 {
			shiftTemplateVars(meta.vars, shift)
			for i := range meta.uses {
				meta.uses[i].query.pos = shiftPositionRange(meta.uses[i].query.pos, shift)
				for j := range meta.uses[i].labels {
					meta.uses[i].labels[j].pos = shiftPositionRange(meta.uses[i].labels[j].pos, shift)
				}
			}
		}
	})
	if !ok {
		return templateMeta{
			aliases:     aliasMap{aliases: nil},
			vars:        nil,
			uses:        nil,
			hasValue:    false,
			hasHumanize: false,
			isValid:     false,
		}
	}
	return meta
}

func checkForValueInLabels(a templateMeta) (msgs []string) {
	if !a.isValid {
		return nil
	}
	valAliases := a.aliases.varAliases(".Value")
	for _, v := range a.vars {
		for _, name := range v.value {
			if slices.Contains(valAliases, name) {
				msg := fmt.Sprintf("Using `%s` in labels will generate a new alert on every value change, move it to annotations.", name)
				msgs = append(msgs, msg)
			}
		}
	}
	return msgs
}

func containsAliasedNode(am aliasMap, node parse.Node, alias string) (string, bool) {
	valAliases := am.varAliases(alias)
	for _, vars := range getVariables(node) {
		for _, v := range vars.value {
			if slices.Contains(valAliases, v) {
				return v, true
			}
		}
	}
	return "", false
}

func hasValue(tt *textTemplate.Template, aliases aliasMap) bool {
	for _, node := range tt.Root.Nodes {
		if _, ok := containsAliasedNode(aliases, node, ".Value"); ok {
			return true
		}
	}
	return false
}

func hasHumanize(tt *textTemplate.Template, aliases aliasMap) bool {
	for _, node := range tt.Root.Nodes {
		if _, ok := containsAliasedNode(aliases, node, ".Value"); !ok {
			continue
		}
		if n, ok := node.(*parse.ActionNode); ok {
			for _, cmd := range n.Pipe.Cmds {
				for _, arg := range cmd.Args {
					if m, ok := arg.(*parse.IdentifierNode); ok {
						for _, f := range []string{"humanize", "humanize1024", "humanizePercentage", "humanizeDuration", "printf"} {
							if slices.Contains(aliases.varAliases(f), m.Ident) {
								return true
							}
						}
					}
				}
			}
		}
	}

	return false
}

type aliasMap struct {
	aliases map[string]map[string]struct{}
}

func (am aliasMap) varAliases(k string) (vals []string) {
	vals = append(vals, k)
	if as, ok := am.aliases[k]; ok {
		for val := range as {
			vals = append(vals, am.varAliases(val)...)
		}
	}
	return vals
}

func aliasesForTemplate(t *textTemplate.Template) aliasMap {
	aliases := aliasMap{aliases: map[string]map[string]struct{}{}}
	for _, n := range t.Root.Nodes {
		getAliases(n, &aliases)
	}
	return aliases
}

func getAliases(node parse.Node, aliases *aliasMap) {
	if n, ok := node.(*parse.ActionNode); ok {
		if len(n.Pipe.Decl) == 1 && !n.Pipe.IsAssign && len(n.Pipe.Cmds) == 1 {
			for _, cmd := range n.Pipe.Cmds {
				for _, arg := range cmd.Args {
					for _, k := range getVariables(arg) {
						for _, d := range n.Pipe.Decl {
							for _, v := range getVariables(d) {
								if _, ok := aliases.aliases[k.value[0]]; !ok {
									aliases.aliases[k.value[0]] = map[string]struct{}{}
								}
								aliases.aliases[k.value[0]][v.value[0]] = struct{}{}
							}
						}
					}
				}
			}
		}
	}
}

type tmplVar struct {
	value  []string
	column int
}

func shiftTemplateVars(vars []tmplVar, shift int) {
	for i := range vars {
		vars[i].column -= shift
	}
}

func shiftPositionRange(pr ast.PositionRange, shift int) ast.PositionRange {
	return ast.PositionRange{Start: pr.Start - ast.Pos(shift), End: pr.End - ast.Pos(shift)}
}

func getVariables(node parse.Node) (vars []tmplVar) {
	switch n := node.(type) {
	case *parse.ActionNode:
		if len(n.Pipe.Decl) == 0 && len(n.Pipe.Cmds) > 0 {
			vars = append(vars, getVariables(n.Pipe.Cmds[0])...)
		}
	case *parse.CommandNode:
		for _, arg := range n.Args {
			vars = append(vars, getVariables(arg)...)
		}
	case *parse.FieldNode:
		ident := make([]string, len(n.Ident))
		copy(ident, n.Ident)
		ident[0] = "." + ident[0]
		vars = append(vars, tmplVar{
			value:  ident,
			column: int(n.Pos) + 1 - templateDefsLen,
		})
	case *parse.VariableNode:
		vars = append(vars, tmplVar{
			value:  n.Ident,
			column: int(n.Pos) + 1 - templateDefsLen,
		})
	}
	return vars
}

func findTemplateVariables(dialect templateDialect, text string) (vars []tmplVar, aliases aliasMap, ok bool) {
	aliases.aliases = map[string]map[string]struct{}{}
	ok = parseTemplate(dialect, text, func(tt *textTemplate.Template) {
		for _, node := range tt.Root.Nodes {
			getAliases(node, &aliases)
			vars = append(vars, getVariables(node)...)
		}
	})
	shiftTemplateVars(vars, dialect.shift())
	return vars, aliases, ok
}

// missingLabel checks if none of the sources can return the given label. If so
// it returns why and where in the query. ok is false if any source can have it.
func missingLabel(src []*source.Source, name string) (reason string, fragment ast.PositionRange, ok bool) {
	for _, s := range src {
		if s.DeadInfo != nil || s.CanHaveLabel(name) {
			continue
		}
		reason, fragment = s.LabelExcludeReason(name)
		return reason, fragment, true
	}
	return "", ast.PositionRange{}, false
}

// nonExistentLabelProblem builds the "template uses non-existent label" problem
// shared by both label checks.
func (c TemplateCheck) nonExistentLabelProblem(rule parser.Rule, issue, context diags.Diagnostic) Problem {
	return Problem{
		Anchor:      AnchorAfter,
		Lines:       rule.Lines,
		Reporter:    c.Reporter(),
		Summary:     "template uses non-existent label",
		Details:     "",
		Severity:    Bug,
		Diagnostics: []diags.Diagnostic{issue, context},
	}
}

// checkQueryLabels reports templates using labels that the query results won't have.
// extraLabels are labels added by the rule evaluator that are always available.
func (c TemplateCheck) checkQueryLabels(group *parser.Group, rule parser.Rule, label *parser.YamlKeyValue, src []*source.Source, a templateMeta, extraLabels map[string]struct{}) (problems []Problem) {
	if !a.isValid {
		return nil
	}

	done := map[string]struct{}{}
	labelsAliases := a.aliases.varAliases(".Labels")
	for _, v := range a.vars {
		// We need both the .Labels alias and the label name after it: .Labels.instance.
		if len(v.value) < 2 || !slices.Contains(labelsAliases, v.value[0]) {
			continue
		}
		name := v.value[1]
		if _, ok := done[name]; ok {
			continue
		}
		done[name] = struct{}{}

		if group != nil && group.Labels != nil && group.Labels.GetValue(name) != nil {
			continue
		}
		if _, ok := extraLabels[name]; ok {
			continue
		}
		reason, fragment, ok := missingLabel(src, name)
		if !ok {
			continue
		}
		problems = append(problems, c.nonExistentLabelProblem(
			rule,
			diags.Diagnostic{
				Message:     fmt.Sprintf("The template is using `%s` label but the query results won't have this label.", name),
				Pos:         label.Value.Pos,
				Expr:        nil,
				FirstColumn: v.column,
				LastColumn:  v.column + len(name),
				Kind:        diags.Issue,
			},
			diags.Diagnostic{
				Message:     reason,
				Pos:         rule.AlertingRule.Expr.Value.Pos,
				Expr:        rule.Expr().Query().Expr,
				FirstColumn: int(fragment.Start) + 1,
				LastColumn:  int(fragment.End),
				Kind:        diags.Context,
			},
		))
	}

	return problems
}
