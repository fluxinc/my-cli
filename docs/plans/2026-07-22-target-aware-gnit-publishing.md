# Target-aware Gnit publishing

Status: shipped in v0.36.0 — jointly designed, implemented, adversarially reviewed, and dogfooded

Fixes [issue #32](https://github.com/fluxinc/my-cli/issues/32): the presence of
`<umbrella>/.gnit/roster.yaml` currently routes all eligible publication
through Gnit, even for checkouts that are not Gnit roster members, and a
successful `gnit push` can mark unrostered targets as pushed.

## Confirmed defects (both agents, code-verified)

1. Backend auto-selection is umbrella-wide marker sniffing: `sync.go` and
   `work.go` call `findGnitWorkspaceRoot` (a bare `os.Stat` of
   `.gnit/roster.yaml`) and switch every target to the Gnit backend. Nothing
   in the codebase parses the roster.
2. `runGnit` gates targets only on *descendant-of-root* (`pathWithin`), not
   roster membership.
3. After one successful `gnit push`, every publishable entry in the request is
   marked `pushed` — false success for unrostered targets when the control
   root is publishable.
4. Scope leak (found in the second audit): `gnit push` takes no member
   selection — it pushes *all* roster members with pending state, then the
   metadata repo. A My-triggered `gnit push` can therefore publish roster
   members the operator never selected.
5. Dry-run (`--print`) reports "would run gnit push" without any membership or
   control-root preflight, so the preview does not predict the real outcome.
6. Failure recovery text re-suggests the same command that selects the same
   broken backend.

## Gnit CLI contract facts (verified against the installed binary)

- `gnit push` has no member-selection flags (only `--resume`, `--verbose`).
  Push is ordered and whole-workspace: member repos first, then the metadata
  repo. Subset delegation is impossible; scope control must be a My-side
  preflight.
- No structured roster query exists (`gnit status` has no `--json`). The
  machine-readable membership source is `.gnit/roster.yaml` itself
  (`version: 1`, `mode`, `members[]` with `id`, `path`, `remote`,
  `required_excludes`). Member `remote` values mix https and ssh forms in real
  workspaces.
- `gnit doctor` exists; My's doctor should point at it for Gnit-side detail
  rather than reimplementing it.

## Converged invariant

One shared planner (used identically by `my sync`, `my session finish
--publish`, and `--print`) classifies every outbound target three ways:

1. **gnit** — the target's canonical checkout is an *exact* roster member:
   canonical path equality (symlink-resolved; descendant-of-member is NOT
   membership), and remote identity equality after https/ssh normalization —
   AND the control root is publishable (is a git repo, has an `origin`
   remote), the roster parsed strictly, and the scope preflight passes.
2. **builtin** — the target is not a roster member. The marker is not
   evidence; the guarded built-in backend is the *correct* backend here, not a
   fallback. No hold, no message on the normal human surface.
3. **held (fail closed, classified)** — ambiguity or a broken Gnit substrate
   never guesses and never silently downgrades:
   - `gnit_root_unpublishable` — target is a roster member but the control
     root has no origin (or is not a git repo). A rostered member is a
     deliberate adoption; publishing it via builtin behind Gnit's back would
     erode the coordinated control-root record that is Gnit's purpose. Hold
     with precise remediation, never silent builtin.
   - `gnit_roster_invalid` — roster exists but is unreadable, structurally
     invalid, or has an unknown `version`. Membership is unprovable, so *all*
     candidates under that root hold. Ambiguity blocks; it never guesses.
   - `gnit_member_identity_mismatch` — path matches a roster member but the
     remote identity differs. Hold; doctor explains.
   - `gnit_scope_exceeds_selection` — the scope preflight found unselected
     roster members that `gnit push` would also publish (ahead of upstream).
     Hold the gnit group, listing the extra members; remediation is to widen
     the selection or reconcile those members. The metadata-repo push itself
     is substrate, not scope leak.
   - `gnit_workspace_unhealthy` — a roster member checkout is missing, so
     `gnit push` cannot succeed; hold the gnit group and suggest
     `gnit doctor`.

Per-target routing (not whole-request): a mixed request may publish rostered
members via one gnit invocation and unrostered targets via builtin in the same
run. Whole-request fallback was debated and rejected — it would let one
unrostered mount silently pull genuinely-rostered repos out of Gnit, the same
action-at-distance bug mirrored.

False-success kill: only entries routed to the gnit group may ever be marked
`pushed` by the gnit path. Structural with per-target grouping; regression
case 4 below is the proof.

## Forced backends

- `--backend gnit` fails **before publication** when any selected target is
  not an exact roster member or the control root/scope preflight fails, naming
  each target and reason.
- `--backend builtin` on a rostered target is allowed (explicit operator
  escape hatch — it keeps recovery inside My's publication audit instead of
  raw git), but the report carries a backend message noting the target is
  Gnit-adopted and was published outside Gnit.

## Dry-run parity

`--print` runs the identical planner, including roster parse, root-origin
check, and the scope preflight. All checks are local (config reads, stored
tracking refs; no fetch — dry-run already never fetches), so preview
classifications equal real-run classifications at zero network cost.

## Human surface

Employee-visible output stays noun-simple: statuses plus a concrete next
command. Backend names stay out of non-verbose lines except inside hold
remediation where an action is genuinely required. Classified reason codes
ride in `--json` for agents. Recovery text must name a step that changes the
outcome — never re-suggest the same print command.

## Doctor

- INFO: control workspace present with unrostered My content mounts — a
  *supported* topology; state which backend each target publishes through.
- INFO: roster members that are not My-managed entries.
- WARN: control root without an origin remote (with fix); roster member
  checkout missing (partial migration).
- ERROR: roster unreadable / unknown version.
- Point to `gnit doctor` for Gnit-side diagnosis.

## Implementation tasks (Codex drafts; Claude reviews adversarially)

1. **Roster parser** — `internal/syncer/gnit_roster.go` (stdlib only, strict):
   require `version: 1` (unknown version ⇒ invalid), `members[]` need
   `id`/`path` and may carry Gnit's optional `remote`; when it is absent,
   identity is still proven from the member checkout's actual `origin` against
   the My AI entry. Tolerate unknown extra keys; any structural failure ⇒
   `gnit_roster_invalid`, never best-effort. Tests: valid roster, unknown
   version, malformed YAML, extra keys tolerated, https and ssh remote forms.
2. **Planner** — shared eligibility function producing the three-way
   classification per entry with reason codes. Tests: exact member match;
   descendant-of-member ⇒ builtin; symlinked checkout canonicalization; remote
   identity mismatch ⇒ hold; missing root origin ⇒ member holds, non-member
   still builtin; invalid roster ⇒ all candidates hold.
3. **`syncer.Run` auto partition** — `Backend: "auto"` moves the decision into
   the syncer; per-result backend recorded; gnit path can only mark gnit-group
   entries pushed. Regression: control root WITH valid origin + unrostered
   target is published by builtin and never reported as pushed-by-gnit.
4. **Scope preflight** — enumerate roster members, compute which `gnit push`
   would publish; unselected-ahead members ⇒ `gnit_scope_exceeds_selection`;
   missing member checkout ⇒ `gnit_workspace_unhealthy`. Tests for both, plus
   selection-covers-all-ahead passes.
5. **Forced-backend semantics** — early failure for forced gnit; warn-allow
   for forced builtin on a rostered target. Tests for both.
6. **Dry-run parity** — test that `--print` classification equals real-run
   classification for every reason code above.
7. **Session publish** — `work.go` drops its duplicated marker test and calls
   the shared planner. Test: session finish and sync choose identical
   backends/holds for the same topology.
8. **Doctor** — items and severities above, with tests.
9. **Docs** — changelog entries (`CHANGELOG.md`, `site/changelog.md` under
   Unreleased); this plan's status and `docs/plans/README.md` updated in the
   same commit as the fix lands.

## Regression matrix (issue #32 suggested coverage)

| # | Fixture | Expected |
|---|---------|----------|
| 1 | Content mount under umbrella, absent from roster | builtin publishes it; no gnit involvement |
| 2 | Unrelated repository rostered | irrelevant to the content mount's routing |
| 3 | Control root without origin | non-members builtin; members hold `gnit_root_unpublishable` |
| 4 | Control root WITH valid origin, unrostered target | target never reported pushed by gnit |
| 5 | Session `--publish` vs ordinary sync | identical decisions (shared planner) |
| 6 | Dry-run output | predicts the real operation's backend and preflight result |

## Dogfood and release

- Dogfood on the reporting umbrella: `my sync --push --print` predicts builtin
  for the unrostered content mount; `my session finish --publish` publishes it
  without Gnit involvement or backend prompts; the previously stuck "failed"
  last-sync report heals on the first successful run (`saveLastSyncReport`
  overwrites — no repair verb needed).
- Upstream ask (non-blocking): request `gnit status --json` / a roster query
  contract so future versions can drop file parsing.
- Ship as a patch/minor release per the repo release process after review,
  tests, and dogfood pass.
