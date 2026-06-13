# CLI Reference

Run `our --help` for the authoritative surface. This page groups the current
commands by job.

## Which command do I run?

Three commands sound alike; the split is converge vs. diagnose vs. plumbing:

- **`our sync`** converges the whole workspace. It pulls every registered
  repository (manifest cache, content mounts, catalog repo clones), reconciles
  generated guidance, skills, and umbrella MCP config when the manifest
  changed, and publishes local content that is safe to publish. This is the
  one routine verb — when a startup notice says something is stale or
  unpublished, run this.
- **`our doctor`** is the dry run for installation and workspace repair: it
  diagnoses manifest validity, per-checkout Git freshness, derived
  guidance/skill/MCP drift, service materialization health, work-session
  health, and the last sync audit,
  marking every repairable finding with `would ...` and a closing fixable
  count. Nothing changes until you re-run with `--fix`, which applies exactly
  that plan; findings `--fix` cannot repair (dirty, diverged, repo checkouts,
  session work) keep their explanatory remediation text instead.
- **`our manifests sync`** refreshes the registered manifest cache. You need
  it before an umbrella exists (bootstrap) or when managing several
  registered manifests; when exactly one manifest changes and an umbrella is
  known, it also reconciles generated guidance, umbrella MCP config, and
  manifest skills. Once an umbrella is set up, plain `our sync` is still the
  routine command.

## Setup and launch

```sh
our init <org-id> [--name NAME] [--path DIR] [--umbrella DIR] [--home DIR] [--setup] [--json]
our publish [--manifest NAME] [--home DIR] [--print] [--json]
our setup [harness...] | --all [--print] [--copy] [--link] [--force] [--role ROLE] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
our root [--repo ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
our ai [--new-session|--session ID|--no-session] [--repo ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]
our sync [--backend auto|gnit|builtin] [--publish auto|never|direct|pr] [--scope all|local|content|manifest|repos] [--manifest NAME] [--home DIR] [--umbrella DIR] [--message TEXT] [--no-derived] [--print] [--json]
our doctor [--no-fetch] [--fix] [--json]
our update [--check] [--version X.Y.Z] [--json] [--yes]
our version
```

`our init` creates two local repositories — a private manifest repo at the
registry path (the control plane) and a content repo at
`<umbrella>/workspace` (`--path` overrides) — commits and registers them, and
prints the follow-up `our setup`, `our ai`, and `our publish` commands. Both
repos work offline and report `local-only` until published.

`our publish` takes the organization online idempotently: it creates private
remotes (`<org>-workspace`, `<org>-manifest`) via `gh`, or adopts existing
origins and pushes (verifying GitHub remotes are private), rewrites local
mount URLs to the published repositories in a commit scoped to
`manifest.json`, updates the registry, and prints the teammate join command.
`our sync` refuses to publish a manifest that still references local mount
paths, and `our doctor` names each such mount with the `our publish`
remediation.

## Skills

```sh
our skills self install [harness...] | --all [--home DIR] [--copy] [--link] [--force] [--json]
our skills self uninstall [harness...] | --all [--home DIR] [--force] [--json]
our skills self status [harness...] | --all [--home DIR] [--json]

our skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]
our skills show <id|slug> [--json] [--source DIR] [--manifest NAME] [--home DIR]
our skills status [--skill ID_OR_SLUG] [--json] [--source DIR] [--manifest NAME] [--home DIR]
our skills install [harness...] | --all [--skill ID_OR_SLUG] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
our skills uninstall <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
our skills sync [harness...] | --all [--skill ID_OR_SLUG] [--no-prune] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
our skills purge <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
```

`--manifest NAME` reads skills from a synced manifest (the default when one is
registered); `--source DIR` reads them from a local directory instead. With no
harness arguments, install targets every supported harness and silently skips
ones that are not present.

## Admin

```sh
our admin skills add <skill-dir> --id namespace:name --manifest-dir DIR [--install-slug SLUG] [--keep-original|--remove-original] [--force] [--json]
our admin skills remove <id|slug> --manifest-dir DIR [--delete-source] [--prune-related] [--prune-orphans] [--force] [--json]
our admin setup ...
our admin manifests add|sync|validate ...
our admin mounts add|remove|sync ...
our admin meetings add ...
our admin support add ...
our admin tools add|edit|remove <id> --manifest-dir DIR [--mode required|optional] [--purpose TEXT] [--install-command CMD] [--docs-url URL] [--skill-install-command CMD] [--skill-install-arg ARG] [--force] [--json]
our admin contract add "RULE TEXT" --manifest-dir DIR [--force] [--json]
our admin contract remove <index|"RULE TEXT"> --manifest-dir DIR [--force] [--json]
```

See the [admin guide](./admin.md) for the full flag set and the
review-commit-push workflow that follows every admin edit.

## Manifests, mounts, and workspace

```sh
our manifests add <name> <git-url>
our manifests list
our manifests sync <name...> | --all [--home DIR] [--umbrella DIR] [--no-derived] [--print] [--json]
our manifests validate <name|path>

our mounts list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
our mounts add <kind:id|id> [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
our mounts sync <mount...> | --all [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
our mounts remove <mount...> [--home DIR] [--umbrella DIR] [--print] [--force] [--json]

our workspaces list [--manifest NAME]
our workspaces sync <workspace...> | --all [--manifest NAME] [--print]

our work start [--slug SLUG] [--json]
our work status [--all] [--json]
our work list [--all] [--json]
our work resume [session-id] [--json]
our work finish [session-id] --land|--publish|--discard [--message TEXT] [--json]
```

## Content and diagnostics

```sh
our meetings list
our meetings search <text>
our meetings get <id|path>
our meetings add <slug>

our support list
our support search <text>
our support get <id|path>
our support add <slug>

our fleet list
our fleet search <text>
our fleet get <id|identifier|path>
our fleet add <id>
our fleet set <id> KEY=VALUE...

our record adopt <path>

our customers list                     # mounted customer identity records
our products list
our repos list [--json]
our repos add <id> [--print] [--json]
our repos remove <id> [--force] [--json]
our tools list
our tools info <name>
our services list [--manifest NAME] [--home DIR] [--json]
our services get <id> [--manifest NAME] [--home DIR] [--json]
our roles list [--manifest NAME] [--home DIR] [--json]
our roles get <id> [--manifest NAME] [--home DIR] [--json]
our contract list [--manifest NAME] [--home DIR] [--json]
```

`our sync` is the routine reconciliation command. `--backend auto` prefers Gnit
when the umbrella is initialized as a Gnit control workspace; Our AI keeps the
bootstrap, policy, duplicate-remote, and PR layers. `--publish direct` can
publish existing local commits directly, but dirty non-content/admin files are
still held back for explicit admin or review handling. Plain untracked (`??`)
files under declared content paths are also held; create records with
`our meetings add`, `our support add`, or `our fleet add`, or run
`our record adopt <path>` to mark a manually created file as intentional
publish content. A manifest can set top-level `sync.publish_policy` to `auto`,
`never`, or `pr` as the default when `--publish` is omitted; an explicit CLI
flag always wins. Non-print syncs write `.our/last-sync.json`; `our doctor`
reports that audit, per-checkout Git freshness, active and archived work
sessions, service health, and derived guidance/skill/MCP drift. Doctor fetches
refs before behind/ahead checks unless `--no-fetch` is passed for an offline
view. `doctor --fix` fast-forwards only clean stale manifest/content checkouts
and reconciles generated guidance, umbrella `.mcp.json`, and manifest skills.
Sync performs the same derived reconcile after manifest checkout changes unless
`--no-derived` is passed.

`our root`, `our ai`, and `our setup` run a best-effort, TTL-gated
refresh for clean manifest/content checkouts before using workspace context.
They leave dirty, diverged, repo, and remote-unknown checkouts untouched.
`our ai` also ensures the bundled `our` self-skill exists for the selected
filesystem harness before launching it. By default it launches from the base
umbrella, or from the current active session when run inside `work/<id>`. Use
`--new-session` to create a fresh isolated session, `--session <id>` to
resume, or `--no-session` to ignore a current session for base
inspection/admin/debug. Repo launches use `--repo <id>`. Use `--no-refresh`
for one command, `OUR_NO_AUTO_REFRESH=1` globally, or `OUR_REFRESH_TTL=30m`
to tune the default six-hour window.

Manifest roles are selected locally with `our setup --role <id>`. The choice
is stored in `.our/state.json`, appends that role's guidance fragments to
`AGENTS.md`, and scopes generated `.mcp.json` to MCP services granted to the
role. Services and roles are manifest vocabulary: inspect them with
`our services list|get` and `our roles list|get`; they do not prune mounts.

Those startup commands also emit a stderr-only notice when a newer Our AI release
is available. Stdout remains clean for command substitutions such as
`cd "$(our root)"`. Use `--no-update-check`, `OUR_NO_UPDATE_CHECK=1`, or
`OUR_UPDATE_CHECK_TTL=12h` to suppress or tune that check.

`our update` downloads the selected GitHub release tarball, verifies it against
`checksums.txt`, and atomically replaces the running binary when the install is
writable and not package-managed. Use `our update --check` for a read-only
version comparison, or `our update --version X.Y.Z` to install a specific
release.
