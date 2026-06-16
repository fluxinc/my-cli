# my Implementation Plan

Status: public-safe working plan for the published CLI repository,
refreshed 2026-06-12 (v0.21.0). Long-form designs live in `docs/plans/`
(see the index there); this file is the short orientation.

## Repository Shape

`my` is the generic mechanism. It stays public and contains the Go CLI,
tests, neutral fixtures, and public architecture docs. It must not contain
organization skills, private manifests, customer notes, internal catalogs, or
company-specific operating content.

Each organization has two private repositories with distinct write
audiences:

- a **manifest repo** (control plane): `manifest.json`, product/repo catalog,
  skills, agent guidance. Checked out at the registry path, outside the
  umbrella; edited via `my admin` commands; admin-writable.
- a **workspace repo** (data plane): customers, meetings, support, fleet,
  decisions, projects, policy, people. Mounted visibly in the umbrella at
  `<umbrella>/workspace`; org-writable.

`my init` creates both locally; `my publish` creates the private remotes,
rewrites mount URLs, and pushes. The umbrella (`~/<org>`) is not itself a
Git repository; it is the per-user operating envelope containing `.my-cli/`,
mounts, catalog repo clones under `repos/`, work sessions under `work/`, and
local scratch under `personal/`.

## Current Baseline (v0.21.0)

- Go standard library only; external tools limited to git, gh, and
  manifest-declared optional tools.
- Two-repo init (`my init`), one-command publish (`my publish`),
  `local-only` reporting for unpublished checkouts, and sync/doctor guards
  against manifests that still reference local mount paths.
- Manifest registry, sync, validation, GitHub auth checks; admin vs
  operational command split.
- Manifest-declared static and tool-provided skills; bundled `my`
  self-skill kept current by the binary and installer.
- Umbrella creation and guidance generation; sparse scoped mounts via
  `include_paths`.
- Catalog: products as business entities that link implementing repos;
  repos opted in on demand (`my repos add`) and cloned under `repos/<id>`.
  Customer identities are mounted markdown records read by `my customers`.
- Markdown-first content commands: meetings, support, fleet (registry with
  in-place updates), with qmd-first search when available; records created
  by the CLI are adopted via Git intent-to-add, the rest via
  `my record adopt`.
- Work sessions, opt-in: `work/<id>` git worktrees per writable mount
  (`my work start|status|list|resume|finish`, `my ai
  --new-session|--session <id>`); content commands and plain `my ai` are
  session-aware when run inside one; `my doctor` reports session health;
  `my sync` holds mounts with dirty or unlanded active sessions.
- Manifest `data_bindings`: stable operational data nouns (`customers`,
  `meetings`, `support`, `fleet`) mapped to declared `mount:<id>` or
  `service:<id>` surfaces.
- Manifest `services` and `roles`: remote surfaces with reference-only auth
  (`op://`, `env://`, `broker://`, `none`) and server.json-shaped local
  connection data; `my services`/`my roles` inspection; `my setup --role`
  selection persisted in umbrella state; roles are loadouts, not authority;
  umbrella-root `.mcp.json` materialized from local connection data only;
  doctor service checks.
- Sync: bidirectional reconcile with auto-publish policy for adopted private
  content, Gnit backend when the umbrella is a Gnit control workspace,
  `.my-cli/last-sync.json` audit, `my doctor [--fix]`.
- Self-update (`my update`) from GitHub releases with checksum
  verification; TTL-gated startup refresh and stderr-only update notices.
- Contract rules: a built-in Fleet Work Contract in baseline guidance and the
  bundled self-skill, a support-record next-step hint in `my fleet get`
  output, and a manifest `contract` string list rendered as an
  `## Organization Contract` section in generated `AGENTS.md`; inspected with
  `my contract list` and edited with `my admin contract add|remove` through
  the normal admin review-commit-push flow.

## Active Direction

The execution plane (see `docs/plans/2026-06-10-execution-plane.md`,
operator-approved combined path):

1. **Shipped in v0.13.1** — adoption-gated publishing: `my sync` stops auto-publishing
   untracked content files; records created by the CLI are adopted via Git
   intent-to-add, `my record adopt` (or an explicit `git add`) adopts the
   rest; held files are named with remediation.
2. **Shipped in v0.14.0–v0.17.0** — sessions: visible `work/<id>` per-session
   git worktrees of writable mounts, `my work start|status|list|resume|finish`,
   a first-class session registry consulted by sync and doctor.
   Harness-agnostic by principle: no integration with any harness's internal
   isolation mechanisms. The launch default was revised after dogfood
   (v0.17.0): `my ai` launches from the base umbrella, sessions are opt-in
   via `--new-session`/`--session <id>`, and content commands resolve to the
   session's mount worktrees when run inside one.
3. **Shipped in v0.18.0** — manifest `roles` + `services` (org APIs, MCP
   servers as `kind: mcp`; reference-first descriptions; URI secret
   references such as `op://`), `my services`/`my roles` inspection,
   `my setup --role` with role-filtered guidance and service visibility,
   umbrella-root `.mcp.json` materialized only from checked-in or inline
   connection data, and doctor service-health checks (see
   `docs/plans/2026-06-12-v018-scope.md`).
4. **Next** — org-side launch-artifact compilation for contained runners
   (container tooling formats are compile targets, not vocabulary sources);
   the manifest `contract` list maps to the artifact's enforce-level
   contract block rather than a parallel dialect; descriptor fetch/cache as
   derived local state.

## Non-Goals

- No org-specific content, skills, or sample data in the public repo.
- No daemon; no runtime/container engine inside `my` (containment belongs
  to external tooling that `my` compiles artifacts for).
- No silent installation of external tools; manifests provide install hints
  and optional tool skill installers.
- No dependence on harness-internal mechanisms (hooks, lifecycle APIs);
  `my` governs at the filesystem/process boundary every harness shares.
