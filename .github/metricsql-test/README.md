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

Many rules here are broken on purpose, so pint exits with code 1. Expected result:
`Fatal=5 Bug=34 Warning=4`. Alerts that must have no problems:
`HostHighLoad`, `CriticalWithRunbook`, `HighWithoutRunbook`, `WarningWithoutRunbook`,
`HostOutOfMemory` in `rules/standard.yml`, `HostHighCpuLoad` and `DiskAlmostFull`.
