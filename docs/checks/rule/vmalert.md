---
layout: default
parent: Checks
grand_parent: Documentation
---

# rule/vmalert

This check reports vmalert group and rule settings that are likely mistakes.
It only runs for rules parsed as [MetricsQL](https://docs.victoriametrics.com/victoriametrics/metricsql/),
which are evaluated by vmalert. Most of it only reads rule files and also runs with `--offline`;
the group limit check below queries VictoriaMetrics servers and is skipped with `--offline`.

It reports:

- `debug: true` on a group or a rule. vmalert then logs every datasource request of the rule
  and every change of its alerts' state at INFO level, see
  [debug mode](https://docs.victoriametrics.com/victoriametrics/vmalert/#debug-mode).
  It's meant for debugging a rule, not for permanent use.
- `latency_offset` in group `params` longer than the group's `eval_delay`, or than 30s, the
  default `-rule.evalDelay` of vmalert, when the group doesn't set `eval_delay`.
  VictoriaMetrics doesn't return samples newer than `latency_offset` and moves the time of a
  query back to `now - latency_offset`, while vmalert evaluates the rules `eval_delay` before
  now and stamps the results with that time, so alerts and recording rule results lag
  behind by the difference. vmalert docs recommend setting `eval_delay` equal to
  `latency_offset`. Groups with `eval_offset` aren't checked, vmalert ignores `eval_delay`
  there. If vmalert runs with a longer `-rule.evalDelay`, set `eval_delay` in the group.
- rules returning more results than vmalert allows: `limit` of their group, or
  `-rule.resultsLimit` of vmalert when the group doesn't set one and the server has
  `vmalertURI` in pint config. vmalert fails such a rule with
  `exec exceeded limit of N with M series` (or `alerts`) and drops all its results, so a
  recording rule writes nothing and an alerting rule fires nothing. pint sends `count(expr)`
  to the VictoriaMetrics servers of the rule file, one instant query per rule, only for rules
  with such a limit, and reports rules returning more results now. `limit: 0` means no limit.

All problems are reported as warnings.

## Configuration

This check doesn't have any configuration options.

## How to enable it

This check is enabled by default when `query_language = "metricsql"` is set in the `parser`
block. It isn't registered for PromQL configs.

## How to disable it

You can disable this check globally by adding this config block:

```js
checks {
  disabled = ["rule/vmalert"]
}
```

You can also disable it for all rules inside a given file by adding
a comment anywhere in that file. Example:

```yaml
# pint file/disable rule/vmalert
```

Or you can disable it per rule by adding a comment to it. Example:

```yaml
# pint disable rule/vmalert
```

## How to snooze it

You can disable this check until given time by adding a comment to it. Example:

```yaml
# pint snooze $TIMESTAMP rule/vmalert
```

Where `$TIMESTAMP` is either use [RFC3339](https://www.rfc-editor.org/rfc/rfc3339)
formatted  or `YYYY-MM-DD`.
Adding this comment will disable `rule/vmalert` *until* `$TIMESTAMP`, after that
check will be re-enabled.
