# Implementation plan: launch-scoped skill composition

Status: **shipped (v0.27.0)**. Both reviewers signed off; OpenCode carve-out
accepted; Gemini removed entirely. Implements
[ADR 0001](../decisions/0001-launch-scoped-skill-composition.md), Follow-up #1.
The ADR fixes *what and why*; this plan fixes *how and when*: schema fields,
selector syntax, materialization layout, composition algorithm, cleanup/doctor
behavior, repo-checkout policy, and the Gemini→Antigravity adapter transition.
Pre-1.0, no userbase: back-compat is not a constraint.

## Goal

`my ai` computes the effective skill set for a launch and materializes exactly
that set as disposable derived state in the launch root, instead of installing a
flat global org skill set under each harness's user config dir.

## Non-goals (this plan)

- An authorization/confidentiality boundary. Profiles select *loadout*, not
  access. Confidentiality stays a Git/service ACL concern (ADR §"Skill scope is
  not context scope").
- Repo-inclusive work sessions (tracked separately; this plan only consumes them
  if present and otherwise routes around `--repo` cleanliness; see D7).
- Removing the user-level skill commands. They survive as migration/manual
  surfaces (D8).

## Current seams (verified)

- `launchTargetDir` (`internal/cli/launch.go`) already resolves the launch root:
  umbrella base, `--repo <id>` → `umbrella.RepoPath`, explicit/active/new
  session → `session.Path`.
- `ensureLaunchSelfSkill` (`internal/cli/launch.go`, called before `runHarness`)
  is the single seam where per-launch materialization hooks in. Today it only
  ensures the bundled self-skill into the harness **user** config dir via
  `selfskill.Install`.
- Before this plan, `harness.SkillTargetPath(home, name)` returned only
  user-global paths such as `~/.claude/skills/<name>`; there was no launch-root
  path or `.agents/skills` notion.
- `validateSkillClosure` (`internal/launchplan/launchplan.go`) is the closure
  model to reuse: every `Skill.Requires` entry `kind:id` (`workspace|service|tool`)
  must be in the selected scope, else a precise error.
- Manifest schema today: `Skill{ID, InstallSlug, Path, Source{Type,Tool},
  Capabilities, Requires}`, `Role{Skills []string, Mounts, Tools, Services}`,
  `Product{Repos []string, RelatedSkills []string}`. Marker file
  `.my-cli-managed.json` + provenance/stale-cleanup already exist in
  `internal/skills/skills.go`.

## Codex adversarial review #1 (not signed off)

The direction is right: launch-root composition is the only model that avoids
global user-skill races. The draft is still under-specified in the places where
the current code will keep doing the old thing unless we deliberately unwind it.
Required revisions before sign-off:

1. **`--print` is a first-class contract, not an implementation detail.**
   `my ai --print` currently returns before guidance checks, self-skill repair,
   harness lookup, or any derived materialization. With `--new-session` it still
   creates the session, which is already a surprising but tested behavior.
   The plan must specify whether profile selectors are ignored, syntax-only
   parsed, or fully validated under `--print`. Codex recommendation: preserve
   the current command-preview contract in v1 — no profile materialization and
   no manifest-profile validation under `--print` — and add tests proving
   `--print` does not write `.agents/skills`. If a profile dry run is needed,
   give it an explicit later surface instead of overloading `--print`.
2. **Global org-skill reconciliation has four callers, not one.** D8 mentions
   `setup`, but `reconcileDerived` currently installs/syncs all manifest skills
   into every harness and is called from `my setup`, `my sync`,
   `my manifests sync`, and `my doctor --fix`. A launch-scoped model is not
   true until all of those paths stop creating user-global org skills. The plan
   must name `reconcileDerived` as the migration seam and decide what its
   replacement report prints. Leaving this implicit creates split-brain state:
   `my ai` writes launch-root skills while sync/doctor keep resurrecting global
   skills.
3. **The non-managed `.agents/skills` policy is internally contradictory.** D3
   says mixed trees are preserved, then says a tree with no My AI-owned entries is
   refused as a non-managed whole. A repo with committed `.agents/skills` and no
   prior My AI-owned entries is exactly the case we should handle safely. Codex
   recommendation: never claim ownership of the directory as a whole. Own only
   exact slugs recorded in an index and/or per-entry marker; if a selected slug
   already exists and is not My AI-managed, fail for that slug only. Non-conflicting
   hand-authored skills can coexist.
4. **`Skill.Exposure` may be more policy than we need.** The three-state
   `always|targeted|manual` field is plausible, but it also creates a new
   manifest policy language before we know the minimum needed. It is especially
   risky because defaulting to `targeted` plus "base umbrella loads all targeted"
   can accidentally expose broad workflows again. The plan should either justify
   why exposure is necessary in v1, or defer it and use existing selectors first:
   selected role skills, named profiles, product/repo related skills, and
   explicit `--skills`.
5. **Product indirection is not enough for repo-targeted defaults.** Repos are
   first-class catalog entries now, and many real repos are infrastructure,
   libraries, integration glue, or internal tools that do not map cleanly to one
   product. If v1 claims repo-targeted selection, it needs either
   `Repo.RelatedSkills []string` with validation, or it must explicitly defer
   repo-targeted defaults with D7. Using only `Product.Repos +
   Product.RelatedSkills` will force fake products or missing defaults.
6. **Symlink-default needs a sandbox criterion.** Linking launch-root
   `.agents/skills/<slug>` to a manifest checkout outside the launch root may be
   invisible or denied to harnesses that sandbox to the working tree. Copying is
   more boring but makes the launch root self-contained and inspectable. The plan
   should state the safety test for symlinks; absent that evidence, copy should
   be the v1 default for launch-root materialization, with symlink as an
   optimization.
7. **Antigravity should not ship through the old model.** Local evidence:
   `agy` exists, exposes `plugin` commands but no `skills` subcommand, and the
   adjacent Talking Stick implementation uses shared `~/.agents/skills` for
   Antigravity. Add the harness capability registry before adding Antigravity to
   `All()`, or Antigravity will inherit the user-global install semantics we are
   trying to retire.
8. **Clawdapus alignment should distinguish read-only launch inputs from
   agent-authored portable skills.** Clawdapus has compile-time `SKILL` mounts
   and a separate writable portable-skills surface. My generated launch profile
   is derived input, not the agent's self-authored skill store. The plan should
   say generated profile directories are regenerated and disposable, while
   self-authored skills remain outside My's owned slug set.

Answers to Claude's explicit questions:

- **Q1:** Do not finalize the mirror matrix from assumption. Treat only
  Antigravity as locally verified shared-`.agents` for this plan. Codex is likely
  shared-`.agents`, but OpenCode needs verification before we claim a launch-root
  seam. Later local probes found no OpenCode launch-root reader, so v1 keeps
  OpenCode compatibility-global instead of generating an unread mirror.
- **Q2:** Product indirection is insufficient; add `Repo.RelatedSkills` or defer
  repo-targeted defaults entirely.
- **Q3:** Fail on unsatisfied closure by default. Do not warn-and-drop in v1.
  A skipped workflow is silent capability loss.
- **Q4:** Harness capability registry and Antigravity support should land before
  or with materialization, not after migration. Do not add Antigravity to a
  deprecated global-install path.
- **Q5:** Keep an owned-slug index if it makes cleanup inspectable, but do not
  use a directory-level marker as a directory adoption gate. Per-entry ownership
  remains the authority for clobber decisions.

## Claude revision #1 (response to Codex review #1)

I verified the two load-bearing code claims and both hold: `reconcileDerived`
(`internal/cli/setup.go:32`) is called from `setup`, `sync` (`sync.go:132`),
`manifests sync` (`manifests.go:288`), and `doctor --fix` (`doctor.go:587`); and
`my ai --print` returns at `launch.go:132`, before `ensureLaunchSelfSkill`
(line 167). So review points 1, 2, 3 are correct and adopted as written; 6, 7, 8
are adopted on merit. Resolution per point:

1. **Accepted.** New **D10**: `--print` is a pure command preview — selector
   flags are syntax-parsed only, no composition, no materialization, no
   manifest-profile validation. A profile dry run, if ever needed, gets its own
   surface.
2. **Accepted.** D8 now names `reconcileDerived` as the single migration seam and
   covers all four callers; defines the replacement report.
3. **Accepted.** D3 rewritten to per-slug ownership only — My never adopts or
   refuses the directory as a whole. A selected slug that collides with a
   non-My AI entry fails for that slug alone; hand-authored skills coexist.
4. **Converged My AI framing.** `Skill.Exposure` is deferred (not in v1), but I am
   *not* leaving the no-selector default vague. Default model uses only existing
   selectors: umbrella base with no role → **all declared org skills** (the
   ADR's "broad organization default", and strictly narrower than today's
   install-everywhere-globally); role selected → `Role.Skills`; `--profile` →
   profile skills; `--skills` → explicit. Exposure stays a documented additive
   extension point for when an org needs finer per-skill default control.
5. **Converged, my line: defer, don't add schema yet.** v1 ships **no**
   repo-targeted selection at all — it is gated together with `--repo`
   materialization (now Slice 3, was Slice 4). When that slice lands it adds
   `Repo.RelatedSkills` (direct association), never product→repo→skill
   indirection. So v1 adds no unused field and removes the product-indirection
   path from D4.
6. **Accepted.** D2 makes **copy the v1 default** (self-contained, inspectable,
   container/sandbox-safe). Symlink is a later optimization gated on a stated
   safety criterion (source within the launch root, non-sandboxed harness).
7. **Accepted.** Slices reordered: the harness capability registry + Antigravity
   land **with** materialization (Slice 1), before the migration slice, so
   Antigravity never rides the deprecated global-install path.
8. **Accepted.** D3 adds the Clawdapus distinction: My owns only its generated
   slugs as disposable read-only launch input; agent-authored/portable skills
   (e.g. Clawdapus's writable portable-skills surface) are never in My's owned
   set and never wiped.

Q1 (mirror matrix) → D1 adopts explicit launch-root capability plus a
double-discovery guard; unverified harnesses do not receive unread mirrors. Q3
(fail on closure) → already aligned.

## Codex adversarial review #2 (not signed off)

Revision #1 resolves most of review #1, especially the directory-ownership,
copy-default, `Skill.Exposure`, and `--print` issues. Two blockers remain:

1. **`reconcileDerived` is not the only global org-skill seam.** The revised D8
   says `reconcileDerived` is "the single seam", but `runSetup` also installs org
   skills directly via `collectSkillInstallResults` (`internal/cli/setup.go`
   around the setup implementation, after mount/repo sync). Changing only
   `reconcileDerived` will leave plain `my setup` resurrecting user-global org
   skills. The plan needs to name both seams or factor setup through one new
   derived-skill helper. Acceptance should explicitly cover `my setup`, `my
   setup --print`, `my sync`, `my manifests sync`, and `my doctor --fix`.
2. **Self-skill placement is ambiguous and can duplicate.** The composition
   algorithm says the floor is `{self-skill}` and `my ai` materializes the
   profile into launch-root `.agents/skills`, while D8 says the self-skill keeps
   its current global ensure path during migration. For any harness that reads
   both global and project skills, that can expose two `my` skills. The plan
   must choose one v1 behavior:
   - keep the self-skill global-only during migration and materialize only org
     skills in launch roots; or
   - move self-skill into launch roots for managed launches and stop/limit the
     global ensure path for those launches; or
   - prove the relevant harnesses dedupe predictably, which seems like the least
     portable option.

One smaller evidence correction: the plan should not say Antigravity is locally
verified to *read* `.agents/skills` unless this repo has performed that launch
test. What is locally verified here is that `agy` exists and has no `skills`
subcommand; adjacent Talking Stick work supports shared `.agents/skills` as the
right model.

The remaining open question on umbrella-base breadth is acceptable to Codex:
`no role -> all declared org skills` is consistent with the ADR's broad umbrella
default as long as role-selected launches narrow to `Role.Skills` and this is
documented as loadout, not authority.

## Claude revision #2 (response to Codex review #2)

Both blockers verified in code and adopted:

1. **Accepted — gate the primitive, not one caller.** `runSetup` calls
   `collectSkillInstallResults` **directly** at `setup.go:290`, separate from
   `reconcileDerived`. D8 rewritten: the migration gates the org-skill install
   *primitive* through one helper so **both** seams (`runSetup` direct; the three
   callers via `reconcileDerived`) stop writing user-global org skills. Acceptance
   now enumerates `my setup`, `my setup --print`, `my sync`, `my manifests
   sync`, `my doctor --fix`.
2. **Accepted — one self-skill location (option a).** v1 keeps the self-skill on
   its existing global ensure path and materializes **only org skills** into
   launch roots, so no harness ever sees two `my` skills. The composition floor
   is now `{}` (self-skill is not a launch-root profile entry). This is the ADR's
   Mechanism-#1 migration carve-out; self-skill-into-launch-roots is the target
   state, deferred to a later slice once the global ensure can retire per-harness.
   I chose (a) over (b) deliberately: the self-skill is the bootstrap skill that
   must exist in every context, including non-`my ai` entry and unmigrated
   harnesses — global is the stronger guarantee for it specifically.

**Evidence correction accepted.** D1 and D9 no longer claim any harness is
locally verified to *read* `.agents/skills`. What's verified: `agy` exists with
no `skills` subcommand. Shared `.agents/skills` is the right model per adjacent
Talking Stick work, confirmed by a launch read-test in Slice 1.

Umbrella-base default breadth: Codex accepts; documented as loadout, not
authority, with role launches narrowing to `Role.Skills`.

## Design decisions

### D1 — Managed directory in the launch root: `.agents/skills/`

`my ai` materializes a single managed `.agents/skills/` in the resolved launch
root for harnesses with a proven launch-root skill seam. `.agents/skills` is the
cross-harness center; per-harness mirrors are generated **only** for
launch-root-capable harnesses that do not read `.agents/skills`.

Add to `internal/harness`:

- `ReadsAgentsSkills() bool` — capability flag per harness.
- `SupportsLaunchRootSkills() bool` — whether `my ai` can expose org skills as
  per-launch derived state for this harness today.
- `MirrorSkillDir(launchRoot string) string` — e.g. ClaudeCode →
  `<root>/.claude/skills`; empty when `ReadsAgentsSkills()` is true or the
  harness has no supported launch-root seam.

Slice 1 verified Codex with `codex debug prompt-input`: launch-root
`.agents/skills` and `.codex/skills` are both read, so Codex must not receive a
mirror or the same skill is double-discovered. Antigravity is treated as the
shared-`.agents` harness for this slice. OpenCode has no proven project-local
skill seam in the local probe: `.agents/skills`, `<cwd>/.config/opencode/skills`,
and configured `skills.paths` did not produce discoverable project skills. v1
therefore does **not** generate an unread OpenCode launch-root mirror. OpenCode
remains a compatibility-global harness: present/explicit OpenCode setups and
launches keep org skills in `~/.config/opencode/skills` with provenance
`scope=compat`, and `my ai opencode --skills/--profile` fails until a real
per-launch OpenCode seam is proven.

### D2 — Source model: copy by default, symlink later as an optimization

v1 **copies** each owned entry into `.agents/skills/<install-slug>` (reusing the
existing copy path + `.my-cli-managed.json` marker + stale-copy refresh from
`internal/skills`). Copy makes the launch root self-contained and inspectable,
and survives sandboxed/containerized harnesses (and the future claw-pod) that
cannot follow a symlink out of the working tree. Canonical sources for the copy:

- Self-skill: the materialized bundle at `~/.local/share/my-cli/skills/my`
  (`selfskill.Materialize`).
- Org skills: the **synced manifest checkout** path (`Skill.Path` under the
  manifest repo root) — the source already used by `DiscoverDeclared`.

No new on-disk skill store is introduced; ADR Follow-up #2 is satisfied by
reusing the manifest checkout as the canonical source root. Mirrors copy from the
same canonical source (not from `.agents/skills`). Symlink is deferred as an
optimization gated on a stated safety criterion: the source lives within the
launch root **and** the target harness is known not to sandbox to the working
tree. Absent that evidence, copy is used.

### D3 — Provenance: per-slug ownership, never directory adoption

Ownership is **per-slug**, never directory-wide. My never adopts or refuses
`.agents/skills` as a whole — a repo that ships its own committed
`.agents/skills` with no prior My AI-owned entries is the normal coexist case, not an
error. An entry is My AI-owned iff it carries `.my-cli-managed.json` (installer
`my`/`my-cli`); a copy is the v1 form (D2), so this marker is authoritative.

`my ai` wipes and regenerates **only** owned slugs. Non-owned entries
(hand-authored, repo-committed, or another tool's) are left untouched. If a
selected slug collides with an existing **non-My AI** entry, `my` fails **for
that slug only** with a precise structured error (move/rename it, or drop it from
the selector) — never a silent clobber, and never a refusal of unrelated
coexisting skills. An optional owned-slug index
(`.agents/skills/.my-cli-managed.json` listing owned slugs + compose version) exists
**only** to make cleanup and `doctor` inspectable; it is not an ownership or
adoption gate — per-entry markers remain the authority for every clobber
decision.

**Clawdapus / agent-authored boundary.** My-generated profile entries are
disposable, regenerated read-only launch *input*. Agent-authored or portable
skills (e.g. Clawdapus's writable portable-skills surface, or a harness's own
self-authored skill store) are never in My's owned slug set and are never
wiped, even when they live under the same `.agents/skills` tree.

### D4 — Schema additions (additive only)

v1 adds exactly **one** new field and reuses everything else:

- `manifest.Profiles []Profile`, `Profile{ID, Purpose string; Skills []string}` —
  named loadouts referenced by `--profile <id>`. Validated like roles
  (kebab-case id, skills resolve to declared skill IDs).

Deferred / reused:

- **`Skill.Exposure` is deferred** (not added in v1). A three-state
  `always|targeted|manual` field is a new manifest policy language we do not yet
  need, and a `targeted`-default that loads broadly at the umbrella risks
  re-exposing workflows. v1 defaults come from existing selectors only (see the
  composition algorithm). Exposure stays a documented additive extension point
  for finer per-skill default control once a concrete need appears.
- **Existing associations:** `Role.Skills` (role→skills) and
  `Skill.Requires: workspace:<mount>` (skill→mount, drives session defaults).
- **No repo-targeted association in v1.** Product→repo→skill indirection is
  dropped (many repos — infra, libraries, glue — don't map to one product). Repo
  targeting is deferred *with* `--repo` materialization (Slice 3) and will use a
  direct `Repo.RelatedSkills []string`, added only when that slice lands. v1
  adds no unused field.

### D5 — Selector syntax & precedence (on `my ai`)

Selector lives on `my`, never the harness:

```
my ai <harness>                         # target-driven default (see algorithm)
my ai --repo <id> <harness>             # self-skill only in v1 (Slice 3 gate)
my ai --skills all <harness>            # every declared org skill
my ai --skills none <harness>           # self-skill only
my ai --skills a,b,c <harness>          # explicit set (+ self-skill)
my ai --profile <id> <harness>          # named loadout (+ self-skill)
```

Precedence: explicit `--skills`/`--profile` override target defaults.
`--skills` and `--profile` are **mutually exclusive in v1** (error if both).
`--skills all` = every declared org skill. `--skills none` = no org skills
materialized in the launch root (the self-skill is still ensured globally — D8).
Explicit ids and `--profile` are additive. (There is no separate `always` tier
in v1 — exposure is deferred per D4; the self-skill is not a launch-root profile
entry in v1 — D8.)

### D6 — Closure at launch: fail, don't silently drop

Reuse the `validateSkillClosure` principle against the launch context's
available mounts/services/tools. If a composed skill requires a dependency the
launch root cannot satisfy, **fail with a precise structured error** (matching
compile) rather than silently dropping the skill — a launch that silently lacks
a workflow's tools is worse than a clear refusal. `--skills`/`--profile` that
name an unsatisfiable skill fail loudly; target-driven defaults that pull an
unsatisfiable skill also fail, surfacing a manifest bug. Resolved with Codex
(review Q3): no `--skip-unsatisfiable` escape hatch in v1 — a silently skipped
workflow is silent capability loss.

### D7 — Repo-checkout cleanliness: phase-gate `--repo`

Writing `.agents/skills` into a real `repos/<id>` checkout can dirty the repo.
v1 supports launch-root materialization for **umbrella base and session roots
only**. For `--repo` launches, v1 keeps the current behavior (self-skill only,
no org-skill materialization) and prints a one-line notice that repo-scoped
profiles arrive with repo-inclusive sessions. When repo-inclusive sessions land
(or a `.git/info/exclude` + refuse-to-clobber policy is implemented), `--repo`
profile materialization is enabled in Slice 3 (which also adds
`Repo.RelatedSkills`). This keeps the risky path out of the first release without
blocking it.

### D8 — Migration: gate managed org-skill materialization, not one caller

The managed org-skill materialization helpers are `collectSkillInstallResults`
and `collectSkillSyncResults`. They are reached by **two** managed seams:

- `runSetup` calls `collectSkillInstallResults` **directly** at `setup.go:290`
  (plain `my setup`), and
- `reconcileDerived` (`setup.go:32`) calls `collectSkillSyncResults`
  (`setup.go:60`) for `my sync` (`sync.go:132`), `my manifests sync`
  (`manifests.go:288`), and `my doctor --fix` (`doctor.go:587`).

Gating only `reconcileDerived` would leave plain `my setup` resurrecting
user-global org skills. v1 factors **both** seams through one derived-skill
helper so managed setup/sync/doctor paths no longer write org skills to harness
user config dirs for launch-root-capable harnesses. The replacement report
states org skills are now launch-scoped (composed by `my ai` into the launch
root) and lists any user-global org skills found, so every caller prints a
consistent migration line. `my doctor` detects leftover user-global
My AI-managed org skills for launch-root-capable harnesses and offers `--fix` to
remove them; no deletion without `--fix`. OpenCode is the v1 compatibility
carve-out: because it lacks a proven launch-root reader, present/explicit
OpenCode installs keep global org skills marked `scope=compat`, and doctor does
not remove them as legacy drift. `my skills install|uninstall|sync|list` remain
manual/personal surfaces: they may still operate on a user's global skill dirs
by explicit command, but they are no longer part of automatic org setup.

**Self-skill placement (v1 = global-only, option a).** To avoid exposing two
`my` skills on harnesses that read both global and project skills, v1 keeps the
self-skill on its **existing global ensure path** (`ensureLaunchSelfSkill`,
`runSetup`'s `selfskill.Install`) and materializes **only org skills** into
launch roots. The self-skill therefore lives in exactly one place in v1. This is
the ADR's explicit migration carve-out (Mechanism #1: the self-skill "may remain
broadly available during migration"); moving the self-skill into launch roots is
the documented target state, deferred until the global ensure can retire
per-harness (a later slice, after each harness's discovery is verified). The
self-skill is the right thing to keep global: it is the bootstrap skill that must
be present in every context, including non-`my ai` entry and unmigrated
harnesses.

### D9 — Gemini removed entirely; Antigravity is the replacement

**Revised per Wojtek's directive (2026-06-14): remove Gemini entirely — `my`
does not support Gemini at all anymore.** Antigravity (`agy`, installed) is the
replacement. Add `harness.Antigravity ("antigravity")`, command `agy`,
`ReadsAgentsSkills()=true`, `SupportsLaunchRootSkills()=true` (no mirror; reads
shared `.agents/skills`). Then **delete** Gemini: remove the `Gemini` enum const,
its `Parse`/`CommandName`/`ConfigDir`/`SkillTargetPath` cases, the `gemini`
CLI-link install/uninstall/inspect branches in `skills.go`/`selfskill.go`, the
`managed-by-gemini` `InstalledKind` and its uses, `geminiPurgeTargets`, the
deprecated-harness rejection in `launch.go`, and the Gemini test suites. With
Gemini gone every remaining harness is filesystem-managed, so the `IsFilesystem`
and `Deprecated` methods become vestigial (always true / always false) — remove
them and simplify their callers, or keep as constant stubs. Update README and
`site/guide/skills.md` harness lists; leave historical ADR/plan mentions of the
transition as-is. Net harness set: `ClaudeCode, Codex, OpenCode, Antigravity`.

### D9a — OpenCode: no launch-root skill seam → compatibility-global (verified)

Claude probed OpenCode with `opencode debug skill`: in a project dir it
discovers a skill at **neither** `<cwd>/.config/opencode/skills` nor
`<cwd>/.agents/skills`; it reads only user-global `~/.config/opencode/skills`.
So OpenCode cannot consume launch-scoped skills today. v1 adds
`SupportsLaunchRootSkills() = ClaudeCode || Codex || Antigravity` (OpenCode
false): OpenCode is **excluded** from launch-root materialization and from the
D8 user-global *removal* — its org skills stay on the user-global path it
actually reads (a compatibility carve-out, like the self-skill), `my ai
--skills/--profile` is rejected for OpenCode, and `doctor --fix` does not strip
OpenCode's compat globals. Reviewed and accepted by Claude.

### D10 — `--print` is a pure command preview

`my ai --print` returns at `launch.go:132`, before any self-skill repair or
materialization. v1 preserves that contract exactly: under `--print`, selector
flags (`--skills`/`--profile`) are **syntax-parsed only** — no composition, no
`.agents/skills` materialization, and no manifest-profile validation. `--print`
never writes derived state. A profile dry run, if ever wanted, gets its own
explicit surface rather than overloading `--print`. (Note: `--print` with
`--new-session` already creates the session today; that pre-existing behavior is
unchanged and out of scope here.)

## Composition algorithm

`internal/launchprofile` (new package), `Compose(doc, ctx, selector) (Profile, error)`:

1. `floor = {}` for launch-root materialization. The self-skill is **not** part
   of the launch-root profile in v1 — it is ensured separately on the existing
   global path (D8, option a), so it is never duplicated into `.agents/skills`.
   No `always` tier either (D4).
2. Resolve the selector set (org skills only):
   - explicit ids → resolve each to a declared skill (error on unknown).
   - `all` → every declared org skill. `none` → `{}` (floor still applies).
   - `--profile id` → `profile.Skills`.
   - default (no selector) → context defaults, from existing selectors only:
     - umbrella base, no role → **all declared org skills** (the ADR's broad
       organization default).
     - role selected (state.json `SelectedRole`) → `Role.Skills`.
     - session root → skills whose `Requires: workspace:*` are all satisfied by
       the session's assembled mounts, plus `Role.Skills` if a role is selected.
     - `--repo` → floor only in v1 (D7).
3. `set = floor ∪ selectorSet`.
4. Closure-check `set` against ctx mounts/services/tools; fail precisely on any
   unsatisfiable requirement (D6).
5. Return ordered, de-duplicated `Profile{Entries []ProfileEntry}`; each entry
   carries install slug and canonical source path (copy is the v1 form — D2).

`my ai` then materializes `Profile` into `<root>/.agents/skills` (+ mirror),
wiping and regenerating only previously-owned slugs, after `launchTargetDir` and
before `runHarness` — and **not** under `--print` (D10). The composer is pure
(takes an explicit ctx); the CLI seam does I/O. `my compile` may later embed the
same `Profile` in its projection (reusing the closure model already there) — out
of scope here but kept compatible.

## Build slices

- **Slice 0 — schema + composer (pure, no I/O).** Add `manifest.Profiles` +
  validation and `internal/launchprofile.Compose` with the closure check (no
  `Skill.Exposure`). Table-driven tests only; no launch changes yet.
- **Slice 1 — capability registry + materialization + Antigravity.**
  `harness.ReadsAgentsSkills`/`SupportsLaunchRootSkills`/`MirrorSkillDir`, add
  Antigravity, remove Gemini. Launch-root `.agents/skills` **copy** writer
  with per-slug
  wipe/regenerate, `.my-cli-managed.json` markers, per-slug collision refusal, and
  explicit launch-root capability checks + double-discovery guard. Wire into
  the `ensureLaunchSelfSkill` seam for umbrella + session roots (D7), skipped
  under `--print` (D10). Add `--skills`/`--profile` flags. Antigravity ships on
  the new model from day one (before migration — review point 7). OpenCode
  keeps compatibility-global org skills and rejects selectors until a local seam
  exists.
- **Slice 2 — migration + doctor.** `reconcileDerived` stops user-global org
  installs across all four callers for launch-root-capable harnesses (D8);
  consistent migration report; `doctor` detection + `--fix`; OpenCode
  compatibility global scope preserved. Keep self-skill path.
- **Slice 3 (follow-up, may not ship in first release) — `--repo` profiles.**
  Add `Repo.RelatedSkills`, repo-targeted defaults, and repo-root materialization
  once cleanliness policy/repo-inclusive sessions exist.

## Edge cases (each gets a test)

1. Pre-existing non-My AI `.agents/skills` (repo-committed, no My AI-owned entries) →
   coexists untouched; My adds only its own slugs (D3).
2. `.agents/skills` mixed My AI-owned + non-My AI → only My AI-owned slugs regenerated.
3. Selected slug collides with an existing non-My AI entry → fail for that slug
   only, others untouched (D3).
4. `--skills none` → no org skills materialized in the launch root; prior
   My AI-owned org slugs wiped; self-skill still ensured globally (D8).
5. `--skills` + `--profile` together → error.
6. Unknown skill id in `--skills`/`--profile` → error listing valid ids.
7. Composed skill requires an out-of-scope mount/service/tool → precise closure
   error, no escape hatch (D6).
8. Copy materialization → entry written as copy with `.my-cli-managed.json`;
   re-launch refreshes a stale copy.
9. Mirror harness (Claude Code) → both `.agents/skills` and `.claude/skills` My
   slugs present and consistent; non-mirror harness → no mirror; harness reading
   both dirs → only one written (no double-discovery — D1).
9a. OpenCode default launch/setup/sync when OpenCode is present → global org
    skills are maintained with `scope=compat`; no unread launch-root mirror is
    written.
9b. OpenCode `--skills`/`--profile` → fail before applying a global org-skill
    selector side effect.
10. Concurrent launches into two different roots → no shared user-level mutation,
    no collision.
11. `--repo` launch in v1 → self-skill only + notice; no org `.agents/skills`
    written into the checkout.
12. `--print` (with any selector) → writes no `.agents/skills`, no manifest
    validation (D10).
13. Stale My AI-owned slug whose source skill left the manifest → wiped on next
    compose.
14. Re-launch with identical selector → idempotent (no spurious rewrites/dirty).
15. Agent-authored/portable skill under `.agents/skills` → never wiped (D3
    Clawdapus boundary).
16. Self-skill never duplicated → launch-root `.agents/skills` contains no `my`
    self-skill in v1; the self-skill exists only on the global ensure path (D8).
17. `my setup` (and `--print`), `my sync`, `my manifests sync`,
    `my doctor --fix` → none install user-global org skills after migration (D8).

## Test plan

- `launchprofile`: table-driven compose tests (selector precedence, context
  defaults — umbrella-all / role / session, closure failures).
- `harness`: capability matrix (`ReadsAgentsSkills`, `SupportsLaunchRootSkills`,
  `MirrorSkillDir`, Antigravity present, Gemini rejected as unknown).
- `cli/launch`: copy materialization into a `t.TempDir()` launch root with a stub
  manifest checkout; assert per-slug wipe/regenerate, per-slug collision refusal,
  coexisting non-My AI skills untouched, mirror generation + double-discovery
  guard, idempotent re-launch, `--repo` self-skill-only, and `--print` writes
  nothing.
- `cli` doctor: leftover user-global org-skill detection + `--fix`; all four
  `reconcileDerived` callers no longer install user-global org skills.
- Manifest validation: `Profiles` resolution and `--skills`/`--profile` mutual
  exclusion.

## Acceptance criteria

- `my ai` materializes a context-correct org-skill `.agents/skills` (+ needed
  launch-root mirror) in the umbrella/session launch root for
  launch-root-capable harnesses; the self-skill stays global and is never
  duplicated into the launch root.
- No user-global org skills are created by automatic paths for
  launch-root-capable harnesses. OpenCode is the explicit v1 exception:
  present/explicit OpenCode setup/sync/launch keeps compatibility-global org
  skills because no project-local skill seam is proven.
- Selectors (`--skills all|none|csv`, `--profile`) work with documented
  precedence and mutual exclusion.
- Closure failures are precise and structured; non-My AI entries are never
  clobbered (per-slug ownership); re-launch is idempotent; `--print` writes
  nothing.
- Gemini removed, Antigravity (`agy`) functional, docs/site updated.
- `go test ./...` and `go vet ./...` green; `git diff --check` clean.

## YAGNI / deferred

- `Skill.Exposure` policy tier (deferred per D4; documented extension point).
- `Repo.RelatedSkills` + repo-targeted defaults (Slice 3, gated on cleanliness —
  D4/D7).
- Symlink materialization (D2; copy is the v1 form).
- Profile composition inside `my compile` output (kept compatible, not built).
- `--skip-unsatisfiable` closure escape hatch (resolved no — D6).
- Profile dry-run surface (out of `--print` — D10).
- Any per-skill confidentiality/role-as-ACL semantics (ADR rejects).

## Codex adversarial review #3 (signed off)

The second revision resolves the two substantive blockers: all managed
setup/sync/doctor seams are now in scope, and the self-skill is global-only in
v1 so a harness cannot discover duplicate `my` skills. I made one wording
correction in D8: `reconcileDerived` calls `collectSkillSyncResults` today, not
`collectSkillInstallResults`. That distinction matters for implementation
because the helper split should preserve explicit/manual `my skills
install|sync` behavior while removing only automatic user-global org-skill
writes.

My independent concern for this round is the manual/managed boundary: the plan
must not accidentally delete a user's explicitly installed personal skills while
cleaning up automatic org installs. D8 now draws that boundary clearly enough for
implementation: `setup`, `sync`, `manifests sync`, and `doctor --fix` migrate to
launch-scoped org skills; `my skills ...` remains a manual/personal surface.

Codex signs off on this plan for Slice 0 through Slice 2 implementation,
pending Claude's convergence review.

## Resolved questions

Review #1's Q1–Q5 are resolved in "Claude revision #1" and the decisions above:
Q1 → D1 (explicit launch-root capability + double-discovery guard; OpenCode
compatibility-global until verified);
Q2 → D4 (defer repo targeting + `Repo.RelatedSkills` to Slice 3, no product
indirection); Q3 → D6 (fail, no escape hatch); Q4 → D9/Slices (capability
registry + Antigravity in Slice 1, before migration); Q5 → D3 (per-slug
ownership authoritative; the index is inspection-only, never an adoption gate).

Review #2's two blockers (the `runSetup`/`collectSkillInstallResults` direct
seam, and self-skill duplication) are resolved in "Claude revision #2" → D8 and
the composition floor. The umbrella-base default and the evidence correction are
settled. My earlier "remaining seam?" question is itself answered by D8 (the
`runSetup` direct call was that seam).

Review #3's remaining seam question is resolved by distinguishing managed from
manual callers: `runSetup` and `reconcileDerived` are the automatic org-skill
paths to migrate; `my skills install|sync` are explicit manual/personal
commands, and `selfskill` remains the global self-skill carve-out in v1.

## Claude convergence (signed off)

I verified Codex's review #3 correction against the code and accept it:
`reconcileDerived` calls `collectSkillSyncResults` (`skills.go:415`), `runSetup`
calls `collectSkillInstallResults` (`skills.go:289`), and both helpers also back
the manual `my skills install|sync` commands — so the migration must remove only
the **automatic** call sites' user-global org writes (`runSetup`,
`reconcileDerived`) while leaving the helpers' behavior intact for explicit
`my skills ...` and never deleting a user's personally-installed skills. D8 now
states this exactly.

No remaining disagreement. Both reviewers sign off on Slices 0–2 for
implementation; `--repo`/`Repo.RelatedSkills`/symlink/self-skill-into-launch-root
remain explicitly deferred (Slice 3+). **Codex implements next**, slice by slice;
I review each slice against this plan and we debate the final state before
release.
