# CLI Reference

Run `flux --help` for the authoritative surface. This page groups the current
commands by job.

## Setup and launch

```sh
flux onboard [harness...] | --all [--manifest NAME] [--home DIR] [--umbrella DIR]
flux root [--product ID] [--manifest NAME] [--home DIR] [--umbrella DIR]
flux launch [--product ID] [--onboard] [--print] [--manifest NAME] [harness] [-- harness args...]
flux sync [--backend auto|nit|flux] [--publish auto|never|direct|pr] [--print] [--json]
flux doctor
flux version
```

## Skills

```sh
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
flux admin skills remove <id|slug> --manifest-dir DIR
flux admin onboard ...
flux admin manifest add|sync|validate ...
flux admin mount add|remove|sync ...
flux admin meetings add ...
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
flux tools info <name>
```

`flux sync` is the routine reconciliation command. `--backend auto` prefers Nit
when the umbrella is initialized as a Nit control workspace; Flux keeps the
bootstrap, policy, duplicate-remote, and PR layers. `--publish direct` can
publish existing local commits directly, but dirty non-content/admin files are
still held back for explicit admin or review handling.
