# Sync, Doctor, and Updates

Three commands keep a workspace healthy. `my sync` converges it, `my doctor`
diagnoses it, and `my update` keeps the CLI itself current.

## my sync

```sh
my sync                 # pull/reconcile only; never publishes local changes
my sync --print         # plan the pull-only default
my sync --push --print  # preview explicit publish work
my sync --push          # publish eligible local changes per policy
```

One routine verb: it pulls every registered repository (manifest cache,
content mounts, catalog repo clones), reconciles derived state when the
manifest changed (generated guidance, umbrella `.mcp.json`, launch-scoped skill
reconciliation notices), and never publishes local changes unless `--push` or
an explicit `--publish` mode is passed. When a startup notice says something is
stale, bare `my sync` is the command it means. When local changes should be
shared, preview with `my sync --push --print`, then run `my sync --push`.

What publishes under the `auto` policy after explicit `--push`:
committed-or-adopted, content-only changes in private repos — new customer
identity records, meeting notes, support records, and fleet updates. What
holds: public repos, diverged branches, plain untracked files that were never
adopted (see `my record adopt`), and mounts with dirty or unlanded active
sessions. For reviewed manifest/catalog/admin control-plane edits, run
`my publish --manifest NAME`; the low-level equivalent is
`my sync --publish direct --scope manifest`.

Held rows include a stable `reason_code` in JSON and, when the remedy is clear,
`next_command`; text output shows the same command as `next=...`. Clean behind
checkouts point at `my sync`, dirty-behind checkouts point first at the local
status command, and diverged checkouts point at `my doctor`.

Scoping and policy:

```sh
my sync --scope all|local|content|manifest|repos
my sync --push
my sync --publish auto|never|direct|pr
my sync --no-derived          # skip the derived reconcile
my sync --push --message TEXT # commit message for published content
```

A manifest can set `sync.publish_policy` as the mode used by `--push`; an
explicit `--publish` flag always wins. Bare `my sync` ignores that policy and
stays pull-only. `--backend auto` is target-aware: exact Gnit roster members
use coordinated publication, while unrostered checkouts use the guarded
built-in Git path. Before delegating, My AI verifies that whole-workspace Gnit
push cannot exceed the selected scope. Every non-print sync writes an audit to
`.my-cli/last-sync.json`.

## my doctor

```sh
my doctor [--no-fetch] [--fix] [--json]
```

The dry run for workspace repair. It reports manifest validity, per-checkout
Git freshness (fetching refs first unless `--no-fetch`), derived
guidance/MCP drift, legacy global org-skill drift, service materialization health,
session health, partial Gnit topology, legacy session layout migration, and the
last sync audit. Every
repairable finding is marked
`would ...` with a closing fixable count; nothing changes until you re-run
with `--fix`, which applies exactly that plan. Findings `--fix` cannot repair
— dirty or diverged checkouts, repo clones, session work — keep explanatory
remediation text instead.

## Startup freshness

`my root`, `my ai`, and `my setup` run a best-effort, TTL-gated refresh of
clean manifest/content checkouts before reading workspace context (default
window six hours; tune with `MYCLI_REFRESH_TTL`, opt out per-command with
`--no-refresh` or globally with `MYCLI_NO_AUTO_REFRESH=1`). They never touch
dirty, diverged, repo, or remote-unknown checkouts — those get stderr-only
`notice` lines naming the repository and the command to run. Stdout stays
clean, so `cd "$(my root)"` is always safe.

## my update

```sh
my update --check             # read-only version comparison
my update                     # download, checksum-verify, replace
my update --version X.Y.Z     # specific release
```

`my update` verifies the release tarball against `checksums.txt` and
atomically replaces the binary when the install is writable and not
package-managed; otherwise it prints the right follow-up (`brew upgrade`,
`go install`, or re-running `install.sh`). The same startup commands above
emit a stderr-only notice when a newer release exists, using GitHub's public
release redirect rather than the rate-limited REST API; suppress with
`--no-update-check` or `MYCLI_NO_UPDATE_CHECK=1`.
