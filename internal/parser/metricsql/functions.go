package metricsql

import (
	"slices"
	"strings"

	mql "github.com/VictoriaMetrics/metricsql"

	"github.com/cloudflare/pint/internal/parser/ast"
	"github.com/cloudflare/pint/internal/parser/promql"
)

// transformSignature describes MetricsQL-only transform functions.
type transformSignature struct {
	returnType ast.ValueType
	// seriesArgs lists indexes of arguments that are series selectors / expressions,
	// -1 means the last argument and all means every argument.
	seriesArgs []int
	all        bool
}

var (
	firstArg = transformSignature{seriesArgs: []int{0}, all: false, returnType: ast.ValueTypeVector}
	lastArg  = transformSignature{seriesArgs: []int{-1}, all: false, returnType: ast.ValueTypeVector}
	allArgs  = transformSignature{seriesArgs: nil, all: true, returnType: ast.ValueTypeVector}
	noArgs   = transformSignature{seriesArgs: nil, all: false, returnType: ast.ValueTypeScalar}
)

// MetricsQL-only transform functions, see https://docs.victoriametrics.com/victoriametrics/metricsql/#transform-functions
// and https://docs.victoriametrics.com/victoriametrics/metricsql/#label-manipulation-functions.
var transformFunctions = map[string]transformSignature{
	"":                           allArgs,
	"union":                      allArgs,
	"bitmap_and":                 firstArg,
	"bitmap_or":                  firstArg,
	"bitmap_xor":                 firstArg,
	"buckets_limit":              lastArg,
	"drop_common_labels":         allArgs,
	"drop_empty_series":          firstArg,
	"end":                        noArgs,
	"histogram_quantiles":        lastArg,
	"histogram_share":            lastArg,
	"interpolate":                firstArg,
	"keep_last_value":            firstArg,
	"keep_next_value":            firstArg,
	"label_copy":                 firstArg,
	"label_del":                  firstArg,
	"label_graphite_group":       firstArg,
	"label_keep":                 firstArg,
	"label_lowercase":            firstArg,
	"label_map":                  firstArg,
	"label_match":                firstArg,
	"label_mismatch":             firstArg,
	"label_move":                 firstArg,
	"label_set":                  firstArg,
	"label_transform":            firstArg,
	"label_uppercase":            firstArg,
	"label_value":                firstArg,
	"labels_equal":               firstArg,
	"limit_offset":               lastArg,
	"now":                        noArgs,
	"prometheus_buckets":         firstArg,
	"rand":                       noArgs,
	"rand_exponential":           noArgs,
	"rand_normal":                noArgs,
	"range_avg":                  firstArg,
	"range_first":                firstArg,
	"range_last":                 firstArg,
	"range_linear_regression":    firstArg,
	"range_mad":                  firstArg,
	"range_max":                  firstArg,
	"range_min":                  firstArg,
	"range_normalize":            allArgs,
	"range_quantile":             lastArg,
	"range_stddev":               firstArg,
	"range_stdvar":               firstArg,
	"range_sum":                  firstArg,
	"range_trim_outliers":        lastArg,
	"range_trim_spikes":          lastArg,
	"range_trim_zscore":          lastArg,
	"range_zscore":               firstArg,
	"remove_resets":              firstArg,
	"running_avg":                firstArg,
	"running_max":                firstArg,
	"running_min":                firstArg,
	"running_sum":                firstArg,
	"smooth_exponential":         firstArg,
	"sort_by_label_numeric":      firstArg,
	"sort_by_label_numeric_desc": firstArg,
	"start":                      noArgs,
	"step":                       noArgs,
	"timezone_offset":            noArgs,
}

// Functions that exist in PromQL but take different arguments in MetricsQL.
var differentSignatures = []string{
	// PromQL: histogram_quantiles(v, "label", phi...)
	// MetricsQL: histogram_quantiles("phiLabel", phi..., buckets)
	"histogram_quantiles",
}

type aggrSignature struct {
	// exprIdx is the index of the series argument, -1 means the last argument.
	exprIdx int
	// paramIdx is the index of the parameter argument, -1 if there's none.
	paramIdx int
}

// Aggregation functions, see https://docs.victoriametrics.com/victoriametrics/metricsql/#aggregate-functions.
var aggrFunctions = map[string]aggrSignature{
	"any":            {exprIdx: 0, paramIdx: -1},
	"avg":            {exprIdx: 0, paramIdx: -1},
	"bottomk":        {exprIdx: 1, paramIdx: 0},
	"bottomk_avg":    {exprIdx: 1, paramIdx: 0},
	"bottomk_last":   {exprIdx: 1, paramIdx: 0},
	"bottomk_max":    {exprIdx: 1, paramIdx: 0},
	"bottomk_median": {exprIdx: 1, paramIdx: 0},
	"bottomk_min":    {exprIdx: 1, paramIdx: 0},
	"count":          {exprIdx: 0, paramIdx: -1},
	"count_values":   {exprIdx: 1, paramIdx: 0},
	"distinct":       {exprIdx: 0, paramIdx: -1},
	"geomean":        {exprIdx: 0, paramIdx: -1},
	"group":          {exprIdx: 0, paramIdx: -1},
	"histogram":      {exprIdx: 0, paramIdx: -1},
	"limitk":         {exprIdx: 1, paramIdx: 0},
	"mad":            {exprIdx: 0, paramIdx: -1},
	"max":            {exprIdx: 0, paramIdx: -1},
	"median":         {exprIdx: 0, paramIdx: -1},
	"min":            {exprIdx: 0, paramIdx: -1},
	"mode":           {exprIdx: 0, paramIdx: -1},
	"outliers_iqr":   {exprIdx: 0, paramIdx: -1},
	"outliers_mad":   {exprIdx: 1, paramIdx: 0},
	"outliersk":      {exprIdx: 1, paramIdx: 0},
	"quantile":       {exprIdx: 1, paramIdx: 0},
	"quantiles":      {exprIdx: -1, paramIdx: -1},
	"share":          {exprIdx: 0, paramIdx: -1},
	"stddev":         {exprIdx: 0, paramIdx: -1},
	"stdvar":         {exprIdx: 0, paramIdx: -1},
	"sum":            {exprIdx: 0, paramIdx: -1},
	"sum2":           {exprIdx: 0, paramIdx: -1},
	"topk":           {exprIdx: 1, paramIdx: 0},
	"topk_avg":       {exprIdx: 1, paramIdx: 0},
	"topk_last":      {exprIdx: 1, paramIdx: 0},
	"topk_max":       {exprIdx: 1, paramIdx: 0},
	"topk_median":    {exprIdx: 1, paramIdx: 0},
	"topk_min":       {exprIdx: 1, paramIdx: 0},
	"zscore":         {exprIdx: 0, paramIdx: -1},
}

// isPromQLShaped returns true if the aggregation can be represented using PromQL (param, expr) form.
func (as aggrSignature) isPromQLShaped(nargs int) bool {
	if as.paramIdx < 0 {
		return as.exprIdx == 0 && nargs == 1
	}
	return as.paramIdx == 0 && as.exprIdx == 1 && nargs == 2
}

// function returns the function definition for a call with given arguments.
// Functions shared with PromQL use PromQL signatures, so queries valid in both languages
// are analysed the same way. Arguments not covered by the signature use their own types.
func function(name string, args []ast.Node) *ast.Function {
	name = strings.ToLower(name)

	if f, ok := promql.Functions()[name]; ok && !slices.Contains(differentSignatures, name) {
		if len(args) <= len(f.ArgTypes) || f.Variadic < 0 {
			return f
		}
		fc := *f
		fc.ArgTypes = append(slices.Clip(fc.ArgTypes), argTypes(args[len(f.ArgTypes):])...)
		fc.Variadic = 0
		return &fc
	}

	types := argTypes(args)
	returnType := ast.ValueTypeVector

	switch {
	case mql.IsRollupFunc(name):
		if idx := getRollupArgIdx(name, len(args)); idx >= 0 && idx < len(types) {
			types[idx] = ast.ValueTypeMatrix
		}
	case transformFunctions[name].returnType != "":
		sig := transformFunctions[name]
		returnType = sig.returnType
		for i := range types {
			if sig.all || isSeriesArg(sig.seriesArgs, i, len(types)) {
				types[i] = ast.ValueTypeVector
			}
		}
		if name == "" && allOfType(args, ast.ValueTypeString) {
			// ("a", "b") is a list of strings, like the first argument of aggr_over_time().
			types = argTypes(args)
			returnType = ast.ValueTypeString
		}
	}

	return &ast.Function{
		Name:         name,
		ArgTypes:     types,
		Variadic:     0,
		ReturnType:   returnType,
		Experimental: false,
	}
}

// isPromQLMatrixArg returns true if the argument at idx of a PromQL function expects a range vector.
func isPromQLMatrixArg(name string, idx, nargs int) bool {
	name = strings.ToLower(name)
	if f, ok := promql.Functions()[name]; ok && !slices.Contains(differentSignatures, name) {
		return idx < len(f.ArgTypes) && f.ArgTypes[idx] == ast.ValueTypeMatrix
	}
	return mql.IsRollupFunc(name) && getRollupArgIdx(name, nargs) == idx
}

func getRollupArgIdx(name string, nargs int) int {
	return mql.GetRollupArgIdx(&mql.FuncExpr{Name: name, Args: make([]mql.Expr, nargs)})
}

func isSeriesArg(idxs []int, idx, nargs int) bool {
	for _, i := range idxs {
		if i == idx || (i < 0 && idx == nargs+i) {
			return true
		}
	}
	return false
}

func argTypes(args []ast.Node) []ast.ValueType {
	types := make([]ast.ValueType, 0, len(args))
	for _, arg := range args {
		types = append(types, arg.Type())
	}
	return types
}

func allOfType(args []ast.Node, vt ast.ValueType) bool {
	for _, arg := range args {
		if arg.Type() != vt {
			return false
		}
	}
	return len(args) > 0
}
