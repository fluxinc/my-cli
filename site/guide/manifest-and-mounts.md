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

For a fuller neutral reference, browse the
[Acme example workspace](https://github.com/fluxinc/our-ai/tree/master/examples/acme-workspace),
which keeps the manifest fixture and content fixture in separate directories.

When `our manifests sync` pulls or clones exactly one manifest, it also
reconciles generated guidance, umbrella MCP config, and manifest skills for an
existing matching umbrella. Derived state means generated guidance (`AGENTS.md`
and the `CLAUDE.md` pointer), umbrella-root `.mcp.json`, and
manifest-declared skills. Use `--no-derived` for a cache-only refresh when you
know derived state is already current, or `--umbrella DIR` to target a
specific umbrella.

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
changes, content mounts, manifest checkouts, or catalog repo clones
(`repos`).

## Organization contract

A manifest can add short, binding rules to the generated guidance with a
top-level `contract` list:

```json
{
  "contract": [
    "Continue an existing relevant support record or create a new dated record when working on any fleet member."
  ]
}
```

Each entry must be a non-empty, single-line rule, with no duplicates. The rules
render as an `## Organization Contract` section in generated `AGENTS.md`,
between the public baseline and any manifest guidance fragments, so every
harness sees them as obligations rather than background reading. Longer
narrative guidance still belongs in `agent_guidance.paths` fragments.

## Mount lifecycle

```sh
our mounts list
our mounts add handbook
our mounts sync --all
our mounts remove handbook --print
```

Required and default mounts are synced during onboarding. Optional mounts are
selected on demand and recorded in umbrella state. Code repositories are not
mounts: they live in `catalog/repos.json` and are cloned under `repos/<id>`
with `our repos add <id>` (legacy `products/` checkouts auto-migrate at
`our setup`).

Content mounts can be broad, such as a handbook, or narrow, such as meetings or
support. A support mount is intended for private anonymized records that capture
problem, context, solution, validation, and feature signals for later search.
A fleet mount is a registry rather than a journal: one record per deployed
instance, keyed by a stable id, updated in place with `our fleet set`, with
state history carried by the record's git log. Its own repo gives
remote-access and commercial fields a tighter access boundary than the
handbook.

## Sparse content

Content mounts can use sparse include paths so only the relevant subtree
lands in the umbrella.

## Catalog commands

```sh
our products list
our repos list
our customers list
```

Products are business catalog entries; each may link the repos that implement
it (`repos: ["<repo-id>"]`) and reference related manifest skills. Repos are
the organization's repositories in `catalog/repos.json`, cloned on demand with
`our repos add <id>`. Cloning a catalog repo does not let that repo inject
organization-namespaced skills.
