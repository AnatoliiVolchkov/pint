# MetricsQL and VictoriaMetrics testing

This document describes how pint's MetricsQL and VictoriaMetrics support is tested. The
goal is not only that tests pass, but to show that a problem pint reports for a MetricsQL
rule is real, that valid MetricsQL doesn't produce false positives, and that diagnostics
point at the right part of the rule.

## Architecture

| Layer | Where | What it proves |
|-------|-------|----------------|
| Upstream tests | `*_test.go` from cloudflare/pint, unchanged | PromQL mode keeps working exactly like upstream. |
| MetricsQL check tests | `internal/checks/metricsql_test.go` (snapshots) | Every check runs in MetricsQL mode against mocked VictoriaMetrics responses, including mocked metadata. |
| Test oracle | `internal/checks/metricsql_oracle_test.go`, `testdata/metricsql_oracle/*.yaml` | Exact expected problems (reporter, severity, summary, message, fragment, byte offsets, line) for positive, negative and boundary cases of every check. |
| Real VictoriaMetrics semantics | oracle cases with `data`, `TestVictoriaMetricsSemantic` | Online checks run against single-node and cluster VictoriaMetrics with synthetic series, after confirming what VictoriaMetrics itself returns for them. |
| Coverage matrix | `metricsql_oracle_coverage_test.go` | Every check in `checks.CheckNames` is classified and has the required cases, a new upstream check fails the test until it gets cases. |
| Parser differential | `internal/parser/metricsql/differential_test.go` | pint accepts exactly the queries the unmodified upstream MetricsQL parser accepts, for 6108 corpus queries. |
| PromQL vs MetricsQL differential | `internal/checks/differential_test.go` | For queries valid in both languages offline checks report the same problems, except documented differences. |
| Metamorphic tests | `internal/checks/metamorphic_test.go` | Whitespace, newlines, redundant parentheses and comments don't change findings, only offsets move. |
| Source position tests | metricsql `source_test.go`, `source_provenance_test.go`; pint `internal/parser/source/metricsql_with_test.go`, `internal/diags/position_block_test.go` | Every expression's source span re-parses to the same expression; WITH expansion maps to documented use-sites. |
| Fuzz tests | metricsql `FuzzParseSource`; pint `FuzzMetricsQLParse`, `FuzzMetricsQLPositions`, `FuzzMetricsQLChecks` | No panics and no out-of-range or misplaced positions for arbitrary input. |
| Backend error tests | `internal/checks/metricsql_errors_test.go` | HTTP errors, malformed and partial responses, timeouts and refused connections are reported as "unable to run checks", never as rule problems. |
| API routing | `internal/promapi/victoriametrics_test.go` (mocks), `TestVictoriaMetricsAPI` (real) | Exact endpoints and URL layout for single-node and cluster (vmselect) VictoriaMetrics. |
| CLI end-to-end | `cmd/pint/tests/02*_vm_*.txt` (mocks), `cmd/pint/tests_vm/*.txt` (real) | The pint binary's `lint`, `ci` and `watch` on valid and broken rules, exit codes, YAML lines and caret positions. |

### Unit and golden tests

`internal/checks/metricsql_test.go` uses the same snapshot harness as upstream check tests
(`metricsql_test.snap`), `internal/parser/source/metricsql_test.go` covers label analysis and
`internal/parser/metricsql/metricsql_test.go` the AST conversion. Snapshots are updated
with `UPDATE_SNAPS=true go test ./internal/...`.

### Test oracle

Each file in `internal/checks/testdata/metricsql_oracle` lists cases for one check:

```yaml
check: promql/series
cases:
  - name: label value never existed
    kind: positive            # positive, negative or boundary
    rules: |
      - record: x
        expr: 'rate(series_label_total{job="web"}[5m])'
    data:                     # optional: imported into real VictoriaMetrics
      step: 15s
      series:
        - {metric: 'series_label_total{job="api"}', values: 0+10x40}
    vm:                       # what VictoriaMetrics returns for the data
      - query: 'count(series_label_total{job="web"})'
        result: empty
    expect:                   # every problem the check must report, nothing else
      - rule: x
        reporter: promql/series
        severity: bug
        summary: query on nonexistent series
        diagnostics:
          - message: '`vm` Prometheus server at $URI has the ...'
            fragment: job="web"
            start: 24         # byte offsets inside the query
            end: 33
            line: 2
```

Positive cases must report at least one problem and negative cases none; `expect_cluster`
overrides `expect` where the cluster legitimately differs, for example vmselect doesn't
report its retention, so cases without `settings.retention` expect nothing on the cluster. Values use `<v>x<n>`, `<v>+<d>x<n>`, `<v>-<d>x<n>` and `_x<n>` (gaps).

Writing new cases:

```sh
ORACLE_PRINT=1 go test ./internal/checks -run 'TestMetricsQLOracle$|TestVictoriaMetricsSemantic' -v
# review the printed problems, then store them as expectations:
ORACLE_WRITE=1 go test ./internal/checks -run 'TestMetricsQLOracle$|TestVictoriaMetricsSemantic'
UPDATE_ORACLE_MATRIX=1 go test ./internal/checks -run TestMetricsQLOracleCoverage
```

`ORACLE_WRITE` refuses to store a positive case without problems or a negative case with
problems.

### Differential tests

The parser corpus in `internal/parser/metricsql/testdata/corpus` is generated from
upstream metricsql `parser_test.go`, Prometheus `promql/parser/parse_test.go`, pint
testscripts, the MetricsQL documentation (pinned commit) and a hand-written list of
MetricsQL-only syntax, plus deterministic malformed mutations. `upstream_verdicts.txt`
records for every query whether the unmodified upstream parser, at the version our fork is
based on, accepts it. Regenerate with network access:

```sh
UPDATE_METRICSQL_CORPUS=1 go test ./internal/parser/metricsql -run TestGenerateCorpus
```

Intentional differences are documented in
`internal/parser/metricsql/testdata/corpus/KNOWN_DIFFERENCES.md`.

### Fuzz tests

CI runs every fuzz target for 30 seconds. Long campaigns:

```sh
go test ./internal/parser/metricsql -run '^$' -fuzz=FuzzMetricsQLParse -fuzztime=1h
go test ./internal/parser/metricsql -run '^$' -fuzz=FuzzMetricsQLPositions -fuzztime=1h
go test ./internal/checks -run '^$' -fuzz=FuzzMetricsQLChecks -fuzztime=1h
# in the metricsql repository
go test -run '^$' -fuzz=FuzzParseSource -fuzztime=1h .
```

Failing inputs are written to `testdata/fuzz/<target>/` and become regression tests; commit
them together with the fix.

## Real VictoriaMetrics

`internal/promapi/vmtest` provides the deployments. `vmtest.Start(t)`:

1. uses `PINT_VM_URL` (single-node), `PINT_VM_CLUSTER_URL` (vmselect, for example
   `http://127.0.0.1:8481/select/0/prometheus`), `PINT_VM_CLUSTER_INSERT_URL` (vminsert) and
   optionally `PINT_VM_CLUSTER_STORAGE_URL` (vmstorage) when `PINT_VM_URL` is set;
2. otherwise starts `internal/promapi/vmtest/docker-compose.yml` with Docker Compose:
   single-node VictoriaMetrics and a vmstorage/vminsert/vmselect cluster, images pinned to
   `v1.153.0` / `v1.153.0-cluster` by tag and digest, random localhost ports. The project is
   shared by all tests of one test binary and removed by a reaper process when the binary
   exits;
3. skips the test if Docker isn't available, unless `PINT_VM_REQUIRED` is set, in which case
   it fails.

Fixture data is written with `Target.Import` to `/api/v1/import/prometheus` (vminsert on the
cluster), the newest sample 5 seconds in the past so `-search.latencyOffset` doesn't hide it,
followed by `/internal/force_flush` and polling until every series is queryable.
`Target.Query` runs MetricsQL directly against VictoriaMetrics.

Run only the real VictoriaMetrics tests:

```sh
# single-node and cluster, started with Docker
PINT_VM_REQUIRED=1 go test -count=1 -v \
  -run 'VictoriaMetrics(Integration|Scripts|Semantic|API)|ImportAndQuery' \
  ./internal/promapi/... ./internal/checks/ ./cmd/pint/

# against your own deployments
docker compose -f internal/promapi/vmtest/docker-compose.yml -p pint-vm up -d --wait
export PINT_VM_URL=http://$(docker compose -p pint-vm port vmsingle 8428)
export PINT_VM_CLUSTER_URL=http://$(docker compose -p pint-vm port vmselect 8481)/select/0/prometheus
export PINT_VM_CLUSTER_INSERT_URL=http://$(docker compose -p pint-vm port vminsert 8480)/insert/0/prometheus
export PINT_VM_CLUSTER_STORAGE_URL=http://$(docker compose -p pint-vm port vmstorage 8482)
go test -count=1 -run 'VictoriaMetricsSemantic' ./internal/checks/
docker compose -p pint-vm down -v
```

Only single-node tests: unset `PINT_VM_CLUSTER_URL` and `PINT_VM_CLUSTER_INSERT_URL` with
`PINT_VM_URL` set (scripts needing a cluster are skipped). Only cluster tests:
`go test -run 'TestVictoriaMetricsSemantic/cluster|TestVictoriaMetricsIntegration/cluster|TestVictoriaMetricsAPI/cluster' ./internal/...`.

## Full suite locally

```sh
export GOPRIVATE='github.com/AnatoliiVolchkov/*'
GOWORK=off make test          # everything, -race -count=3, Docker tests included
GOWORK=off make lint
GOWORK=off go test -count=1 ./...
```

## CI

- `pint-ci.yml`: `go mod tidy` check, `make lint`, `make format` check, `go vet`, `make test`
  with `PINT_VM_REQUIRED=1` (all tests with the race detector, Docker VictoriaMetrics
  included), a coverage summary, 30 second fuzz smoke tests, `make build` plus cross builds
  uploaded as the `pint-binaries` artifact, and validation of `docs/examples`.
- `integration.yml`: only the real VictoriaMetrics tests, verbose, with the log as an
  artifact.
- metricsql `ci.yml`: vet, tests, race tests and fuzz smoke test on go.mod's and the latest
  Go, plus the upstream parity job running upstream's test files unchanged.

## Known limitations

- Metric metadata depends on the VictoriaMetrics version. Single-node VictoriaMetrics stores
  it since v1.130.0 and has it enabled by default since v1.137.0 (`-enableMetadata`), older
  versions return none. The v1.153.0 test images store metadata from `# TYPE` lines of
  `/api/v1/import/prometheus`, but the oracle imports data without them. So the positive paths
  of `promql/counter` and of the gauge detection in `promql/rate` are covered with mocked
  metadata, real VictoriaMetrics cases prove there are no false positives when metadata is
  missing.
- VictoriaMetrics keeps metadata in memory, separately from samples. Entries are refreshed on
  ingestion, dropped after an hour without updates and lost on restart, so a metric can have
  no metadata. VictoriaMetrics v1.145.0 returned a single entry per metric name, and when
  exporters disagree about a metric's type the answer can change between runs:
  `promql/counter` and `promql/rate` then report a counter as a gauge if an exporter declares
  it with a wrong `TYPE`.
- `promql/features` doesn't apply to VictoriaMetrics, which has no feature flags.
- vmselect doesn't report its retention (VictoriaMetrics v1.153.0 cluster: only vmstorage has
  `-retentionPeriod`). Without `retention` in the server config pint doesn't guess it,
  `promql/offset` and `promql/range_query` don't check retention on clusters and pint logs a
  warning. Oracle cases set `retention: 1488h` (62 days, the test deployments' value).
- Upstream pint only checks range selectors, not subquery ranges, against retention.
- VictoriaMetrics keeps the metric name for some rollup functions like `max_over_time()`,
  pint doesn't model metric names dropped by functions in either language.
- YAML position mapping limitations shared with PromQL mode are listed in
  `KNOWN_DIFFERENCES.md`.

## Check coverage matrix

Generated by `TestMetricsQLOracleCoverage` from `internal/checks/testdata/metricsql_oracle`.
Regenerate with `UPDATE_ORACLE_MATRIX=1 go test ./internal/checks -run TestMetricsQLOracleCoverage`.

<!-- oracle-matrix:start -->
| Check | Classification | Positive | Negative | Boundary | Real VictoriaMetrics cases | Notes |
|-------|----------------|----------|----------|----------|----------------------------|-------|
| `alerts/absent` | query, online | 3 | 1 | 0 | 4 |  |
| `alerts/annotation` | language agnostic | 1 | 1 | 0 | 0 |  |
| `alerts/comparison` | query, offline | 2 | 2 | 1 | 0 |  |
| `alerts/count` | query, online | 1 | 1 | 2 | 4 |  |
| `alerts/external_labels` | query, online | 2 | 1 | 0 | 3 |  |
| `alerts/for` | language agnostic | 1 | 1 | 0 | 0 |  |
| `alerts/template` | query, offline | 2 | 2 | 1 | 0 |  |
| `group/interval` | query, offline | 4 | 5 | 1 | 1 |  |
| `labels/conflict` | query, online | 2 | 2 | 1 | 3 |  |
| `promql/aggregate` | query, offline | 3 | 2 | 1 | 0 |  |
| `promql/counter` | query, online, no VictoriaMetrics positive | 0 | 2 | 0 | 2 | needs metric metadata, the oracle imports data without TYPE lines; positive cases use mocked metadata in metricsql_test.go |
| `promql/features` | query, online, no VictoriaMetrics positive | 0 | 2 | 0 | 2 | VictoriaMetrics has no feature flags, every MetricsQL function is always available |
| `promql/fragile` | query, offline | 2 | 2 | 1 | 0 |  |
| `promql/impossible` | query, offline | 3 | 3 | 1 | 0 |  |
| `promql/nan` | query, offline | 2 | 3 | 1 | 2 |  |
| `promql/offset` | query, online | 3 | 1 | 1 | 5 |  |
| `promql/range_query` | query, online | 2 | 1 | 3 | 6 |  |
| `promql/rate` | query, online | 2 | 3 | 2 | 7 |  |
| `promql/regexp` | query, offline | 3 | 2 | 1 | 0 |  |
| `promql/selector` | query, offline | 3 | 2 | 1 | 0 |  |
| `promql/series` | query, online | 3 | 3 | 1 | 7 |  |
| `promql/syntax` | query, offline | 6 | 5 | 1 | 0 |  |
| `promql/vector_matching` | query, online | 1 | 3 | 1 | 5 |  |
| `query/cost` | query, online | 3 | 1 | 0 | 4 |  |
| `rule/dependency` | query, offline | 3 | 3 | 1 | 0 |  |
| `rule/duplicate` | query, offline | 1 | 1 | 1 | 0 |  |
| `rule/for` | language agnostic | 1 | 1 | 0 | 0 |  |
| `rule/label` | language agnostic | 1 | 1 | 0 | 0 |  |
| `rule/link` | language agnostic | 1 | 1 | 0 | 0 |  |
| `rule/name` | language agnostic | 1 | 1 | 0 | 0 |  |
| `rule/reject` | language agnostic | 2 | 1 | 0 | 0 |  |
| `rule/report` | language agnostic | 1 | 0 | 0 | 0 |  |
<!-- oracle-matrix:end -->
