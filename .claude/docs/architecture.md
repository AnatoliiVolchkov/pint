# Architecture of the MetricsQL support

How the fork adds VictoriaMetrics and MetricsQL to pint, and where each piece lives.
The original requirements are in `plans/plan-1.md` (outside this repository, in the
parent workspace directory if present). In short:

- PromQL mode must behave exactly like upstream.
- MetricsQL gets full AST-based analysis.
- No check is disabled just because the backend is VictoriaMetrics.

## The two switches

| Setting | Values | Where | Effect |
|---|---|---|---|
| `parser { query_language = ... }` | `promql` (default), `metricsql` | `internal/config/parser.go` | Which parser reads every `expr:`. It also selects vmalert semantics for templates and rule file fields. |
| `prometheus "x" { backend = ... }` | `prometheus` (default), `victoriametrics` | `internal/config/prometheus.go` | Which API client talks to the server. The same option exists in discovery `template` blocks. |

The settings are validated together in `internal/config/config.go`:

- `metricsql` with a `prometheus` backend is a config error.
- A `victoriametrics` backend with `promql` only logs a warning.

`scrapeInterval` and `externalLabels` are allowed only on `victoriametrics` servers.
VictoriaMetrics has no `/api/v1/status/config`, so pint can't read those values from the server.

## Query pipeline

```
expr string
  └─ ast.Language.ParseExpr                  internal/parser/ast/language.go
       ├─ promql.Language                     internal/parser/promql/promql.go     (Prometheus parser → pint AST)
       └─ metricsql.Language                  internal/parser/metricsql/metricsql.go (VictoriaMetrics parser → pint AST)
  └─ parser.PromQLExpr / PromQLNode tree     internal/parser/promql.go (Parent/Children walk helpers)
  └─ source.LabelsSource                     internal/parser/source/*.go  (which labels a query returns)
  └─ checks                                  internal/checks/*.go
  └─ diags                                   internal/diags/*.go  (map byte offsets in the query to YAML line/column)
```

- **`internal/parser/ast`** is a language-neutral AST shaped like Prometheus' one. It has
  `VectorSelector`, `MatrixSelector`, `SubqueryExpr`, `AggregateExpr`, `Call`, `BinaryExpr`
  and so on, plus fields only MetricsQL fills:
  - `VectorSelector.OrMatchers`, for `{a="1" or b="2"}`;
  - `MatrixSelector.Implicit` and `SubqueryExpr.Implicit`, for a window left out, as in `rate(foo)`;
  - `StepRelative`, for windows like `[5i]`;
  - `AggregateExpr.Args` and `Limit`;
  - `Call.KeepMetricNames`;
  - fill modifiers in `VectorMatching`;
  - `OffsetExpr`, for `sum(foo) offset 5m`.

  `ast.Children` returns child nodes in the order the Prometheus parser would, so upstream
  logic ported to the AST behaves the same. `printer.go` prints nodes back to text.
- **`internal/parser/promql`** converts `prometheus/promql/parser` nodes 1:1. Upstream
  checks were migrated from `promParser.*` and `posrange.*` to `ast.*`. When merging
  upstream, port their logic changes to the `ast` types.
- **`internal/parser/metricsql`** converts `github.com/VictoriaMetrics/metricsql` nodes:
  - Positions come from the metricsql fork's `SourceInfo` (`Pos`, `Origin`, `Parens`).
  - `functions.go` maps MetricsQL functions to signatures. Functions shared with PromQL use
    PromQL's signatures, so queries valid in both languages are analysed the same way.
  - `convertRollupArg` turns a rollup argument without a window into a
    `MatrixSelector{Implicit: true}`, or into a `SubqueryExpr{Implicit: true}` for expressions.
  - `convertRollupExpr` turns `x[5m]` into a `MatrixSelector`, `f(x)[5m:1m]` into a
    `SubqueryExpr`, and `sum(x) offset 5m` into an `OffsetExpr`.
  - Parse errors become `*ast.ParseError` with a `PositionRange`.
- **`internal/parser/source/metricsql.go`** describes how MetricsQL-only functions change
  labels, for example `label_set`, `alias` and `label_*`. Functions not listed there don't
  change labels.

## Rule files: vmalert schema

`internal/parser/vmalert.go` is fork-only. It is used only when `query_language = "metricsql"`,
because vmalert evaluates MetricsQL rules.

- **Extra group fields:** `type`, `eval_offset`, `eval_delay`, `eval_alignment`, `concurrency`,
  `params`, `headers`, `notifier_headers`, `debug`.
- **Extra rule fields:** `debug`, `update_entries_limit`.
- **Validation** follows vmalert:
  - `eval_offset` can't be combined with `eval_delay`;
  - `|eval_offset|` must be below `interval`;
  - `headers` entries need a `:`;
  - `type` is `prometheus`. `graphite` and `vlogs` are reported as unsupported.
- **`query_offset`** is rejected with "query_offset is not supported by vmalert, use eval_offset instead".
- **Strict and relaxed mode:** strict mode (`strict.go`) and relaxed mode (`parser.go`) both
  call into `vmalert.go`. Relaxed mode handles Kubernetes manifests (ConfigMap, VMRule,
  PrometheusRule) with `parser { relaxed = [...] }`.
- **PromQL mode** still rejects these fields, as upstream does.

## Checks with MetricsQL-specific behaviour

All checks run in both modes. Language branches use `expr.Language().Name() == metricsql.Name`:

| Check | MetricsQL difference |
|---|---|
| `promql/syntax` | Title "MetricsQL syntax error" and a link to the MetricsQL docs. Two MetricsQL-only Warnings live in `promql_syntax_metricsql.go`. "misplaced lookbehind window" is for `rate(foo)[5m]`, where the window is applied to `rate()` results, not to `foo`; subqueries passed to rollup functions are not reported. "implicit subquery conversion" is for `rate(sum(foo))` and `sum(foo)[5m]`, decided by upstream `metricsql.IsLikelyInvalid()` on a fresh `mql.Parse`. `implicitConversions()` mirrors its rules to find positions, and `promql_syntax_metricsql_internal_test.go` keeps the two in agreement over the parser corpus. | Since `969ed765` also "subquery without a step" for `[range:]`, decided from the query text because the AST doesn't keep the colon.
| `promql/rate` | `rate`, `irate`, `increase` (from upstream v0.89.0) and `deriv` windows need ≥1× the scrape interval, because VictoriaMetrics uses the last sample before the window. `rate_prometheus` and `increase_prometheus` are checked too and need 2×. Implicit and step-relative windows are skipped. The details text links to the VictoriaMetrics docs. | Since `772f09aa` (`promql_rate_vm.go`) "non-counter" from VictoriaMetrics metadata is a Warning, Information for `_total` names. Since `6995921d` windows derived from the step (`rate(x)`, `rate(x[2i])`) are worked out from group `params` step or `queryStep` plus `-search.*` flags (shared `vmLookbackSettingsFor`) and get a Warning when too short. Since `fd0476b4` (`vmWindowNeed`, `shortWindowProblems`) explicit windows in `[S, 2S)` get a Warning too: `rate`/`irate`/`increase` need `max(S, 2S - maxPrev)`, `deriv` `2S` (it ignores the sample before the window); `/flags` is read only when such a window exists (`hasShortWindow`). Since `1d7fbcb8` S is the interval measured with `max(scrape_interval(sel[15m]))` (`[2h]` if empty) when it's longer than `scrapeInterval` (`measureIntervals`, `scrapeFor`); the "< 1x" Bug keeps the configured S.
| `promql/nan` | VictoriaMetrics drops NaN, so only Inf can break aggregations. The message says "can return Inf". |
| `alerts/template` | vmalert dialect, see below. |
| `alerts/external_labels`, `labels/conflict` | VictoriaMetrics servers use static `externalLabels` from config and from `--external-label`. VictoriaMetrics texts and the offline rule live in `external_labels_vm.go`: `Meta().Online` is false for VictoriaMetrics servers, and with `--offline` `DisableOnlineChecks` disables them only per Prometheus server instance (`disableOnlyForPrometheus` in `internal/config/external_labels.go`). Configs without VictoriaMetrics servers behave exactly like upstream. | Since `012fcd51` `labels/conflict` (`labels_conflict_vm.go`) also warns when a selector can match more than one metric name and the name survives to the results, vmalert fails such rules with "same labelset".
| `group/interval` | `group_interval_vmalert.go`, its own logic for MetricsQL (`f67b81d3`): finds rules reading the results (recording metric, or `ALERTS{alertname}`), computes each reader's lookback (window, or step from group `params`, the `queryStep` server option or 5m, adjusted by `-search.*` flags from `/flags`), Warning below 1× interval, Information below 2× with aligned timestamps, Information for unread recording rules. Gets servers through `WithServers` in `baseRules` (like `TemplateCheck.WithExternalLabels`) and `Offline()` from `DisableOnlineChecks` (`internal/config/group_interval.go`). Group fields come from `parser.Group.VMAlert` (`6f3473c0`). |
| `rule/dependency` | `rule_dependency_vmalert.go` (`da3a1914`): cross-group details explain why one group doesn't help in vmalert and how to chain groups. Readers in groups chained with `eval_offset` (same interval, reader at least 30s later) aren't reported. PromQL text is upstream's. |
| `promql/range_query`, `promql/offset` | `promapi.RetentionUnknown` is true for vmselect without the `retention` option, and both checks then skip retention comparisons instead of assuming 31 days. |
| `promql/features` | Never reports for MetricsQL or VictoriaMetrics, which have no feature flags. |
| `rule/vmalert` | Fork-only check (`rule_vmalert.go`), registered only with `query_language = "metricsql"` (`config/vmalert_checks.go`: `vmalertStaticRules`, `withVMAlertChecks`, `knownCheckNames`). It is in `checks.VMAlertCheckNames`, not `CheckNames`, so `pint config` and `TestGetChecksForRule` of PromQL configs don't change. Reports `debug: true` (group and rule, `Rule.VMAlert` in the parser) and `latency_offset` longer than `eval_delay`. Since `f136b1c6` also rules returning more results than the group `limit` or vmalert `-rule.resultsLimit` (`rule_vmalert_limit.go`): `baseRules` gives it the servers of the file with `WithServers`, like `group/interval`, `setStaticChecksOffline` makes it skip queries with `--offline`, and it sends `count(expr)` only for rules with a limit. |
| `alerts/count` | `alerts_count_vm.go` (`598f8a39`): selectors without a window are counted as `default_rollup(selector[W])`, W the instant query lookback (`vmLookbackSettingsFor`), so the range query sees what vmalert sees. It was `last_over_time` until `d7413a45`, which ignores staleness markers and kept replaced series for the whole window. |
| `alerts/absent` | `alerts_absent_vm.go` (`6147897d`, `046f5a38`): pauses of `absent()`/`absent_over_time()` selectors measured with `vmPresenceQuery` (`count(count_over_time(sel[W]))`) and `vmPauses`; Warning only for regular pauses (`vmPauseStats.regular`, at least once a day on average) that `for` doesn't cover. |
| `promql/series` | `promql_series_vm.go` (`1fcdc6b0`), hook at the end of `Check`: every instant selector (not in `absent()`, no fallback, not another rule's output) gets a 1h presence query, and only with a pause there a `lookbackRange` one; Warning for regular pauses. |
| `query/cost` | VictoriaMetrics query stats only have `seriesFetched` and `executionTimeMsec`. Series counts, `maxSeries`, `maxEvaluationDuration` and recording rule suggestions (comparing `seriesFetched`) work. `maxTotalSamples` works since `d420e103`: the `count(...)` query is sent with `trace=1&nocache=1` and samples are the sum of `samplesScanned` in the trace (`promapi.QueryWithTrace`, `vm_trace.go`, `query_cost_vm.go`). `maxPeakSamples` gives an "unsupported query cost limit" Warning. Since `cace9e15` the memory estimate (Prometheus process metrics) is skipped for VictoriaMetrics servers. The VictoriaMetrics section in `docs/checks/query/cost.md` documents this. |
| `promql/offset`, `promql/counter`, `promql/fragile`, `promql/regexp`, `promql/selector`, `promql/vector_matching`, `rule/dependency`, `rule/duplicate`, `promql/series` | Handle `or` filters, MetricsQL functions and offset on any expression. |

### alerts/template: vmalert dialect

`internal/checks/alerts_template.go` uses `templateDialect`:

- **`prometheusTemplates`** for PromQL rules.
- **`vmalertTemplates`** for MetricsQL rules:
  - extra variables `$expr`, `$type`, `$alertID`, `$groupID`, `$activeAt`, `$for` and `$isPartial`;
  - extra variable `$interval` (vmalert v1.153.0);
  - extra functions `quotesEscape`, `jsonEscape`, `htmlEscape`, `crlfEscape`, `pathEscape`,
    `queryEscape`, `formatTime` and `parseDurationTime`;
  - no `toDuration`, `urlQueryEscape`, `graphLink`, `tableLink` or `tmpl`
    (`vmalertMissingTemplateFuncs`). Execution uses Prometheus' expander, which knows them,
    so `checkTemplateSyntax` first parses with `vmalertTemplateFuncMap` to report them;
  - error positions are shifted per dialect by the header length (`shift()`).
- **Labels available in annotations:**
  - In vmalert, annotations can read `alertname`, `alertgroup`, the rule's own labels and
    the external labels, besides the labels the query returns.
  - Label templates (`labels:`) see only the query labels, so external labels used there are
    still reported.
  - In PromQL mode none of this applies, which is upstream behaviour.
- **External labels:**
  - `TemplateCheck.WithExternalLabels(names)` is applied in `internal/config/parsed_rule.go`
    (`baseRules`).
  - `commonExternalLabels` keeps only labels set on every VictoriaMetrics server of the rule's
    path. If any server is a Prometheus server, the list is empty.

## VictoriaMetrics backend

`internal/promapi/victoriametrics.go` is created with `promapi.NewVictoriaMetrics(..., VictoriaMetricsOptions{ExternalLabels, ScrapeInterval})`.

- **Retention:** the `retention` option (`VictoriaMetricsOptions.Retention`) overrides
  `-retentionPeriod`. vmselect, detected by the `/select/<tenant>/prometheus` path or by
  `-storageNode` in `/flags`, has unknown retention without it, and `flags.go` logs a warning.
- **Config and flags:**
  - `Config()` is synthesised from the static options, so no request is made, unless
    `vmalertURI` is set (`b2070241`): then it reads vmalert `/flags` and `/api/v1/rules`
    (`internal/promapi/vmalert.go`, cached 10m, or the given TTL for `pint watch rule_files`)
    and adds vmalert external labels (pint config wins by name) and `RuleFiles`.
    `PrometheusGenerator.LoadVMAlert` (`internal/config/vmalert.go`, called from `checkRules`
    in `cmd/pint/scan.go` online only) loads it before checks, so `VMAlertQueryStep()` and
    `StaticExternalLabels()` include it; the result is an `atomic.Pointer` in
    `victoriaMetrics`. Offline, `disableOnlyForPrometheus` also disables the external label
    checks for servers with `vmalertURI`.
  - Flags are read from `/flags` (`-name="value"` lines). `-retentionPeriod` is translated to
    Prometheus' `storage.tsdb.retention.time`.
  - vmselect doesn't know the vmstorage retention: the `retention` option sets it, without it
    retention checks are skipped and pint logs a warning (`294c8f26`).
- **Partial responses** (`isPartial: true`) are rejected as backend errors, never turned into
  rule problems.
- **`sampleRateLimit`** (upstream v0.89.0) is passed as 0 to `NewPrometheus`. VictoriaMetrics
  query stats only have `seriesFetched` and `executionTimeMsec`, so the limit can't work.
  `validateBackends` in `internal/config/config.go` logs a warning when it is set.
- **URL layouts:** single-node (`http://vm:8428`) and cluster
  (`http://vmselect:8481/select/0/prometheus`).
- **Static external labels:** `FailoverGroup.StaticExternalLabels()` (`internal/promapi/failover.go`)
  exposes them to the template check.

## Texts for VictoriaMetrics servers and vmalert

Upstream checks build "`name` Prometheus server at <uri>" with `promText` in 34 places.
`checks.VictoriaMetricsTexts` (`vm_texts.go`) rewrites problems once, in `checkRules`
(`cmd/pint/scan.go`): server text for servers with the VictoriaMetrics backend
(`PrometheusGenerator.VictoriaMetricsServerNames`), and with MetricsQL configs
(`Config.IsMetricsQL`) "when Prometheus restarts", "Prometheus regexp matchers" and "not a
valid Prometheus rule". Unit tests and the oracle run checks directly and keep the upstream
texts; testscripts show the rewritten ones.

## CLI additions

- **`--external-label`** (`cmd/pint/main.go`) is a global `StringSliceFlag` with the env var
  `PINT_EXTERNAL_LABELS`:
  - format `name=value`, separated by commas or by repeating the flag;
  - parsed by `config.ParseExternalLabels`;
  - merged into every VictoriaMetrics server and discovery template by
    `(*Config).SetExternalLabels` (`internal/config/external_labels.go`), overriding values
    from the config file;
  - applied in `actionSetup` and in `pint config`;
  - logs "Using external labels from the command line", and a warning when no VictoriaMetrics
    server exists.
- **`pint version`** (`cmd/pint/version.go`) prints a second line from `debug.ReadBuildInfo`,
  for example `metricsql:  v0.87.6-pint.1 (replaces VictoriaMetrics/metricsql v0.87.6)`.
  A `go.work` build shows `(devel)`.
- **`pint parse --query-language metricsql '<query>'`** prints the pint AST, including the
  MetricsQL-only fields. Use it when debugging the converter.

## Fork-only files vs edited upstream files

Prefer **new files** for fork logic, because they never conflict with upstream syncs. Examples:

- `promql_syntax_metricsql.go`
- `vmalert.go`
- `external_labels.go`
- `victoriametrics.go`
- `metricsql_test.go`
- everything under `.github/metricsql-test/` and `.claude/`

When an upstream file has to change, keep the edit to a few lines that call into the new
file. `docs/UPSTREAM_SYNC.md` lists the fork-only paths.

List every upstream file the fork touches, compared with the upstream release it is based on:

```sh
git diff --name-status v0.89.0 main | grep '^M'     # tag of the latest merged upstream release
```
