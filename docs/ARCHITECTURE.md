# our — Architecture & Design

This document explains *why* `our` is shaped the way it is. The README covers
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
- **Idempotent and re-runnable.** `setup` and `sync` can be run repeatedly
  and safely; they converge, they don't accumulate.

Humans own agency — products, goals, decisions, ownership. That belongs in
*content* (a manifest repo, handbook documents), not in CLI surface area. The
CLI deliberately does not grow human-workflow verbs; it grows content kinds.

## 3. The seven concepts

**Manifest** — an org's configuration in its own private Git repo, checked
out locally on `manifests add` + `manifests sync`. The single source of truth
for what skills exist, what mounts exist, what products are in the catalog,
what tools the org expects, and the default `our sync` publish policy.
Validated by a schema (`manifests validate`). The manifest is the **control
plane**: it is not the workspace — the workspace is a mount of things the
manifest defines — and it lives outside the umbrella so day-to-day work never
touches it. Write access can differ per plane: admins push the manifest; the
whole organization pushes workspace content.

**Skill** — a capability installed into harness skill directories. Two kinds:

- *Static*: a directory inside the manifest repo. Copied/symlinked into each
  present harness.
- *Tool-provided*: declared with a source tool; `our` invokes that tool's own
  skill installer to materialize the skill, then installs the result. This
  keeps tool-owned skills authoritative to the tool, not vendored copies.

Our AI also ships one public, organization-neutral self-skill named `our`. It
teaches harnesses how to use the CLI and is managed by `our skills self ...`,
not by organization manifests.
Keeping it in the binary, not in a manifest, is deliberate: the public CLI
carries no organization-specific content, so every org's particulars stay in the
manifests they control.

**Umbrella** — a per-user operating envelope (default `~/<org>`). It contains
Our AI state, generated guidance, version-controlled mounts, product repositories,
and local-only scratch. When initialized as a Gnit control workspace, the
umbrella's root records workspace metadata and pins, while the member
repositories remain ordinary Git checkouts.

```
~/<org>/
├── .our/
│   ├── workspace.json   identity: schema version, org, manifest ref, created_at
│   └── state.json       dynamic: selected products, per-mount sync status
├── .gnit/                optional Gnit control metadata for multi-repo sync
├── workspace/           the org content repo (its own remote), mounted
├── <other mounts>/
├── repos/               opted-in catalog product repositories
├── personal/            local-only, never synced — agent + human scratch
├── AGENTS.md            generated workspace instructions for agents
└── CLAUDE.md            alias for harnesses that read Claude-specific names
```

`personal/` and `repos/` always exist after `setup`. Entity commands
(`our meetings ...`) resolve against the umbrella by walking up from the
working directory to find `.our/workspace.json`. If the caller is outside the
umbrella, meeting commands use the single configured registered manifest's
recommended umbrella when it has been set up.

**Mount** — a Git-backed content folder cloned into the umbrella. Kinds include
handbook, meetings, support, fleet, policy, docs, repo. Modes: `required`, `default`,
`optional`. Optional mounts are clone-if-accessible: if the user lacks access
they are skipped with a warning, not a failure (RBAC by Git permissions, not by
the CLI).

**Catalog** — JSON inventories for products and canonical customers. Each
product records its source-code `git_url`, a short purpose, and any related
manifest skill IDs that help agents work in that repo. Products are *not*
mounted by default; a user opts one in with `our mounts add product:<id>`, which
clones it under `repos/<id>` and records it in umbrella state. This keeps the
default umbrella small and lets each operator pull only what they work on.
`related_skills` are references to skills declared by the manifest; mounting a
product repo does not let that repo inject new org-namespaced skills. Customer
catalog entries provide stable IDs, aliases, partner associations, and optional
domain confirmation so meeting metadata can resolve to one canonical identity.

**Guidance** — generated root instructions for agents. `our setup` writes
`AGENTS.md` from the embedded public baseline plus manifest-declared guidance
fragments, and points `CLAUDE.md` at the same content where the platform allows
it. `our ai` checks guidance freshness before starting a harness.

**Tool** — an external executable the org depends on. The manifest declares
purpose and install hints. `our doctor`, `our tools list`, and `our tools
info` report presence and what to run. `our` never silently installs a tool —
hints, not actions. Meeting search is allowed to use `qmd` when present because
it is an operator tool declared by the manifest, but the built-in markdown scan
remains the fallback and keeps the CLI functional without optional tools.

## 4. The public/private boundary (a first-class constraint)

The mechanism is generic and public; the content is proprietary and private.
These are **three repositories**:

1. **`our` (public)** — this CLI. Generic, no org data, tests use neutral
   placeholders.
2. **`<org>-manifest` (private, control plane)** — `manifest.json`,
   proprietary skills, catalog JSON, tool declarations, agent guidance
   fragments. Lives at the registry path, outside the umbrella; changed
   through `our admin` commands; hosting permissions can restrict pushes to
   admins.
3. **`<org>-workspace` (private, data plane)** — the operating content:
   meetings, support, fleet, decisions, projects, policy, people. Mounted
   visibly in the umbrella; pushed by the whole organization.

`our init` creates both private repos locally and `our publish` takes them
online; teammates register only the manifest URL, and the manifest defines
which content repos mount where. Keeping the planes in separate repositories
is what lets write access differ (admins vs everyone) and guarantees an agent
grepping the umbrella can never read — or dirty — the manifest.

A **conflated layout** (one private repo serving as both manifest source and
content mount) remains supported for compatibility: `our` keeps a single
checkout for it, skips sparse-checkout on it, and syncs it as one merged
entry. New organizations get the separated layout.

**Mount scoping** narrows external content mounts. A mount may declare
`include_paths`; `our` then clones with `git clone --sparse` and applies
`git sparse-checkout set --no-cone <paths>`, so only the listed directories
appear in the umbrella. The same scoping mechanism is the forward path for
finer access control: narrow what a given umbrella materializes without
splitting a repo apart.

Include paths are validated as portable, repo-relative paths (no absolute
paths, no `..` traversal, no backslashes) so a manifest cannot scope a mount
outside its own tree.

## 5. Onboarding flow

`our setup --manifest <name>`:

1. Resolve the registered manifest; ensure the local cache exists and, when the
   TTL allows it, best-effort fast-forward clean stale manifest/content
   checkouts.
2. Validate the manifest (schema + cross-references).
3. Install the bundled `our` self-skill and declared organization skills into
   every present harness (static ones from the cache; tool-provided ones via
   the tool's installer). Provenance is recorded; a directory `our` did not
   place is never overwritten.
4. Create/repair the umbrella: `.our/workspace.json`, `.our/state.json`,
   `personal/`, `repos/`.
5. Generate root `AGENTS.md` from the embedded public baseline plus
   `agent_guidance.paths` declared by the manifest, and make `CLAUDE.md` point
   at it where the platform permits symlinks.
6. Sync `required` and `default` mounts (scoped by `include_paths` if present).
7. Re-sync any previously selected catalog products recorded in umbrella state.

Every step is convergent: re-running `setup` reconciles rather than
duplicates.

Startup commands (`our root`, `our ai`, and `our setup`) use the same
best-effort refresh path. The guard is deliberately narrow: fast-forward only,
clean manifest/content checkouts only, TTL-gated, and never product repos,
dirty checkouts, diverged branches, or remote-unknown repos. Operators can use
`--no-refresh`, `OUR_NO_AUTO_REFRESH=1`, or `OUR_REFRESH_TTL` when they need
fully offline or deterministic reads.

The same startup commands also perform a user-level, TTL-gated release check
using `~/.local/share/our/update-check.json`. The notice is stderr-only so
stdout remains machine-safe. Operators can use `--no-update-check`,
`OUR_NO_UPDATE_CHECK=1`, or `OUR_UPDATE_CHECK_TTL` when release checks should
be suppressed or tuned separately from workspace refreshes.

## 6. Authentication contract

Private manifests and mounts live in private Git repos. `our` does not invent
a credential mechanism: for GitHub HTTPS URLs it checks `gh auth status` before
a real fetch and, if unauthenticated, fails with the exact remediation
(`gh auth login`). SSH URLs fall through to the user's normal Git/SSH auth.
Dry-run paths never touch the network or the auth check.

## 7. Error contract

Non-trivial failures return a structured value (and JSON under `--json`):

```json
{ "error": "unknown_product",
  "message": "no catalog product \"x\" in manifest \"acme\"",
  "remediation": "our products list --manifest acme" }
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
  organization-specific skills, or real knowledge.
- **No MCP/daemon surface.** A single CLI is the whole interface for both
  humans and agents; no second integration surface to keep in sync.

## 10. Extensibility

New content kinds are added as mount kinds and (optionally) a thin entity verb
set following the `list / add / search / get` shape already used by meetings —
same shape, different on-disk directory. The CLI grows by adding *content
kinds*, not by adding workflow features, keeping the agent-facing contract
stable.

Support knowledge is one such content kind: anonymized records capture problem,
context, solution, validation, and feature signals in private mounted markdown
under `support/`. Operator tools such as `qmd` can index those records, and
agents can later use them to draft feature requests without turning support
capture into a separate workflow system.

The fleet registry is the first *registry-shaped* content kind: where meetings
and support are dated, append-only journals, fleet keeps one record per
deployed instance under `fleet/<id>.md`, keyed by a stable id and updated in
place with `our fleet set` (which preserves untouched frontmatter and body).
State history is the record's git history rather than event files, so each
meaningful transition should publish with a descriptive `our sync --message`.
The `identifiers` frontmatter list is the join currency with support records:
`our fleet get` resolves any identifier and surfaces related incidents, and
`our support add --identifier` warns when an identifier is unknown to the
registry. Both nouns share the `internal/record` engine.
