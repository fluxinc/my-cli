# CLI Reference

Run `our --help` for the authoritative surface. This page groups the current
commands by job.

## Which command do I run?

Three commands sound alike; the split is converge vs. diagnose vs. plumbing:

- **`our sync`** converges the whole workspace. It pulls every registered
  repository (manifest cache, content mounts, product clones), reconciles
  generated guidance and skills when the manifest changed, and publishes
  local content that is safe to publish. This is the one routine verb — when
  a startup notice says something is stale or unpublished, run this.
- **`our doctor`** is the dry run for installation and workspace repair: it
  diagnoses manifest validity, per-checkout Git freshness, derived
  guidance/skill drift, and the last sync audit, marking every repairable
  finding with `would ...` and a closing fixable count. Nothing changes until
  you re-run with `--fix`, which applies exactly that plan; findings `--fix`
  cannot repair (dirty, diverged, product checkouts) keep their explanatory
  remediation text instead.
- **`our manifests sync`** refreshes the registered manifest cache. You need
  it before an umbrella exists (bootstrap) or when managing several
  registered manifests; when exactly one manifest changes and an umbrella is
  known, it also reconciles generated guidance and manifest skills. Once an
  umbrella is set up, plain `our sync` is still the routine command.

## Setup and launch

```sh
our setup [harness...] | --all [--print] [--copy] [--link] [--force] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
our root [--product ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
our ai [--product ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]
our sync [--backend auto|gnit|builtin] [--publish auto|never|direct|pr] [--scope all|local|content|manifest|repos] [--manifest NAME] [--home DIR] [--umbrella DIR] [--message TEXT] [--no-derived] [--print] [--json]
our doctor [--no-fetch] [--fix] [--json]
our update [--check] [--version X.Y.Z] [--json] [--yes]
our version
```

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
our admin customers add|edit <id> --manifest-dir DIR
our admin tools add|edit|remove <id> --manifest-dir DIR [--mode required|optional] [--purpose TEXT] [--install-command CMD] [--docs-url URL] [--skill-install-command CMD] [--skill-install-arg ARG] [--force] [--json]
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

our customers list
our products list
our tools list
our tools info <name>
```

`our sync` is the routine reconciliation command. `--backend auto` prefers Gnit
when the umbrella is initialized as a Gnit control workspace; Our AI keeps the
bootstrap, policy, duplicate-remote, and PR layers. `--publish direct` can
publish existing local commits directly, but dirty non-content/admin files are
still held back for explicit admin or review handling. A manifest can set
top-level `sync.publish_policy` to `auto`, `never`, or `pr` as the default when
`--publish` is omitted; an explicit CLI flag always wins. Non-print syncs write
`.our/last-sync.json`; `our doctor` reports that audit, per-checkout Git
freshness, and derived skill/guidance drift. Doctor fetches refs before
behind/ahead checks unless `--no-fetch` is passed for an offline view. `doctor
--fix` fast-forwards only clean stale manifest/content checkouts and reconciles
derived guidance plus manifest skills. Sync performs the same derived reconcile
after manifest checkout changes unless `--no-derived` is passed.

`our root`, `our ai`, and `our setup` run a best-effort, TTL-gated
refresh for clean manifest/content checkouts before using workspace context.
They leave dirty, diverged, product, and remote-unknown repositories untouched.
`our ai` also ensures the bundled `our` self-skill exists for the selected
filesystem harness before launching it. Use `--no-refresh` for one command,
`OUR_NO_AUTO_REFRESH=1` globally, or `OUR_REFRESH_TTL=30m` to tune the default
six-hour window.

Those startup commands also emit a stderr-only notice when a newer Our AI release
is available. Stdout remains clean for command substitutions such as
`cd "$(our root)"`. Use `--no-update-check`, `OUR_NO_UPDATE_CHECK=1`, or
`OUR_UPDATE_CHECK_TTL=12h` to suppress or tune that check.

`our update` downloads the selected GitHub release tarball, verifies it against
`checksums.txt`, and atomically replaces the running binary when the install is
writable and not package-managed. Use `our update --check` for a read-only
version comparison, or `our update --version X.Y.Z` to install a specific
release.
