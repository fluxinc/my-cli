# CLI Reference

Run `flux --help` for the authoritative surface. This page groups the current
commands by job.

## Setup and launch

```sh
flux onboard [harness...] | --all [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
flux root [--product ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
flux launch [--product ID] [--onboard] [--print] [--manifest NAME] [--no-refresh] [--no-update-check] [harness] [-- harness args...]
flux sync [--backend auto|gnit|flux] [--publish auto|never|direct|pr] [--scope all|local|content|manifest|products] [--no-derived] [--print] [--json]
flux doctor [--no-fetch] [--fix] [--json]
flux update [--check] [--version X.Y.Z] [--json] [--yes]
flux version
```

## Skills

```sh
flux skills self install [harness...] | --all [--copy] [--link] [--force] [--json]
flux skills self uninstall [harness...] | --all [--force] [--json]
flux skills self status [harness...] | --all [--json]

flux skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]
flux skills show <id|slug> [--json] [--source DIR] [--manifest NAME] [--home DIR]
flux skills status [--skill ID_OR_SLUG] [--json] [--source DIR] [--manifest NAME] [--home DIR]
flux skills install [harness...] | --all [--skill ID_OR_SLUG] [--copy] [--link] [--force]
flux skills uninstall <harness...> | --all [--skill ID_OR_SLUG] [--force]
flux skills sync [harness...] | --all [--skill ID_OR_SLUG] [--no-prune] [--copy] [--link]
flux skills purge <harness...> | --all [--skill ID_OR_SLUG] [--force]
```

## Admin

```sh
flux admin skills add <skill-dir> --id namespace:name --manifest-dir DIR
flux admin skills remove <id|slug> --manifest-dir DIR [--prune-orphans]
flux admin onboard ...
flux admin manifest add|sync|validate ...
flux admin mount add|remove|sync ...
flux admin meetings add ...
flux admin customers add|edit <id> --manifest-dir DIR
flux admin tools add|edit|remove <id> --manifest-dir DIR
```

## Manifests, mounts, and workspace

```sh
flux manifest add <name> <git-url>
flux manifest list
flux manifest sync <name...> | --all [--print]
flux manifest validate <name|path>

flux mount list [--manifest NAME]
flux mount add <kind:id|id> [--manifest NAME]
flux mount sync <mount...> | --all [--manifest NAME] [--print]
flux mount remove <mount...> [--print] [--force]

flux workspace list [--manifest NAME]
flux workspace sync <workspace...> | --all [--manifest NAME] [--print]
```

## Content and diagnostics

```sh
flux meetings list
flux meetings search <text>
flux meetings get <id|path>
flux meetings add <slug>

flux customers list
flux catalog list products
flux tools list
flux tools info <name>
```

`flux sync` is the routine reconciliation command. `--backend auto` prefers Gnit
when the umbrella is initialized as a Gnit control workspace; Flux keeps the
bootstrap, policy, duplicate-remote, and PR layers. `--publish direct` can
publish existing local commits directly, but dirty non-content/admin files are
still held back for explicit admin or review handling. A manifest can set
top-level `sync.publish_policy` to `auto`, `never`, or `pr` as the default when
`--publish` is omitted; an explicit CLI flag always wins. Non-print syncs write
`.flux/last-sync.json`; `flux doctor` reports that audit, per-checkout Git
freshness, and derived skill/guidance drift. Doctor fetches refs before
behind/ahead checks unless `--no-fetch` is passed for an offline view. `doctor
--fix` fast-forwards only clean stale manifest/content checkouts and reconciles
derived guidance plus manifest skills. Sync performs the same derived reconcile
after manifest checkout changes unless `--no-derived` is passed.

`flux root`, `flux launch`, and `flux onboard` run a best-effort, TTL-gated
refresh for clean manifest/content checkouts before using workspace context.
They leave dirty, diverged, product, and remote-unknown repositories untouched.
Use `--no-refresh` for one command, `FLUX_NO_AUTO_REFRESH=1` globally, or
`FLUX_REFRESH_TTL=30m` to tune the default six-hour window.

Those startup commands also emit a stderr-only notice when a newer Flux release
is available. Stdout remains clean for command substitutions such as
`cd "$(flux root)"`. Use `--no-update-check`, `FLUX_NO_UPDATE_CHECK=1`, or
`FLUX_UPDATE_CHECK_TTL=12h` to suppress or tune that check.

`flux update` downloads the selected GitHub release tarball, verifies it against
`checksums.txt`, and atomically replaces the running binary when the install is
writable and not package-managed. Use `flux update --check` for a read-only
version comparison, or `flux update --version X.Y.Z` to install a specific
release.
