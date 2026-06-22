# my — Architecture & Design

This document explains *why* `my` is shaped the way it is. The README covers
usage; this covers the model and the decisions behind it. It is intentionally
generic — no organization is described here.

## 1. Problem

A company adopts AI agents across several harnesses (Claude Code, Codex,
OpenCode, Antigravity) on many machines. Each agent is only as useful as the
skills and context it can reach. Without a mechanism, every machine drifts:
different skills, stale company knowledge, ad-hoc tool setup, no provenance.

The goal: **one command on a fresh machine makes every installed agent operate
from the same manifest-defined context and launch profile.** No per-harness
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

## 3. The eight concepts

**Manifest** — an org's configuration in its own private Git repo, checked
out locally on `manifests add` + `manifests sync`. The single source of truth
for what skills exist, what mounts exist, what products are in the catalog,
what services and roles exist, what tools the org expects, and the default
`my sync` publish policy.
Validated by a schema (`manifests validate`). The manifest is the **control
plane**: it is not the workspace — the workspace is a mount of things the
manifest defines — and it lives outside the umbrella so day-to-day work never
touches it. Write access can differ per plane: admins push the manifest; the
whole organization pushes workspace content.

**Skill** — a capability exposed to harnesses. Two kinds:

- *Static*: a directory inside the manifest repo. Composed by `my ai` into
  launch-root `.agents/skills` for harnesses with a project-local skill seam,
  with per-harness mirrors where needed. OpenCode currently remains
  compatibility-global because no launch-root seam has been proven.
- *Tool-provided*: declared with a source tool; `my` invokes that tool's own
  skill installer to materialize the skill, then includes the result in the
  launch profile. This keeps tool-owned skills authoritative to the tool, not
  vendored copies.

My AI also ships one public, organization-neutral self-skill named `my`. It
teaches harnesses how to use the CLI and is managed by `my skills self ...`,
not by organization manifests.
Keeping it in the binary, not in a manifest, is deliberate: the public CLI
carries no organization-specific content, so every org's particulars stay in the
manifests they control.

**Umbrella** — a per-user operating envelope (default `~/<org>`). It contains
My AI state, generated guidance, version-controlled mounts, opted-in catalog
repositories, work sessions, and local-only scratch. When initialized as a
Gnit control workspace, the umbrella's root records workspace metadata and
pins, while the member repositories remain ordinary Git checkouts.

```
~/<org>/
├── .my-cli/
│   ├── workspace.json   identity: schema version, org, manifest ref, created_at
│   └── state.json       dynamic: selected repos/role, per-mount sync status
├── .gnit/                optional Gnit control metadata for multi-repo sync
├── workspace/           the org content repo (its own remote), mounted
├── <other mounts>/
├── repos/               opted-in catalog repositories
├── sessions/            isolated sessions: git worktrees per mount
├── personal/            local-only, never synced — agent + human scratch
├── .mcp.json            generated local MCP config for selected role services
├── AGENTS.md            generated workspace instructions for agents
└── CLAUDE.md            alias for harnesses that read Claude-specific names
```

`personal/` and `repos/` always exist after `setup`. Entity commands
(`my meetings ...`) resolve against the umbrella by walking up from the
working directory to find `.my-cli/workspace.json`; when the working directory
is inside an active session under `sessions/<id>`, they resolve one level
further, to that session's mount worktrees instead of the base mounts. If the
caller is outside the umbrella, meeting commands use the single configured
registered manifest's recommended umbrella when it has been set up.

**Mount** — a Git-backed content folder cloned into the umbrella. Kinds include
handbook, customers, meetings, support, fleet, policy, docs. Modes:
`required`, `default`, `optional`. Optional mounts are clone-if-accessible: if
the user lacks access
they are skipped with a warning, not a failure (RBAC by Git permissions, not by
the CLI).

**Session** — an isolated unit of work under `sessions/<id>` in the umbrella: a
git worktree of each writable content mount on a fresh `my/session/<id>`
branch, plus session-local scratch, a `SESSION.md` summary, and generated
session guidance with concrete umbrella, organization, selected-role, session,
mount, and exact finish/resume command context, recorded in a registry under
`.my-cli/sessions/`. Sessions are opt-in: `my session start` or `my ai
--new-session` creates one, `my session join <id> <harness>` or
`my ai --session <id>` launches another harness into one, and plain `my ai`
launches from the base umbrella unless the current directory is already inside
an active session. Resume launches rewrite session guidance before exec so
older active sessions pick up the current startup contract. Commands are
session-aware by working directory — run inside an active session, content
commands write to that session's mount worktrees and plain `my ai` stays in
the session; a directory under `sessions/` that matches no active session is an
error, never a silent fallback to base. Legacy `work/<id>` sessions are
recognized for launch compatibility and migrate lazily through session
commands or `my doctor --fix`.
Work leaves a
session only through `my session finish --land | --publish | --discard`, and
`my sync` holds outbound publish of a mount while an active session on it is
dirty or unlanded.

Sessions exist because writes are the risky operation: a half-edited tracked
file or stray draft in the base checkout would otherwise ride the next
`my sync` publish. Two mechanisms compose. Adoption-gated publishing means
`my sync` only auto-publishes content files that were explicitly adopted —
records created by the CLI are adopted via Git intent-to-add, and `my record
adopt` (or a plain `git add`) adopts the rest. Sessions add structural
isolation on top for work that should not touch the base checkout at all,
while staying plain Git: a human can `cd` into a session and take over with
ordinary commands. Keeping sessions opt-in keeps the default launch
ergonomic for humans; agents are directed to the flag by generated guidance.

**Catalog** — JSON inventories for products and repos.
Products are business entities: a name, description, purpose, optional links
to the repos that implement them (`repos: ["<repo-id>"]`), and any related
manifest skill IDs. Repos (`catalog/repos.json`) are the organization's
repositories — each records its `git_url` and an optional `default` flag. A
user opts a repo in with `my repos add <id>`, which clones it under
`repos/<id>` and records it in umbrella state. This keeps the default
umbrella small and lets each operator pull only what they work on.
`related_skills` are references to skills declared by the manifest; cloning a
catalog repo does not let that repo inject new org-namespaced skills. Customer
identity records live in mounted workspace content under `customers/*.md`, so
the same backend permissions that protect meetings/support/fleet protect
ordinary customer data.

**Guidance** — generated root instructions for agents. `my setup` writes
`AGENTS.md` from the embedded public baseline plus manifest-declared guidance
fragments plus any selected-role fragments, and points `CLAUDE.md` at the same
content where the platform allows it. `my ai` checks guidance freshness before
starting a harness.

Manifest **services** and **roles** remain manifest vocabulary rather than new
top-level concepts. Services describe remote organization surfaces such as HTTP
APIs and MCP servers. Data bindings map stable data nouns to declared mount or
service surfaces. Roles select services and optional guidance fragments as
local loadouts; `my setup --role <id>` stores the local role selection and
materializes umbrella-root `.mcp.json` for locally described MCP services
visible to that role. Roles do not grant authority or prune mounts.

**Tool** — an external executable the org depends on. The manifest declares
purpose and install hints. `my doctor`, `my tools list`, and `my tools
info` report presence and what to run. `my` never silently installs a tool —
hints, not actions. Meeting search is allowed to use `qmd` when present because
it is an operator tool declared by the manifest, but the built-in markdown scan
remains the fallback and keeps the CLI functional without optional tools.

## 4. The public/private boundary (a first-class constraint)

The mechanism is generic and public; the content is proprietary and private.
These are **three repositories**:

1. **`my` (public)** — this CLI. Generic, no org data, tests use neutral
   placeholders.
2. **`<org>-manifest` (private, control plane)** — `manifest.json`,
   proprietary skills, product/repo catalog JSON, tool declarations, agent
   guidance fragments. Lives at the registry path, outside the umbrella; changed
   through `my admin` commands; hosting permissions can restrict pushes to
   admins.
3. **`<org>-workspace` (private, data plane)** — the operating content:
   customers, meetings, support, fleet, decisions, projects, policy, people.
   Mounted visibly in the umbrella; pushed by the whole organization.

`my init` creates both private repos locally and `my publish` takes them
online; teammates register only the manifest URL, and the manifest defines
which content repos mount where. Keeping the planes in separate repositories
is what lets write access differ (admins vs everyone) and guarantees an agent
grepping the umbrella can never read — or dirty — the manifest.

**Mount scoping** narrows content mounts. A mount may declare
`include_paths`; `my` then clones with `git clone --sparse` and applies
`git sparse-checkout set --no-cone <paths>`, so only the listed directories
appear in the umbrella. The same scoping mechanism is the forward path for
finer access control: narrow what a given umbrella materializes without
splitting a repo apart.

Include paths are validated as portable, repo-relative paths (no absolute
paths, no `..` traversal, no backslashes) so a manifest cannot scope a mount
outside its own tree.

## 5. Onboarding flow

`my setup --manifest <name>`:

1. Resolve the registered manifest; ensure the local cache exists and, when the
   TTL allows it, best-effort fast-forward clean stale manifest/content
   checkouts.
2. Validate the manifest (schema + cross-references).
3. Install or refresh the bundled `my` self-skill for every present
   filesystem harness. Declared organization skills are not installed globally
   by setup; `my ai` composes them into the selected launch root when a
   launch-root-capable harness is launched. OpenCode is the compatibility
   exception and keeps org skills in its user config dir when present or
   explicitly selected. Provenance is recorded; a directory `my` did not place
   is never overwritten.
4. Create/repair the umbrella: `.my-cli/workspace.json`, `.my-cli/state.json`,
   `personal/`, `repos/`.
5. Generate root `AGENTS.md` from the embedded public baseline plus
   `agent_guidance.paths` declared by the manifest, and make `CLAUDE.md` point
   at it where the platform permits symlinks.
6. Sync `required` and `default` mounts (scoped by `include_paths` if present).
7. Re-sync any previously selected catalog repos recorded in umbrella state.

Every step is convergent: re-running `setup` reconciles rather than
duplicates.

Startup commands (`my root`, `my ai`, and `my setup`) use the same
best-effort refresh path. The guard is deliberately narrow: fast-forward only,
clean manifest/content checkouts only, TTL-gated, and never catalog repos,
dirty checkouts, diverged branches, or remote-unknown repos. Operators can use
`--no-refresh`, `MYCLI_NO_AUTO_REFRESH=1`, or `MYCLI_REFRESH_TTL` when they need
fully offline or deterministic reads.

The same startup commands also perform a user-level, TTL-gated release check
using `~/.local/share/my-cli/update-check.json`. The notice is stderr-only so
stdout remains machine-safe. Operators can use `--no-update-check`,
`MYCLI_NO_UPDATE_CHECK=1`, or `MYCLI_UPDATE_CHECK_TTL` when release checks should
be suppressed or tuned separately from workspace refreshes.

## 6. Authentication contract

Private manifests and mounts live in private Git repos. `my` does not invent
a credential mechanism: for GitHub HTTPS URLs it checks `gh auth status` before
a real fetch and, if unauthenticated, fails with the exact remediation
(`gh auth login`). SSH URLs fall through to the user's normal Git/SSH auth.
Dry-run paths never touch the network or the auth check.

## 7. Error contract

Non-trivial failures return a structured value (and JSON under `--json`):

```json
{ "error": "unknown_product",
  "message": "no catalog product \"x\" in manifest \"acme\"",
  "remediation": "my products list --manifest acme" }
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
place with `my fleet set` (which preserves untouched frontmatter and body).
State history is the record's git history rather than event files, so each
meaningful transition should publish with a descriptive
`my sync --push --message`.
The `identifiers` frontmatter list is the join currency with support records:
`my fleet get` resolves any identifier and surfaces related incidents, and
`my support add --identifier` warns when an identifier is unknown to the
registry. Both nouns share the `internal/record` engine.
