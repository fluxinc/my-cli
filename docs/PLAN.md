# our Implementation Plan

Status: public-safe working plan for the published CLI repository,
refreshed 2026-06-10 (v0.13.0). Long-form designs live in `docs/plans/`
(see the index there); this file is the short orientation.

## Repository Shape

`our` is the generic mechanism. It stays public and contains the Go CLI,
tests, neutral fixtures, and public architecture docs. It must not contain
organization skills, private manifests, customer notes, internal catalogs, or
company-specific operating content.

Each organization has two private repositories with distinct write
audiences:

- a **manifest repo** (control plane): `manifest.json`, catalog, skills,
  agent guidance. Checked out at the registry path, outside the umbrella;
  edited via `our admin` commands; admin-writable.
- a **workspace repo** (data plane): meetings, support, fleet, decisions,
  projects, policy, people. Mounted visibly in the umbrella at
  `<umbrella>/workspace`; org-writable.

`our init` creates both locally; `our publish` creates the private remotes,
rewrites mount URLs, and pushes. The umbrella (`~/<org>`) is not itself a
Git repository; it is the per-user operating envelope containing `.our/`,
mounts, product clones under `repos/`, and local scratch under `personal/`.

## Current Baseline (v0.13.0)

- Go standard library only; external tools limited to git, gh, and
  manifest-declared optional tools.
- Two-repo init (`our init`), one-command publish (`our publish`),
  `local-only` reporting for unpublished checkouts, and sync/doctor guards
  against manifests that still reference local mount paths.
- Manifest registry, sync, validation, GitHub auth checks; admin vs
  operational command split.
- Manifest-declared static and tool-provided skills; bundled `our`
  self-skill kept current by the binary and installer.
- Umbrella creation and guidance generation; sparse scoped mounts via
  `include_paths`.
- Catalog: products (opt-in clones under `repos/<id>`) and customers.
- Markdown-first content commands: meetings, support, fleet (registry with
  in-place updates), with qmd-first search when available.
- Sync: bidirectional reconcile with auto-publish policy for private
  content, Gnit backend when the umbrella is a Gnit control workspace,
  `.our/last-sync.json` audit, `our doctor [--fix]`.
- Self-update (`our update`) from GitHub releases with checksum
  verification.

## Active Direction

The execution plane (see `docs/plans/2026-06-10-execution-plane.md`,
operator-approved combined path):

1. **v0.13.1** — adoption-gated publishing: `our sync` stops auto-publishing
   untracked content files; records created by the CLI are adopted via Git
   intent-to-add, `our record adopt` (or an explicit `git add`) adopts the
   rest; held files are named with remediation.
2. **v0.14** — sessions: visible `work/<id>` per-session git worktrees of
   writable mounts, `our work start|status|finish`, `our ai` defaulting into
   a fresh session, a first-class session registry consulted by sync.
   Harness-agnostic by principle: no integration with any harness's internal
   isolation mechanisms.
3. **v0.15** — manifest `roles` + `services` (org APIs, MCP servers as
   `kind: mcp`, gated brokers; reference-first descriptions; URI secret
   references such as `op://`); harness MCP config materialization; org-side
   launch-artifact compilation for contained runners (container tooling
   formats are compile targets, not vocabulary sources).

## Non-Goals

- No org-specific content, skills, or sample data in the public repo.
- No daemon; no runtime/container engine inside `our` (containment belongs
  to external tooling that `our` compiles artifacts for).
- No silent installation of external tools; manifests provide install hints
  and optional tool skill installers.
- No dependence on harness-internal mechanisms (hooks, lifecycle APIs);
  `our` governs at the filesystem/process boundary every harness shares.
