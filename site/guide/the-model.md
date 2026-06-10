# The Model

`our` has seven concepts. Every command works against one of them.

## Manifest

The organization source of truth. A manifest repo declares skills, mounts,
catalog entries, tool hints, umbrella defaults, and generated guidance inputs.

Register and refresh it:

```sh
our manifests add acme https://github.com/example/acme-workspace.git
our manifests sync acme
our manifests validate acme
```

## Skill

A capability installed into harness skill directories. Static skills live
inside the manifest repo. Tool-provided skills are materialized by their owning
tool, then installed by `our`. The public CLI also ships one bundled
organization-neutral self-skill named `our`, managed by
`our skills self ...`, so harnesses know how to use Our AI itself.
These are the two skill sources, split by a public/private line: the self-skill
is public and ships in the binary; organization skills are private to a manifest
you control and appear only once that manifest is synced. Nothing
organization-specific is baked into the public CLI.

## Umbrella

A per-user workspace envelope, normally `~/<org>`. It contains local state,
generated guidance, content mounts, product repos, and local scratch. When
initialized as a Gnit control workspace, multi-repo Change creation, ordered
push, and resume use Gnit instead of Our AI reimplementing that transaction layer.
Pins remain available for deliberate recorded workspace states.

## Mount

A Git-backed content folder cloned into the umbrella. Mounts can be required,
default, or optional. Common content kinds include handbook, meetings, support,
fleet, policy, docs, and repo. Sparse include paths keep private manifest internals out
of the operating workspace.

## Catalog

JSON inventories for products and canonical customers. Product repos are
opted in on demand with `our mounts add product:<id>`.

## Guidance

Generated root instructions for agents, written as `AGENTS.md`. `CLAUDE.md`
points to the same file where supported.

## Tool

An external executable the organization depends on. `our` reports presence and
install hints; it does not silently install tools.

## Public and private repos

This repository is the public mechanism. Organization content belongs in a
private manifest or workspace repo. Public fixtures should stay generic:
`acme`, `example`, and `sampleco`.
