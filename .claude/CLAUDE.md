# pint fork with VictoriaMetrics and MetricsQL support

This repository is **AnatoliiVolchkov/pint** (public), a fork of
[cloudflare/pint](https://github.com/cloudflare/pint). It adds VictoriaMetrics servers and
MetricsQL rules, as evaluated by vmalert, while PromQL mode stays identical to upstream.

The MetricsQL parser comes from the companion fork **AnatoliiVolchkov/metricsql**, which adds
source positions. It is pinned in `go.mod`:
`replace github.com/VictoriaMetrics/metricsql => github.com/AnatoliiVolchkov/metricsql v0.87.6-pint.1`.
That repository has its own `.claude/CLAUDE.md`.

Clone both repositories side by side. `go.work.example` builds pint against `../metricsql`.

## This repository is public

Anything pushed to a fork stays reachable by SHA through the upstream fork network, even
after it is deleted. Only GitHub support can remove it, so prevent leaks instead of fixing
them.

- **Never write real addresses or names anywhere in this repository,** including `.claude/`,
  commit messages and pull requests: host names, URLs, cluster, tenant, account or team names,
  dashboard or data source IDs, real alert files, credentials. Use `example.com` and made-up
  names.
- **Tests and examples use invented names only.** No real alert, rule group, metric, job or
  service names in tests, fixtures, docs or commit messages. When a bug comes from a real
  rule, rewrite the rule with made-up names before it lands anywhere.
- **No figures from real infrastructure:** how many alerts, rules, ConfigMaps or series a
  real server has, its retention or its version. Describe the finding without them.
- **Internal material stays out.** Real rule files, server addresses and test reports live
  outside this repository. Ask the user for them, and keep their details out of commits,
  docs and pull requests.
- **Accounts.** `AnatoliiVolchkov` owns both repositories. Day-to-day work happens as the
  collaborator `avixyio`. The user doesn't log in to the owner account on the development
  PC, so give them browser steps for what needs the owner: repository settings, secrets and
  rulesets. Commits use the account's `users.noreply.github.com` address, never a work email.
- **Before pushing:** read the commits, run
  `gitleaks git --redact --log-opts="origin/main..HEAD"`, and name refs explicitly, never
  `--all` or `--mirror`. gitleaks finds tokens and keys, not host names, so only reading the
  commits catches those. See `docs/UPSTREAM_SYNC.md`, "Before pushing".

## Rules from the user (follow them every time)

1. **Separate commits.** "all big changes must be separate commit". Documentation-only work
   goes in its own commit.
2. **Commit or push only when the user asks.** The user approves pushing explicitly ("yes,
   push"). Run releases only on request.
3. **"Don't break any existing functionality."** PromQL mode and Prometheus servers must
   behave exactly like upstream. Gate MetricsQL behaviour on the query language or the
   backend.
4. **"Do not rewrite the implementation unless a real bug is found."** Prove a bug before
   fixing it, with a VictoriaMetrics query, the vmalert source, or a diff against upstream
   pint. Put the evidence in the commit body.
5. **Keep upstream merges conflict-free.**
   - Put fork logic in new files and keep hooks in upstream files to a few lines.
   - Never edit upstream workflows; disable them instead.
   - Fork-only paths are listed in `docs/UPSTREAM_SYNC.md`.
6. **"dont touch files related to pint".** Don't edit `.github/pint/pint.hcl`, upstream's own
   config. The alert standard lives in `.github/metricsql-test/`.
7. **"my patched version with metricsql patches must build and work then usual".** Only
   stable upstream releases are synced, and the fork must keep building and passing tests.
8. **Real servers:** "Keep load low: no aggressive parallelism; no unnecessary repeated
   queries; reuse results where possible." Never commit or print credentials. Pass them only
   through `VM_AUTH`.
9. **Language.** Answer in the user's language. The user writes Russian or English, and
   asks for "simple and clear" explanations in Russian when they ask in Russian.
10. **Pasted analysis.** Treat analysis pasted from other sessions as leads to verify, not as
    facts.
11. **Severity (2026-10-08).** New findings are Warning at most, never Bug.

## Quick start

```sh
GOWORK=off make -B pint && ./pint version        # CI-identical build, pinned metricsql
GOWORK=off go test ./...                         # macOS-only failures: 0255, 0129, TestSeriesCheck/bad_uri
GOWORK=off make lint
./pint parse -q metricsql 'rate(foo)[5m]'        # print the pint AST for a MetricsQL query
```

A gitignored `go.work` (copied from `go.work.example`) builds against `../metricsql`. Use
`GOWORK=off` to get what CI and releases get.

## Where to read next

| Topic | File |
|---|---|
| How MetricsQL support is built, code map, which checks differ | [docs/architecture.md](docs/architecture.md) |
| Build, test, snapshots, testscripts, lint, adding a check, shell gotchas | [docs/development.md](docs/development.md) |
| Verified VictoriaMetrics and vmalert behaviour, with evidence | [docs/vmalert-and-victoriametrics.md](docs/vmalert-and-victoriametrics.md) |
| Testing against a real server and real rule files, how to judge findings | [docs/real-world-testing.md](docs/real-world-testing.md) |
| Versions, releases, CI, secrets, upstream sync, metricsql pin | [docs/release-and-sync.md](docs/release-and-sync.md) |
| What was built and why, commit by commit | [docs/history.md](docs/history.md) |
| Open items, known bugs, ideas waiting for approval | [docs/backlog.md](docs/backlog.md) |
| Compare two pint binaries on every rule set; restore snapshot order | [tools/compare-binaries.sh](tools/compare-binaries.sh), [tools/snapmerge.py](tools/snapmerge.py) |

Repository docs written for humans (paths from the repository root):

- `docs/UPSTREAM_SYNC.md`: sync procedure, fork-only paths, what to check before pushing.
- `docs/METRICSQL_TESTING.md`: test layers, oracle, real VictoriaMetrics tests.
- `docs/configuration.md`: `query_language`, `backend`, `scrapeInterval`, `externalLabels`.
- `.github/metricsql-test/README.md`: the alert standard and real-server commands.

## Current state (2026-11-05)

- **Base:** upstream cloudflare/pint **v0.89.2** plus the fork's commits, with metricsql
  `v0.87.6-pint.1` (upstream metricsql v0.87.6, since 2026-11-05). PromQL mode output was
  byte-identical to upstream v0.89.2 on the local rule sets at the last release.
- **Next upstream sync:** v0.89.3 and v0.89.4 are out and not merged yet. A test merge on
  2026-10-30 conflicted only in `docs/changelog.md`, but it also touches
  `internal/checks/alerts_count.go` and `internal/promapi/range.go`, so run the full tests.
- **Public move (2026-11):** this repository replaced a private predecessor, with rewritten
  history, see `docs/history.md`. The first release here is `v0.89.2-95-gf61500a3`
  (2026-11-05). Older release names in these docs refer to the predecessor.
- **CI skips docs-only commits.** Before a release check CI on the last commit that ran it,
  see `docs/release-and-sync.md`.
- **Don't re-offer rejected ideas,** see backlog "Rejected ideas".
- **Waiting for the user's answer:**
  - the rest of the improvement list (backlog, "offered on 2026-10-08");
  - SARIF output, only if the user has somewhere to upload it.

## Keep these docs current

When you change behaviour, verify a new VictoriaMetrics or vmalert fact, finish a backlog
item, or cut a release, update the matching file under `.claude/` in the same commit, or in
a follow-up docs-only commit. Date anything that can go stale.
