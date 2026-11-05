# History and decision log

What was built, in which commits, and why. Run `git show <sha>` for the full reasoning: the
commit bodies explain the evidence. Dates are 2026.

Release names and pull request numbers before phase 14 refer to the private predecessor of
this repository. Commit SHAs were mapped to this repository's history.

## Phase 1: MetricsQL support and the platform (to 2026-10-01)

Requirements are in `plans/plan-1.md` and `plans/plan-2.md`, in the parent workspace.

- Started from upstream `cloudflare/pint` `main` at `373a5a2c`, after v0.88.0. One local
  commit was split into reviewable commits that each build:
  - `d0147cf9` added the language-neutral AST (`internal/parser/ast`);
  - `0dcbfd6c` and `fe0262fc` added the PromQL and MetricsQL adapters;
  - `2768b9ca` added the VictoriaMetrics backend;
  - `f8c2d21b` migrated the parser, source, diags and checks to the AST;
  - `28339556` added the config options;
  - `5223215d` added the MetricsQL test suite.
- Both repositories became private standalone repos, with remotes `origin` and `upstream`
  (push disabled).
- **Merge-based upstream sync, never rebase or squash.** `50c13184`, `07d76baf`.
- **metricsql pinned to the tag `v0.87.5-pint.3`** (`d5ce3e48`). CI read the then private
  module with a read-only deploy key.
- **Test layers** (`22ee4c33`, `ea04a279`, `95184285`, `e9c3bbb4`, `f9a4c7f8`, `07014c02`): an
  oracle with real VictoriaMetrics data, differential, metamorphic and fuzz tests, API
  routing tests, Docker Compose VictoriaMetrics, and pint CI. See `docs/METRICSQL_TESTING.md`.
- **Robustness fixes found by those tests:**
  - `bdeb7382` source: handle queries MetricsQL accepts without validating them;
  - `87c625d2` accept `sum()` with no series argument;
  - `8ae06b8a` reject partial responses (`isPartial`);
  - `764e936d` diags: don't match block scalar indicators as part of the value;
  - `f1eb3563` parser: don't panic on a comment reported past the end of the file.

## Phase 2: validation against a real VictoriaMetrics (2026-10-01 to 10-02)

The user's rules: test with `test/node-exporter.yml` plus about 300 generated rules
against prom.example.com, keep the load low, and "Do not rewrite the implementation unless a
real bug is found".

- **`3182f327` promql/rate.** VictoriaMetrics' `rate`, `irate` and `deriv` need only 1× the
  scrape interval: they use the last sample before the window. `rate_prometheus` needs 2×.
  The proof was `rate(x[15s])` at a 10s scrape returning every series, against about half for
  `rate_prometheus`.
- **`bcaa36f4` alerts/template, vmalert dialect.**
  - Annotations in vmalert can read `alertname`, `alertgroup` and rule labels. 7 of the 33
    real rules had false Bugs on `$labels.alertname`.
  - The vmalert-only variables and functions were reported as Fatal.
  - The first approach, padding the template header, changed PromQL output, so it was
    replaced by a per-dialect position shift.
- The report is in `test/results/REPORT.md` in the parent workspace.

## Phase 3: product features requested by the user (2026-10-02 to 10-05)

- **`b49687ff`.** `pint version` prints `metricsql:  v0.87.5-pint.3 (replaces VictoriaMetrics/metricsql v0.87.5)`.
  The user asked for exactly this format, in its own commit.
- **`e5f4a709` upstream sync: stable releases only.** The same change was made in
  metricsql (`11dbe28`). The user wanted to backport only stable tagged releases, with the
  patched build working as before.
- **`34df93e3`.** Fork changelog entries moved to "Unreleased (VictoriaMetrics fork)".
- **`9e246035`.** Accept the vmalert rule file schema (`eval_offset`, `concurrency`,
  `headers`, `debug`, `update_entries_limit` and others). `query_offset` is rejected for
  vmalert.
- **Release workflow, in three commits:**
  - `e43ae84e` added it;
  - `9e38f006` fixed the release notes: list the commits since the previous fork release;
  - `7e52997a` fixed the binaries: build in `$RUNNER_TEMP`, so binaries aren't marked
    `vcs.modified`.

  The version format is `v0.88.0-58-g2c1ba3ed`, chosen by the user.
- **`dfdb69fa` and `a9a086bc`.** `.github/metricsql-test/` holds the user's alerting
  standard with valid and broken rules plus Kubernetes manifests. It is placed under
  `.github` so it never conflicts with upstream.
  - User rule: "dont touch files related to pint". `.github/pint/pint.hcl` stays upstream's.
  - `runbook_url` is required only for critical alerts.
- **`e3033735`.** `--external-label name=value[,name=value]` and `PINT_EXTERNAL_LABELS`. The
  user said "Don't break any existing functionality".
- **`7845743c`.** External labels are allowed in MetricsQL annotation templates
  (`$labels.<external>`). It fixed a false positive the user hit: vmalert's `NewGroup` merges
  external labels into rule labels, and label templates still reject them.

## Phase 4: real ConfigMaps (2026-10-05)

- A private rule set was checked with no pint false
  positives. The one exception was the VictoriaMetrics metadata issue. See
  `real-world-testing.md`.
- **`43f024af` promql/syntax.** Warns about `rate(foo)[5m]` in MetricsQL. It was found in
  a real alert, and VictoriaMetrics confirmed the result equals `rate(foo)` with a
  step-based window. The severity is Warning: the query is valid, it just doesn't do what
  it looks like. The logic is in the new file `promql_syntax_metricsql.go` to keep upstream
  merges clean.
- Released as `v0.88.0-67-g8f58695b`.

## Phase 5: latest vmalert, metadata docs, implicit conversions (2026-10-06)

The user asked to fix three items from the backlog, each in its own commit, with a full
recheck after every code change and the latest vmalert as the reference.

- **`9e8928e8` alerts/template matches vmalert v1.153.0.**
  - The 5 Prometheus-only functions are reported for MetricsQL rules: `toDuration`,
    `urlQueryEscape`, `graphLink`, `tableLink`, `tmpl`.
  - `$interval` was added.
  - Templates are parsed with vmalert's function set first, because the Prometheus
    expander knows the extra functions.
- **`4afd21d5` docs: VictoriaMetrics metadata.** Stored since v1.130.0, on by default since
  v1.137.0, kept in memory. The test images just predate it.
- **`fed6e413` promql/syntax warns about implicit subquery conversions.**
  - Covers `rate(sum(foo))` and `sum(foo)[5m]`.
  - Uses upstream `IsLikelyInvalid()`, the same function as VictoriaMetrics'
    `-search.disableImplicitConversion`.
  - A corpus test keeps pint's position finder in agreement with it.
- **Recheck after each code commit:**
  - lint, the full test suite and fuzz smoke tests;
  - old vs new binaries on all rule sets. Only intended findings changed; the private
    rule set was unchanged.
- **Found while rechecking, unrelated to the changes:** a parser differential for
  `foo{bar!~")000("}`, see `backlog.md`.

## Phase 6: upstream release v0.89.0 (2026-10-07)

The user asked to backport upstream v0.89.0 "without breaking anything". It was merged
manually in `sync/upstream-v0.89.0` and landed through PR #3 (merge commit `fae94057`).

- **`4f7845df` merge.**
  - Upstream brought `sampleRateLimit`, `increase()` in `promql/rate`, git-change logging in
    `pint ci`, and dependency bumps.
  - 6 conflicting files were resolved keeping both sides.
  - `NewVictoriaMetrics` follows the new `NewPrometheus(…, sampleRateLimit, …)` signature.
- **`38c6ca45`.** `increase_prometheus()` needs 2x the scrape interval in MetricsQL, and
  `increase()` 1x. Verified on prom.example.com at a 10s scrape interval: `increase(x[15s])`
  returned every series, `increase_prometheus(x[15s])` about half, and
  `increase_prometheus(x[25s])` every series.
- **`7ba6862d`.** `sampleRateLimit` on a VictoriaMetrics server logs a warning. The
  VictoriaMetrics query stats have no sample count, so upstream's limiter can't work there.
  Upstream's documented no-op behaviour was kept, so no config breaks.
- **`0fa62d41`, in both repositories.** The sync script pushes the merged upstream release
  tag. Without `v0.89.0` in the repository, versions would still read `v0.88.0-…`.
- **Recheck results:**
  - the fork was identical before and after offline on every rule set;
  - PromQL output was byte-identical to upstream v0.89.0;
  - the online `promql/rate` check was identical;
  - CI on the PR was 8 of 8 green.
- **Divergence from upstream v0.89.0** (summarised for the user):
  - 220 files changed;
  - 17 new product Go files (+3.3k lines);
  - 48 of 116 upstream product files edited (+1.8k/−0.8k lines, about 10% of upstream code),
    mostly because checks were migrated to the shared AST;
  - the rest is tests, test data, CI and docs.
- Released as `v0.89.0-46-g7624ce44`.

## Phase 7: review of the user's demo on vm-demo (2026-10-07)

The user ran 37 demo alerts (one per check) against their VictoriaMetrics cluster vm-demo,
a federated vmselect, and asked for false positives and improvements. The fixes:

- `cace9e15`, `66ac6ed7`: no `query/cost` memory estimate for VictoriaMetrics, plus a
  VictoriaMetrics section in `docs/checks/query/cost.md`.
- `31b78d83`: testscripts 0278 and 0280 run with `--workers 1`. Concurrent uptime warnings
  made the exact stderr comparison flaky and broke pint CI on main.
- `294c8f26`, `fcfcfbd9`: no guessed 31d retention on vmselect, plus the new `retention` option.
- `9ce9b2b2`: VictoriaMetrics texts for `alerts/external_labels` and `labels/conflict`, and both
  run offline.
- `2211c902`: `group/interval` keeps reporting recording rules (gaps verified in Docker) but no
  longer reports vmalert alerting rules (no flapping, from the vmalert source). **Superseded**
  by `f67b81d3`: the user rejected it, because the 5m limit is a Prometheus setting.
- `6f3473c0`, `f67b81d3`: `group/interval` rebuilt for vmalert. The user asked to re-read the
  upstream docs and make it correct for VictoriaMetrics. Experiments with VictoriaMetrics and
  vmalert v1.153.0 in Docker showed the lookback is the query step, with no gaps in range
  queries, and found the `eval_alignment` lag (readers need 2× the interval). The check now
  looks at the rules reading the results, see `vmalert-and-victoriametrics.md`. It added the
  `queryStep` server option.
- `99d9f944`: `docs/configuration.md` still said vmselect assumes one month of retention.
- `8cfc01df`: Docker test images moved from v1.115.0 to v1.153.0 at the user's request, before
  the release. Oracle cases passed unchanged; `rate_prometheus` is now queried directly.
- `b6f7a2d5`, the fix commit after a docs commit dropped at the public move: the alerting-rule shortcut in `group/interval` panicked on
  entries without a rule. It was pushed, pint CI failed on `0288`, no release was built from
  it.
- Released as `v0.89.0-63-g00421a63` (2026-10-07, after green CI on a docs commit dropped at the public move).

Each code commit was rechecked:

- the full suite with `PINT_VM_REQUIRED=1` (Docker VictoriaMetrics);
- PromQL parity with upstream v0.89.0;
- `compare-binaries.sh`.

Only the intended findings changed.

## Phase 8: upstream release v0.89.1 (2026-10-08)

The user asked for a careful backport of v0.89.1 and to be asked about anything unclear or
dangerous. Nothing was: three bug fixes, one changelog conflict, no follow-up commits. See
`release-and-sync.md` for the checks. Merged by PR #4 (`8c233f7a`) after green CI and
released as `v0.89.1-68-gef0f90be`.

## Phase 9: improvements for VictoriaMetrics users (2026-10-08 and 2026-10-09)

The user asked what else the fork could do for VictoriaMetrics. The list offered after
reading the vmalert "Common mistakes", the VictoriaMetrics alerting best-practices article,
promruval's validations and the VictoriaMetrics sources is in `backlog.md`. The user chose
two items and set a rule: new findings are Warning at most.

- `980d40db`: no panic when positions of a query in a double-quoted ConfigMap value point
  at another line; the line is printed in full. Upstream v0.89.1 panics the same way.
- `da3a1914`: `rule/dependency` gives vmalert advice (chain groups with `eval_offset`), and
  groups chained that way are not reported, both verified with vmalert in Docker.

The user then chose 1.2, 2.1, 2.4, 2.2 and 3.2, each a separate commit, pushed together:

- `772f09aa`: "non-counter" from VictoriaMetrics metadata is a Warning, Information for
  `_total` names; the false Bug on prom.example.com became that Information.
- `6995921d`: `promql/rate` works out windows derived from the step. Docker showed the 1x
  rule also needs the sample before the window to be within the step, see the backlog.
- `012fcd51`: item 2.4 as offered was wrong. vmalert renames a conflicting query label to
  `exported_<name>` instead of failing, so it was replaced by the real "same labelset" case,
  series of more than one metric, verified with vmalert.
- `969ed765`: subqueries without a step in `promql/syntax`; testscript 0293 and a differential
  test allowance were adjusted.
- `d420e103`: `maxTotalSamples` on VictoriaMetrics from `samplesScanned` in query traces.

Every commit: full suite with Docker VictoriaMetrics, PromQL parity with upstream v0.89.1
(console and JSON), `compare-binaries.sh`. A reusable script for these was kept in the session
scratchpad only.

Released as `v0.89.1-78-gfe5a1cef` on 2026-10-09 from a docs commit dropped at the public move (the commits above plus the
`.claude` docs), after green pint CI and VictoriaMetrics integration tests on that commit.

Later that day the user showed an alert from a cluster pint can't reach,
`sum(sum_over_time(S[24h] offset 1h)) > 0 and absent(S) == 1` on a stream-aggregation metric,
whose graph fired almost all the time, and asked whether pint could see that online. Verified
in Docker (vmsingle, vmalert replay, generated sparse series, see
`vmalert-and-victoriametrics.md`): range queries hide pauses shorter than the usual sample
interval, so `promql/series`, `alerts/count` and `vmalert -replay` all said nothing, while
vmalert fired on every other evaluation. Two commits, each verified with the full suite,
compare-binaries and PromQL parity:

- `598f8a39`: `alerts/count` counts MetricsQL rules on VictoriaMetrics with
  `last_over_time(selector[W])` in place of selectors without a window, W being the instant
  query lookback already computed for `group/interval`.
- `6147897d`: `alerts/absent` measures pauses with `count(count_over_time(selector[W]))` over
  the `promql/series` lookback and warns when `absent()` or `absent_over_time()` windows are
  shorter than them and `for` doesn't cover them. pint's `ExpandRangesEnd` adds `step-1s` to
  range ends, so the gap between ranges rounded to the step is the number of missing points.

The user had asked earlier whether a tool could "understand" that such an alert is a false
constant firing; the answer given: no tool knows the intent, but these two facts together
(fires X% of the time, samples kept arriving) are what pint can now show.

Released as `v0.89.1-82-gcb23ca0b` on 2026-10-09 from a docs commit dropped at the public move (the two commits plus the
`.claude` docs), after green pint CI and VictoriaMetrics integration tests on that commit.

## Phase 10: promql/series, vmalertURI and CI (2026-10-09)

The user asked for the `promql/series` gap and backlog item 3.1 as separate commits, Warning
at most, and to stop running CI on documentation commits.

- `799f8b7a`: the last docs push failed the integration workflow on a Docker Hub pull rate
  limit; the test images now come from quay.io, same digests.
- `671290cd`: `paths` filters skip pint CI and the integration tests for Markdown, `docs/`
  and `.claude/` changes (examples and the coverage matrix doc still run pint CI); PR runs
  cancel superseded ones.
- `046f5a38`: proved in Docker that `alerts/absent` from `6147897d` warned about a single 12m
  outage of a dense metric as a "normal pause"; now only pauses at least once a day count.
- `1fcdc6b0`: `promql/series` measures pauses of instant selectors, 1h first, then the
  lookback range. Docker: sparse metrics reported, dense ones stopped after one request.
- `bfb7a3bd`: the pause unit tests depended on the time they ran (RangeQuery's first slice
  starts at a 2h boundary); holes moved to the middle of the week.
- `b2070241`: `vmalertURI`, formats checked against vmalert v1.153.0 in Docker, end to end
  with group/interval, external labels and `pint watch rule_files`.

Each commit: full suite with Docker VictoriaMetrics, compare-binaries, PromQL parity with
upstream v0.89.1. The workflow change can only be proven on GitHub, by the next docs push.

Released as `v0.89.1-90-gf3b4609f` on 2026-10-10 after green CI on a docs commit dropped at the public move (the push had
code, so CI ran on its last, docs-only commit). Then `9848fb0e`: the fuzz-found regexp
difference (`{A!~")("}` failed CI on a docs commit dropped at the public move) documented in `KNOWN_DIFFERENCES.md` and
skipped in `FuzzMetricsQLParse`, as the user decided. The user also chose to remove
upstream's Dependabot config (`1bed0368`, PR #5 closed, Dependabot alerts enabled instead)
after an explanation of the three Dependabot features, and `183e6de9` retries image pulls
after a quay.io 502.

Then, as separate commits: `6cd41dfa` (the upstream `\u` console panic), `fd0476b4`
(VictoriaMetrics rate windows, measured in Docker), `a697af28` (CI `-race -count=1`), and the
`promql/series` check on a real server. Released as
`v0.89.1-101-g24123150` on 2026-10-10 after green CI on that commit; pint CI took 9.5
minutes.

## Phase 11: rule/vmalert, wording and measured intervals (2026-10-10)

The user picked four items from the backlog, each a separate commit (Warning at most):

- `da3694d7`, `7c394a90`: new `rule/vmalert` check (`debug: true`, `latency_offset` vs
  `eval_delay`). A first version added it to `checks.CheckNames`, which changed `pint config`
  in 45 upstream testscripts and every `TestGetChecksForRule` case; it now lives in
  `VMAlertCheckNames` and is only registered for MetricsQL configs.
- `305b9d44`: the `sum by (__name__)` backlog item was wrong, pint already reported it;
  vmalert in Docker found the real false positive, a one-name regexp.
- `f4a3639b`: "VictoriaMetrics server" and vmalert wording, rewritten where reports are
  collected instead of in 34 upstream `promText` calls.
- `1d7fbcb8`: `promql/rate` measures intervals between samples with `scrape_interval()`.

Each commit: full suite with Docker VictoriaMetrics, compare-binaries, PromQL parity.

The user pushed the batch and asked for a release: `v0.89.1-108-g3e535ce9` on 2026-10-10
after green pint CI and integration tests on a docs commit dropped at the public move. The user then pasted a feature list
for the fork from another session ("metrule"). Most of it already exists in pint (`pint ci`
checks only changed rules, `rule {}` blocks are the custom checks); only SARIF was offered,
conditionally. See backlog "Rejected ideas".

## Phase 12: staleness markers in alerts/count (2026-10-12)

Another session with access to a test server found that `598f8a39` broke
`alerts/count` on series replaced within the lookback: `last_over_time(selector[W])` ignores
staleness markers, so a recreated pod or a moved kube-state-metrics left the old series in
the rewritten query. Eight rules got a false "unable to run checks" Bug with "duplicate time
series", and a rule was counted many times instead of once. The user pasted the analysis
and a draft patch. Reproduced first in Docker v1.153.0 with a vmsingle scraping a local file
whose label was changed (old pint: Bug, exit 1; a `count() > 1` rule 1 alert instead of 0),
then fixed with `default_rollup`, which VictoriaMetrics itself uses for selectors without a
window (`d7413a45`). Range queries hid the error while the series overlapped in at most two
points, VictoriaMetrics merges those. `promql_series_vm.go` keeps `last_over_time` in its
advice to rule authors, where bridging pauses is the point. Released as
`v0.89.1-111-g7f2581ff` on 2026-10-12 after green CI on a docs commit dropped at the public move.

## Phase 13: upstream release v0.89.2 (2026-10-13)

The user asked to backport v0.89.2 like v0.89.1, without conflicts or problems and with
docs. One upstream commit (range query cache, `sampleRateLimit`), one changelog conflict, no
follow-ups; see `release-and-sync.md`. The scheduled sync had already stopped on the same
conflict on 2026-10-12 and opened issue #6. Merged by PR #7 (`a92c67d1`) after green CI. The
remote sync branch was deleted by mistake after the merge and restored at the same commit;
earlier sync branches are kept too. During the sync the user asked to remove the weekly
sync: `41d2e53d` keeps the workflow for manual runs only, and `b6762e9` did the same in
AnatoliiVolchkov/metricsql. Released as `v0.89.2-116-ga54bdcaf` on 2026-10-13 after green CI on
`4e2c5f27`.

## Phase 14: public repositories (2026-11)

Both repositories moved from a private predecessor to public forks under `AnatoliiVolchkov`,
so the upstream projects stay their fork parents.

- **History rewritten** with `git filter-repo`, only for the fork's commits: upstream commits
  keep their SHAs. Internal names were replaced, author addresses became
  `users.noreply.github.com`, and `.claude/` was removed from the old history and added back
  in one commit. Commits that only changed `.claude/` are gone, and these docs say so where
  they mention one.
- **metricsql is public,** so the `private-go-modules` action and its secrets are gone.
- Releases and pull request numbers before this point belong to the predecessor. They were
  not carried over, see "Releases in the predecessor repository" below.
- **metricsql v0.87.6 (2026-11-05).** The metricsql fork merged upstream v0.87.6 (its PR #1,
  no conflicts), which parses numeric literals with a leading zero like `08` as decimal
  numbers instead of rejecting them as invalid octal. Tag `v0.87.6-pint.1`, pinned in
  `b7f794fa`. Findings on every local rule set stayed the same; only the corpus error text
  for `0x` changed from `ParseInt` to `ParseFloat`. `upstream_verdicts.txt` still comes
  from upstream v0.87.5: none of its queries changed verdict, so it wasn't regenerated.
- **Releases (2026-11-05):** `release.yml` builds no Windows binaries (`f61500a3`) and pins
  Go 1.27.3 itself instead of reading upstream's `go.ver` (`dc761f82`). The first release
  here, `v0.89.2-95-gf61500a3`, was built from `f61500a3`, with metricsql `v0.87.5-pint.3`.

## Recurring decisions

- **PromQL mode stays byte-for-byte upstream.** Every MetricsQL behaviour is gated on the
  query language or the backend.
- **Fix only real, demonstrated bugs.** Prove each one with VictoriaMetrics queries, the
  vmalert source, or a differential against upstream pint. Put the evidence in the commit
  body.
- **Keep merges cheap.** Prefer fork-only files, keep upstream workflows unchanged
  (disabled instead), and use small hooks in upstream files.
- **Severity.**
  - Bug: the rule will misbehave.
  - Warning: the query is valid but suspicious.
  - Information: style.
- **When the user asks for an explanation,** answer in their language. The user writes both
  Russian and English and has asked for "simple and clear" explanations in Russian.

## Releases in the predecessor repository

Newest first. Not carried over to this repository.

- **Latest release:** `v0.89.2-116-ga54bdcaf` (2026-10-13), metricsql `v0.87.5-pint.3`, the
  first on upstream v0.89.2, built from `main` after green pint CI and VictoriaMetrics
  integration tests on that exact commit. Since `v0.89.1-111-g7f2581ff` it adds the v0.89.2
  merge (upstream's range query cache and `sampleRateLimit` changes, no fork follow-ups) and
  `41d2e53d`. Upstream syncs are manual or `workflow_dispatch` only: the weekly schedule was
  removed at the user's request in pint (`41d2e53d`) and in AnatoliiVolchkov/metricsql (`b6762e9`).
- **Release** `v0.89.1-111-g7f2581ff` (2026-10-12) added, since `v0.89.1-108-g3e535ce9`, one
  fix, `d7413a45`: `alerts/count` counts
  selectors without a window as `default_rollup(selector[W])`, not `last_over_time`, which
  ignored staleness markers, so series replaced within W (recreated pods) gave false
  "duplicate time series" Bugs and inflated counts. Reproduced in Docker, see
  `docs/vmalert-and-victoriametrics.md`.
- **Release** `v0.89.1-108-g3e535ce9` (2026-10-10) added, since `v0.89.1-101-g24123150`:
  - new `rule/vmalert` check, MetricsQL configs only: `debug: true` left on groups and rules
    (`da3694d7`), `latency_offset` longer than `eval_delay` (`7c394a90`);
  - `labels/conflict`: a regexp of literal metric names like `{__name__=~"x"}` counts as
    those metrics, not as any metric (`305b9d44`);
  - "VictoriaMetrics server" and vmalert wording instead of Prometheus in reports
    (`f4a3639b`);
  - `promql/rate` checks windows against intervals measured with `scrape_interval()` when
    they're longer than `scrapeInterval`, Warning (`1d7fbcb8`).
- **Release** `v0.89.1-101-g24123150` (2026-10-10) added, since `v0.89.1-90-gf3b4609f`:
  - no console panic on a double-quoted expr starting with a `\u` escape (`6cd41dfa`);
  - VictoriaMetrics rate windows: `rate`/`irate`/`increase` need `max(S, 2S - maxPrev)`,
    `deriv` `2S`, shorter windows of at least S are a Warning (`fd0476b4`);
  - the fuzz-found regexp difference documented and skipped in `FuzzMetricsQLParse`
    (`9848fb0e`);
  - repository and CI: Dependabot version updates removed (`1bed0368`, alerts enabled in the
    settings, PR #5 closed), image pulls retried (`183e6de9`), the test suite runs once
    (`a697af28`, pint CI about 9.5 minutes instead of 19).
  `promql/series` pauses were checked on a real server: no false positives.
- **Release** `v0.89.1-90-gf3b4609f` (2026-10-10) added, since `v0.89.1-82-gcb23ca0b`:
  - `promql/series` warns about metrics with regular pauses longer than the vmalert query
    step (`1fcdc6b0`);
  - `alerts/absent` reports only regular pauses, not a single outage (`046f5a38`);
  - the `vmalertURI` option: query step, external labels and `pint watch rule_files` from
    vmalert (`b2070241`);
  - CI: test images from quay.io (`799f8b7a`), docs-only commits skip CI (`671290cd`).
- **Release** `v0.89.1-82-gcb23ca0b` (2026-10-09) added, since `v0.89.1-78-gfe5a1cef`:
  - `alerts/count` counts MetricsQL rules on VictoriaMetrics with the lookback of vmalert
    instant queries, `last_over_time(selector[W])` for selectors without a window
    (`default_rollup` since `d7413a45`)
    (`598f8a39`);
  - `alerts/absent` warns when `absent()` or `absent_over_time()` windows are shorter than
    measured pauses between samples that `for` doesn't cover (`6147897d`).
- **Previous release** `v0.89.1-78-gfe5a1cef` (2026-10-09) added, since `v0.89.1-68-gef0f90be`:
  - no console panic on rules in double-quoted ConfigMap values (`980d40db`);
  - `rule/dependency` vmalert advice, no warning for groups chained with `eval_offset`
    (`da3a1914`);
  - "non-counter" from VictoriaMetrics metadata as a Warning, Information for `_total` names
    (`772f09aa`);
  - `promql/rate` windows derived from the query step (`6995921d`);
  - `labels/conflict` for rules returning series of more than one metric (`012fcd51`);
  - `promql/syntax` for subqueries without a step (`969ed765`);
  - `maxTotalSamples` on VictoriaMetrics from query traces (`d420e103`).
- **Older releases:** `v0.89.1-68-gef0f90be` (2026-10-08, the first on upstream v0.89.1,
  only upstream's three fixes on top of `v0.89.0-63-g00421a63`) and `v0.89.0-63-g00421a63`
  (2026-10-07: `query/cost` without the memory estimate, the `retention` option, external
  labels checks offline, `group/interval` rebuilt for vmalert with the `queryStep` option,
  v1.153.0 Docker images). Details in `docs/history.md`.
- **Metric metadata, verified 2026-10-08** (details in `docs/vmalert-and-victoriametrics.md`):
  - Docker v1.153.0: single-node and the cluster both serve metadata from `# TYPE` lines.
    The cluster stores it through vminsert and serves it per tenant from vmselect at
    `/select/<tenant>/prometheus/api/v1/metadata`. Against both, pint reported
    `promql/counter` (counter read directly, Warning) and `promql/rate` (`rate()` on a gauge,
    a Warning for VictoriaMetrics servers since `772f09aa`) from real metadata.
  - prom.example.com serves metadata, but `process_cpu_seconds_total` is
    `type: gauge` there. Releases up to `v0.89.1-68-gef0f90be` reported a false "non-counter"
    Bug for `rate(process_cpu_seconds_total[5m])`; since `772f09aa` it is an Information
    "metric metadata contradicts the metric name". VictoriaMetrics' own `vm_*` metrics have no
    metadata, so they are never reported.
  - So metadata reaches pint everywhere. Wrong types in the data remain possible, which is
    why the finding is never a Bug on VictoriaMetrics.
- **Sparse metrics (2026-10-09):** `598f8a39` and `6147897d` came from a real alert on a
  stream-aggregation metric written every few minutes to tens of minutes. Range queries hide
  pauses shorter than the usual sample interval, so `alerts/count` said "0 alerts" while
  vmalert fired on every other evaluation. `promql/series` warns about such pauses since
  `1fcdc6b0`.
