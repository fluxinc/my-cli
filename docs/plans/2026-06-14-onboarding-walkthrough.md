# Human Onboarding Walkthrough

Status: **shipped (v0.26.0)**, 2026-06-14. Converged design between Claude and
Codex over Talking Stick; green-lit by Wojtek. Revised once to answer Codex
review #1 and signed off by Codex after review #2. Codex implemented; Claude
boundary-tested (findings F1/F1b/F3/F2, locked by regression tests); both
signed off.

## Problem

`our setup` configures a *machine*: it ensures the umbrella, persists the
role selection, generates `AGENTS.md`, materializes `.mcp.json`, syncs
mounts/repos, and installs skills. It is entirely flag-driven and assumes the
operator already understands the model.

Nothing welcomes a *human* who just joined the company, has `our` installed,
and needs to learn what it does by doing. The `onboard` verb currently exists
only as a deprecated alias that warns and dispatches to setup — historical
baggage, not a feature. We want to reclaim `onboard` as an interactive,
human-facing intro: an "our by example" walkthrough that explains the model,
asks a few questions with press-enter defaults, and points the newcomer at a
second console to watch real commands take effect.

## Constraint: generic mechanism only

The walkthrough teaches **the model**, never an organization's data. It must
stay public-safe and content-free: it explains mounts/services/roles/skills
and the verb set, using whatever manifest happens to be registered as the
live example, without baking in any company name, customer, or private
detail. This is the same altitude rule the rest of the CLI follows — `our`
declares what to mount and call, never who may, and here it *teaches* that
shape rather than asserting any org's specifics.

## Two verbs, no third

The configurator the newcomer will "still want to run afterward" already has
a name: **`our setup`**. We deliberately keep exactly two top-level verbs and
introduce **no** `our configuration` / `our configure` / `our tour`:

- **`our setup`** — the machine configurator. Owns durable `.our/state.json`.
  Stays scriptable and deterministic. Grows one *flag*, `--interactive`, for
  press-enter prompting. A flag is not a verb; the surface does not bloat.
- **`our onboard`** — the human tour. Teaches by example, branches on its own
  tour-completion marker, and delegates every durable change back through the
  shared setup path. It is the only entry point that works from zero (before
  a manifest is even registered).

Principle that keeps the names from blurring: **setup writes durable machine
state; onboard mutates nothing durable except by delegating to setup.** The
one thing onboard tracks for itself is whether the *tour* was taken, which is
not the same fact as whether the *machine* is configured.

## Decisions

1. **Two top-level verbs only.** `our setup` (configure) and `our onboard`
   (teach). No `our configuration`/`configure`/`tour`/`learn`. The "run the
   configurator again later" need is served by re-running `our setup`
   (optionally `our setup --interactive`).

2. **`our setup --interactive`.** An explicit flag adds press-enter prompting
   for the inputs setup currently takes via flags (role; and manifest
   selection — see Decision 6). Plain `our setup` keeps its exact current
   behavior — non-interactive and scriptable. v1 is **flag-gated only**: plain
   setup never auto-prompts, even on a TTY. (A narrowly guarded TTY
   auto-prompt is explicitly deferred, not adopted.)

3. **`--interactive` flag conflicts and EOF.** `--interactive` is rejected
   together with `--json` or `--print` (prompts plus machine output are
   incoherent) — clear error, non-zero exit. Prompt input is read from
   `app.stdin` (Decision 9), so EOF is resolved **per prompt**, never by
   blocking or looping:
   - a prompt that has a safe default takes the default on bare Enter **and**
     on EOF (role → current selection, else unscoped);
   - a prompt with no safe default (manifest choice when several are
     registered) **errors clearly** on EOF, naming the non-interactive flag
     (`--manifest`) that resolves it.
   This keeps CI/pipes graceful where a default is safe and fails loudly where
   a silent choice would be wrong.

4. **Reclaim `onboard`.** Drop the deprecated `case "onboard"` dispatch and
   its `warnDeprecated("our onboard", "our setup")` call; remove the
   `warnDeprecated` helper if it becomes unused. Rename the internal
   `runOnboard` to `runSetup` (it *is* setup) so the new `runOnboard` is free
   to mean the tour. Full rename checklist in Step 0.

5. **Onboard branches on tour-completion.**
   - *First run* (no tour marker): a short tour + press-enter prompts that
     delegate real changes through the shared interactive setup helper. This
     is Wojtek's "combined" experience.
   - *Configured-but-not-toured*: the machine may already be set up
     (`SelectedRole` present) while the tour was never taken. Onboard must
     **not** blindly re-run setup; it teaches, acknowledges the existing
     config, and *offers* (never forces) a role review.
   - *After completion*: a concise "you're already onboarded" review with
     explicit next actions. It is pure read, exit 0, zero mutation.
     Re-running the configurator is `our setup` / `our setup --interactive`;
     the tour offers to invoke setup interactively **only on explicit
     confirmation**, never silently.

6. **Manifest selection in interactive flow.** `our setup` today errors when
   several manifests are registered and `--manifest` is absent. Interactive
   setup/onboard instead **prompts to choose** when more than one exists, and
   auto-selects when exactly one exists. This is the highest-value interactive
   input after role.

7. **Role selection and clearing.** The role prompt defaults (bare Enter / EOF)
   to the *current* selection when one exists, else to **unscoped (no role)** —
   it does not auto-pick the first declared role. To **clear** an existing
   role back to unscoped, the prompt accepts an explicit `none` sentinel.
   `our setup` already treats roles-declared + no-selection as legal and
   unscoped; the clear path is tested.

8. **Minimal tour state, umbrella-local only.** Store
   `tour: { completed_at, version }` as a namespaced field inside the existing
   umbrella `.our/state.json`. **No global/home-level state.** Because the
   marker lives in the umbrella, zero-manifest onboard (Decision 10) writes
   nothing and cannot be "completed" until setup creates/loads the umbrella —
   which is correct: there is no machine to have been toured yet. The write is
   read-modify-write: load current state, set `tour`, save, **without
   clobbering `SelectedRole`**; the marker is stamped only on **full tour
   completion** (after the setup delegation succeeds), so an aborted tour
   re-runs as first-run. `tour.version` is a package constant bumped when tour
   content materially changes; on a future bump past the stored value, onboard
   **soft-notifies** ("tour updated, run `our onboard`") — no forced re-tour in
   v1. No name/harness/notification preferences are persisted in v1.

9. **App gains stdin.** `app` currently has `stdout`/`stderr` but no input.
   Add `stdin io.Reader` defaulting to `os.Stdin`. The interactive helper reads
   from `a.stdin`; tests inject a `strings.Reader`/pipe rather than depending
   on a real TTY.

10. **Onboard works from zero — by printing, not mutating.** `our setup`
    hard-errors without a registered manifest. `our onboard` runs *before*
    that: with no manifest it explains the model and **prints/copies the exact
    `our manifests add <org> <git-url>` command**, then exits with the tour
    **unmarked**. It does **not** prompt for a name/git URL or call
    `manifests add` itself — the newcomer gets the URL from their admin, the
    two-pane model wants them to run it in the sandbox pane and watch it land,
    and prompting for it would drag onboard into the manifest-authoring wizard
    we put in YAGNI. This print-only zero state is a core reason the tour is
    its own verb rather than a mode of setup.

11. **Onboard flag surface.** `our onboard` supports only `--home`,
    `--manifest`, `--umbrella`, `--no-refresh`, `--no-update-check`. It does
    **not** inherit setup's `--copy`/`--link`/`--force`/`--print`/`--all`.
    `--reset`/`--again` (force re-tour) are deferred.

12. **Two-pane model is the combined model.** Onboard narrates in one console
    and tells the newcomer to keep a second console open to run real commands
    and watch effects. "Combined for first-timers" and the original two-pane
    vision are the same design: onboard offers to configure inline (with skip),
    then sends the user to the sandbox pane to try `our roles list`,
    `our services list`, `our customers list`, etc.

13. **Minimal v1 scope.** The tour covers: the model (control vs data plane),
    mounts/services/roles/skills, the common verb set, what `our setup` will
    change, and the launch hints. No GUI, no manifest-authoring wizard, no
    CRM-style data-entry flow.

## Edge cases (must be answered by the implementation/tests)

- `--interactive` + `--json`/`--print` → error, non-zero exit.
- `--interactive` + `--role X` → `--role` pre-fills/confirms that prompt; other
  prompts still run.
- EOF on a prompt with a safe default → take the default (role).
- EOF on manifest choice when several exist → clear error naming `--manifest`.
- Multiple manifests, no `--manifest`, interactive → prompt to choose.
- Clearing a set role back to unscoped → `none` sentinel.
- Zero manifests → print `manifests add` guidance, exit, tour unmarked.
- Configured-but-not-toured → teach + acknowledge, offer (not force) review.
- Already toured → pure-read review, exit 0.
- Aborted tour before completion → marker not written, re-runs as first-run.
- Tour marker write must preserve `SelectedRole`.
- `tour.version` bump → soft-notify, no forced re-tour.

## Build steps

Steps 0–2 are an **internal development sequence, not release boundaries**:
removing the `onboard` alias in Step 0 makes `our onboard` unknown until Step
2, so the shipped release must contain at least Steps 0+1+2 (+3 docs). Step 1
(`--interactive`) is the only piece that could ship on its own.

- **Step 0 — rename refactor (no behavior change).** Rename internal
  `runOnboard` → `runSetup` and helper `onboardArgsForLaunch` →
  `setupArgsForLaunch`; remove the deprecated `onboard` dispatch +
  `warnDeprecated` call; delete `warnDeprecated` if now unused. Call-site
  checklist (verified by grep):
  - `internal/cli/setup.go:119` — definition `runOnboard` → `runSetup`.
  - `internal/cli/cli.go:131` — `setup` case → `runSetup`.
  - `internal/cli/cli.go:132-134` — remove the deprecated `onboard` case.
  - `internal/cli/admin.go:25` — admin setup → `runSetup`.
  - `internal/cli/init.go:85` — `init --setup` (`setupArgs`) → `runSetup`.
  - `internal/cli/launch.go:150` — `ai --setup` → `runSetup`; and
    `internal/cli/launch.go:483` + `internal/cli/setup_test.go:117` — rename
    `onboardArgsForLaunch` → `setupArgsForLaunch`.
  `our setup` behaves exactly as before; suite stays green.

- **Step 1 — `our setup --interactive`.** Add `app.stdin` (Decision 9) and a
  shared interactive helper that prompts for manifest (Decision 6) and role
  (Decision 7) with the EOF semantics of Decision 3, then routes the mutation
  through the existing `runSetup` logic. Plain setup unchanged. Tested with
  injected stdin: bare-Enter default, explicit selection, `none` clear,
  multi-manifest prompt, EOF-default vs EOF-error, and `--json`/`--print`
  rejection.

- **Step 2 — `our onboard` tour.** Real `--help` surface and the flag set of
  Decision 11. Reads/writes the tour marker (Decision 8); branches per
  Decision 5. Zero-manifest path prints `manifests add` guidance and exits
  unmarked (Decision 10). First-run weaves the tour with the Step-1 helper.
  Tested for: zero-manifest, first-run, configured-but-not-toured, and
  already-completed paths, all with injected I/O.

- **Step 3 — docs.** New site guide page (e.g. `site/guide/onboarding.md`),
  README mention, `skills/our/SKILL.md` note. (Roadmap + plans index are
  updated earlier, in the plan commit — see Acceptance.) `cd site && npm run
  build` if nav/content changes.

## Acceptance criteria

- Deprecated hidden `onboard` alias removed; `warnDeprecated` gone if unused.
- `runOnboard` renamed to `runSetup`; `onboardArgsForLaunch` →
  `setupArgsForLaunch`; no stale references in admin/init/ai call sites.
- `our onboard` has a real help surface and is no longer a setup alias.
- `our setup` (no flags) remains scriptable: no prompts, no TTY dependence,
  unchanged JSON/print output.
- `app` has an injectable `stdin`; interactive helper covered by tests using
  fake stdin/stdout (no real TTY).
- All Edge cases above have a corresponding test.
- Tour-completion marker round-trips through umbrella `.our/state.json` without
  clobbering `SelectedRole`.
- No new top-level verbs.
- README Roadmap and `docs/plans/README.md` index updated in the **plan
  commit** (this doc is itself a direction change), and again at
  implementation if status changes.

## Out of scope / YAGNI

- No `our configuration`/`configure`/`tour`/`learn` verb.
- No auto-prompt from plain `our setup` (deferred, Decision 2).
- No onboard-driven `manifests add` mutation or git-URL prompting (Decision 10).
- No persisted human preferences (name, preferred harness, notifications).
- No `--reset`/`--again` re-tour flag in v1 (Decision 11).
- No manifest-authoring or data-entry wizard; no GUI; no new network calls.

## Review #2 resolutions

- Decision 3 EOF synthesis accepted: defaults are allowed only where the prompt
  has a safe, non-surprising default; ambiguous manifest selection errors and
  names `--manifest`.
- Tour state is a nested `tour` object inside umbrella `.our/state.json`:
  `tour: { completed_at, version }`.

## Review history

- **Draft** (Claude, 2026-06-14) — converged with Codex over Talking Stick;
  green-lit by Wojtek for drafting.
- **Codex review #1** (event 5439) — 10 points: zero-manifest vs umbrella-local
  marker, manifests-add mutation question, multi-manifest selection, role
  clearing, flag/EOF conflicts, app stdin plumbing, onboard flag surface,
  roadmap/index timing, Step 0 not a release boundary, rename call-site
  coverage.
- **Revision #1** (Claude, 2026-06-14) — answered all 10: zero-manifest prints
  guidance and exits unmarked (no mutation); marker umbrella-local only;
  per-prompt EOF resolution; `none` role sentinel; `app.stdin`; explicit
  onboard flag set; rename checklist with file:line; roadmap/index in the plan
  commit; Steps 0–2 as an internal sequence. Added: read-modify-write marker
  ordering, `tour.version` soft-notify, pure-read review path.
- **Codex review #2** (2026-06-14) — signed off. The per-prompt EOF policy is
  acceptable, the nested `tour` object is the implementation target, and all
  remaining questions are covered by the edge-case test list.
