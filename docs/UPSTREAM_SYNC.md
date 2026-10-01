# Upstream synchronization

This repository (`github.com/AnatoliiVolchkov/pint`, private) is pint with VictoriaMetrics
and MetricsQL support. It tracks the public upstream `github.com/cloudflare/pint`
and depends on the private source-aware MetricsQL fork `github.com/AnatoliiVolchkov/metricsql`,
which tracks `github.com/VictoriaMetrics/metricsql`.

## Repository layout

| Repository                    | Visibility | Product branch | Upstream                                      | Upstream branch |
|-------------------------------|------------|----------------|-----------------------------------------------|-----------------|
| `github.com/AnatoliiVolchkov/pint`      | private    | `main`         | `https://github.com/cloudflare/pint.git`       | `main`          |
| `github.com/AnatoliiVolchkov/metricsql` | private    | `main`         | `https://github.com/VictoriaMetrics/metricsql.git` | `master`    |

Remotes in a local checkout:

```sh
git clone https://github.com/AnatoliiVolchkov/pint.git
cd pint
git remote add upstream https://github.com/cloudflare/pint.git
# Never push to the public upstream by accident.
git remote set-url --push upstream DISABLE-pushing-to-public-upstream
git fetch upstream
```

`git remote -v` must show:

```
origin    https://github.com/AnatoliiVolchkov/pint.git (fetch)
origin    https://github.com/AnatoliiVolchkov/pint.git (push)
upstream  https://github.com/cloudflare/pint.git (fetch)
upstream  DISABLE-pushing-to-public-upstream (push)
```

## Branch model

- `main` is the maintained product branch. It contains the complete upstream history
  plus our commits on top, joined by merge commits from upstream syncs.
- Upstream changes are brought in with `git merge`, never by rebasing our commits,
  squashing, or re-applying the implementation from scratch.
- Our own work stays in small, focused commits (see `git log upstream/main..main`).
  Do not squash them during syncs.
- `main` is never force-pushed. Every sync goes through a `sync/upstream-YYYYMMDD`
  branch and a pull request.

## Manual sync procedure

Run from a clean checkout of `main`:

```sh
git switch main
git pull --ff-only origin main
git fetch upstream main --tags

# Anything to do?
git rev-list --count main..upstream/main     # 0 means nothing to sync

BRANCH=sync/upstream-$(date -u +%Y%m%d)
git switch -c "$BRANCH" main
git merge --no-ff upstream/main -m "Merge upstream cloudflare/pint $(git rev-parse --short upstream/main)"
```

If the merge stops with conflicts:

```sh
git diff --name-only --diff-filter=U        # list conflicting files
```

Resolve only real conflicts, keeping both upstream's change and our change.
Guidance for the files we touch most:

- `go.mod` / `go.sum`: keep upstream's dependency bumps, keep our
  `replace github.com/VictoriaMetrics/metricsql => github.com/AnatoliiVolchkov/metricsql <version>`
  line, then run `go mod tidy`.
- `internal/checks/*.go`, `internal/parser/**`: upstream code uses the Prometheus parser
  types (`promParser.*`, `posrange.*`), ours uses `internal/parser/ast`. Port upstream's
  logic change to the `ast` types instead of reverting either side.
- New upstream checks must be added to the MetricsQL test suite
  (`internal/checks/metricsql_test.go`); the coverage matrix test fails until they are.
- Snapshot files (`*.snap`, `cmd/pint/tests/*.txt`): resolve the code first, then regenerate
  (see below) instead of hand-merging snapshot output.

Then finish the merge and test:

```sh
git add <resolved files>
git commit --no-edit

go mod tidy && git diff --exit-code go.mod go.sum
make lint
make test
go test -race ./...
make build
```

Regenerating snapshots after a resolved merge (review the diff before committing):

```sh
UPDATE_SNAPS=true go test ./internal/...
UPDATE_SNAPSHOTS=1 go test ./cmd/pint/ -run TestScripts
```

Push the branch and open a pull request against `main`:

```sh
git push -u origin "$BRANCH"
gh pr create --repo AnatoliiVolchkov/pint --base main --head "$BRANCH" \
  --title "Sync with upstream cloudflare/pint $(git rev-parse --short upstream/main)" \
  --body "Merge of upstream/main into main."
```

Merge the pull request with a **merge commit** (not squash, not rebase) so upstream
history stays intact and future merges only see new upstream commits.

## Disabled upstream workflows

Upstream workflows are kept byte-for-byte unchanged so they never conflict during
syncs. Workflows that cannot work in this private repository are disabled through
GitHub workflow state, not by editing them:

```sh
for f in benchmark.yml ci.yml compile.yml deps.yml docker.yml docs.yml examples.yml \
         go-mod-tidy.yml goreleaser.yml lint.yml pages.yml semgrep.yml test.yml; do
  gh workflow disable "$f" -R AnatoliiVolchkov/pint
done
gh workflow list -R AnatoliiVolchkov/pint --all
```

| Workflow           | Why it is disabled                                                                       |
|--------------------|------------------------------------------------------------------------------------------|
| `test.yml`         | Cannot fetch the private MetricsQL module; `make test` runs in `pint-ci.yml` instead.     |
| `lint.yml`         | Same; `make lint` and `make format` run in `pint-ci.yml`.                                 |
| `go-mod-tidy.yml`  | Same; `go mod tidy` check runs in `pint-ci.yml`.                                          |
| `examples.yml`     | Same; example validation runs in `pint-ci.yml`.                                           |
| `compile.yml`      | Publishes release binaries and ghcr.io images for cloudflare; `pint-ci.yml` builds the binary artifact. |
| `docker.yml`       | Builds upstream Docker images.                                                            |
| `goreleaser.yml`   | Upstream release configuration check.                                                     |
| `pages.yml`        | Deploys upstream GitHub Pages.                                                            |
| `docs.yml`         | Upstream docs spellcheck/markdown lint, needs Pages permissions.                          |
| `ci.yml`           | Self-gated to `cloudflare/pint`, never runs here.                                         |
| `benchmark.yml`    | Downloads upstream benchmark data on pull requests.                                       |
| `deps.yml`         | GitHub dependency review needs GitHub Advanced Security on private repositories.         |
| `semgrep.yml`      | Upstream Semgrep scan on a schedule.                                                      |

After every sync, check for **new upstream workflows** and decide whether to disable them:

```sh
git diff --name-status <previous-upstream-sha> upstream/main -- .github/workflows
gh workflow list -R AnatoliiVolchkov/pint --all
```

New workflows start active, so review them before merging the sync pull request.
