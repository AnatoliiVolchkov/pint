# Upstream synchronization

This repository (`github.com/AnatoliiVolchkov/pint`, public) is pint with VictoriaMetrics
and MetricsQL support. It tracks the public upstream `github.com/cloudflare/pint`
and depends on the source-aware MetricsQL fork `github.com/AnatoliiVolchkov/metricsql`,
which tracks `github.com/VictoriaMetrics/metricsql`.

## Repository layout

| Repository                    | Visibility | Product branch | Upstream                                      | Upstream branch |
|-------------------------------|------------|----------------|-----------------------------------------------|-----------------|
| `github.com/AnatoliiVolchkov/pint`      | public     | `main`         | `https://github.com/cloudflare/pint.git`       | `main`          |
| `github.com/AnatoliiVolchkov/metricsql` | public     | `main`         | `https://github.com/VictoriaMetrics/metricsql.git` | `master`    |

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

## Fork-only paths

Files that exist only in this fork never conflict with upstream merges. Put fork
additions into new files under these paths instead of editing upstream files:

| Path | Contents |
|---|---|
| `.github/scripts/` | `upstream-sync.sh` |
| `.github/metricsql-test/` | MetricsQL rules and pint config tested against real VictoriaMetrics, see its README |
| `.github/workflows/pint-ci.yml`, `integration.yml`, `release.yml`, `upstream-sync.yml` | fork CI, sync and release workflows |
| `docs/UPSTREAM_SYNC.md`, `docs/METRICSQL_TESTING.md` | fork documentation |
| `.claude/` | onboarding docs for AI coding agents (`.claude/CLAUDE.md`, `.claude/docs/`) |

Check that a new path doesn't exist upstream before using it:

```sh
git fetch upstream main
git log --oneline upstream/main -- <path>    # empty output means upstream never had it
```

## Branch model

- `main` is the maintained product branch. It contains the complete upstream history
  plus our commits on top, joined by merge commits from upstream syncs.
- Upstream changes are brought in with `git merge`, never by rebasing our commits,
  squashing, or re-applying the implementation from scratch.
- Our own work stays in small, focused commits (see `git log refs/upstream-tags/<tag>..main`).
  Do not squash them during syncs.
- `main` is never force-pushed. Every sync goes through a `sync/upstream-<tag>`
  branch and a pull request.
- Only stable upstream releases are merged: tags matching `vX.Y.Z`, never the head of
  upstream `main` and never `-rc`/`-beta` tags. Upstream tags releases on `main`, so
  merging one release after another is an ordinary merge. To take a single upstream fix
  before it is released, cherry-pick it (`git cherry-pick -x <sha>`) in a pull request;
  the next release merge absorbs it.

## Manual sync procedure

Run from a clean checkout of `main`:

```sh
git switch main
git pull --ff-only origin main
# Upstream tags go to refs/upstream-tags/* so they never mix with our tags.
git fetch --no-tags --force upstream '+refs/tags/*:refs/upstream-tags/*'

# Newest stable upstream release:
TAG=$(git for-each-ref --sort=-version:refname --format='%(refname:lstrip=2)' refs/upstream-tags/ \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1)

# Anything to do?
git rev-list --count main..refs/upstream-tags/$TAG     # 0 means nothing to sync

BRANCH=sync/upstream-$TAG
git switch -c "$BRANCH" main
git merge --no-ff refs/upstream-tags/$TAG -m "Merge upstream release $TAG into main"
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
- `.github/dependabot.yml`: deleted in this fork (see "Dependabot" below). If upstream
  changed it, the merge stops with a modify/delete conflict; keep it deleted with
  `git rm .github/dependabot.yml`.

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
# Build versions are counted from the newest upstream release tag in this repository
# (git describe), push the merged release tag too:
git push origin "refs/upstream-tags/$TAG:refs/tags/$TAG"
gh pr create --repo AnatoliiVolchkov/pint --base main --head "$BRANCH" \
  --title "Sync with upstream release $TAG" \
  --body "Merge of upstream cloudflare/pint $TAG into main."
```

Merge the pull request with a **merge commit** (not squash, not rebase) so upstream
history stays intact and future merges only see new upstream commits.

## MetricsQL dependency

pint imports `github.com/VictoriaMetrics/metricsql` unchanged and pins our fork
to an exact tag with a `replace` directive in `go.mod`:

```
replace github.com/VictoriaMetrics/metricsql => github.com/AnatoliiVolchkov/metricsql v0.87.5-pint.3
```

`go.sum` records its checksum, so builds are reproducible. The module is public, the go
command fetches it without credentials:

```sh
GOWORK=off go build ./cmd/pint
```

Updating the pin after a new metricsql release (see metricsql `docs/UPSTREAM_SYNC.md`):

```sh
go mod edit -replace github.com/VictoriaMetrics/metricsql=github.com/AnatoliiVolchkov/metricsql@v0.87.5-pint.N
go mod tidy
GOWORK=off go test ./...
git commit -am "Update metricsql to v0.87.5-pint.N"
```

If upstream metricsql has a newer release than the one our fork is based on, sync the fork
first, release a new `-pint.N` tag from its `main`, then bump the `require` line too:

```sh
go mod edit -require github.com/VictoriaMetrics/metricsql@vX.Y.Z
```

### Developing pint and metricsql together

To change both repositories without tagging metricsql after every edit, check them out
side by side and use a Go workspace:

```sh
mkdir workspace && cd workspace
git clone https://github.com/AnatoliiVolchkov/pint.git
git clone https://github.com/AnatoliiVolchkov/metricsql.git
cd pint
cp go.work.example go.work     # uses . and ../metricsql, gitignored
go build ./...                 # builds against ../metricsql
GOWORK=off go build ./...      # builds against the pinned tag, like CI
```

`go.work` is never committed and CI always runs with `GOWORK=off`.

## Before pushing

These repositories are public, and anything pushed to a fork stays reachable by SHA through
the upstream fork network, even after it is deleted. Before every push:

- Read the commits you are about to push. Real host names, addresses, cluster, tenant, team
  and account names must not be in them.
- Scan them for secrets:

  ```sh
  gitleaks git --redact --log-opts="origin/main..HEAD"
  ```

gitleaks finds tokens and keys, not host names, so only reading the commits catches those.

## Sync workflow

`.github/workflows/upstream-sync.yml` runs `.github/scripts/upstream-sync.sh` on demand
only. It has no schedule: upstream releases are synced when someone decides to, by hand (see
"Manual sync procedure") or with the workflow:

```sh
gh workflow run upstream-sync.yml -R AnatoliiVolchkov/pint                    # newest stable release
gh workflow run upstream-sync.yml -R AnatoliiVolchkov/pint -f dry_run=true    # detect and test-merge only
gh workflow run upstream-sync.yml -R AnatoliiVolchkov/pint -f tag=v0.89.0     # a specific release
gh workflow run upstream-sync.yml -R AnatoliiVolchkov/pint -f mode=branch     # head of upstream main (exception)
gh run watch -R AnatoliiVolchkov/pint $(gh run list -R AnatoliiVolchkov/pint -w upstream-sync.yml -L1 --json databaseId --jq '.[0].databaseId')
```

What it does:

1. Fetches upstream tags into `refs/upstream-tags/*` and picks the newest stable release
   (`^v[0-9]+\.[0-9]+\.[0-9]+$`, highest version) or the `tag` input. It counts the release's
   commits missing from `main`; if there are none it exits successfully without changing
   anything. Between upstream releases every run is a no-op.
2. Creates `sync/upstream-<tag>` from `main` and merges the release with `git merge --no-ff`.
3. On conflicts it aborts the merge, prints the conflicting files in the job log and summary,
   opens (or comments on) an issue titled `Upstream sync conflict: release <tag>` with the
   exact commands to resolve it, and fails. Nothing is pushed.
4. On a clean merge it runs `go build`, `go vet` and `go test ./...`, pushes the sync branch
   and the upstream release tag (build versions like `v0.89.0-12-g1234abcd` are counted from
   it), and opens or updates a pull request against `main` (a draft if tests failed).
5. It never pushes to `main` and never resolves conflicts by itself.

Tokens:

- `SYNC_TOKEN` (optional): fine-grained PAT on `AnatoliiVolchkov/pint` with Contents, Pull requests,
  Issues and Workflows read/write. Without it the job uses `GITHUB_TOKEN`, which cannot push
  merges that modify `.github/workflows/*` (the job then opens an issue saying so), and pull
  requests it opens do not trigger `pint-ci.yml`; tests still run inside the sync job.

The repository setting "Allow GitHub Actions to create and approve pull requests" must be
enabled for `GITHUB_TOKEN` to open pull requests:

```sh
gh api -X PUT repos/AnatoliiVolchkov/pint/actions/permissions/workflow \
  -f default_workflow_permissions=read -F can_approve_pull_request_reviews=true
```

## Dependabot

Upstream's `.github/dependabot.yml` is deleted in this fork, which turns off Dependabot
version updates; GitHub has no setting for that. Its pull requests bumped upstream's
`Dockerfile` (unused here), Go modules that upstream bumps itself (they arrive with
upstream syncs and would conflict in `go.mod`/`go.sum`), and actions in upstream workflows
that must stay byte-for-byte unchanged.

Dependabot alerts (vulnerability notifications, no pull requests) are enabled in the
repository settings, Dependabot security updates are not:

```sh
gh api repos/AnatoliiVolchkov/pint/vulnerability-alerts -i          # 204: alerts enabled
gh api repos/AnatoliiVolchkov/pint/automated-security-fixes         # {"enabled":false,...}
```

## Disabled upstream workflows

Upstream workflows are kept byte-for-byte unchanged so they never conflict during
syncs. Workflows that duplicate `pint-ci.yml` or publish for cloudflare are disabled
through GitHub workflow state, not by editing them:

```sh
for f in benchmark.yml ci.yml compile.yml deps.yml docker.yml docs.yml examples.yml \
         go-mod-tidy.yml goreleaser.yml lint.yml pages.yml semgrep.yml test.yml; do
  gh workflow disable "$f" -R AnatoliiVolchkov/pint
done
gh workflow list -R AnatoliiVolchkov/pint --all
```

| Workflow           | Why it is disabled                                                                       |
|--------------------|------------------------------------------------------------------------------------------|
| `test.yml`         | `make test` runs in `pint-ci.yml` instead.                                                |
| `lint.yml`         | `make lint` and `make format` run in `pint-ci.yml`.                                       |
| `go-mod-tidy.yml`  | `go mod tidy` check runs in `pint-ci.yml`.                                                |
| `examples.yml`     | Example validation runs in `pint-ci.yml`.                                                 |
| `compile.yml`      | Publishes release binaries and ghcr.io images for cloudflare; `pint-ci.yml` builds the binary artifact. |
| `docker.yml`       | Builds upstream Docker images.                                                            |
| `goreleaser.yml`   | Upstream release configuration check.                                                     |
| `pages.yml`        | Deploys upstream GitHub Pages.                                                            |
| `docs.yml`         | Upstream docs spellcheck/markdown lint, needs Pages permissions.                          |
| `ci.yml`           | Self-gated to `cloudflare/pint`, never runs here.                                         |
| `benchmark.yml`    | Downloads upstream benchmark data on pull requests.                                       |
| `deps.yml`         | Upstream dependency review; Dependabot alerts report vulnerable dependencies here.       |
| `semgrep.yml`      | Upstream Semgrep scan on a schedule.                                                      |

After every sync, check for **new upstream workflows** and decide whether to disable them:

```sh
git diff --name-status <previous-tag> refs/upstream-tags/<tag> -- .github/workflows
gh workflow list -R AnatoliiVolchkov/pint --all
```

New workflows start active, so review them before merging the sync pull request.
