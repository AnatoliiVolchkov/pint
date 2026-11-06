# Testing against real VictoriaMetrics and real alert files

Real host names, tenants, accounts and rule files are not stored in this repository, which
is public. `prom.example.com` and `vmalert.example.com` below stand for a real
VictoriaMetrics and vmalert. Ask the user for the URIs, and keep them out of commits.

## The server

- **Credentials.** Basic auth. **Never commit or print the credentials.** Ask the user for
  them if you don't have them. Pass them only through the environment:
  `export VM_AUTH=$(printf 'user:password' | base64)`. The pint configs use
  `headers = { "Authorization" = "Basic ${ENV_VM_AUTH}" }`, because pint exposes an
  environment variable `X` as `ENV_X`.
- **Load rule from the user:** "Keep load low: no aggressive parallelism; no unnecessary
  repeated queries; reuse results where possible."
  - The configs use `concurrency = 2` and `rateLimit = 5`.
  - Probe with a few cheap instant queries, such as `count(...)` or a selector narrowed to
    one job, instead of broad regex-on-`__name__` queries.
  - Keep and reuse output files instead of re-running online lints.
- **`scrapeInterval` must be the real one.** For VictoriaMetrics servers, pint takes the
  scrape interval only from `scrapeInterval` in the `prometheus` block, and uses 1m when it
  isn't set. The `promql/rate` window checks are only as correct as that value.
- **`VM_AUTH` must be set.** If it is unset, the config fails to load, even with
  `--offline`. Any value works offline.
- **Mistake to avoid:** running an online lint with a wrong or empty `VM_AUTH`. pint then
  sends a burst of requests that all get `401 Unauthorized`. Scripts should fail fast when
  `VM_AUTH` is unset, for example `: "${VM_AUTH:?...}"`.
- **Recommended config for a cluster:**
  - `headers` with Authorization from an env var plus `publicURI`, so the URL with a username
    doesn't show in every message;
  - `retention` set to the vmstorage `-retentionPeriod`;
  - low `concurrency` and `rateLimit` for CI.

Useful single queries (curl with `-u` from your environment):

```sh
# Is a metric present?
query=count(some_metric)

# Which external labels does vmalert use?
query=flag{job="vmalert", name="external.label"}

# vmalert query step
query=flag{name="datasource.queryStep"}

# Does VictoriaMetrics reject or log implicit subquery conversions?
query=flag{name=~"search.(disable|log)ImplicitConversion"}

# Is metadata enabled? (the default since v1.137.0)
query=flag{name="enableMetadata"}

# Metric metadata, see the caveat below
GET /api/v1/metadata?metric=<name>
```

## In-repo real-world suite: `.github/metricsql-test/`

- **What it is:** an alerting standard plus vmalert and Kubernetes manifests. It is
  fork-only, and nothing runs it automatically. The README there has the commands.
- **Expected result:** `Fatal=5 Bug=34 Warning=4`. Re-check it after any change to checks.
- **Alert standard it enforces:**
  - group: `name`, `interval`;
  - rule: `alert`, `expr`, `for`;
  - labels: `severity` (critical, high or warning), `team_name` and `tags`;
  - annotations: `actions`, `summary`, `description`, `grafana_url`, `runbook_url` and
    `logs_url`;
  - `runbook_url` is required only for `severity: critical`.
- **Rule from the user:** don't edit `.github/pint/pint.hcl`. That is upstream pint's own
  config ("dont touch files related to pint"). The fork's standard lives only in
  `.github/metricsql-test/.pint.hcl`.

## Private rule sets outside the repository

The user keeps real rule files and reports next to the checkouts, outside version control.
They may not exist for other agents. `.claude/tools/compare-binaries.sh` uses them when
`PINT_PRIVATE_RULES` points at them.

What a check of real alerts in ConfigMaps taught us (2026-10-05,
relaxed mode, `relaxed = ["(.*/)?k8s/.*"]`):

- **External labels matter.** Pass the deployment's labels with `--external-label`. Without
  them, templates using those labels give false `alerts/template` and
  `alerts/external_labels` Bugs.
- **Disable data checks when the rules target another data source.** Their metrics don't
  exist on the server you query, so `promql/series`, `promql/counter`, `alerts/absent` and
  `promql/vector_matching` say nothing useful.
- **How the result was verified:**
  - an independent `yq` extraction found the same rules as pint;
  - every code line quoted in pint's output matched the source;
  - PromQL mode gave identical findings for every rule PromQL can parse;
  - an independent label-flow script found no missed template problems.
- **Metadata false positive:**
  - The online `promql/rate` reported `rate()` on `process_cpu_seconds_total` as "called on a
    non-counter", because the server's metadata returned `type: gauge` for that metric, and
    an earlier run of the same binary got a different answer.
  - VictoriaMetrics metadata is therefore unreliable.
  - `docs/METRICSQL_TESTING.md` says VictoriaMetrics serves no metadata. That is true for the
    old v1.115.0 test images only: newer VictoriaMetrics with metadata enabled does return it.
- **Real rule problems pint has no check for:**
  - `delta(counter)` without a window;
  - `delta()` on counters in general. `promql/counter` needs metadata, see above.
- **Canary rules.** Of 11 planted problems, 10 were caught. The missed one, duplicate group
  names inside one ConfigMap data key, is reported by `rule/vmalert` since `d2aebc54`.

### How to judge a finding

Classify every finding as one of:

- a real rule problem;
- a pint false positive;
- a data-source artefact;
- a standard or policy finding.

For false-negative hunting, compare pint with an independent analysis (yq plus python),
with PromQL mode on the same rules, and with planted canary rules.
