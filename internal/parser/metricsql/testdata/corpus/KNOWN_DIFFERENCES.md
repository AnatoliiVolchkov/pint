# Known differences

## Syntax validity (TestParserDifferential)

There are no known differences. For every query in this directory pint's MetricsQL mode
accepts exactly the queries the unmodified upstream `github.com/VictoriaMetrics/metricsql`
parser accepts (`upstream_verdicts.txt`), and so does the source-aware fork.
`knownDifferences` in `differential_test.go` is empty; any entry added there must be
documented here with the reason.

Rejected queries always produce an `*ast.ParseError` with a message and a position inside
the query.

## PromQL vs MetricsQL findings (TestPromQLMetricsQLDifferential)

For expressions valid in both languages offline checks report the same problems in both
modes, except for these intentional differences (`languageDifferences` in
`internal/checks/differential_test.go`, every entry must match at least once):

1. `promql/nan` doesn't report modulo inside aggregations in MetricsQL mode: modulo by zero
   returns NaN and VictoriaMetrics aggregations skip NaN values. This is confirmed against
   real VictoriaMetrics by `promql_nan.yaml`.
2. `promql/nan` says "can return Inf" instead of "can return NaN or Inf" in MetricsQL mode
   for the same reason.
3. Constant-only expressions (`1 + 1`, `-1h`, `nan > bool 1`) are folded by the MetricsQL
   parser while parsing (upstream behavior). Diagnostics highlight the folded expression or
   the literal without its sign, `promql/impossible` has no comparison left to report and,
   because VictoriaMetrics evaluates a number as a single series without labels,
   `alerts/comparison` and `alerts/template` report constant alerts.

## Position mapping limitations shared with PromQL mode

These come from upstream pint's YAML position mapping (`internal/diags/position.go`) and
affect PromQL and MetricsQL mode alike:

- A block scalar whose value starts with a line break (`expr: |` followed by an empty line)
  maps offsets one byte off.
- Values that are only representable as escaped double-quoted YAML (control characters,
  `\n` escapes) are not mapped character by character.

Fixed in this repository: characters of a block scalar indicator such as `|-` (and of a
comment after it) were matched as part of the value, so a query starting with `-` or `+`
was highlighted one character off.
