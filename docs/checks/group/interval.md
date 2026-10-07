---
layout: default
parent: Checks
grand_parent: Documentation
---

# group/interval

This check will warn when a rule group declares an `interval` value greater
than 5 minutes.

Prometheus evaluates rules based on the group `interval` and each evaluation
becomes a sample for the resulting time series of a recording rule or a
potential alert state change for an alerting rule.

If you set the group `interval` to a value greater than 5 minutes you will
end up with gaps in the recording rule results when querying them, because
Prometheus defaults to a 5 minute query lookback. See the
[staleness](https://prometheus.io/docs/prometheus/latest/querying/basics/#staleness)
section of the Prometheus documentation for details.

Any sample older than 5 minutes is considered stale and won't be returned by
instant queries, so a recording rule that only produces a sample every
10 minutes will appear to be missing for half of that time when queried.

For alerting rules, a long interval will also cause flapping alerts, because
the alert state is only re-evaluated every `interval`. Between evaluations
the alert can resolve and then fire again on the next tick, unless you also
set `keep_firing_for` to a value greater than or equal to the group
`interval`.

Example rule group that will trigger this check:

```yaml
groups:
- name: example
  interval: 10m
  rules:
  - record: job:up:sum
    expr: sum(up) by(job)
```

To fix this problem, either lower the `interval` to `5m` or less, or, for
alerting rules, set `keep_firing_for` to a value greater than or equal to
the group `interval`.

## VictoriaMetrics

With `query_language = "metricsql"` rules are evaluated by vmalert. The 5 minute limit above
is the Prometheus `--query.lookback-delta`, VictoriaMetrics doesn't have it. There the check
works differently, it looks at the rules that read results of the group.

### How vmalert and VictoriaMetrics read rule results

- vmalert evaluates every rule with an instant query and sends the `-datasource.queryStep`
  flag as the query `step`, 5m by default. A `step` set in the `params` of a rule group
  replaces it for that group.
- VictoriaMetrics answers an instant query with the newest sample inside the lookback window,
  which is the `step`. A sample older than that is not found, so a rule reading the results
  of a group evaluated every 10m gets empty results for at least 5m after each evaluation:
  a recording rule produces nothing and an alerting rule resolves.
- These VictoriaMetrics flags change the window: `-search.maxStalenessInterval` and
  `-search.maxLookback` limit it, `-search.minStalenessInterval` raises it and
  `-search.setLookbackToStep` pins it to the step. pint reads them from the `/flags` endpoint
  of VictoriaMetrics servers, except with `--offline`.
- By default vmalert aligns the timestamp of rule results to the group `interval`
  (`eval_alignment`). A result gets written up to one interval after its timestamp, depending
  on when vmalert evaluates the group, so readers need a lookback of 2 x interval. With
  `eval_offset` or `eval_alignment: false` results are timestamped with the evaluation time and
  a lookback of 1 x interval is enough. The
  [vmalert documentation](https://docs.victoriametrics.com/victoriametrics/vmalert/#common-mistakes)
  recommends the same: a step at least 2x the resolution of the series, and `eval_offset` for
  chained groups.
- Range queries, used by graphs and subqueries, have no gaps: VictoriaMetrics looks back as
  far as the interval it detects between samples.
- Alerts don't flap in Alertmanager: vmalert sends firing alerts with `EndsAt` set to
  4 x max(group `interval`, `-rule.resendDelay`) from now and resends them on every evaluation.

All of this was checked with VictoriaMetrics and vmalert v1.153.0 in Docker. For example, with
`-datasource.queryStep=1m` an alerting rule reading a recording rule evaluated every 2m never
fired, with a step of 2m it saw the results 30 seconds out of every 2 minutes, with 4m or with
`eval_offset` on the writing group it saw them all the time.

### What is reported

For a group with `interval` set, pint finds rules that read its results: the metric of a
recording rule, or `ALERTS` and `ALERTS_FOR_STATE` series with `alertname` of an alerting rule.
For each reader it calculates the lookback: the `[window]` of a selector, or the query step
(`step` in group `params`, or `queryStep` set for the server in pint config, 5m by default)
adjusted by VictoriaMetrics flags.

- **Warning** if a reader looks back less than the interval. It gets empty results between
  evaluations, whatever the timing. Rules in the same group always see results of the previous
  evaluation, so they need a lookback longer than the interval.
- **Information** if a reader looks back less than 2 x interval and the group timestamps are
  aligned. It gets empty results only for some evaluation timings, which pint can't know.
- **Information** for a recording rule that no checked rule reads, when instant queries with
  the default step (5m) can't see its results between evaluations. Graphs are not affected.

Alerting rules that no rule reads are not reported. Selectors inside subqueries and
`[window]` values relative to the step are not reported either.

Example: `HighRequests` reads `job:requests:rate10m` with the default 5m step, but the group
writes it every 10m:

```yaml
groups:
- name: writer
  interval: 10m
  rules:
  - record: job:requests:rate10m
    expr: sum(rate(http_requests_total[10m])) by (job)
- name: readers
  interval: 1m
  rules:
  - alert: HighRequests
    expr: job:requests:rate10m > 100
```

To fix it, do one of:

- lower the `interval` of the writing group;
- read with a longer lookback, for example `params: {step: ["20m"]}` on the `readers` group;
- set `eval_offset` on the writing group and read with a lookback of at least the interval.

## Configuration

This check doesn't have any configuration options.

## How to enable it

This check is enabled by default.

## How to disable it

You can disable this check globally by adding this config block:

```js
checks {
  disabled = ["group/interval"]
}
```

You can also disable it for all rules inside a given file by adding
a comment anywhere in that file. Example:

```yaml
# pint file/disable group/interval
```

Or you can disable it per rule by adding a comment to it. Example:

```yaml
# pint disable group/interval
```

## How to snooze it

You can disable this check until a given time by adding a comment to it. Example:

```yaml
# pint snooze $TIMESTAMP group/interval
```

Where `$TIMESTAMP` is either [RFC3339](https://www.rfc-editor.org/rfc/rfc3339)
formatted or `YYYY-MM-DD`.
Adding this comment will disable `group/interval` *until* `$TIMESTAMP`, after which
the check will be re-enabled.
