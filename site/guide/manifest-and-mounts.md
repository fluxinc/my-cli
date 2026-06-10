# Manifests and Mounts

Manifests define the workspace contract. Mounts materialize Git-backed content
inside the umbrella.

## Manifest lifecycle

```sh
our manifests add acme https://github.com/example/acme-workspace.git
our manifests list
our manifests sync acme
our manifests validate acme
```

The synced cache is disposable derived state. Admin authoring commands should
write a maintainer checkout through `--manifest-dir`.

Manifests can also set the default sync publish policy:

```json
{
  "sync": {
    "publish_policy": "auto"
  }
}
```

Allowed values are `auto`, `never`, and `pr`. The setting applies when
`our sync` is run without `--publish`; an explicit CLI flag overrides it.

## Mount lifecycle

```sh
our mounts list --manifest acme
our mounts add handbook --manifest acme
our mounts add product:sample-product --manifest acme
our mounts sync --all --manifest acme
our mounts remove handbook --print
```

Required and default mounts are synced during onboarding. Optional mounts are
selected on demand and recorded in umbrella state.

Content mounts can be broad, such as a handbook, or narrow, such as meetings or
support. A support mount is intended for private anonymized records that capture
problem, context, solution, validation, and feature signals for later search.
A fleet mount is a registry rather than a journal: one record per deployed
instance, keyed by a stable id, updated in place with `our fleet set`, with
state history carried by the record's git log. Its own repo gives
remote-access and commercial fields a tighter access boundary than the
handbook.

## Sparse content

A private workspace repo can be both the manifest source and a handbook mount.
Sparse include paths prevent manifest internals such as `manifest.json` and
`skills/` from appearing as a second copy inside the umbrella.

## Catalog commands

```sh
our products list --manifest acme
our customers list --manifest acme
```

Product catalog entries can reference related manifest skills. Mounting a
product repo does not let the repo inject organization-namespaced skills.
