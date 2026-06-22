# The Model

`my` has eight concepts. Every command works against one of them.

## Manifest

The organization source of truth — the control plane. A manifest repo declares
skills, mounts, catalog entries, tool hints, services, roles, umbrella
defaults, and generated guidance inputs. The manifest is not the workspace:
the workspace is a mount of things the manifest defines. It lives in its own
private repository outside the umbrella, so day-to-day work never edits it and
hosting permissions can restrict who pushes it.

Create a starter organization or register an existing manifest:

```sh
my init acme --name "Acme"
my publish
my manifests add acme <git-url>
my manifests sync acme
my manifests validate acme
```

Services and roles are manifest vocabulary, not separate top-level concepts.
`data_bindings` map stable data nouns (`customers`, `meetings`, `support`,
`fleet`) to an existing `mount:<id>` or `service:<id>`, and may carry
`guidance` fragments rendered as source-attributed `## Domain Notes` sections.
`services` describe
remote organization surfaces such as HTTP APIs and MCP servers using
reference-first auth (`env://`, `op://`, `broker://`, or `none`). `roles`
select mounts, skills, tools, services, and optional guidance fragments as
local loadouts. In Mode A, `my setup --role <id>` stores the selected role
locally, appends role guidance to `AGENTS.md`, and materializes an
umbrella-root `.mcp.json` for locally described MCP services visible to that
role.

## Skill

A capability exposed to harnesses. Static organization skills live inside the
manifest repo. Tool-provided skills are materialized by their owning tool.
`my ai` composes organization skills into the launch root's `.agents/skills`
tree, with harness mirrors where needed. Pick the loadout with
`my ai --skills all|none|<id,...>` or `my ai --profile <id>` (mutually
exclusive); a `profile` is a named skill loadout declared in the manifest's
`profiles` list, distinct from a role. With no selector, `my ai` uses the
default for the launch target: selected role skills for a base umbrella,
workspace-satisfied skills for a session, all org skills for an unscoped
umbrella, and no org skills for a repo launch. OpenCode is compatibility-global
until a launch-root seam is proven, so its org skills stay user-global and these
selectors are rejected for it. The public CLI also ships one bundled
organization-neutral self-skill named `my-cli`, managed by `my skills self ...`,
so harnesses know how to use My AI itself.
These are the two skill sources, split by a public/private line: the self-skill
is public and ships in the binary; organization skills are private to a manifest
you control and appear only once that manifest is synced. Nothing
organization-specific is baked into the public CLI.

## Umbrella

A per-user workspace envelope, normally `~/<org>`. It contains local state,
generated guidance, content mounts, product repos, generated `.mcp.json`, and
local scratch. When
initialized as a Gnit control workspace, multi-repo Change creation, ordered
push, and resume use Gnit instead of My AI reimplementing that transaction layer.
Pins remain available for deliberate recorded workspace states.

## Mount

A Git-backed content folder cloned into the umbrella. Mounts can be required,
default, or optional. Common content kinds include handbook, meetings, support,
fleet, policy, docs, and repo. `my init` scaffolds the organization's content
repo as a mount at `<umbrella>/workspace`. Support content holds anonymized problem-to-solution
records with frontmatter attribution (customer id, repeatable identifiers,
claimed_by/observed_by/approved_by), searched via `my support`. Fleet content
holds one record per deployed instance, updated in place via `my fleet set`;
any identifier resolves via `my fleet get`.

## Session

An isolated unit of work under `<umbrella>/sessions/<id>`: a git worktree of each
content mount on a fresh `my/session/<id>` branch, session-local `scratch/`, a
`SESSION.md` summary, and generated session guidance, recorded in a registry
under `.my-cli/sessions/`. Create one with `my session start` or
`my ai --new-session`; `my session join <id> <harness>` adds another harness
to the same session. When the current directory is inside an active session,
content commands write to that session's mount worktree, so session work does
not leak into the base umbrella. Work leaves a session only through
`my session finish --land | --publish | --discard`, and `my sync` holds
outbound publish of a mount while an active session on it is dirty or unlanded.
`my session status` and `my session list` show active session state; `my doctor`
includes session health alongside workspace
diagnostics.

## Catalog

JSON inventories for products and repos. Products are business entities (no
`git_url`) that may link the repos implementing them. Repos in
`catalog/repos.json` are the organization's repositories, opted in on demand
with `my repos add <id>` and cloned under `repos/<id>` in the umbrella;
legacy `products/` checkouts migrate automatically at `my setup`. Customer
identities are mounted workspace records under `customers/*.md`, not manifest
catalog rows.

## Guidance

Generated root instructions for agents, written as `AGENTS.md`. `CLAUDE.md`
points to the same file where supported. Manifest role guidance is appended
when the local umbrella has a selected role. The public baseline includes a
built-in fleet work contract (start fleet work from `my fleet get`, record it
in support records). A manifest can add its own short, binding rules with a
top-level `contract` list of strings, rendered as an `## Organization
Contract` section between the baseline and manifest guidance fragments.

## Tool

An external executable the organization depends on. `my` reports presence and
install hints; it does not silently install tools.

## Public and private repos

This repository is the public mechanism. Organization content belongs in a
private manifest or workspace repo. Public fixtures should stay generic:
`acme`, `example`, and `sampleco`.

## Where `my` Sits In The Stack

`my` is the organization layer of a broader agentic toolchain: it owns
manifest semantics, workspace materialization, and safe publish. Multi-repo
publish delegates to [gnit](https://github.com/mostlydev/gnit) when the
umbrella is a gnit control workspace. For unattended fleet agents, manifest
roles compile into [clawdapus](https://github.com/mostlydev/clawdapus)
containers whose model and tool access is mediated by the cllama governance
proxy — the `my` CLI rides inside as a governed work surface, with only the
operational verb set reachable. `my` never becomes a container runtime or a
policy engine; it speaks those tools' formats at the boundary.
