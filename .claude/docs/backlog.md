# Backlog: open items, known issues, unapproved ideas

Nothing here is approved for implementation unless it says so. Ask the user before
starting any of it. When an item is done or rejected, update this file in the same commit.

State as of 2026-10-13: `main` is the release `v0.89.2-116-ga54bdcaf` plus `.claude` docs,
based on upstream v0.89.2.

Done on 2026-10-07, removed from the lists below:

- the misleading VictoriaMetrics texts in `alerts/external_labels` and `labels/conflict`, which
  now also run offline (`9ce9b2b2`);
- the guessed 31d retention on vmselect, replaced by the `retention` option (`294c8f26`);
- `group/interval` false positives for vmalert alerting rules (`2211c902`), then the whole
  check rebuilt for vmalert around rules reading the results, verified on v1.153.0 in Docker
  (`6f3473c0`, `f67b81d3`);
- the `query/cost` memory estimate for VictoriaMetrics (`cace9e15`).

Done on 2026-10-09 (items of the improvement list below):

- the console reporter panic `slice bounds out of range` on rules in double-quoted ConfigMap
  values (`980d40db`), also in upstream v0.89.1 and v0.89.2;
- vmalert advice in `rule/dependency` cross-group warnings, no warning for groups chained
  with `eval_offset` (`da3a1914`);
- "non-counter" from VictoriaMetrics metadata as a Warning, Information for `_total` names
  (`772f09aa`), which fixes the false Bug on prom.example.com;
- windows derived from the query step in `promql/rate` (`6995921d`);
- rules returning series of more than one metric in `labels/conflict` (`012fcd51`);
- subqueries without a step in `promql/syntax` (`969ed765`);
- `maxTotalSamples` on VictoriaMetrics from query traces (`d420e103`).

Done on 2026-10-12, released in `v0.89.1-111-g7f2581ff`: `alerts/count` counts selectors without a window as
`default_rollup(selector[W])` instead of `last_over_time(selector[W])` (`d7413a45`).
`last_over_time` ignores staleness markers, so a series replaced within W (a recreated pod)
gave a false "unable to run checks" Bug with "duplicate time series" and inflated counts.
Found by another session on vm-demo, reproduced in Docker before the fix.

Done on 2026-10-10 (fourth batch, released in `v0.89.1-108-g3e535ce9`):

- new `rule/vmalert` check, MetricsQL configs only: `debug: true` left on groups and rules
  (`da3694d7`) and `latency_offset` longer than `eval_delay` (`7c394a90`);
- `sum(...) by (__name__)` turned out to be reported since `012fcd51` (the backlog entry was
  wrong); the real problem was a one-name regexp like `{__name__=~"x"}` treated as any
  metric, fixed in `selectorMetricNames` (`305b9d44`);
- "VictoriaMetrics server" and vmalert wording instead of Prometheus (`f4a3639b`);
- `promql/rate` checks windows against intervals measured with `scrape_interval()` when
  they're longer than `scrapeInterval` (`1d7fbcb8`). A data-driven `group/interval` wasn't
  needed: it checks rule outputs, whose interval is the group interval, not scraped data.

Done on 2026-10-10 (third batch, released in `v0.89.1-101-g24123150`):

- the upstream console panic on a double-quoted expr starting with a `\u` escape: values
  with no mapped character are placed on their first line (`6cd41dfa`);
- explicit `rate()`/`irate()`/`increase()` windows need `max(S, 2S - maxPrev)` and
  `deriv()` `2S` on VictoriaMetrics, Warning (`fd0476b4`); the step-derived rule from
  `6995921d` was too strict and uses the same formula now;
- CI runs the suite once (`-race -count=1`, `a697af28`).

`promql/series` (`1fcdc6b0`) was then checked against prom.example.com: no false positives, see `real-world-testing.md`.

Done on 2026-10-10: Dependabot version updates removed (`1bed0368`), alerts enabled; image
pulls retried in CI (`183e6de9`).

Done on 2026-10-10: the fuzz-found regexp differential (`{A!~")("}`, `)000(`) is documented
in `internal/parser/metricsql/testdata/corpus/KNOWN_DIFFERENCES.md` and skipped by
`knownRegexpDifference` in the fuzz test, pint keeps rejecting such regexps (user decision).

Done on 2026-10-09 (second batch):

- `alerts/absent` reports only regular pauses, at least once a day on average; a single
  outage of a dense metric was reported as a "normal pause" (`046f5a38`);
- `promql/series` warns about metrics with regular pauses longer than the vmalert query step,
  measuring the last hour first (`1fcdc6b0`); time-independent pause tests (`bfb7a3bd`);
- 3.1 `vmalertURI`: `-datasource.queryStep`, `-external.label` and rule files for
  `pint watch rule_files` read from vmalert (`b2070241`);
- CI: images from quay.io (`799f8b7a`), docs-only changes skip CI (`671290cd`).

Open after it:

- `alerts/template` offline only knows `externalLabels` from pint config, so `$labels.<name>`
  of a label set only on vmalert is reported with `--offline`; online it's known.
- The new `promql/series` queries: one extra 1h range request per distinct selector, 7d only
  after a recent pause; on prom.example.com no selector needed the 7d request.

Done on 2026-10-09, after the user asked whether pint could see that `X and absent(S)` on a
sparse metric fires all the time (verified in Docker first, see
`vmalert-and-victoriametrics.md`):

- `alerts/count` counts MetricsQL rules on VictoriaMetrics with the lookback of vmalert
  instant queries, selectors without a window become `last_over_time(selector[W])`
  (`598f8a39`, `default_rollup` since `d7413a45`). Before, `... and absent(x) == 1` on a
  10m metric counted 0 alerts a week;
- `alerts/absent` measures pauses between samples with `count(count_over_time(selector[W]))`
  and warns when they are longer than the `absent()` or `absent_over_time()` window and `for`
  doesn't cover them (`6147897d`).

## Waiting for a user decision

- **Check duplicate group names per ConfigMap data key in relaxed mode.** Offered, not
  chosen.
  - vmalert rejects duplicate group names within one file; pint checks them in strict mode
    only, which is upstream behaviour.
  - A whole-file check would be wrong, because the same group name in different ConfigMaps
    is valid.
- **The improvement list offered on 2026-10-08.** New findings must be Warning at most, never
  Bug (the user's rule). Done: 1.1, 1.3 (2026-10-08), 1.2, 2.1, 2.2, 2.4 (reworked), 3.2,
  3.1 (2026-10-09), 2.3, 2.6 (2026-10-10). Still open:
  - 2.5: negative `offset` in rules. Unverified, and the fork AST prints `Offset: 0s` for
    `foo offset 5m` too, so check how offsets are stored first;
  - 2.7: subqueries over `-search.maxPointsSubqueryPerTimeseries`;
  - 2.8: severity of implicit conversions from the server flag (the idea below, conflicts
    with the "Warning at most" rule, ask first);
  - 4.x: optional policies: window as a multiple of the scrape interval, `for` not shorter
    than the `increase()` window, group `interval` not shorter than the scrape interval.
- **SARIF output** (`--sarif` for `lint` and `ci`), offered on 2026-10-10. Cheap: one new
  reporter next to `internal/reporter/checkstyle.go` plus a flag, no upstream conflicts. Only
  useful with a SARIF consumer, such as GitHub Code Scanning, and pint already comments on
  PR lines through
  `repository { github {} }`. Asked whether the user has one, no answer yet.
- **`query/cost` limit severity.** Exceeding a configured limit uses the cost block
  `severity`, Bug when not set (upstream code), while the upstream docs say Warning. With
  `maxTotalSamples` now working on VictoriaMetrics such Bugs can appear; set
  `severity = "warning"` in the cost block to follow the user's rule.

## Rejected ideas

Evaluated on 2026-10-10 from a list pasted from another session ("metrule", ideas from
RuleScope, RuleProbe, promlint and others). Don't offer them again unless the user asks.

- **Regression-only CI** and **`--changed-since`**: that's what `pint ci` already does. It
  checks only rules `added`, `modified` or `renamed` against `ci { baseBranch }` (or
  `--base-branch`), problems in untouched rules aren't reported and don't fail CI, and
  `--fail-on` sets the threshold.
- **Plugin or custom-check API**: the examples (required labels, allowed severity values,
  `runbook_url`) are `rule { label {...} annotation {...} }` config blocks, and `rule/link`
  requests the URLs. Go plugins would break macOS and Windows builds and make every upstream
  merge harder.
- **`explain`**: problems already carry `Details` and a link to the check docs, and the
  online checks explain what they saw (when the metric was last present, which label value
  never existed, measured pauses).
- **`compare` before and after**: would run every online check on the base branch too,
  doubling the load on real servers, to show what `pint ci` already implies.
- **Quality score, LLM-assisted fix, web UI**: a score is subjective, `pint lint --json`
  can already be fed to an agent while lint stays deterministic, a UI doesn't fit a CLI.
- **Renaming to "metrule"**: the binary and the repository can be renamed cheaply, but not
  the Go module path `github.com/cloudflare/pint`, which every file imports; changing it
  turns every upstream merge into a conflict in every file.

## Known bugs

- **VictoriaMetrics metadata** keeps one type per metric name. Since `772f09aa` a wrong type
  gives a Warning (Information for `_total` names), not a Bug, for `rate()` and `increase()`.
## Ideas (not offered to the user yet)

- **Real metadata in the oracle.** Low value. Since `8cfc01df` the test images are v1.153.0,
  which store metadata from `# TYPE` lines. A `type` field for oracle series would replace the
  mocks in `promql/counter` and `promql/rate` positive cases. The end-to-end behaviour, single
  and cluster, was already checked by hand on 2026-10-08, and it can't catch the real problem,
  wrong types in production data.
- **`group/interval` with several servers per file** uses the longest lookback of all of them,
  and readers are evaluated with the servers of the writer's file. Fine for one vmalert per
  repository; revisit if the user lints rules for several vmalerts at once.

- **Severity from the server.** "implicit subquery conversion" is a Warning, because
  VictoriaMetrics runs these queries by default. When a server has
  `-search.disableImplicitConversion=true`, they fail outright and should be a Bug. An
  online check could read the flag from `/flags`. prom.example.com has it off.
- **Template error offsets.** Template execution errors include an internal column, such as
  `300: executing ...`, which counts pint's hidden header. Upstream pint shows it too, for
  PromQL. It moves whenever the vmalert header changes, as it did by 26 bytes when
  `$interval` was added.

## Gaps (real rule problems pint doesn't report)

- **`promql/series` on a sparse metric is nondeterministic upstream-side.** Its first query is
  an instant `count(selector)` without `step`, so VictoriaMetrics looks back 5m: a metric
  written every 10m is "missing" on half of the runs and then goes through the "sometimes
  present" path. The new pause warning is deterministic, the old path isn't.
- **`alerts/count` keeps range semantics for step-derived windows.** `rate(x)` and `x[2i]`
  are not rewritten, their window is the step in vmalert but `max(step, interval)` in a range
  query. `promql/rate` reports them when the scrape interval is longer than the step.
- **Rollup functions without a window.**
  - Example: `delta(nginx_vts_upstream_requests_total{...})` gets its window from the query
    step (vmalert: 5m by default).
  - `rate(x)[d]` (`43f024af`) and implicit subqueries like `rate(sum(x))` (`fed6e413`) are
    reported, but a plain windowless `rate(x)` or `delta(x)` is not. Upstream
    `IsLikelyInvalid()` calls `rate(foo)` valid, since it usually returns the expected
    results.
- **`delta()` on counters.** Only `promql/counter` catches it, and that check needs reliable
  metadata.
- **`index $labels "x"`** in templates isn't checked. `$labels.x`, `.Labels.x` and
  `$l := $labels` aliases are. This is upstream behaviour.
- **Function arity isn't checked offline.** `abs()`, `topk(x)` and `histogram_quantile(0.9)`
  pass and fail only online, as "unable to run checks". Porting VictoriaMetrics' arity rules
  for about 250 functions was judged not worth it.
- **Subquery ranges aren't checked against retention.** This is upstream behaviour.
- **Parser version skew.** pint pins metricsql v0.87.5, while a real server may run an older one.
  The test Docker images are v1.153.0 (metricsql v0.87.4).

## Cosmetic (seen 2026-10-02, recheck before fixing)

- Template errors include an internal offset, such as `300: executing ...` (see Ideas).
- The dead-code message repeats itself: "`0 > 0` always evaluates to `0 > 0`" for
  `default 0`.
- An alert whose `expr` is just a string is reported as always firing.

## From code reading only, not confirmed as bugs

- `keep_metric_names` is ignored by label analysis.
- `rollup_rate()` and similar rollups aren't window-checked by `promql/rate`.
- `metricsql.Language.ParseMetricSelector` rejects `or` filters. Production code only calls
  `parser.PromQL.ParseMetricSelector`, in `promql/series` for its settings and for
  `# pint disable promql/series(...)` selectors. So selectors in those places must use
  PromQL syntax, even in MetricsQL mode.
- The VictoriaMetrics `cluster` flag is unused outside tests.

## Repository and CI chores

- **Upstream timing flake** `TestSeriesCheck/series_present_on_other_servers_/_timeout`
  (50ms fallback timeout) failed pint CI on `183e6de9` on a loaded runner, it checked 2
  servers instead of 3; green on rerun. Upstream test, PromQL mode, untouched by the fork.

- **`SYNC_TOKEN` isn't set** in either repository (user decision, 2026-11-04): syncs are done
  from the development PC as `avixyio`, whose token can push workflow changes. Upstream
  changes to
  `.github/workflows/*` can't be pushed by the sync job: it opens an issue instead. In the
  180 days before 2026-10, 69 of 501 upstream pint commits touched workflows. Less relevant
  since `41d2e53d`: the pint job only runs on demand, syncs are done by hand.
- **Squash and rebase merges are still allowed** in both repositories, although sync PRs
  must use merge commits. Disabling them is recommended, the owner does that in the
  repository settings.
- **Helm.** Templates must be rendered before pint can lint them. Relaxed Kubernetes
  parsing needs `parser { relaxed = ["(.*/)?k8s/.*"] }`, and `pint ci` needs
  `ci { baseBranch = "main" }`.
- **GitHub runners.** Jobs are sometimes cancelled with "not acquired by Runner". Rerun
  them with `--failed`.
- **`ORACLE_WRITE` loses data.** Rewriting the oracle files drops YAML comments,
  `message_re` and empty `expect_cluster: []`. After using it, restore unrelated files with
  `git checkout` and re-add what it dropped. Better: edit the expectations by hand.
- **Watch testscripts flake under load** on macOS. `0275_watch_symlink_owner` timed out
  after 540s in a full run on 2026-10-06 and passed alone. `0282` and `0228` behave the same.
