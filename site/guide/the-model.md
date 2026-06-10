# The Model

`our` has seven concepts. Every command works against one of them.

## Manifest

The organization source of truth — the control plane. A manifest repo declares
skills, mounts, catalog entries, tool hints, umbrella defaults, and generated
guidance inputs. The manifest is not the workspace: the workspace is a mount
of things the manifest defines. It lives in its own private repository
outside the umbrella, so day-to-day work never edits it and hosting
permissions can restrict who pushes it.

Create a starter organization or register an existing manifest:

```sh
our init acme --name "Acme"
our publish
our manifests add acme <git-url>
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
fleet, policy, docs, and repo. `our init` scaffolds the organization's content
repo as a mount at `<umbrella>/workspace`. Support content holds anonymized problem-to-solution
records with frontmatter attribution (customer id, repeatable identifiers,
claimed_by/observed_by/approved_by), searched via `our support`. Fleet content
holds one record per deployed instance, updated in place via `our fleet set`;
any identifier resolves via `our fleet get`.

## Catalog

JSON inventories for products and canonical customers. Product repos are
opted in on demand with `our mounts add product:<id>` and cloned under
`repos/<id>` in the umbrella; legacy `products/` checkouts migrate
automatically at `our setup`.

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

## Where our sits in the stack

`our` is the organization layer of a broader agentic toolchain: it owns
manifest semantics, workspace materialization, and safe publish. Multi-repo
publish delegates to [gnit](https://github.com/mostlydev/gnit) when the
umbrella is a gnit control workspace. For unattended fleet agents, manifest
roles compile into [clawdapus](https://github.com/mostlydev/clawdapus)
containers whose model and tool access is mediated by the cllama governance
proxy — the `our` CLI rides inside as a governed work surface, with only the
operational verb set reachable. `our` never becomes a container runtime or a
policy engine; it speaks those tools' formats at the boundary.
