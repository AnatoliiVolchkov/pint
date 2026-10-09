---
layout: default
parent: Checks
grand_parent: Documentation
---

# promql/syntax

This is the most basic check that will report any syntax errors in a PromQL
query on any rule.

When rules use [MetricsQL](https://docs.victoriametrics.com/victoriametrics/metricsql/)
it also warns about a lookbehind window placed after a function call that
has no window of its own, for example `rate(foo)[5m]`.
PromQL doesn't allow this, but MetricsQL does and it doesn't do what it looks like:

- `rate(foo)` has no window, so MetricsQL picks one from the query step.
  vmalert sends `-datasource.queryStep` as the step, it's `5m` by default.
- `[5m]` is applied to results of `rate(foo)` and creates a subquery.
  A subquery used outside of rollup functions is wrapped in `default_rollup()`,
  which returns the last value.

The result is the same as `rate(foo)`, put the window inside the function call
to calculate the rate over the last `5m`: `rate(foo[5m])`.
Subqueries passed to rollup functions, like `max_over_time(rate(foo)[1h:1m])`,
are not reported.

It also warns about MetricsQL queries that VictoriaMetrics turns into subqueries with
[implicit query conversions](https://docs.victoriametrics.com/victoriametrics/metricsql/#implicit-query-conversions):

- a rollup function called on something other than a time series selector, for example
  `rate(sum(foo))`, which is calculated as `rate(sum(foo)[1i:1i])` over the query step,
- a lookbehind window placed after something other than a time series selector,
  for example `sum(foo)[5m]`, which is the subquery `sum(foo)[5m:]`.

These queries return unexpected results most of the time. VictoriaMetrics runs them by
default, rejects them when started with `-search.disableImplicitConversion` and logs them
with `-search.logImplicitConversion`. pint reports exactly the queries these flags act on,
using the same `IsLikelyInvalid()` function from the MetricsQL parser.
Write subqueries explicitly, for example `sum(rate(foo[5m]))` instead of `rate(sum(foo))`,
or `max_over_time(sum(foo)[1h:1m])` instead of `max_over_time(sum(foo)[1h])`.

It also warns about subqueries without a step, like `max_over_time(foo[1h:])`. Their step
comes from the query: vmalert sends `-datasource.queryStep`, 5m by default, or `step` from the
group `params`, while vmui and Grafana use the step of the graph. The same query then returns
different results in vmalert and on dashboards. VictoriaMetrics v1.153.0 returned 12, 60 and
240 for `count_over_time(foo[1h:])` with a query step of 5m, 1m and 15s, and always 60 for
`count_over_time(foo[1h:1m])`. Set the step explicitly, like `[1h:1m]`.

## Configuration

This check doesn't have any configuration options.

## How to enable it

This check is enabled by default.

## How to disable it

You can disable this check globally by adding this config block:

```js
checks {
  disabled = ["promql/syntax"]
}
```

You can also disable it for all rules inside a given file by adding
a comment anywhere in that file. Example:

```yaml
# pint file/disable promql/syntax
```

Or you can disable it per rule by adding a comment to it. Example:

```yaml
# pint disable promql/syntax
```

## How to snooze it

You can disable this check until a given time by adding a comment to it. Example:

```yaml
# pint snooze $TIMESTAMP promql/syntax
```

Where `$TIMESTAMP` is either [RFC3339](https://www.rfc-editor.org/rfc/rfc3339)
formatted or `YYYY-MM-DD`.
Adding this comment will disable `promql/syntax` *until* `$TIMESTAMP`, after which
the check will be re-enabled.
