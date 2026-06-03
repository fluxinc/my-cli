# Admin and Operational Command Split

Status: planning draft. No implementation should start until this boundary is
reviewed.

## Problem

The current CLI mixes two different jobs:

- Agent-facing operational use: read, inspect, diagnose, launch, and retrieve
  information from the current workspace.
- Administration: create, change, remove, or reconcile the workspace contract,
  its defaults, source-of-truth manifest content, generated guidance, local
  mounts, or content files.

This matters most for skill management. `flux skills uninstall` currently
removes harness materializations, but it does not remove one skill from the
manifest or from the workspace defaults. Adding manifest-writing behavior to
that operational-looking command would make the boundary worse.

## Boundary

Operational commands answer "what can this agent use right now?" They should be
read-only or execute the requested harness. Examples:

- `flux skills list`
- `flux skills show <skill>`
- `flux skills status`
- `flux meetings list`
- `flux meetings search <text>`
- `flux meetings get <id|path>`
- `flux customers list`
- `flux catalog list products`
- `flux root`
- `flux launch` without `--onboard`
- `flux doctor`

Admin commands answer "how should this workspace or shared source of truth be
configured?" They may write to a manifest checkout, workspace defaults,
generated guidance, mounts, or content files. They should not be used as a
catch-all for local per-user materialization. Examples:

- `flux admin skills add`
- `flux admin skills remove`
- future `flux admin skills defaults`
- `flux admin manifest add`
- `flux admin manifest sync`
- `flux admin manifest validate`
- `flux admin onboard`
- `flux admin meetings add`
- `flux admin mount add`
- `flux admin mount remove`
- `flux admin mount sync`

Existing top-level mutating commands that are reclassified as admin should
remain as compatibility aliases for now, but help text and docs should teach
the `flux admin ...` form. Local harness materialization commands remain
top-level under `flux skills ...`.

## Write Target Decision

Manifest authoring commands must not write to the synced manifest cache by
default. The cache under the user data directory is a disposable clone refreshed
by `git pull --ff-only`; local edits there would create fragile pull conflicts.

Manifest authoring should instead require one of:

- `--manifest-dir PATH`, an explicit maintainer checkout, or
- a future registry field such as `dev_path` that points to a maintainer
  checkout.

Admin authoring commands should refuse to run against a dirty maintainer
checkout unless `--force` is supplied for the specific operation. They should
never auto-commit or auto-push. They should print the relevant `git diff`,
`git status`, or next git commands as operator handoff, not perform irreversible
publication.

## Skill Surfaces

This section defines three things -- the read-only operational surface, local
harness materialization, and admin manifest authoring -- plus the skill-origin
model that governs authoring. Operational and materialization commands live
under `flux skills ...`; authoring lives under `flux admin skills ...`.

### Skill Origins

Before implementing admin skill authoring, research and test how each supported
harness discovers skills. The origin model must prevent duplicate exposure and
non-portable manifest entries.

Observed source categories:

- Manifest-owned static skill: source of truth is a directory inside the
  maintainer manifest checkout.
- Tool-provided skill: source of truth is an external tool; `flux` asks the
  tool to materialize the skill locally.
- Project-local skill: source of truth is a project repository already
  configured for one or more harnesses, such as `.claude/skills`,
  `.agents/skills`, or `.opencode/skill`.
- User-local skill: source of truth is a personal harness location, such as a
  home directory skill.
- Bundled or plugin skill: source of truth is the harness or plugin; `flux`
  should observe it but not mutate it.

Admin add/remove must support these source modes deliberately:

- Copy/import: copy an existing skill folder into the manifest checkout and
  declare it there. This is the safest default because the manifest becomes the
  portable source of truth.
- Move/import: copy into the manifest checkout, validate, then delete the
  original folder only when an explicit flag such as `--remove-original` is
  supplied. This avoids duplicate exposure when a project-local skill becomes
  an organization default.
- Link/reference: do not implement as an absolute filesystem symlink in the
  manifest. A manifest entry that points outside the maintainer checkout is not
  portable and will break for other users. If repo-local skills should be
  referenced without import, design a manifest source type that references a
  declared mount or product plus a relative path. Defer this until the product
  and mount semantics are clear.

Admin commands should scan the likely harness-visible source locations before
importing. If the same skill name already exists in the origin project and will
also be exposed by the manifest install, the command should warn or require
`--remove-original` / `--keep-original` so the operator makes the duplication
choice explicitly. In non-interactive CLI mode, prefer explicit flags over
prompts.

Current research points:

- Claude Code has personal, project, enterprise, and plugin skills. Project
  skills are discovered from `.claude/skills/` in the starting directory and
  parent directories, and edits under already-watched skill directories can be
  picked up live.
- Codex reads repository, user, admin, and system skills from `.agents/skills`,
  `$HOME/.agents/skills`, `/etc/codex/skills`, and bundled system locations.
  Codex follows symlinked skill folders, and duplicate names are not merged.
- OpenCode reads project and global `.opencode/skill` locations plus
  Claude-compatible `.claude/skills` locations. It requires the `name`
  frontmatter to match the directory name.
- Gemini CLI has a first-class `gemini skills` surface in the installed CLI:
  `list`, `enable`, `disable`, `install`, `link`, and `uninstall`. `install`
  accepts a local path or git URL with `--scope`, `--path`, and `--consent`;
  `link` accepts a local path with `--scope` and `--consent`; `uninstall`
  accepts a skill name and `--scope`. `gemini skills list` reports enabled
  state, source location, and duplicate conflicts.

Research anchors:

- Claude Code skills: https://code.claude.com/docs/en/skills
- Codex agent skills: https://developers.openai.com/codex/skills
- OpenCode skills: https://opencode.ubitools.com/skills/
- Gemini CLI configuration: https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md
- Gemini CLI skill lifecycle issue: https://github.com/google-gemini/gemini-cli/issues/16365

Local evidence gathered on 2026-06-02:

- A private maintainer checkout and its synced cache agreed that static
  manifest skills use `skills/<install_slug>` paths. A tool-sourced skill had
  an empty path and `source.type: tool`.
- The maintainer checkout was dirty during research, which reinforces the
  dirty-tree refusal requirement for admin authoring.
- Gemini currently has user-scope skills under its user skills directory, and
  `gemini skills list` reported a real duplicate conflict where an agent-skill
  location overrides a Gemini user-skill location. Admin import commands should
  therefore detect and surface duplicate exposure instead of creating it
  silently.

### Operational

`flux skills list` remains read-only. It should list manifest-declared skills
with canonical id, install slug, description, source type, and provenance.

`flux skills show <id|slug>` should show one skill's metadata and source path.
It should not edit installs or manifests.

`flux skills status [--manifest NAME] [--json]` should show the declared-vs-
installed state per harness: present, absent, symlink/copy, tool-managed,
blocked, stale, and provenance where available. "stale" applies only to
copy-mode installs whose content has diverged from the current source, or to an
install whose declared source path changed; symlink installs track live source
and are never stale. status is the linchpin where operational hands off to
admin, so each non-ok row must carry a remedy naming the exact command that
fixes it (for example `flux skills install <slug>` for absent, `flux skills
sync` for stale, `flux admin skills add ...` for an undeclared local skill), and
that remedy must appear in `--json` too, not only the human output. This is the
command an agent should run before deciding whether it needs admin help.

### Operational Harness Materialization

These commands manage local harness skill directories. They operate on local
derived state, not source-of-truth manifest content. After Claude review, this
category stays operational rather than moving under `flux admin`:

- `flux skills install [--skill ID_OR_SLUG] [harness...] | --all`
- `flux skills uninstall [--skill ID_OR_SLUG] [harness...] | --all`
- `flux skills sync [harness...] | --all`
- `flux skills purge [harness...] | --all`

`install` and `uninstall` keep the current behavior but gain a repeatable
`--skill` filter so a user can target one skill without uninstalling every
declared skill.

`sync` should reconcile local harness installs to the selected manifest/source:
install or update declared skills and prune stale Flux-managed skills that are
no longer declared. Pruning is on by default but only ever removes Flux-managed
targets; it must print exactly what it removes and offer `--no-prune` to skip
removal. It must support `--print`, `--json`, and `--force`, and it must never
remove non-Flux-managed targets without `--force`. Because Gemini skills are
managed through the gemini CLI rather than a filesystem target, sync and purge
must call Gemini's own lifecycle commands instead of filesystem deletion.

`purge` should remove Flux-managed skill materializations from selected
harnesses. It should be narrower and more explicit than `sync`: remove what
Flux installed, optionally filtered by `--skill`, and leave source-of-truth
manifest content untouched.

The existing `flux skills install/uninstall/sync/purge` commands can remain as
top-level operational commands because they affect one user's harness installs.

### Admin Manifest Authoring

These commands edit a maintainer checkout:

- `flux admin skills add <skill-dir> --id namespace:name --manifest-dir PATH`
- `flux admin skills remove <id|slug> --manifest-dir PATH`

`add` imports an existing skill directory containing `SKILL.md`. It writes or
updates a manifest `skills[]` entry and, when needed, copies the source under
`skills/<install_slug>` inside the maintainer checkout. That path convention is
confirmed by the current private manifest. Scaffolding a brand-new
skill is a separate future command, not part of add.

`add` should distinguish `--copy` from `--move`/`--remove-original`. The default
should be copy/import with a warning when the original will remain visible to a
supported harness and produce a duplicate. Removing the original is destructive
and must require an explicit flag.

`remove` removes the manifest declaration. `--delete-source` may delete the
static source directory, but only when the path stays inside the maintainer
checkout. Product catalog `related_skills` references must block removal unless
`--prune-related` is supplied.

Out of scope for v1 authoring (named so the gaps are deliberate, not silent):
`add` handles static directory import only; declaring a tool-sourced skill
(`source.type: tool`, which has no directory to import) stays hand-edited or a
later flag. Renaming or moving a declared skill (changing id, install_slug, or
path) is not `add`/`remove`; it is a future `flux admin skills rename` or an
explicit remove-then-add.

Authoring safety rules:

- Refuse duplicate ids or install slugs unless the command is explicitly an
  update or `--force` is supplied.
- Validate that the id namespace is the organization id or an allowed external
  namespace.
- Validate `install_slug` and warn when `SKILL.md name:` does not match it.
- Reject manifest-owned symlinks that point outside the maintainer checkout
  unless a future explicit external-reference source type is designed.
- Before writing, verify the `--manifest-dir` checkout actually is the named
  manifest: its `organization.id` (and ideally git remote) must match the
  registry ref, so authoring cannot land in the wrong repo.
- If the id namespace is not the organization id or a declared
  `allowed_external_namespaces` entry, refuse with an instruction; do not
  silently widen the org's allowed namespaces -- that is a deliberate policy
  edit, not a side effect of adding a skill.
- Validate after the planned edit and refuse to finish if the manifest or
  catalog would become invalid; validate the whole manifest and catalog, not
  just the new entry, since add can collide on id and remove can orphan
  `related_skills`.
- Use atomic file writes, and keep cross-file consistency: add/remove can touch
  both `manifest.json` and `catalog/products.json`. Per-file atomicity is not
  enough -- snapshot both, write both, validate, and restore both on any
  failure so the checkout is never left half-edited.
- Support `--print` dry-run and `--json`.
- Do not auto-install into harnesses by default. Print the follow-up command:
  `flux skills sync --manifest NAME`.

## Meetings and Other Content

The same boundary applies outside skills.

Read-only meeting commands are operational:

- `flux meetings list`
- `flux meetings search`
- `flux meetings get`

Creating meeting files or folders is administration:

- `flux admin meetings add`
- future `flux admin meetings init` if folder scaffolding becomes needed.

The current `flux meetings add` should remain as a compatibility alias during
the transition, but docs should teach the admin form.

Mount and manifest commands should receive the same treatment:

- Listing and diagnosing are operational.
- Adding, removing, syncing, onboarding, and changing defaults are admin.

## Workspace Defaults

"Defaults" need a precise model before implementation. Initial defaults likely
include:

- manifest-declared skills available to every agent,
- required/default mounts,
- generated agent guidance fragments,
- configured umbrella root,
- future default product opt-ins, if the product workflow needs them.

Do not add a broad `flux admin defaults` command until the data model is clear.
For now, keep defaults encoded in the manifest fields that already exist and
make admin commands modify those specific fields.

## Implementation Phases

1. Documentation and command taxonomy.
   Update README, architecture docs, and CLI help to explain operational,
   local-materialization, and admin/shared-configuration boundaries. Add
   `flux admin` only for shared/workspace configuration writers.

2. Operational skill inspection.
   Add `flux skills show` and `flux skills status` so agents can understand
   skill availability without mutating anything. Implemented in the first
   pass with JSON output and absent-skill install remedies.

3. Operational harness materialization.
   Add `--skill` filters to operational install/uninstall and implement the
   existing `sync` and `purge` stubs under top-level `flux skills`. The
   install/uninstall filters, `sync`, and `purge` are implemented. `sync`
   installs/updates declared skills and prunes stale Flux-managed filesystem
   materializations by default unless `--no-prune` is supplied. `purge` removes
   Flux-managed materializations, including stale filesystem entries selected by
   slug or canonical id.

4. Harness-origin research.
   Verify Claude Code, Codex, OpenCode, and Gemini discovery, symlink, duplicate
   name, reload, and remove behavior with official docs and local smoke tests.
   The first research pass confirmed duplicate exposure is real, static
   manifest paths use `skills/<install_slug>`, and Gemini has a usable skills
   lifecycle surface. Convert those findings into test fixtures before adding
   manifest authoring.

5. Admin manifest authoring.
   Add explicit `--manifest-dir` skill add/remove. Do not write to the synced
   cache. Support copy/import first, require explicit flags for move/delete, and
   emit git/operator follow-up commands. Implemented for static directory
   import and declaration removal: add copies into `skills/<install_slug>`,
   remove blocks product `related_skills` references unless `--prune-related`
   is supplied, and both commands refuse dirty git checkouts unless `--force`
   is supplied. Imports from harness-visible skill directories require an
   explicit `--keep-original` or `--remove-original` choice. The commands do not
   commit or push, and they print `git status` / `git diff` follow-up commands.

6. Broader admin migration (separate follow-up plan, does not gate skills).
   Move `meetings add`, mount mutators, manifest mutators, and onboard help
   into the `flux admin` taxonomy while retaining compatibility aliases. This is
   a CLI-wide change and should get its own plan doc and review; phases 1-5
   deliver ergonomic skill management without waiting on it.

## Verification Plan

- Unit tests for command parsing and alias routing.
- Unit tests for skill selection by canonical id and install slug.
- Unit tests for status output across absent, symlink, copy, tool-managed, and
  stale Flux-managed installs. Initial coverage now checks stale copy detection
  and a concrete `flux skills sync ... --skill ...` remedy.
- Unit tests for sync/purge provenance protection. Initial coverage now checks
  stale Flux-managed pruning, `--no-prune`, and stale canonical-id purge.
- Manifest-authoring tests using a temporary maintainer checkout, including
  duplicate ids, catalog related-skill pruning, source deletion guardrails, and
  dirty-tree refusal. Initial coverage now checks static import, related-skill
  blocking/pruning, source deletion, dirty-checkout refusal, and explicit
  keep/remove choice for harness-visible source skills.
- Harness-origin tests for duplicate detection, local source import, move after
  validation, and refusal to write non-portable external symlinks.
- E2E test for the neutral Acme workspace covering operational read commands
  and admin reconciliation.
- Full `go test ./...` and `go vet ./...`.

Final implementation verification passed with focused package tests, full
`go test ./...`, `go vet ./...`, `git diff --check`, and help smoke checks for
`flux`, `flux skills`, and `flux admin`.

## Resolved Decisions

Settled during the Claude/Codex review (2026-06-02):

- `flux skills sync` prunes stale Flux-managed installs by default, prints what
  it removes, and offers `--no-prune`; it never touches non-Flux-managed
  targets without `--force`, and Gemini pruning/removal must use the Gemini CLI
  lifecycle commands rather than filesystem deletion.
- Use explicit `--manifest-dir` for v1; add a registry `dev_path` only if
  repeated use proves the need. Verify the dir is the named manifest checkout
  before writing.
- Project-local skills are import-only for v1; the mount/product-relative
  external source type is deferred until product and mount semantics settle.
- The destructive flag is `--remove-original` (copy-then-delete), not `--move`,
  which would falsely imply an atomic filesystem move.
- `flux launch` stays operational (it execs a harness); `--onboard` is
  documented as performing an admin mutation, with `flux admin onboard` as the
  canonical mutating form and `launch --onboard` as the convenience.
- Alias transition notes are a phase-6 concern (the CLI-wide admin migration of
  meetings/mount/manifest); they are out of scope for skills-first delivery,
  where install/uninstall/sync/purge stay genuinely operational rather than
  aliases.

## Remaining Open Questions

- Skills-first v1 (phases 1-5) is complete and reviewed; one cross-file write
  ordering fix was applied to `flux admin skills remove --prune-related` so a
  failure between the catalog and manifest writes leaves a valid checkout.
- Phase 6 admin routing is now implemented: `flux admin onboard` delegates to
  `flux onboard`, while `flux admin manifest|mount|meetings` are gated to the
  mutating/configuration subcommands documented for admin. Read commands such
  as `manifest list`, `mount list`, and `meetings list/search/get` stay
  top-level and return a pointer back to the operational command if invoked
  through `admin`.
- Top-level mutating forms remain quiet compatibility aliases in v1. They do
  not print transition notes yet, so existing scripts do not receive new stderr
  output; CLI help and docs teach the admin form as the canonical path.
- Broader user docs beyond README/help remain the main phase-6 follow-up.
