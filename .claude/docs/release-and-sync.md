# Releases, CI and upstream sync

`docs/UPSTREAM_SYNC.md` in both repositories is the full procedure. This page covers the
short version, the current state, and the mistakes already made once.

## Versions

- **Format.** A fork build's version is `git describe` against **upstream release tags
  only**: `v0.89.0-46-g7624ce44` means upstream release v0.89.0, 46 commits on top, commit
  `fae94057`. The user chose this format.
- **Tag filter.** The describe command is
  `git describe --tags --match 'v[0-9]*' --exclude '*-*'`. `--exclude '*-*'` skips the
  fork's own release tags, which contain dashes, so the count keeps growing from the
  upstream release. The same filter is used in:
  - the `Makefile` (`PINT_VERSION`);
  - `.github/workflows/release.yml`;
  - `.github/workflows/pint-ci.yml`.
- **Upstream tags must exist in this repository.** `git describe` only sees tags in
  `refs/tags`. `v0.89.2` was pushed when this repository was created. Since `0fa62d41`, the
  sync script pushes each merged release tag; push the next release tag the same way. Without the tag, builds keep the previous release name. Pushed
  from `refs/upstream-tags/<tag>`, the tag isn't in the local `refs/tags`: run
  `git fetch origin tag <tag>` or local `git describe` keeps the old name.
- **Fork base.** The fork started from upstream `main` at `373a5a2c`, after v0.88.0. Since
  2026-10-07 it is based on upstream releases: **v0.89.0** (PR #3), then **v0.89.1**
  (2026-10-08, PR #4, `8c233f7a`), then **v0.89.2** (2026-10-13, PR #7, `a92c67d1`). The
  changelog keeps
  the fork's "Unreleased (VictoriaMetrics fork)" section above upstream's release sections.

## Releasing pint

Manual workflow `.github/workflows/release.yml`. Run it only when the user asks for a
release.

```sh
gh workflow run release.yml -R AnatoliiVolchkov/pint -f ref=main -f publish=true
gh run list -R AnatoliiVolchkov/pint --workflow release.yml --limit 1
gh run watch <id> -R AnatoliiVolchkov/pint --exit-status       # run it in the background
gh release view -R AnatoliiVolchkov/pint                       # tag, assets, notes
```

To build without publishing, use `-f publish=false`: the binaries are only uploaded as a
workflow artifact.

To check a published release yourself, download its assets. That is a file download, so ask
the user first.

```sh
gh release download <tag> -R AnatoliiVolchkov/pint -p '*darwin-arm64.tar.gz' -p checksums.txt -D <dir>
cd <dir> && shasum -a 256 -c --ignore-missing checksums.txt
tar xzf pint-*-darwin-arm64.tar.gz && ./pint-*/pint version
go version -m ./pint-*/pint | grep -E 'metricsql|vcs.modified'    # vcs.modified=false
```

`gh release view --json` has no `isLatest` field. `gh release list` prints "Latest" in its
text output.

What the workflow does:

1. **Version.** Computes the version with the filter above. It fails if a release with that
   name already exists, meaning the commit was already released.
2. **Build.** Builds linux amd64/arm64 and darwin amd64/arm64 (no Windows build) with
   `GOWORK=off -trimpath`, in `$RUNNER_TEMP` (see the pitfalls below). Writes `checksums.txt`.
   Go is pinned in the workflow (`go-version`, 1.27.3 since 2026-11-05), not read from
   upstream's `go.ver`, which CI uses. Bump it when a new Go patch release is out.
3. **Verify.** Runs `pint version` on the linux binary. The first line must equal
   `<version> (revision: <sha>)`, the second must match the `metricsql:` format, and
   `vcs.modified` must be false.
4. **Publish.** Runs `gh release create <version>` with `--latest`. The notes list the
   commits since the previous fork release, or since the upstream release for the first one.

Pitfalls already fixed:

- **Missing tags.** Checkout with `persist-credentials: false` can't `git fetch` later. The
  previous-release lookup uses local tags from `fetch-depth: 0` with `git rev-parse -q --verify`.
- **`vcs.modified=true`.** Untracked build output inside the checkout marked binaries as
  modified, so the build runs in `$RUNNER_TEMP`.

Releases so far: `v0.88.0-60-g4a44089f`, `v0.88.0-62-g20cff42e`, `v0.88.0-66-gc051ddb6`,
`v0.88.0-67-g8f58695b`, `v0.88.0-72-g28dc5bad`, `v0.89.0-46-g7624ce44` (the first release
on upstream v0.89.0), `v0.89.0-63-g00421a63`, `v0.89.1-68-gef0f90be` (the first release on
upstream v0.89.1), `v0.89.1-78-gfe5a1cef` (2026-10-09, the phase 9 VictoriaMetrics
improvements) and `v0.89.1-82-gcb23ca0b` (2026-10-09, `alerts/count` and `alerts/absent` for
sparse metrics) and `v0.89.1-90-gf3b4609f` (2026-10-10, `promql/series` pauses,
`vmalertURI`, CI changes) and `v0.89.1-101-g24123150` (2026-10-10, `\u` panic fix,
VictoriaMetrics rate windows, Dependabot removal, CI `-count=1`) and `v0.89.1-108-g3e535ce9`
(2026-10-10, `rule/vmalert`, VictoriaMetrics wording, measured rate intervals) and
`v0.89.1-111-g7f2581ff` (2026-10-12, `default_rollup` in `alerts/count`) and
`v0.89.2-116-ga54bdcaf` (2026-10-13, the first on upstream v0.89.2, Latest as of
2026-10-13).

Before releasing, wait for green pint CI and VictoriaMetrics integration tests. The release
workflow doesn't run tests: a docs commit dropped at the public move panicked in testscript `0288` and only CI caught it, so
it was never released. Since `671290cd` documentation-only commits don't run CI, so `main` can
end with a docs commit that has no runs. Then CI must be green on the last commit that ran it,
and `git diff --name-only <that commit>..main` must list only Markdown files, `docs/` (except
`docs/examples/` and `docs/METRICSQL_TESTING.md`) or `.claude/`.

## CI workflows (AnatoliiVolchkov/pint)

| Workflow | Trigger | Notes |
|---|---|---|
| `pint-ci.yml` "pint CI" | push and PR to main except docs-only changes, dispatch | go.mod tidy check, `make lint`, format check, `go vet`, `make test` with Docker VictoriaMetrics (`PINT_VM_REQUIRED=1`), coverage, 4 fuzz smoke jobs (30s each), builds, examples. The test step is `make test`'s command with `-count=1` since `a697af28` (`-count=3` took about 17 of 19 minutes) |
| `integration.yml` | push and PR except docs-only changes, dispatch | real VictoriaMetrics tests only, about 2 minutes |
| `release.yml` | manual | see above |
| `upstream-sync.yml` | dispatch only, the weekly schedule was removed on 2026-10-13 (`41d2e53d`, user decision) | see below |

- **Docs-only changes skip CI** (`671290cd`, 2026-10-09). Both workflows have `paths`
  filters: `**`, then `!**.md`, `!docs/**`, `!.claude/**`. pint CI re-includes
  `docs/examples/**` (the build job validates them) and `docs/METRICSQL_TESTING.md`
  (`TestMetricsQLOracleCoverage` checks its matrix). No other test reads Markdown or docs
  files. Run them by hand with `gh workflow run pint-ci.yml -R AnatoliiVolchkov/pint` if needed.
- **Concurrency.** A new push to a pull request cancels its previous runs; every push to
  `main` has its own group, runs on `main` never wait for or cancel each other.
- **Images come from quay.io** (`799f8b7a`). Docker Hub refused an anonymous pull with
  `toomanyrequests` on 2026-10-09 (run 37989066831): GitHub runners share IPs and both
  workflows pull the same four images. quay.io serves the same index digests. quay.io
  answered one pull with `502 Bad Gateway` on 2026-10-09 (run 37998612265, green on rerun),
  so since `183e6de9` the pull step retries up to 4 times (15s, 30s, 45s apart) with a
  warning annotation per failed attempt.
- **Dependabot** (2026-10-10, user decision). Version updates are off: `1bed0368` deleted
  upstream's `.github/dependabot.yml`, the only switch for them. Their PRs bumped upstream's
  unused `Dockerfile`, Go modules upstream bumps itself (conflicts in `go.mod` at syncs) and
  actions in upstream workflows. PR #5 was closed. On a modify/delete conflict at a sync keep the file deleted
  (`docs/UPSTREAM_SYNC.md`). Dependabot alerts were enabled
  (`gh api repos/AnatoliiVolchkov/pint/vulnerability-alerts -i` gives 204); enabling them started
  "Dependency Graph" runs (event `dynamic`). Security updates stay off.
- **Disabled upstream workflows.** They are kept byte-for-byte and disabled through GitHub
  workflow state, never edited. The list is in `docs/UPSTREAM_SYNC.md`. After every sync,
  check whether upstream added a new workflow and disable it if needed.
- **Flaky runners.** GitHub sometimes can't assign a runner ("The job was not acquired by
  Runner of type hosted even after multiple attempts"). The job is then **cancelled with
  zero steps** and the run shows as failed. That's infrastructure, not code: rerun with
  `gh run rerun <id> -R AnatoliiVolchkov/pint --failed`. It happened twice in a row to the fuzz jobs
  on 2026-10-05.

### Secrets

- **Only the owner can set secrets,** in the browser. None are needed for CI.
- **`SYNC_TOKEN` isn't set** (user decision, 2026-11-04): syncs are done from the development
  PC as `avixyio`, by hand, see `docs/UPSTREAM_SYNC.md`. It is optional. Without it, the sync
  job uses
  `GITHUB_TOKEN`, which:
  - can't push merges that touch `.github/workflows/*`;
  - opens PRs that don't trigger `pint-ci.yml`. The sync job runs the tests itself.
- Never print secret values.

## Upstream sync

Both repositories use the same script, `.github/scripts/upstream-sync.sh`, with identical
copies. Keep them identical when you change one.

- **What gets merged.** By default (`UPSTREAM_MODE=release`), only stable upstream releases
  are merged: the newest tag matching `^v[0-9]+\.[0-9]+\.[0-9]+$`. Upstream tags are
  fetched into `refs/upstream-tags/*` so they never mix with fork tags. The user asked that
  only stable releases are backported, and the fork must keep building and working the same
  way afterwards.
- **Where the merge happens.** In a branch `sync/upstream-<tag>`, with `git merge --no-ff`.
  The script runs build, vet and tests, then pushes and opens a PR.
  - On conflict it opens an issue and fails without pushing.
  - It never pushes to `main`.
- **Exceptions.** Use the `tag=` input to merge a specific release, or `mode=branch` to merge
  the head of upstream main.
- **Merging the PR.** Always use a **merge commit**. Never squash or rebase, and never
  force-push `main`.

```sh
gh workflow run upstream-sync.yml -R AnatoliiVolchkov/pint -f dry_run=true
gh workflow run upstream-sync.yml -R AnatoliiVolchkov/metricsql -f dry_run=true
```

- **No schedule** (2026-10-13, the user's decision, `41d2e53d`): the scheduled run for
  v0.89.2 only opened issue #6 for the changelog conflict every release has. Sync upstream
  releases by hand when the user asks. AnatoliiVolchkov/metricsql dropped its Monday schedule the
  same day (`b6762e9`); both workflows are `workflow_dispatch` only.

The pint CI `test` job takes about 9.5 minutes since `a697af28` (`-race -count=1` with the
Docker VictoriaMetrics tests).

### How the v0.89.0 sync was done (2026-10-07), repeat it for the next release

The sync was done by hand, not with the workflow, to resolve conflicts and recheck
everything locally. The steps:

1. **Merge.** `git switch -c sync/upstream-<tag> main`, then
   `git merge --no-ff refs/upstream-tags/<tag>`.
2. **Resolve conflicts, keeping both sides.**
   - v0.89.0 conflicted in 6 files: `internal/config/prometheus.go` and `discovery.go`
     (struct fields), `go.sum`, `docs/changelog.md` (fork section first, then upstream's
     release), `docs/checks/promql/rate.md` and `docs/configuration.md`.
   - After resolving, `git diff refs/upstream-tags/<tag> -- <file>` must show only the
     fork's additions.
3. **Build.** Upstream may change constructor signatures used by fork code. v0.89.0 added
   `sampleRateLimit` to `promapi.NewPrometheus`, so `NewVictoriaMetrics` and fork tests had
   to follow. `go vet ./...` lists every call site.
4. **Run the full suite.** Fork testscripts that compare stderr exactly need upstream's new
   log lines. `pint ci` now logs "Git file changes parsed", which changed testscript 0281.
5. **Review clean auto-merges of files the fork changed**, for example `promql_rate.go`.
   Upstream's new logic may need MetricsQL semantics. `increase()` got the 1x rule
   automatically, and `increase_prometheus()` needed its own commit.
6. **Commit each adaptation separately** after the merge commit.
7. **Recheck:**
   - `go mod tidy`, `make format`, `make lint` (upstream may bump golangci-lint), the full
     suite and fuzz smoke tests;
   - `.claude/tools/compare-binaries.sh` with the old `main` against the sync branch, which
     must be identical offline;
   - **PromQL parity:** build the upstream tag with `git worktree add`, then run both
     binaries with an empty config on the PromQL-compatible sets. The output must be
     byte-identical.
8. **Push.** Push the branch and the tag
   (`git push origin refs/upstream-tags/<tag>:refs/tags/<tag>`), then open a PR. CI takes
   about 20 minutes. Merge with `gh pr merge <n> --merge`, never squash.
9. **Release only when the user asks.**

### The v0.89.1 sync (2026-10-08)

Done with the same steps. Upstream had 3 bug-fix commits in 5 files:

- the owners metric in `pint watch`;
- `sampleRateLimit` only for query endpoints, which doesn't affect VictoriaMetrics, it has no
  sample limiter;
- no background query cache cleaner, with `CleanCache` renamed to `GC`, called after each
  `pint watch` iteration.

Results:

- **Conflicts:** only `docs/changelog.md`.
- **Fork changes:** the fork's diff against upstream in the four auto-merged code files was
  byte-identical before and after the merge (compare `git diff <old tag> main -- f` with
  `git diff <new tag> -- f`, a quick way to prove a clean merge didn't drop fork code).
- **Follow-ups:** none.
- **Checks:** `compare-binaries.sh` identical, PromQL parity with v0.89.1 identical, lint
  clean, fuzz smoke clean.
- **Full `-race` suite:** only the macOS failures, plus one `0212` timing flake under full
  load. It passed 3 of 3 loaded `TestScripts` runs on both the sync branch and `main`.
- **Landing:** tag `v0.89.1` and the branch were pushed, then PR #4 was opened. pint CI and the
  VictoriaMetrics tests were green. The PR was merged with a merge commit (`8c233f7a`). Its
  tree was identical to the tested PR head, so the release ran right away.

### The v0.89.2 sync (2026-10-13)

Done with the same steps. Upstream had one commit, `5f66bcca` "Round cache start/end", in 7
files: range query cache keys round both ends to the step, slice results are cached for one
slice plus one step, `sampleRateLimit` waits after a query for at most the limit, and a
`sampleRateLimit` note in `docs/configuration.md`.

- **Conflicts:** only `docs/changelog.md`. The scheduled sync job had stopped on it on
  2026-10-12 and opened issue #6; the PR closed it.
- **Fork changes:** the fork's diff against upstream identical before and after the merge in
  all 7 files. The fork's VictoriaMetrics range queries (`alerts/absent`, `promql/series`)
  go through the same `RangeQuery` and only share cache entries more often;
  `sampleRateLimit` stays a no-op on VictoriaMetrics.
- **Follow-ups:** none.
- **Checks:** full `-race -count=1` suite with Docker VictoriaMetrics (only the macOS
  failures), lint, `compare-binaries.sh` identical, PromQL parity with v0.89.2 identical,
  `FuzzMetricsQLChecks` 30s, and old `main` against the sync branch online on a local
  VictoriaMetrics with test data: identical.
- **Landing:** tag `v0.89.2` and the branch pushed, PR #7, all 8 checks green, merged with a
  merge commit (`a92c67d1`), its tree identical to the PR head.
- Both console panics fixed by the fork (`980d40db`, `6cd41dfa`) are still in upstream
  v0.89.2; the changelog says so.

In general:

- Port upstream logic in files the fork migrated to `internal/parser/ast` (checks, parser,
  source, diags) to the `ast` types. Don't revert either side.
- New upstream checks need MetricsQL tests and oracle cases, or the coverage matrix test
  fails.
- Regenerate snapshots after resolving the code; don't hand-merge them.

## Updating the metricsql pin

The metricsql fork is released as `v<upstream>-pint.N` tags. The current tag is
`v0.87.5-pint.3`, the first in this repository. Don't reuse lower numbers. The tags are
public and cached forever by `proxy.golang.org`, so never move or delete a pushed tag.

1. Make the change in metricsql and merge it to its `main`. Tag it:
   `git tag -a v0.87.5-pint.N -m ...`, then `git push origin v0.87.5-pint.N`.
2. Update the pin in pint:
   ```sh
   go mod edit -replace github.com/VictoriaMetrics/metricsql=github.com/AnatoliiVolchkov/metricsql@v0.87.5-pint.N
   GOWORK=off go mod tidy
   GOWORK=off go test ./...
   ```
3. If metricsql moved to a newer upstream release, also update
   `go mod edit -require github.com/VictoriaMetrics/metricsql@vX.Y.Z`.
4. Run `pint version` and check the second line shows the new tag and the upstream version
   it replaces.
5. Commit the pin as its own commit.
