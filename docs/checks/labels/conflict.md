---
layout: default
parent: Checks
grand_parent: Documentation
---

# labels/conflict

This check will look for any conflicting labels used in rules.
Below is the list of conflicts it looks for.

## External labels

### Recording rules

If any recording rules are manually setting some labels that are
already present in `external_labels` Prometheus configuration option
then both labels might conflict when metrics are ingested to another
Prometheus via federation or remote read/write.

Example:

Consider this recording rule:

```yaml
groups:
- name: recording rules
  rules:
  - record: prometheus_http_requests_total:rate2m
    expr: rate(prometheus_http_requests_total[2m])
    labels:
      cluster: dev
```

If this rule is deployed to Prometheus server with this configuration:

```yaml
global:
  external_labels:
    site: site01
    cluster: staging
```

Then making a `/federate` request will return time series with `cluster="staging"` label,
except for `prometheus_http_requests_total:rate2m` time series which will have `cluster="dev"`
label from the recording rule, which might cause unexpected inconsistencies.

If both the recording rule and `external_labels` config section uses same label value for the
`cluster` label then this effectively makes `cluster` label redundant, so in both cases it's
best to avoid setting labels used in `external_labels` on individual rules.

### Alerting rules

Same problem exists for alerting rules, with the only difference being how the label is
being used.
Any label listed in `external_labels` will be added to all firing alerts.
Setting `cluster` label on alerting rule will override `external_labels` and
can cause confusion when alert sent from `cluster="staging"` Prometheus has `cluster="dev"`
label set.

### VictoriaMetrics

VictoriaMetrics servers (`backend = "victoriametrics"`) have no configuration API. pint takes
their external labels from `externalLabels` of the server in the config and from the
`--external-label` flag, set them to the same labels as vmalert `-external.label` flags.

vmalert merges external labels into the labels of every alerting and recording rule, rule
labels take priority. A rule label with the same name and value as an external label is
redundant for both rule types, a rule label with a different value overrides the external
label for that rule. The check doesn't send any queries to VictoriaMetrics servers, so it also
runs with `--offline`.

## Series of more than one metric (vmalert)

With `query_language = "metricsql"` and a server using `backend = "victoriametrics"` this check
also warns when a rule query can return series of more than one metric. Examples are
`{__name__=~"foo|bar"}`, `{__name__="foo" or __name__="bar"}`, or a selector without a metric
name, when the metric name is still there in the results. Series that only differ by the
metric name get the same labels once the name is dropped, and the whole rule fails on every
evaluation:

- vmalert drops the metric name from alert labels, and replaces it with the record name for
  recording rules, then fails with `result contains metrics with the same labelset during
  evaluation`, see
  [series with the same labelset](https://docs.victoriametrics.com/victoriametrics/vmalert/#series-with-the-same-labelset);
- functions like `rate()` drop the metric name in VictoriaMetrics, which then rejects the
  query with `duplicate output timeseries`.

Keep the metric name in another label to fix it:

```yaml
- alert: Errors
  expr: 'label_replace({__name__=~"foo_errors|bar_errors"} > 0, "metric", "$1", "__name__", "(.+)")'
```

This was checked with vmalert v1.153.0, with two metrics that had series with the same other
labels:

| Rule | Result |
|---|---|
| alert `{__name__=~"x\|y"} > 0` | fails on every evaluation |
| recording rule `{__name__=~"x\|y"}` | fails, nothing is written |
| recording rule `rate({__name__=~"x\|y"}[1m])` | VictoriaMetrics rejects the query |
| alert with `label_replace(...)` as above | works |
| alert `x > 0 or y > 0` | works, `or` drops right hand series with the same labels |
| rule labels that override query labels | work, vmalert keeps the query value as `exported_<name>` |

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
  disabled = ["labels/conflict"]
}
```

You can also disable it for all rules inside a given file by adding
a comment anywhere in that file. Example:

```yaml
# pint file/disable labels/conflict
```

Or you can disable it per rule by adding a comment to it. Example:

```yaml
# pint disable labels/conflict
```

If you want to disable only individual instances of this check
you can add a more specific comment.

```yaml
# pint disable labels/conflict($prometheus)
```

Where `$prometheus` is the name of Prometheus server to disable.

Example:

```yaml
# pint disable labels/conflict(prod)
```

## How to snooze it

You can disable this check until a given time by adding a comment to it. Example:

```yaml
# pint snooze $TIMESTAMP labels/conflict
```

Where `$TIMESTAMP` is either [RFC3339](https://www.rfc-editor.org/rfc/rfc3339)
formatted or `YYYY-MM-DD`.
Adding this comment will disable `labels/conflict` *until* `$TIMESTAMP`, after which
the check will be re-enabled.
