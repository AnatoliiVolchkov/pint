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

## Done on 2026-11-06 (approved in the backlog review)

- Duplicate group names in one `groups` list in relaxed mode, like a ConfigMap data key, are
  reported by `rule/vmalert` (`d2aebc54`); the canary's planted duplicate is now caught.
- vmalert template execution errors no longer start with pint's internal offset (`fac165d5`).
- The `default` fallback is described as `default 0` in dead code messages (`98ec6870`).
- A MetricsQL rule whose `expr` is a string is dead code, not an always firing alert
  (`d9051673`), checked with vmalert and VictoriaMetrics v1.155.0.

## Parked (the user's decision, 2026-11-06)

- **Custom vmalert templates (`-rule.templates`).** An annotation using a template defined in
  a `-rule.templates` file, like `{{ template "app.summary" . }}`, is a Fatal "template not
  defined" in `alerts/template`, while vmalert loads it. Fix: a config option listing the
  template files, read like vmalert does. The user doesn't use `-rule.templates`.
- **`group/interval` with several servers per file** uses the longest lookback of all of them,
  and readers are evaluated with the servers of the writer's file. Fine for one vmalert per
  repository; revisit if the user lints rules for several vmalerts at once.

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

Removed by the user on 2026-11-06 after a review of the whole backlog, don't offer again
unless asked: negative `offset` (2.5), subqueries over
`-search.maxPointsSubqueryPerTimeseries` (2.7), Bug severity for implicit conversions from the
server flag (2.8), optional policies (4.x), SARIF output, `query/cost` limit severity (set
`severity = "warning"` in the cost block instead), warnings before VictoriaMetrics query limits
(the server's own error already reaches pint), real metadata in the oracle, `promql/series`
nondeterminism on sparse metrics, `alerts/count` for step-derived windows, windowless `rate(x)`
and `delta(x)`, `index $labels "x"` in templates, offline function arity, subquery ranges
against retention, the parser version note, and `or` filters in `promql/series` selectors.
A Warning for `delta()` on `_total` metrics in MetricsQL rules was built and then dropped by
the user on 2026-11-07 before pushing: restart counter alerts using `delta()` would all get
it, while a new pod is a new series, so resets inside one series are rare there.
The "unused VictoriaMetrics `cluster` flag" was a wrong note: `cluster` is derived from the
`/select/<tenant>/prometheus` URI and used in `withRetention`, vmselect doesn't know the
retention, so pint doesn't assume the default there.
`keep_metric_names` isn't a gap in `labels/conflict` (checked 2026-11-06): vmalert fails on
series that differ only by the metric name with or without it, and pint reports both.

## Known bugs

- **VictoriaMetrics metadata** keeps one type per metric name. Since `772f09aa` a wrong type
  gives a Warning (Information for `_total` names), not a Bug, for `rate()` and `increase()`.
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
- **Merge settings (checked 2026-11-06):** both repositories allow only merge commits and
  delete head branches after a merge.
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
