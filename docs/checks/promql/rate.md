---
layout: default
parent: Checks
grand_parent: Documentation
---

# promql/rate

This check inspects `rate()`, `irate()` and `increase()` function calls used in queries
to verify that:

- [Range queries](https://prometheus.io/docs/prometheus/latest/querying/basics/#range-vector-selectors)
  are using a valid time duration.
  This is done by first getting global `scrape_interval` value for selected
  Prometheus servers and comparing duration to it.
  It will report a bug if duration is less than 2x `scrape_interval` because
  Prometheus must have at least two samples to be able to calculate rate, so
  the time range used in queries must be at least 2x `scrape_interval` value.
  When using [MetricsQL](https://docs.victoriametrics.com/victoriametrics/metricsql/)
  `rate()`, `irate()`, `increase()` and `deriv()` only need a time range of at least
  1x `scrape_interval` for this Bug, because VictoriaMetrics also uses the last sample before
  the time range when calculating them, if it's close enough to the time range, see
  [Windows and the query step](#windows-and-the-query-step) for when that's not enough.
  `rate_prometheus()` and `increase_prometheus()` follow Prometheus semantics and are still
  required to use at least 2x `scrape_interval`.
- Metrics passed to `rate()`, `irate()` and `increase()` are counters.
  All three functions only work with counters and, although any metric type can be
  passed to them and will return calculated value, using a non-counter will cause
  problems. This is because counters are only allowed to increase in value and any
  value drop is interpreted as counter overflow.
  For gauge metrics use [`delta()`](https://prometheus.io/docs/prometheus/latest/querying/functions/#delta)
  or [`deriv()`](https://prometheus.io/docs/prometheus/latest/querying/functions/#deriv)
  functions instead.
- `rate()` is never called on result of `sum(counter)` since that will always return
  invalid results.
  Chaining `rate(sum(...))` is only possible when passing a metric produced via recording rules
  to `rate()` and so pint will try to find such chains.
  See [this blog post](https://www.robustperception.io/rate-then-sum-never-sum-then-rate/)
  for details.

## Common problems

### Metadata mismatch

Metric type checks are using
[metadata API](https://prometheus.io/docs/prometheus/latest/querying/api/#querying-metric-metadata).
Metadata is aggregated from all scraped metrics.

This can cause a few potential problems:

- You might have the same metric reported with multiple different types and Prometheus or pint won't know
  which time series is which type, because all we have to match a metric to a type is its name.
  Best solution here is to never export same name as multiple metrics with different types.
- If you change the type of some exported metric then the old type will still show up in metadata,
  plus the new one, as long as there's at least one target still exporting old metric type.
  If you accidentally exported some metric with wrong type, then fixed it, but pint is still complaining,
  then it's very likely that you didn't release your fix to all targets yet.

### Windows and the query step

vmalert evaluates rules with instant queries. VictoriaMetrics uses the last sample before
the window only if it's at most one query step before the window (its maxPrevInterval), so
for MetricsQL rules checked against a server with `backend = "victoriametrics"` the window
of `rate()`, `irate()` and `increase()` must be at least `2 x scrape_interval - step`, and at
least `scrape_interval`. `deriv()` only uses samples inside the window and needs
`2 x scrape_interval` whatever the step. Shorter windows are reported as a Warning: they can
have a single sample, `rate()` and `irate()` then return nothing and `increase()` returns 0.
Windows shorter than `scrape_interval` are still reported as a Bug, see above.

The query step is `step` from the group `params`, otherwise `queryStep` set for the server
in pint config or read from vmalert with `vmalertURI`, otherwise 5m, the vmalert default
`-datasource.queryStep`. `-search.maxStalenessInterval`, `-search.maxLookback` and
`-search.minStalenessInterval` flags of the server can lower or raise how far back the
sample before the window is used.

The scrape interval is `scrapeInterval` of the server in pint config, 1m by default. Metrics
can be written less often than that, for example by stream aggregation, recording rules of
another system or targets with their own scrape interval. So pint also measures the interval
between samples of every selector used in these functions with
`max(scrape_interval(selector[15m]))`, and with `[2h]` when there are fewer than two samples
in the last 15m. When the slowest series has a longer interval than the configured one, the
window is checked against the measured interval, as a Warning that names it. Windows are
never reported as a Bug because of a measured interval.

MetricsQL also allows `rate(foo)` without a lookbehind window, and windows relative to the
step, like `rate(foo[2i])`. pint works out such windows the same way:

- `rate(foo)` uses the step as the window, raised by `-search.minStalenessInterval` if the
  server sets it;
- `rate(foo[2i])` uses two steps.

This was checked with VictoriaMetrics v1.153.0 and a counter scraped every 10m:

- with step 5m `rate(foo[12m])` returned nothing or 0 on 36 of 120 instant queries,
  `rate(foo[15m])` never; with step 2m `rate(foo[15m])` failed 36 times, `rate(foo[18m])`
  never;
- `deriv(foo[15m])` returned 0 on half of the queries with step 5m and with step 15m,
  `deriv(foo[20m])` never;
- with step 5m `rate(foo)` was always empty and `increase(foo)` returned 0, while with step
  15m both were correct. `rate(foo[2i])` with step 5m returned nothing for half of every
  scrape interval.

### VictoriaMetrics metadata

VictoriaMetrics returns one metadata entry per metric name, and the type can come from any
target exposing a metric with that name. A single target with a wrong `# TYPE` line is enough
to make a counter look like a gauge. For example, a VictoriaMetrics server returned
`type: gauge` for `process_cpu_seconds_total`. So for servers using
`backend = "victoriametrics"`:

- a function that needs a counter used on a metric that metadata calls something else is
  reported as a Warning, not a Bug;
- if metadata says `gauge` but the metric name ends with `_total`, which is reserved for
  counters, it's reported as an Information, "metric metadata contradicts the metric name".
  Check the `# TYPE` line of targets exposing that metric.

## Configuration

This check doesn't have any configuration options.

## How to enable it

This check is enabled by default for all configured Prometheus servers.

Example:

```js
prometheus "prod" {
  uri     = "https://prometheus-prod.example.com"
  timeout = "60s"
  include = [
    "rules/prod/.*",
    "rules/common/.*",
  ]
}

prometheus "dev" {
  uri     = "https://prometheus-dev.example.com"
  timeout = "30s"
  include = [
    "rules/dev/.*",
    "rules/common/.*",
  ]
}
```

## How to disable it

You can disable this check globally by adding this config block:

```js
checks {
  disabled = ["promql/rate"]
}
```

You can also disable it for all rules inside a given file by adding
a comment anywhere in that file. Example:

```yaml
# pint file/disable promql/rate
```

Or you can disable it per rule by adding a comment to it. Example:

```yaml
# pint disable promql/rate
```

If you want to disable only individual instances of this check
you can add a more specific comment.

```yaml
# pint disable promql/rate($prometheus)
```

Where `$prometheus` is the name of Prometheus server to disable.

Example:

```yaml
# pint disable promql/rate(prod)
```

## How to snooze it

You can disable this check until a given time by adding a comment to it. Example:

```yaml
# pint snooze $TIMESTAMP promql/rate
```

Where `$TIMESTAMP` is either [RFC3339](https://www.rfc-editor.org/rfc/rfc3339)
formatted or `YYYY-MM-DD`.
Adding this comment will disable `promql/rate` *until* `$TIMESTAMP`, after which
the check will be re-enabled.
