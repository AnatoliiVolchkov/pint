---
layout: default
parent: Checks
grand_parent: Documentation
---

# rule/vmalert

This check reports vmalert group and rule settings that are likely mistakes.
It only runs for rules parsed as [MetricsQL](https://docs.victoriametrics.com/victoriametrics/metricsql/),
which are evaluated by vmalert, and doesn't query any server, so it also runs with `--offline`.

It reports:

- `debug: true` on a group or a rule. vmalert then logs every datasource request of the rule
  and every change of its alerts' state at INFO level, see
  [debug mode](https://docs.victoriametrics.com/victoriametrics/vmalert/#debug-mode).
  It's meant for debugging a rule, not for permanent use.

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
