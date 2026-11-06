# Verified VictoriaMetrics and vmalert behaviour

Facts the fork relies on, each with how it was proven. **Don't assume VictoriaMetrics or
vmalert behave like Prometheus.** Several fork bugs came from that assumption. Verify new
assumptions the same way, and add them here.

Servers and versions used:

- prom.example.com: a real single-node VictoriaMetrics.
- vmalert.example.com: a real vmalert.
- Docker test images: v1.155.0 and v1.155.0-cluster since 2026-11-06, the version the user runs (pinned in `internal/promapi/vmtest`;
  v1.153.0 before, facts below marked v1.153.0 were measured there,
  `8cfc01df`; v1.115.0 before). Every oracle case passed unchanged on the switch.
- Docker experiment images: also `victoriametrics/vmalert:v1.153.0`. The user allows pulling
  and running the latest VictoriaMetrics images for experiments (2026-10-07).
- **Reference version for vmalert semantics: the latest release, v1.153.0** (2026-09-28).
  It uses metricsql v0.87.4. The user asked to follow the latest vmalert. When a newer
  release appears, re-read the files listed below and update pint and this page.

  ```sh
  gh api repos/VictoriaMetrics/VictoriaMetrics/releases/latest --jq .tag_name
  curl -sfL https://raw.githubusercontent.com/VictoriaMetrics/VictoriaMetrics/<tag>/app/vmalert/templates/template.go
  ```

## Query semantics (MetricsQL / VictoriaMetrics)

| Fact | Evidence | Used by |
|---|---|---|
| `rate()`, `irate()` and `deriv()` include the last sample **before** the window, so a window of 1× the scrape interval returns data. `rate_prometheus()` follows Prometheus and needs 2×. | prom.example.com, 10s scrape interval: `rate(x[15s])` returned all 153 series, `rate_prometheus(x[15s])` returned 76 (2026-10-02). | `promql/rate` (`3182f327`) |
| MetricsQL rewrites some queries into subqueries ("implicit query conversions"). A rollup function on a non-selector, `rate(sum(foo))`, runs as `rate(sum(foo)[1i:1i])`. A window after a non-selector, `sum(foo)[5m]`, runs as `sum(foo)[5m:1i]`. VictoriaMetrics runs them by default, rejects them with `-search.disableImplicitConversion` and logs them with `-search.logImplicitConversion`. Both flags use `metricsql.IsLikelyInvalid()`, which is identical in metricsql v0.87.4 and v0.87.5. prom.example.com has both flags off. | v1.153.0 `app/vmselect/promql/exec.go`; MetricsQL docs "Implicit query conversions"; `flag{name=~"search.(disable\|log)ImplicitConversion"}`. | `promql/syntax` "implicit subquery conversion" (`fed6e413`) |
| `increase()` also uses the last sample before the window, so a window of 1× the scrape interval works. `increase_prometheus()` needs 2×. | prom.example.com at a 10s scrape interval, `process_cpu_seconds_total`, 2026-10-07: `increase(x[15s])` returned every series, `increase_prometheus(x[15s])` about half, `increase_prometheus(x[25s])` every series. | `promql/rate` (`38c6ca45`) |
| A rollup function without a window (`rate(foo)`) gets an automatic window. Per the MetricsQL docs it is `max(step, scrape_interval)` for `rate` and `default_rollup`, and `step` for the other rollups. | MetricsQL docs, section "Implicit query conversions". | `MatrixSelector.Implicit`; `promql/rate` skips these windows |
| `rate(foo)[1m]` is **not** `rate(foo[1m])`. `[1m]` makes a subquery of `rate(foo)`, wrapped in `default_rollup()`, which returns the last value. The result equals `rate(foo)`, so it depends on the step. | prom.example.com, 2026-10-05, `process_cpu_seconds_total{job="vmalert"}`: with step=5m, `sum(rate(x)[1m])` = `sum(rate(x))` = `sum(rate(x[5m]))` ≠ `sum(rate(x[1m]))`. With step=1m all three equal `rate(x[1m])`. | `promql/syntax` "misplaced lookbehind window" (`43f024af`) |
| vmalert sends `step` = `-datasource.queryStep`, **5m by default**. If set to 0, it uses the group interval. `step` in group `params` replaces it: `setReqParams` deletes arguments vmalert set and adds the params. | `flag{name="datasource.queryStep"}` on prom.example.com shows `value="5m0s", is_set="false"`; v1.153.0 `app/vmalert/datasource/client_prom.go`, `client.go`. | `group/interval` |
| Regex matchers are fully anchored, as in Prometheus. | MetricsQL docs and real queries. | `promql/regexp` anchor warnings are valid |
| Division by zero gives `+Inf`. NaN results are dropped as missing values. | VictoriaMetrics behaviour, oracle cases in `testdata/metricsql_oracle/promql_nan.yaml`. | `promql/nan` says "can return Inf" |
| `sum(foo)` with no series argument (`sum()`) is valid and returns nothing. | metricsql parser, commit `87c625d2`. | converter adds a synthetic `union()` |
| Dots are allowed in metric names (`app.example.x:sum_samples`). PromQL rejects them. | Real alerts parse in MetricsQL mode. | — |
| `delta(x)` and `rate(x)` without a window are valid MetricsQL; PromQL rejects them. | Real alerts. | — |
| A bare `x keep_metric_names` is rejected by the server parser. `(x + 0) keep_metric_names` works. | prom.example.com `/prettify-query`. | test rules |

## HTTP API differences

| Fact | Evidence |
|---|---|
| No `/api/v1/status/config`. pint synthesises the config from `scrapeInterval` and `externalLabels` in `.pint.hcl`. | VictoriaMetrics API. |
| `/prometheus/api/v1/status/flags` is unsupported. `/flags` returns `-name="value"` lines, including `-retentionPeriod`. | prom.example.com. |
| `isPartial: true` responses are treated as backend errors, never as rule problems. | `8ae06b8a`. |
| Query stats contain only `seriesFetched` and `executionTimeMsec`, with no sample count, so upstream's `sampleRateLimit` (v0.89.0) and query/cost's `maxTotalSamples` and `maxPeakSamples` can't work against VictoriaMetrics. VictoriaMetrics has no `go_memstats_alloc_bytes` / `prometheus_tsdb_head_series` of its own, so query/cost's Prometheus memory estimate doesn't apply. Repeating an instant query took about the same time (30 ms, then 27 ms), so the cache doesn't distort `maxEvaluationDuration`. | v1.153.0 `app/vmselect/prometheus/query_response.qtpl`; `stats=1` query on prom.example.com (2026-10-07). |
| vmselect doesn't know the retention: in v1.153.0 only vmstorage has `-retentionPeriod` (default `1M`); vmselect has no such flag and no API reporting it. Single-node VictoriaMetrics doesn't list flags left at their default in `/flags`. pint uses the `retention` option, then `-retentionPeriod`, then the single-node default, and on vmselect skips retention checks (`294c8f26`). | v1.153.0-cluster `app/vmselect/main.go`, `app/vmstorage/main.go`. | `promql/range_query`, `promql/offset` |
| **Instant query lookback.** An instant query sees the newest sample in `(time - W, time]`. `W = min(max(step, -search.minStalenessInterval), LD)`, where `LD` is `max_lookback` from the query, else `-search.maxLookback`, else `-search.maxStalenessInterval` (0 = no limit), and `-search.setLookbackToStep` sets `LD = step`. Without a `step` argument, step = `LD` or 5m (`defaultStep`). Plain selectors (`default_rollup`) are capped by `LD`; explicit `[window]`s are never adjusted. There is **no fixed 5m lookback-delta** as in Prometheus. Sample every 10m: value at +4m59s, empty from +5m00s (step 300s or none); value until +9m (step 900s); with `maxStalenessInterval=1m`, empty from +1m00s even with step 900s; with `minStalenessInterval=15m`, never empty; with `maxStalenessInterval=8m`, step 300s still 5m, but no step means 8m. | v1.153.0 `QueryHandler`, `getMaxLookback` (`app/vmselect/prometheus/prometheus.go`) and `rollupConfig.doInternal` (`app/vmselect/promql/rollup.go`); Docker v1.153.0, 2026-10-07; oracle `group_interval.yaml` (v1.115.0 behaves the same). | `group/interval` (`f67b81d3`) |
| **Range queries and subqueries have no such gaps.** For `start < end` VictoriaMetrics estimates the interval between raw samples and looks back about 1.125× that. A 2h graph with step 60s over a 10m series returned 121 of 121 points; `max_over_time(x[20m:1m])` was never empty. Prometheus graphs do have gaps here. | Docker v1.153.0, 2026-10-07; `doInternal` comment "For instant queries, use rc.Step directly". | `group/interval` skips subqueries; unread rules are only Information |
| **Windows and the step for instant queries** (v1.153.0, 2026-10-09): a counter scraped every 10m, step 5m: `rate(x)` always empty, `increase(x)` 0 then empty, `rate(x[10m])` and `rate(x[2i])` correct only for 5 minutes after a sample (the sample before the window is used only within maxPrevInterval = step); with step 15m all correct. A subquery without a step uses the query step: `count_over_time(x[1h:])` = 12, 60, 240 for step 300s, 60s, 15s; `[1h:1m]` always 60. | Docker v1.153.0. | `promql/rate` (`6995921d`), `promql/syntax` (`969ed765`) |
| **`latency_offset` query arg** (v1.153.0, 2026-10-10), a sample every 15s: VictoriaMetrics moves the query time back to `now - latency_offset` when it's newer; queried at now-30s with `latency_offset=3m` the newest sample seen was 161s before the query time, at now-3m20s 6s before. vmalert stamps results with its request time (`now - eval_delay`), so results lag `latency_offset - eval_delay`. vmalert docs recommend `eval_delay` = `latency_offset`; `eval_delay` is ignored with `eval_offset`. | Scratch vmsingle, `lat_metric`; `rule_vmalert.go` (`7c394a90`). |
| **`scrape_interval(m[d])`** (v1.153.0, 2026-10-10): the average interval between samples per series in seconds: 600 for a 10m series, 15 for 15s, `max()` over 60s and 300s series gave 300; empty with fewer than two samples in the window (`[10m]` over 10m samples). | Scratch vmsingle `si_*` series; `promql_rate_vm.go` (`1d7fbcb8`). |
| **`sum by (__name__)` in vmalert** (v1.153.0, 2026-10-10): over two metrics, as an alert, a recording rule and `by (__name__, job)`: "same labelset"; `abs()` around it: VictoriaMetrics 422; outer `sum()`, `by (job)`, `without (job)`, `label_replace()` and a one-name regexp `{__name__=~"x"}`: work. | Scratch vmalert `pint-absent-vmalert3`, `dupn` rules. |
| **Explicit windows and maxPrevInterval** (v1.153.0, 2026-10-10), a counter written every 10m, 120 instant queries 30s apart: step 5m `rate(x[12m])` empty or 0 on 36, `rate(x[15m])` on 0; step 2m `rate(x[15m])` 36, `rate(x[18m])` 0; `increase(x[12m])` returns 0 (not empty) when it fails; `irate` like `rate`; `deriv(x[15m])` 0 on half with step 5m and 15m, `deriv(x[20m])` never. Rule: `rate`/`irate`/`increase` need `max(S, 2S - maxPrev)`, `deriv` needs `2S`. | Scratch vmsingle, `rate_ctr_10m` series; `promql_rate_vm.go` (`fd0476b4`). |
| **Rollup functions** (v1.153.0, 2026-11-06), counters growing 1/s, 120 instant queries 30s apart, wrong or empty results: samples every 10m `rollup_rate(x[10m])` 54, `[12m]` 30, `[14m]` 6, `[15m]` 0, the same with steps 1m to 10m; every 5m `[4m]` 24, `[5m]` 0; every 1m `[30s]` 60, `[1m]` 0; every 10s `[5s]` 0. `rollup_increase`, `rollup_delta` and `rollup_deriv` behave the same and return 0 (min, max, avg), not nothing. `-search.minStalenessInterval=10m` made `[10m]` right for samples every 10m; `-search.maxStalenessInterval` and `-search.maxLookback` at 10m changed nothing. A 15m step made `[10m]` right, a 5m step didn't help `[30s]` on samples every 1m. Rule: window >= max(S, 2S - max(5m, minStalenessInterval)). `rollup_scrape_interval` returns nothing instead of 0; `rollup` and `rollup_candlestick` weren't measured for this. | Docker, `TestVictoriaMetricsSemantic` oracle cases on single-node and cluster. | `promql/rate` (`02077992`) |
| **Group `limit` in vmalert** (v1.153.0, 2026-11-06), `limit: 2`: a recording rule returning 10 series failed with "exec exceeded limit of 2 with 10 series" and wrote nothing, an alerting rule with "exec exceeded limit of 2 with 10 alerts" and fired nothing, 2 series worked. `-rule.resultsLimit` sets the default, `limit` of a group overrides it (vmalert -help). | Docker vmalert + vmsingle; oracle `rule_vmalert.yaml`. | `rule/vmalert` (`f136b1c6`) |
| **Query limits** (v1.153.0, 2026-11-06): `-search.maxSamplesPerQuery` is compared with samples fetched per selector ("fetch unique series=N, blocks=B, samples=S" in the trace, whole blocks), not `samplesScanned`: `samplesScanned=1920` passed a limit of 1000, 5 series fetching 1205 samples failed, two selectors with 964 each passed. `-search.maxUniqueTimeseries=5` didn't stop a 6-series `/api/v1/query` on single-node. `/flags` lists only flags set explicitly. | Docker vmsingle. | backlog |
| **Instant against range queries on sparse metrics** (v1.153.0, 2026-10-09), a metric with one sample every 10m: instant `absent(x)` is empty 2m after a sample, 1 at 7m and empty again with `step=600`. Range `absent(x)` over 6h at 1m returns 0 of 361 points, `x` and `count(x)` 361 of 361, so `promql/series` sees a continuous metric; of a 40m pause only ~28 minutes show, the range lookback is ~1.1× the usual interval. Explicit windows keep the pauses: `count_over_time(x[5m])` and `last_over_time(x[5m])` have 180 of 361 points, `absent(last_over_time(x[5m]))` is 1 on 181, matching vmalert. `last_over_time()` keeps `__name__`; `absent(last_over_time(x{job="a"}[5m]))` returns `{}`, not `{job="a"}` like `absent(x{job="a"})`. `vmalert -replay` sends `/query_range` (its docs): `absent(x)` fired 0 of 10080 minutes in a week, `absent(last_over_time(x[5m]))` 5006. Before the fix pint `alerts/count` said "0 alert(s) in the last 1w" for `... and absent(x) == 1`, 1001 for the rewritten query. | Scratch compose with vmsingle, vmalert replay and generated series (session scratchpad `absent/`, not kept); `alerts_count_vm.go`, `alerts_absent_vm.go`. |
| **Staleness markers: `default_rollup` against `last_over_time`** (v1.153.0, 2026-10-12): a scraped series replaced by one with another label value, the old one ended by a staleness marker: instant `x` and `default_rollup(x[5m])` return the new series only, `last_over_time(x[5m])` both for 5 minutes (`stale_samples_over_time` 1 on the old one). `default_rollup` keeps `__name__`, `absent(default_rollup(missing[5m]))` is `{} 1`. So `last_over_time(x[5m]) / on() y` fails with "duplicate time series on the left side of / on()" and `count(last_over_time(x[5m]))` is 2 where vmalert sees 1. Range queries merge same-label series that overlap in at most 2 points (`mergeNonOverlappingTimeseries`), instant queries never: with step 1m the range query failed from the third overlapping point. | Scratch vmsingle scraping a local file (`repl_metric`, `example_up`); first seen on a test server: rules with a false "unable to run checks (alerts/count)", a rule counted many times instead of once. Oracle `alerts_count.yaml`: `default_rollup` shows the same pauses of a 10m metric as `last_over_time`. | `alerts/count` (`d7413a45`) |
| **Query traces** (`trace=1`) report `samplesScanned` for rollups over raw samples, with incremental aggregation and over subquery results; the sum is VictoriaMetrics' scanned samples and can exceed raw samples (84 for 80). Single-node and cluster give the same sums, vmstorage nodes add nothing. prom.example.com uses the same format. Send `nocache=1`. | `app/vmselect/promql/eval.go` v1.153.0; Docker 2026-10-09; one query on prom.example.com. | `query/cost` (`d420e103`) |
| `max_over_time(x[15m])` keeps `__name__` in VictoriaMetrics, Prometheus drops it. | Oracle `group_interval.yaml` on v1.115.0 and v1.153.0, 2026-10-07. | oracle expectations |
| `rate_prometheus(x[2m])` over two samples 60 apart returns 0.5 (60 / 120s), and `[1m]` at a 1m resolution is empty. | Oracle `promql_rate.yaml` on v1.153.0, 2026-10-08. | `promql/rate` |
| `/prettify-query` parses without evaluating, so it works as a server-side syntax oracle. | prom.example.com. |
| Fresh samples are hidden by `-search.latencyOffset`. The test fixtures write the newest sample 5s in the past and call `/internal/force_flush`. | `internal/promapi/vmtest`. |
| Wide queries can hit `-search.maxSamplesPerQuery`. Failed queries aren't cached, so pint resends them; upstream pint does the same. For `uptime`, use `vm_app_version`, 2 series, instead of `up`. | prom.example.com. |
| **Metadata:** single-node VictoriaMetrics stores metric metadata since v1.130.0, enabled by default since v1.137.0 (`-enableMetadata`). It is kept in memory, refreshed on ingestion, dropped after 1h without updates and lost on restart. v1.115.0 returns none. v1.153.0 stores metadata from `# TYPE` lines of `/api/v1/import/prometheus`, single-node and cluster: vminsert stores it, vmselect serves it per tenant at `/select/<tenant>/prometheus/api/v1/metadata` (tenant 1 got `{}`) with `"isPartial":false`. pint `promql/counter` and `promql/rate` reported the counter and the gauge from it on both (Docker, 2026-10-08). The oracle imports data without `# TYPE` lines. On 2026-10-08 prom.example.com still had `process_cpu_seconds_total` as `gauge` (help "Total user and system CPU time spent."), and pint reported a false Bug for `rate()` on it, an Information since `772f09aa` (released in `v0.89.1-78-gfe5a1cef`). `vm_http_requests_total` had no metadata: VictoriaMetrics components don't expose `# TYPE` unless `-metrics.exposeMetadata` is set. prom.example.com (`enableMetadata=true`) returned **one entry per metric**, and it changes over time: `type: unknown` on 2026-10-02, and `type: gauge` for `process_cpu_seconds_total` on 2026-10-05, which made `promql/rate` report a false "non-counter" Bug. | v1.153.0 docs "Metrics Metadata"; direct `/api/v1/metadata` queries; `flag{name="enableMetadata"}`. |
| v1.115.0 lacked `rate_prometheus`; the v1.153.0 test images have it and the oracle queries it. | Docker tests. |

## vmalert (rule evaluation)

- **vmalert HTTP API used by `vmalertURI`** (v1.153.0 in Docker, 2026-10-09):
  - `/flags` lists only flags set explicitly, as `-name="value"` lines; secrets print as
    `"secret"` (`-datasource.url`, `-remoteWrite.url`). Example: `-datasource.queryStep="10m0s"`.
  - Array flags are joined with commas: two `-external.label` flags print as
    `-external.label="cluster=demo,dc=dc-1"`. An item with a comma is Go-quoted:
    `-external.label='team="a,b"'` printed `-external.label="\"team=\\\"a,b\\\"\",env=prod"`.
  - `/api/v1/rules` returns `{"status":"success","data":{"groups":[...]}}`, each group with
    `name`, `file` (the path vmalert opened, e.g. `/etc/rules/a.yaml`), `interval` in seconds
    (the default `-evaluationInterval` for groups without one), `params` as `["step=10m"]`,
    `concurrency`, `rules`. `eval_offset` appears only when set.
  - vmalert refuses to start with recording rules and no `-remoteWrite.url`.
- **`absent()` during a real outage** (v1.153.0, 2026-10-09): a 15s metric with one 12m gap
  gave one 5m-grid pause; `absent(x)` without `for` rightly fires then, so a single pause is
  not a sign of a sparse metric (`046f5a38`).

Verified in the vmalert source (`app/vmalert/`) of v1.145.0, then re-checked against the
latest v1.153.0. The files were read raw with curl from raw.githubusercontent.com. A WebFetch summary once got `labelSet.add` wrong, so always
read the raw source.

- **Labels available to annotation templates** (`rule/alerting.go`):
  - `labelSet.add` builds two sets: `processed` (the alert's final labels) and `origin`
    (what annotation templates see as `$labels`). It always writes to `processed`.
  - It writes a rule label to `origin` only when the query result doesn't have that key. On
    a conflict the query value is kept in `origin`, and the displaced value is stored as
    `exported_<name>`.
  - So annotations can read `alertname`, `alertgroup`, rule labels and external labels, and
    a label the query returns always shows the query's value.
- **External labels** (`rule/group.go`, `NewGroup` and `mergeLabels`; flag in `main.go`):
  - External labels (`-external.label`) are merged into every rule's labels. Priority is
    external < group `labels` < rule `labels`.
  - So `{{ $labels.account }}` works in annotations even when the query aggregates
    `account` away.
- **Alert resolution:** firing alerts are sent with `EndsAt` = now + 4 × max(group interval,
  `-rule.resendDelay`), capped by `-rule.maxResolveDuration`, and resent on every evaluation
  (`getResolveDuration`, `alertsToSend` in `app/vmalert/rule`, v1.153.0). A long group interval
  doesn't make alerts flap.
- **Rule labels vs query labels** (Docker v1.153.0, 2026-10-09): a rule label that overrides
  a query label with another value doesn't fail. vmalert keeps the query value as
  `exported_<name>`, for alerts and recording rules.
- **"same labelset" (`errDuplicate`)** happens when series differ only by `__name__`: alert
  `{__name__=~"x|y"} > 0`, recording rule `{__name__=~"x|y"}` and `sum(...) by (__name__)`
  failed on every evaluation; `label_replace(..., "metric", "$1", "__name__", "(.+)")` fixed
  it; `x > 0 or y > 0` worked. `rate({__name__=~"x|y"}[1m])` fails earlier: VictoriaMetrics
  rejects it with "duplicate output timeseries". The rule state API shows the error as `{}`
  in `updates[].error`; read `lastError` from `/api/v1/rules` instead.
- **Chaining groups** (Docker v1.153.0, 2026-10-08, groups every 1m, writers record
  `vector(time())`, readers record what they saw):
  - writer `eval_offset: 10s`, reader `eval_offset: 40s`: the reader saw the result written
    30 seconds earlier, in the same cycle (4 of 4);
  - writer and reader in one group (`params: step: [2m]`): the previous evaluation (4 of 4);
  - groups without `eval_offset`: the previous cycle (4 of 4).
  - With `eval_offset` vmalert sends `time` = now, so VictoriaMetrics shifts the query by
    `-search.latencyOffset` (30s) and `time()` returns a value 30s earlier than the sample
    timestamp. Match values to writer samples instead of comparing them with timestamps.
  - Used by `rule/dependency` (`da3a1914`).
- **Result timestamps and evaluation phase** (v1.153.0 `app/vmalert/rule/group.go`):
  - `adjustReqTimestamp`: without `eval_offset` the query time is `now - eval_delay` (30s,
    `-rule.evalDelay`), truncated to the group interval when `eval_alignment` is true (the
    default). The same time stamps recording results and `ALERTS`. With `eval_offset` it is
    the evaluation time; with `eval_alignment: false` it is `now - 30s`.
  - `delayBeforeStart`: a group starts at a phase within `min(interval, -group.maxStartDelay=5m)`
    from a hash of the group ID, unless `eval_offset` is set. Intervals above 5m also depend on
    when vmalert started.
  - So with alignment a result is written up to one interval after its timestamp. Readers need
    a lookback of `interval + lag`, `lag` in `[0, interval)`: 2× the interval to be safe, as the
    vmalert docs "Common mistakes" say. With `eval_offset` 1× is enough.
  - Docker v1.153.0, group A `interval: 2m` (phase about 1m55s), readers every 15s,
    2026-10-07: `-datasource.queryStep=1m`: the reader never saw A and an alert `a_rec > 0`
    never fired; step 2m: data 30s out of every 2m; step 3m: 30s gap every 2m; step 4m: no
    gap; step 2m with `eval_offset: 40s` on A: no gap. Scripts are in the session scratchpad
    `gi153/` only; recreate them from this description if needed.
  - A reader in the **same group** runs at the same timestamp right after the writer, before
    remote write lands, so it sees only the previous result and needs a lookback longer than
    the interval. Docker v1.153.0, `interval: 1m`, 16 evaluations: with `params: step: [1m]`
    the reader never saw the writer; with `[2m]` it saw it every time.
- **External labels and recording rules:** `NewGroup` merges external labels into **every** rule,
  alerting and recording, and rule labels take priority. In `RecordingRule.toTimeSeries` a query
  result label that conflicts with a rule (or external) label of a different value is renamed to
  `exported_<name>`; identical values are left alone.
- **Label templates (`labels:`)** are expanded from the **query labels only**. External
  labels, `alertname` and rule labels aren't available there.
  - In v1.153.0, `toLabels` in `rule/alerting.go` runs label templates with the full header,
    but the data holds only `Labels`, `Value` and `Expr`. Other variables like `$activeAt`
    are zero values, not errors. The docs say: "Labels only support limited templating
    variables ... including `$labels`, `$value` and `$expr`".
- **Recording rule labels** are copied literally and never templated (v1.153.0 docs,
  `rule/recording.go`). pint's template check only covers alerting rules anyway.
- **Prometheus, for comparison:** annotations see only the query labels, and external labels
  are added after templating. That is why PromQL mode keeps upstream's stricter behaviour.
- **Extra template variables** (`notifier/alert.go`, `tplHeaders`): `$expr`, `$type`,
  `$interval` (new after v1.145.0, the group evaluation interval), `$alertID`, `$groupID`,
  `$activeAt`, `$for`, `$isPartial`, and also `$externalLabels` and `$externalURL`.
- **Extra template functions** (`templates/template.go`): `quotesEscape`, `jsonEscape`,
  `htmlEscape`, `crlfEscape`, `pathEscape`, `queryEscape`, `formatTime`, `parseDurationTime`.
- **Prometheus template functions vmalert lacks:** `graphLink`, `tableLink`, `tmpl`,
  `toDuration`, `urlQueryEscape`. vmalert validates templates when it loads rules, so such
  a rule file fails to load. pint reports them for MetricsQL rules since `9e8928e8`.
- **The full v1.153.0 function list:** `args`, `crlfEscape`, `externalURL`, `first`,
  `formatTime`, `htmlEscape`, `humanize`, `humanize1024`, `humanizeDuration`,
  `humanizePercentage`, `humanizeTimestamp`, `jsonEscape`, `label`, `match`, `now`,
  `parseDuration`, `parseDurationTime`, `pathEscape`, `pathPrefix`, `query`, `queryEscape`,
  `quotesEscape`, `reReplaceAll`, `safeHtml`, `sortByLabel`, `stripDomain`, `stripPort`,
  `strvalue`, `title`, `toLower`, `toTime`, `toUpper`, `value`.
- **Rule file fields** (`config/config.go`):
  - group: `type`, `eval_offset`, `eval_delay`, `eval_alignment`, `concurrency`, `params`,
    `headers`, `notifier_headers`, `debug`, `limit`;
  - rule: `debug`, `update_entries_limit`, `keep_firing_for`;
  - vmalert rejects unknown keys (inline `XXX` and `checkOverflow`), so `query_offset` is
    not supported;
  - `limit` and `concurrency` must be ≥ 0;
  - `Header.UnmarshalYAML` skips empty entries and requires a `:`;
  - the rest of the validation is listed in `architecture.md`.
- **Files and groups:**
  - Each file, which in Kubernetes is each ConfigMap `data` key, is parsed separately.
  - Group names must be unique **within a file**; a duplicate is rejected.
  - The same group name in different files or ConfigMaps is fine.
- **vmalert.example.com external labels:** it sets no `-external.label`.
  - `/flags` (no auth) lists only the flags that are set: `-external.url` and `-rule`.
  - The VictoriaMetrics metric `flag{job="vmalert", name="external.label"}` shows
    `is_set="false"`.
  - `prom.example.com/vmalert/*` returns 400, because `-vmalert.proxyURL` isn't configured.
- **Another real vmalert** uses the external labels `account` and `grafana_ds`. Pass them
  with `--external-label`.
