# Workspace currency & observable publishing (#7, #8, #11)

Status: design / proposed. Authored by Claude, for Codex to implement in slices.
Supersedes nothing; builds on `2026-06-08-overarching-sync.md`.

## Problem

Keeping a workspace current is still several easy-to-forget commands, and
automatic publishing is invisible. Three open issues form one theme:

- **#8** — all updates are pull-on-demand; `flux doctor` diagnoses `synced` /
  `stale` without remediating and without showing *how far* behind.
- **#7** — `flux sync` reconciles Git repos but leaves derived artifacts
  (installed skills, generated `AGENTS.md`/`CLAUDE.md`) stale.
- **#11** — automatic publishing emits no audit trail, and the admin-class
  hold-back needs confirming on every automation path.

Goal: collapse "keep current" toward zero manual steps for read-mostly content,
one `--fix` for the rest, and make every publish observable — **without** surprise
clobbering of local work.

## Operator decisions captured

- Freshness aggressiveness (#8): **include auto fast-forward of clean read-mostly
  mounts on a TTL**, in addition to `doctor --fix` + freshness reporting.
- Scope: **implement all three, staged.**
- Publish-policy gate (#11): delegated to Codex, **resolved** — top-level manifest
  `sync.publish_policy`, staged to Slice 3 (see §C.2).

## Grounding (current code)

- `flux sync` already has `--scope all|local|content|manifest|products`
  (`collectSyncEntries`) and `--publish auto|never|direct|pr`. `--publish auto`
  already holds admin-class (manifest/catalog/guidance/skills/tools) back; only
  private content records auto-publish.
- `syncer.inspect()` already computes per-repo `Ahead`/`Behind`/`Dirty`/`Changed`
  and `Report`/`Result` carry them. #8/#11 are largely **surfacing** this.
- `doctor.buildDoctorReport` validates manifests and lists workspaces/tools/
  guidance but does **not** inspect git freshness yet — #8 adds that step.
- Per-umbrella state lives under `.flux/` (`umbrella.DirName`); the manifest cache
  lives under `~/.local/share/flux/manifests/<name>/`.

## A. Content-type freshness & `doctor --fix` (#8)

Per-content-type update policy (the model the issue asks for):

| Content type | Refresh policy |
|---|---|
| Read-mostly mounts (handbook) | ff-only auto-refresh on TTL when clean; `--fix` ff-only |
| Manifest checkout | ff-only auto-refresh on TTL when clean; derived reconcile on change |
| Product repos | manual/explicit only — never auto-pulled |
| Derived artifacts (skills, guidance) | never independently synced — reconciled from manifest (§B) |

1. **doctor freshness.** Add a git-inspection pass (reuse/extract `syncer.inspect`
   so logic isn't duplicated) to report per-repo `behind=N`, `ahead=N`, dirty,
   and a last-refresh timestamp. Replace bare `synced`/`stale` with
   "`handbook: up to date` / `handbook: 40 behind origin, refreshed 6h ago`".
   **Do not trust stale tracking refs** (Codex): by default **fetch refs first**
   (fetch-only — `git fetch`, no worktree/merge mutation) per inspected repo, then
   compute `behind` against the fresh ref. If the fetch fails (offline/
   unreachable), report `behind=unknown (remote unreachable)` rather than a
   misleading stale count. A `--no-fetch` flag gives a fast, offline, local-only
   view that explicitly labels counts "as of last fetch".
2. **`flux doctor --fix`.** For each detected drift, run the appropriate
   remediation: ff-only pull for **clean** read-mostly mounts + manifest checkout;
   reconcile derived artifacts (§B); **report — never auto-merge** anything dirty
   or diverged, and never touch product repos. `--fix` is explicit and idempotent.
3. **Auto fast-forward on TTL** (operator-chosen). On `flux root` / startup, if a
   clean read-mostly mount or the manifest checkout is older than a TTL
   (default 6h; configurable), do an ff-only pull. Hard guards: ff-only, skip any
   dirty/diverged checkout (fall back to a one-line "behind, run `flux doctor
   --fix`" note), never product repos, and an opt-out (`FLUX_NO_AUTO_REFRESH=1`
   and a `--no-refresh` flag on affected commands). Record last-refresh in
   `.flux/` so the TTL is cheap to check and doesn't pull on every invocation.

## B. Derived-artifact reconcile in `flux sync` (#7)

After the repo reconcile, if the **manifest** or a **guidance fragment** changed
in this run (detectable from `Result.Changed` / a manifest-role result), also
reconcile derived artifacts:

- run the equivalent of `flux skills sync --all` (install new / prune removed), and
- regenerate guidance (`flux onboard`'s guidance step) for `AGENTS.md`/`CLAUDE.md`.

Gating: include for `--scope all` and `--scope manifest`; skip for `products`,
`content`, `local`. Add a `--no-derived` escape hatch. `doctor` reports
**derived-artifact drift** (installed skills / generated guidance out of sync with
the current manifest) even when reconcile stays manual, so it's visible
regardless.

## C. Observable publish + policy gate (#11)

1. **Audit trail (build now).** After any sync/publish, persist the
   `syncer.Report` to `.flux/last-sync.json`: per repo — pushed vs held-back,
   commit SHAs, remote + branch, timestamp, publish policy in force. Surface a
   "last publish" summary in `flux doctor`. This makes a background/automatic
   publish observable instead of invisible.
2. **Policy gate (Codex's call — decided).** A **top-level manifest `sync` policy
   object**, not per-workspace: `sync: { publish_policy: auto|never|pr }` (and
   `refresh_ttl` later if/when manifest config of the TTL is wanted). The CLI
   `--publish` flag overrides per invocation; unset = today's `auto` behavior.
   Rationale: policy governs CLI publish for the whole org/manifest envelope;
   per-workspace is ambiguous for mounts/products and can be added later as an
   override if real demand appears. **Lands in Slice 3** (with proactive refresh),
   not Slice 1 — Slice 1 stays report-only/audit so we prove current hold-back +
   observability before adding policy semantics. Keep it generic (public repo).
3. **Out-of-repo note.** The specific background routine that pushed a catalog
   change in #11's evidence is **not** this CLI — `flux sync --publish auto`
   already holds catalog/manifest back. This repo can only (a) confirm/keep that
   hold-back on the CLI path and (b) add the audit + policy mechanism so any
   automation *built on the CLI* is observable and gate-able. The investigation of
   the private routine belongs in the private workspace repo.

## Staging

- **Slice 1 — report-only (lowest risk):** doctor freshness (A.1) + derived-drift
  reporting (B, report half) + last-publish persistence & surfacing (C.1).
- **Slice 2 — remediation:** `doctor --fix` (A.2) + sync derived-reconcile (B,
  action half).
- **Slice 3 — proactive + policy:** auto-pull-on-TTL (A.3) + publish-policy gate
  (C.2).

Each slice: tests against `examples/acme-workspace`, lockstep `skills/flux/SKILL.md`
+ site docs + CHANGELOG, Claude reviews before commit, no tag/release.

## Open questions

- TTL default (proposed 6h) and where it's configured (env now; `sync.refresh_ttl` later?).
- Exact `.flux/last-sync.json` schema — reuse `syncer.Report` verbatim or a trimmed audit view.
- (resolved) Publish-policy placement = top-level `sync.publish_policy`, Slice 3.
- (resolved) Freshness fetches refs by default; `--no-fetch` for offline/local-only.
