# Development guide

Commands and pitfalls confirmed while building this fork on macOS (arm64, zsh). CI runs
on `ubuntu-latest`.

## Build

```sh
GOWORK=off make pint                              # like CI and releases: pinned metricsql tag from go.mod
go build ./...                                    # with a local go.work: builds against ../metricsql
./pint version                                    # 2nd line shows the metricsql version
```

- **Stale `./pint`.** `make pint` / `make build` does nothing when `./pint` is newer than
  every source file. That happens after switching `go.work` on or off, or after a pin
  change. Run `rm -f pint` or `make -B pint` first.
- **`go.work`.**
  - A local, gitignored `go.work` (copied from `go.work.example`) points at `../metricsql`.
    While it exists, `go` builds against that local checkout, and `pint version` prints
    `metricsql:  (devel)`.
  - Use `GOWORK=off` for anything that must match CI or a release.
  - `go tool -modfile=...` doesn't work in workspace mode, so it also needs `GOWORK=off`.
- **Version string.** `make pint` sets it with
  `git describe --tags --match 'v[0-9]*' --exclude '*-*'`, giving for example
  `v0.89.0-46-g7624ce44`. Plain `go build` gives `unknown (revision: unknown)`.
- **Reproducing a release binary.**
  - Build with `GOWORK=off`, from a clean tree, at the release commit.
  - `go version -m ./pint` must show `vcs.modified=false` and
    `=> github.com/AnatoliiVolchkov/metricsql v<upstream>-pint.N`, the tag pinned in `go.mod`.

## Test

```sh
GOWORK=off go test ./...                          # everything; Docker VictoriaMetrics tests skip without Docker
GOWORK=off make test                              # -race -count=3 -coverpkg (slow); CI runs it with -count=1
go test ./internal/checks/ -run TestMetricsQLSyntaxCheck
go test ./cmd/pint/ -run 'TestScripts/0292'       # one testscript
CI=true go test ./...                             # go-snaps: fail on missing snapshots instead of creating them
```

### Tests that fail on macOS only

These fail on a clean checkout too, so ignore them unless your change touches the area:

- `TestScripts/0255_lint_checkstyle_write_error`
- `TestScripts/0129_tls_cacert_bad`
- `TestSeriesCheck/bad_uri`
- occasional timing flakes in watch testscripts under load, such as `0282`, `0228`, `0275`
  (timed out after 540s once) and `0212` (its `curl` after `sleep 3` ran before the first
  watch iteration finished, during a full `-race` run with Docker). Rerun them alone before
  suspecting a change.

### Snapshots (go-snaps) and testscripts

- **Check tests.** They use `*.snap` files next to the test file, for example
  `internal/checks/metricsql_test.snap`.
  - Without `CI=true`, a missing snapshot is **created** and the test passes. Read the new
    snapshot before committing.
  - `UPDATE_SNAPS=true go test ./internal/checks/` rewrites the snapshots of that package.
- **Pitfall: `UPDATE_SNAPS=true` with `-run`** deletes the snapshots of tests that didn't
  run. Always update a whole package, or restore the deleted ones with `git checkout`.
- **Pitfall: snapshot order.**
  - A full-package update can reorder entries in `metricsql_test.snap` and produce a huge
    diff.
  - New entries are appended at the end of the file. Move them next to their siblings
    without changing anything else, then confirm with `git diff` that the change is
    insertions only.
- **Restoring the committed order.** `.claude/tools/snapmerge.py` does the reordering. Pass
  `--drop-missing` when tests were renamed or removed:
  ```sh
  UPDATE_SNAPS=true GOWORK=off go test ./internal/checks/
  git checkout internal/checks/promql_series_test.snap      # macOS-only change
  python3 .claude/tools/snapmerge.py internal/checks/metricsql_test.snap
  git diff --stat internal/checks/                          # insertions and real changes only
  ```
- **Pitfall: macOS-only snapshot changes.** Updating on macOS can rewrite the
  `TestSeriesCheck/bad_uri` snapshot with macOS-specific text. Don't commit that change.
- **Testscripts.** They live in `cmd/pint/tests/NNNN_name.txt` (txtar). The next free number
  is one above the highest existing file.
  - `UPDATE_SNAPSHOTS=1 go test ./cmd/pint/ -run TestScripts` rewrites expected output.
    Review it.
  - For long, wrapped messages prefer `cmp stderr stderr.txt` with exact output, or short
    `stderr 'regex'` assertions.
  - Tests against real VictoriaMetrics use `cmd/pint/tests_vm/*.txt`.
  - **Mock server ports.** Testscripts with a mock HTTP server use a unique port matching
    the test number, for example `0287` listens on `127.0.0.1:7287`. Never reuse another
    test's port.

### Oracle, differential, fuzz and real VictoriaMetrics tests

All are documented in `docs/METRICSQL_TESTING.md`:

- **Oracle:** `internal/checks/testdata/metricsql_oracle/*.yaml`, written with
  `ORACLE_PRINT=1` and `ORACLE_WRITE=1`.
- **Coverage matrix:** every check needs positive and negative oracle cases. A new upstream
  check fails `TestMetricsQLOracleCoverage` until it gets them.
- **Parser corpus:** `internal/parser/metricsql/testdata/corpus/` holds about 6,100 queries
  and the upstream parser's verdicts.
- **Fuzz targets:** `FuzzMetricsQLParse`, `FuzzMetricsQLPositions` and `FuzzMetricsQLChecks`.
- **Real VictoriaMetrics:** `internal/promapi/vmtest` starts Docker Compose with
  `victoriametrics` `v1.155.0` and `v1.155.0-cluster` (since 2026-11-06; v1.153.0 since `8cfc01df` before that), pinned by tag
  and multi-arch index digest (`docker buildx imagetools inspect <image>` prints it).
  - Without Docker these tests skip. `PINT_VM_REQUIRED=1` turns the skip into a failure.
  - Docker Desktop is often not running on the dev Mac. In that case you can check behaviour
    with a few light queries against the real VictoriaMetrics instead, see
    `real-world-testing.md`.

### Lint and format

```sh
GOWORK=off make lint     # golangci-lint + deadcode, via go tool -modfile=tools/...
GOWORK=off make format   # betteralign + golangci-lint fmt
gofmt -l internal cmd
```

Past lint findings worth remembering: `unlambda`, `ST1023`, and exhaustruct for struct
literals. Copy the field set of a neighbouring `Problem{...}` literal.

## Adding or changing a check for MetricsQL

1. **Look at what upstream does first.** PromQL mode must stay identical to upstream, so
   gate new behaviour with `expr.Language().Name() == metricsql.Name`, or with
   `prom.Backend() == promapi.BackendVictoriaMetrics` for server-specific behaviour.
2. **Put MetricsQL logic in a new file** where you can, for example
   `promql_syntax_metricsql.go`, and call it from the upstream file in one or two lines.
   This keeps upstream merges clean.
3. **Verify the semantics before coding.** Use a light query against real VictoriaMetrics,
   or read the vmalert source. Record the evidence in the commit message and in
   `vmalert-and-victoriametrics.md`. Several fixes in this fork came from discovering that
   VictoriaMetrics and vmalert differ from Prometheus. Never assume they behave the same.
4. **Tests:**
   - cases in `internal/checks/metricsql_test.go` (snapshots);
   - an oracle case, if the check's positive or negative coverage changes;
   - a testscript in `cmd/pint/tests/`, for user-visible CLI behaviour.
5. **Run the fuzz smoke tests.** Run `FuzzMetricsQLChecks` (30s), and
   `FuzzMetricsQLParse` if the parser or converter changed.
   - A failing input is written to `testdata/fuzz/<target>/`. If it is caused by your change,
     fix the change and commit the input as a regression test.
   - If it predates your change (check with the previous release binary), move it out of
     the repository and record it in `backlog.md`. Committing it would fail CI.
6. **Re-run everything after the last code change.** A one-line shortcut added after the full
   run in `f67b81d3` panicked on entries without a rule and was only caught by pint CI
   (`0288`). Run the full suite and `compare-binaries.sh` again before every push, and test
   entries of groups that failed to parse, they have no rule.
7. **Check for false positives on large inputs.** Build the previous and the new binary,
   then compare them on every rule set:
   ```sh
   .claude/tools/compare-binaries.sh <old pint> <new pint> /tmp/pint-compare
   ```
   - It covers `.github/metricsql-test`, the parser corpus as rules, and, when present, the
     workspace `test/` sets and the private rule set from `PINT_PRIVATE_RULES`.
   - Only the intended findings may change. Explain every difference in the commit message.
8. **Docs:** `docs/checks/<check>.md` and an entry under
   `docs/changelog.md` → "Unreleased (VictoriaMetrics fork)" (Added, Fixed or Changed).

## Comparing with upstream pint

Build the upstream release the fork is based on (now v0.89.2) to tell fork regressions from
upstream behaviour:

```sh
git worktree add --detach /tmp/pint-upstream refs/upstream-tags/v0.89.2
(cd /tmp/pint-upstream && GOWORK=off go build -o /tmp/pint-up ./cmd/pint)
git worktree remove --force /tmp/pint-upstream
# then run both binaries with an empty config (-c empty.hcl) on PromQL rules and diff the output
```

- Upstream pint rejects configs that contain `query_language` or `backend`. Give it a
  config without them.
- Bugs found this way so far live in upstream code. Report them separately; don't mix them
  into fork commits. Example: the console reporter panics on escaped Unicode in
  double-quoted YAML, see `backlog.md`.

## Committing

- **Separate commits.** One logical change per commit. The user requires every big change
  to be a separate commit ("all big changes must be separate commit"), and docs-only work
  in its own commit.
- **Message format:** `area: lowercase summary` (`checks: ...`, `ci: ...`, `docs: ...`,
  `parser: ...`, `cmd: ...`, `test: ...`). The body explains why, with the evidence. End it
  with the attribution trailer the harness asks for.
- **Two pending changes that touch the same files.** Commit one, run `git stash push -u`,
  test that commit alone, then `git stash pop`.
- **Commit and push only when the user asks.** The user approves pushing explicitly each time,
  for example "yes, push", "fix it in a separate commit and push".

## Environment gotchas seen in this workspace

- **Command output proxy.** Shell output on the user's machine goes through an output
  condenser (`rtk`):
  - It can summarise long output. Re-run with `rtk proxy <cmd>` if a result looks garbled.
  - It may intercept `./pint ...` when chained with `&&`, failing with "Failed to spawn process".
    Call the binary by absolute path in a separate command.
- **zsh.**
  - zsh doesn't word-split unquoted variables. Use arrays or functions in scripts.
  - A bare `=====` line is parsed as a command. Quote separators: `echo '====='`.
  - `path` is tied to `$PATH` in zsh. A loop variable named `path` wipes `PATH`, and `cmp`,
    `diff` and `head` then fail with "command not found". Use another name.
- **`PATH` contains a space** (VMware Fusion). Quote `"$PATH"` in scripts.
- **No PyYAML.** Use `yq` (Homebrew) to convert YAML to JSON, then process it with `jq` or
  `python3`.
- **`go -C <dir>`** runs the go tool in another directory without `cd`. The Bash tool's
  working directory resets between calls, and background `go test ./...` with relative
  paths has failed because of that.
- **Counting server requests.** With `-l debug`, count `Getting prometheus ...` and
  `Running prometheus query` log lines. Many more lines say `Scheduling ...`, but repeated
  queries are served from pint's cache.
- **No `timeout` command on macOS.** Use `go test -timeout 12m` to stop a hanging test run.
  A full `TestScripts` run hung once on 2026-10-08 and passed in 22s on the rerun.
- **Background waits.** Plain `sleep` loops can be blocked. Wait for long CI runs with a
  backgrounded `gh run watch <id> --exit-status`.
