# Manifests and Mounts

Manifests define the workspace contract. Mounts materialize Git-backed content
inside the umbrella.

## Manifest lifecycle

```sh
our init acme --name "Acme"
our publish
our manifests add acme <git-url>
our manifests list
our manifests sync acme
our manifests validate acme
```

The manifest repository is the control plane: it defines the workspace but is
not part of it. `our init` keeps it at the registry path, outside the
umbrella, so day-to-day work cannot accidentally edit `manifest.json`; admin
authoring commands write to a maintainer checkout through `--manifest-dir`.
Content lives in separate mounted repositories — `our init` scaffolds one at
`<umbrella>/workspace` — which also lets hosting permissions differ: admins
push the manifest, the whole organization pushes content.

Until `our publish` runs, the scaffolded mount's `git_url` is the content
repo's local path and every sync reports `local-only`. `our publish` creates
the private remotes, rewrites mount URLs to the published repositories, and
pushes; `our sync` refuses to publish a manifest that still references local
paths, and `our doctor` names each such mount.

A mount may also read from the same repository as the manifest (`git_url`
`"."` or the manifest's own URL). This conflated layout is supported for
existing organizations — the CLI keeps a single checkout for it and skips
sparse-checkout so manifest files stay available — but new organizations get
the separated layout.

For a fuller neutral reference, browse the
[Acme example workspace](https://github.com/fluxinc/our-ai/tree/master/examples/acme-workspace).

When `our manifests sync` pulls or clones exactly one manifest, it also
reconciles generated guidance and manifest skills for an existing matching
umbrella. Derived state means the generated guidance (`AGENTS.md` and the
`CLAUDE.md` pointer) and manifest-declared skills. Use `--no-derived` for a
cache-only refresh when you know derived state is already current, or
`--umbrella DIR` to target a specific umbrella.

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
`our sync --scope all|local|content|manifest|repos` limits a run to local-only
changes, content mounts, manifest checkouts, or product clones (`repos`; the
older `products` spelling still works).

## Mount lifecycle

```sh
our mounts list
our mounts add handbook
our mounts add product:sample-product
our mounts sync --all
our mounts remove handbook --print
```

Required and default mounts are synced during onboarding. Optional mounts are
selected on demand and recorded in umbrella state. Product mounts keep the
`product:<id>` syntax, but clones land under `repos/<id>` (legacy `products/`
checkouts auto-migrate at `our setup`).

Content mounts can be broad, such as a handbook, or narrow, such as meetings or
support. A support mount is intended for private anonymized records that capture
problem, context, solution, validation, and feature signals for later search.
A fleet mount is a registry rather than a journal: one record per deployed
instance, keyed by a stable id, updated in place with `our fleet set`, with
state history carried by the record's git log. Its own repo gives
remote-access and commercial fields a tighter access boundary than the
handbook.

## Sparse content

External content mounts can use sparse include paths so only the relevant
subtree lands in the umbrella. For legacy conflated repos that serve as both
manifest source and content mount, the CLI shares one checkout and does not
apply sparse-checkout to it (narrowing the tree would hide the manifest
files the registry reads).

## Catalog commands

```sh
our products list
our customers list
```

Product catalog entries can reference related manifest skills. Mounting a
product repo does not let the repo inject organization-namespaced skills.
