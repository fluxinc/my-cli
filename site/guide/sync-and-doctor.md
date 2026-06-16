# Sync, Doctor, and Updates

Three commands keep a workspace healthy. `my sync` converges it, `my doctor`
diagnoses it, and `my update` keeps the CLI itself current.

## my sync

```sh
my sync --print     # plan only: show what would pull, push, or hold
my sync             # reconcile and publish per policy
```

One routine verb: it pulls every registered repository (manifest cache,
content mounts, catalog repo clones), reconciles derived state when the
manifest changed (generated guidance, umbrella `.mcp.json`, launch-scoped skill
reconciliation notices), and publishes local content that is safe to publish.
When a startup notice says something is stale or unpublished, this is the
command it means.

What publishes under the default `auto` policy: committed-or-adopted,
content-only changes in private repos — new meeting notes, support records,
fleet updates. What holds: manifest/catalog/admin changes (review-commit-push
by hand), public repos, diverged branches, plain untracked files that were
never adopted (see `my record adopt`), and mounts with dirty or unlanded
active sessions.

Scoping and policy:

```sh
my sync --scope all|local|content|manifest|repos
my sync --publish auto|never|direct|pr
my sync --no-derived          # skip the derived reconcile
my sync --message TEXT        # commit message for published content
```

A manifest can set `sync.publish_policy` as the default; an explicit flag
always wins. `--backend auto` prefers Gnit when the umbrella is a Gnit
control workspace, with a guarded built-in Git path otherwise. Every
non-print sync writes an audit to `.my-cli/last-sync.json`.

## my doctor

```sh
my doctor [--no-fetch] [--fix] [--json]
```

The dry run for workspace repair. It reports manifest validity, per-checkout
Git freshness (fetching refs first unless `--no-fetch`), derived
guidance/MCP drift, legacy global org-skill drift, service materialization health, work-session
health, and the last sync audit. Every repairable finding is marked
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
emit a stderr-only notice when a newer release exists; suppress with
`--no-update-check` or `MYCLI_NO_UPDATE_CHECK=1`.
