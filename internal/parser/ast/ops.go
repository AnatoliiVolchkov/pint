package ast

// Op is an operator or keyword used in a query, its value is the operator text.
type Op string

const (
	OpAdd   Op = "+"
	OpSub   Op = "-"
	OpMul   Op = "*"
	OpDiv   Op = "/"
	OpMod   Op = "%"
	OpPow   Op = "^"
	OpAtan2 Op = "atan2"

	OpEql Op = "=="
	OpNeq Op = "!="
	OpGtr Op = ">"
	OpLss Op = "<"
	OpGte Op = ">="
	OpLte Op = "<="

	OpAnd    Op = "and"
	OpOr     Op = "or"
	OpUnless Op = "unless"

	// MetricsQL only binary operators.
	OpIf      Op = "if"
	OpIfNot   Op = "ifnot"
	OpDefault Op = "default"

	OpSum         Op = "sum"
	OpAvg         Op = "avg"
	OpCount       Op = "count"
	OpMin         Op = "min"
	OpMax         Op = "max"
	OpGroup       Op = "group"
	OpStddev      Op = "stddev"
	OpStdvar      Op = "stdvar"
	OpTopK        Op = "topk"
	OpBottomK     Op = "bottomk"
	OpCountValues Op = "count_values"
	OpQuantile    Op = "quantile"
	OpLimitK      Op = "limitk"
	OpLimitRatio  Op = "limit_ratio"

	// Used in `@ start()` and `@ end()` modifiers.
	OpStart Op = "start"
	OpEnd   Op = "end"

	// Aggregation and vector matching modifiers.
	OpBy         Op = "by"
	OpWithout    Op = "without"
	OpOn         Op = "on"
	OpIgnoring   Op = "ignoring"
	OpGroupLeft  Op = "group_left"
	OpGroupRight Op = "group_right"
	OpFill       Op = "fill"

	// PromQL range selector modifiers.
	OpAnchored Op = "anchored"
	OpSmoothed Op = "smoothed"

	// PromQL duration expression functions.
	OpStep  Op = "step"
	OpRange Op = "range"
	OpMinOf Op = "min_of"
	OpMaxOf Op = "max_of"
)

func (o Op) String() string {
	return string(o)
}

// IsComparisonOperator returns true if the operator is a comparison operator.
func (o Op) IsComparisonOperator() bool {
	switch o { // nolint: exhaustive
	case OpEql, OpNeq, OpLte, OpLss, OpGte, OpGtr:
		return true
	default:
		return false
	}
}

// IsSetOperator returns true if the operator is a set operator.
func (o Op) IsSetOperator() bool {
	switch o { // nolint: exhaustive
	case OpAnd, OpOr, OpUnless:
		return true
	default:
		return false
	}
}

// IsAggregatorWithParam returns true if the PromQL aggregation operator takes a parameter.
func (o Op) IsAggregatorWithParam() bool {
	switch o { // nolint: exhaustive
	case OpTopK, OpBottomK, OpCountValues, OpQuantile, OpLimitK, OpLimitRatio:
		return true
	default:
		return false
	}
}
