# MetricsQL rule tests

Rules and pint configuration used to test this fork against a real VictoriaMetrics:
MetricsQL syntax, vmalert templates and rule file fields, and the alerting standard
(required labels and annotations, `runbook_url` required only for critical alerts).

This directory exists only in AnatoliiVolchkov/pint, upstream cloudflare/pint never touches it,
so it can't cause conflicts during upstream syncs. Nothing runs it automatically.

| Path | Contents |
|---|---|
| `.pint.hcl` | MetricsQL parser, VictoriaMetrics server, alerting standard |
| `rules/standard.yml` | alerts following and breaking the standard |
| `rules/runbook.yml` | `runbook_url` required for `severity: critical` only |
| `rules/vmalert-fields.yml` | vmalert-only group and rule fields, valid and broken |
| `k8s/` | ConfigMap, VMRule and PrometheusRule manifests, parsed in relaxed mode |

Run from the repository root:

```sh
make build
export VM_AUTH=$(printf 'user:password' | base64)   # VictoriaMetrics basic auth

# online, also queries VictoriaMetrics
./pint -c .github/metricsql-test/.pint.hcl lint .github/metricsql-test/rules .github/metricsql-test/k8s

# offline, no queries (VM_AUTH must still be set, any value works)
./pint -c .github/metricsql-test/.pint.hcl --offline lint .github/metricsql-test/rules .github/metricsql-test/k8s
```

## External labels

vmalert adds labels set with `-external.label=name=value` to every alert, and templates
can read them as `{{ $externalLabels.name }}`. Pass the same labels to pint, before the
subcommand, separated by commas or by repeating the flag, or with an environment variable:

```sh
./pint -c .github/metricsql-test/.pint.hcl --external-label account=alpha,dc=test \
  lint .github/metricsql-test/rules .github/metricsql-test/k8s

./pint -c .github/metricsql-test/.pint.hcl --external-label account=alpha --external-label dc=test \
  lint .github/metricsql-test/rules .github/metricsql-test/k8s

PINT_EXTERNAL_LABELS=account=alpha,dc=test \
  ./pint -c .github/metricsql-test/.pint.hcl lint .github/metricsql-test/rules .github/metricsql-test/k8s
```

The labels are added to `externalLabels` of the `vm` server and used by the
`alerts/external_labels` and `labels/conflict` checks. The flag takes precedence over
`PINT_EXTERNAL_LABELS`.

To see which external labels a vmalert really uses, read its flags. `/flags` only lists
flags that are set, and VictoriaMetrics stores every flag of a scraped vmalert as a
`flag` metric:

```sh
curl -s https://vmalert.example.com/flags | grep external.label
curl -s -u "$VM_USER:$VM_PASSWORD" https://prom.example.com/prometheus/api/v1/query \
  --data-urlencode 'query=flag{job="vmalert", name="external.label"}'
```

`is_set="false"` without a `value` label means vmalert sets no external labels, and
`{{ $externalLabels.name }}` renders an empty string.

## Expected result

Many rules here are broken on purpose, so pint exits with code 1. Expected result:
`Fatal=5 Bug=34 Warning=4`. Alerts that must have no problems:
`HostHighLoad`, `CriticalWithRunbook`, `HighWithoutRunbook`, `WarningWithoutRunbook`,
`HostOutOfMemory` in `rules/standard.yml`, `HostHighCpuLoad` and `DiskAlmostFull`.
