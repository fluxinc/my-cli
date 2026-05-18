# flux — Architecture & Design

This document explains *why* `flux` is shaped the way it is. The README covers
usage; this covers the model and the decisions behind it. It is intentionally
generic — no organization is described here.

## 1. Problem

A company adopts AI agents across several harnesses (Claude Code, Codex,
OpenCode, Gemini) on many machines. Each agent is only as useful as the skills
and context it can reach. Without a mechanism, every machine drifts: different
skills, stale company knowledge, ad-hoc tool setup, no provenance.

The goal: **one command on a fresh machine makes every installed agent
operate from the same skills and the same company context.** No per-harness
fiddling, no manual cloning, no "which version of the skill is this."

## 2. Audience Principle: agents are primary

The CLI is optimized for an AI agent as the primary caller, not a human typing
interactively:

- **Deterministic discovery.** Given a manifest, the resulting layout is fully
  determined. No prompts, no interactive setup.
- **Machine-parseable everything.** Every command takes `--json`.
- **Structured failure.** Errors are `{error, message, remediation}` with a
  concrete next command. An agent that hits a wall can self-recover.
- **Idempotent and re-runnable.** `onboard` and `sync` can be run repeatedly
  and safely; they converge, they don't accumulate.

Humans own agency — products, goals, decisions, ownership. That belongs in
*content* (a manifest repo, handbook documents), not in CLI surface area. The
CLI deliberately does not grow human-workflow verbs; it grows content kinds.

## 3. The six concepts

**Manifest** — an org's configuration in a Git repo, cached locally on
`manifest add` + `manifest sync`. The single source of truth for what skills
exist, what mounts exist, what products are in the catalog, and what tools the
org expects. Validated by a schema (`manifest validate`).

**Skill** — a capability installed into harness skill directories. Two kinds:

- *Static*: a directory inside the manifest repo. Copied/symlinked into each
  present harness.
- *Tool-provided*: declared with a source tool; `flux` invokes that tool's own
  skill installer to materialize the skill, then installs the result. This
  keeps tool-owned skills authoritative to the tool, not vendored copies.

**Umbrella** — a per-user, non-Git directory (default `~/<org>`), the operating
envelope. It is not itself version-controlled; it *contains* version-controlled
mounts plus local-only scratch:

```
~/<org>/
├── .flux/
│   ├── workspace.json   identity: schema version, org, manifest ref, created_at
│   └── state.json       dynamic: selected products, per-mount sync status
├── <handbook mount>/    manifest-declared content
├── <other mounts>/
├── products/            opted-in catalog products (detached clones)
├── personal/            local-only, never synced — agent + human scratch
├── AGENTS.md            generated workspace instructions for agents
└── CLAUDE.md            alias for harnesses that read Claude-specific names
```

`personal/` and `products/` always exist after `onboard`. Entity commands
(`flux meetings ...`) resolve against the umbrella by walking up from the
working directory to find `.flux/workspace.json`, so agents can run them from
anywhere inside it.

**Mount** — a Git-backed content folder cloned into the umbrella. Kinds include
handbook, meetings, policy, docs, repo. Modes: `required`, `default`,
`optional`. Optional mounts are clone-if-accessible: if the user lacks access
they are skipped with a warning, not a failure (RBAC by Git permissions, not by
the CLI).

**Catalog** — a JSON inventory of the org's products. Each product records its
source-code `git_url`, a short purpose, and any related manifest skill IDs that
help agents work in that repo. Products are *not* mounted by default; a user
opts one in with `flux mount add product:<id>`, which clones it under
`products/<id>` and records it in umbrella state. This keeps the default
umbrella small and lets each operator pull only what they work on.
`related_skills` are references to skills declared by the manifest; mounting a
product repo does not let that repo inject new org-namespaced skills.

**Tool** — an external executable the org depends on. The manifest declares
purpose and install hints. `flux doctor` / `flux tools info` report presence
and what to run. `flux` never silently installs a tool — hints, not actions.

## 4. The public/private boundary (a first-class constraint)

The mechanism is generic and public; the content is proprietary and private.
These are **two repositories**:

1. **`flux` (public)** — this CLI. Generic, no org data, tests use neutral
   placeholders.
2. **`<org>-workspace` (private)** — `manifest.json`, proprietary skills,
   catalog JSON, tool declarations, and handbook content.

A subtle consequence: the private workspace repo plays two roles. It is the
**manifest source** (cached under the user's data dir, authoritative for skills
and config) *and* it is the **handbook content mount** inside the umbrella. A
naive design would clone the whole repo twice and expose `skills/` and
`manifest.json` a second time inside the umbrella, where an agent grepping the
umbrella could read a stale duplicate.

**Mount scoping** solves this. A mount may declare `include_paths`. When set,
`flux` clones with `git clone --sparse` and applies
`git sparse-checkout set --no-cone <paths>`, so only the listed content
directories appear in the umbrella. The manifest and skill sources stay in the
manifest cache and never appear as a second source of truth. The same scoping
mechanism is the forward path for finer access control: narrow what a given
umbrella materializes without splitting the repo apart.

Include paths are validated as portable, repo-relative paths (no absolute
paths, no `..` traversal, no backslashes) so a manifest cannot scope a mount
outside its own tree.

## 5. Onboarding flow

`flux onboard --manifest <name>`:

1. Resolve the registered manifest; ensure the local cache is synced.
2. Validate the manifest (schema + cross-references).
3. Install declared skills into every present harness (static ones from the
   cache; tool-provided ones via the tool's installer). Provenance is recorded;
   a directory `flux` did not place is never overwritten.
4. Create/repair the umbrella: `.flux/workspace.json`, `.flux/state.json`,
   `personal/`, `products/`.
5. Generate root `AGENTS.md` from the embedded public baseline plus
   `agent_guidance.paths` declared by the manifest, and make `CLAUDE.md` point
   at it where the platform permits symlinks.
6. Sync `required` and `default` mounts (scoped by `include_paths` if present).
7. Re-sync any previously selected catalog products recorded in umbrella state.

Every step is convergent: re-running `onboard` reconciles rather than
duplicates.

## 6. Authentication contract

Private manifests and mounts live in private Git repos. `flux` does not invent
a credential mechanism: for GitHub HTTPS URLs it checks `gh auth status` before
a real fetch and, if unauthenticated, fails with the exact remediation
(`gh auth login`). SSH URLs fall through to the user's normal Git/SSH auth.
Dry-run paths never touch the network or the auth check.

## 7. Error contract

Non-trivial failures return a structured value (and JSON under `--json`):

```json
{ "error": "unknown_product",
  "message": "no catalog product \"x\" in manifest \"acme\"",
  "remediation": "flux catalog list products --manifest acme" }
```

`remediation` is always a real command that exists in the CLI. The design rule:
a dead end must always hand the caller the next move, because the caller is
usually an agent, not a person who can improvise.

## 8. Dependency policy

Go standard library only. A tool whose job is installing skills and cloning
repos is part of the user's supply chain; minimizing its own dependency surface
is a security property, not a preference. `git` and (for private GitHub) `gh`
are the only external executables, invoked as subprocesses, never linked.

## 9. Deliberate non-goals

- **No human-workflow verbs.** Assignments, reviews, approvals are content in a
  mount kind, not CLI commands.
- **No silent tool installation.** Hints only.
- **No bundled org content.** The public repo never carries a real manifest,
  real skills, or real knowledge.
- **No MCP/daemon surface.** A single CLI is the whole interface for both
  humans and agents; no second integration surface to keep in sync.

## 10. Extensibility

New content kinds are added as mount kinds and (optionally) a thin entity verb
set following the `list / add / search / get` shape already used by meetings —
same shape, different on-disk directory. The CLI grows by adding *content
kinds*, not by adding workflow features, keeping the agent-facing contract
stable.
