# Architecture decision records

Short, durable records of **decisions** — the choice made, the forces behind it,
and the alternatives rejected — newest first. An ADR captures *why* a direction
was chosen and is not rewritten as the world changes; when a decision is
reversed, a new ADR supersedes the old one and the old one's status is updated.

ADRs precede planning here: an accepted ADR fixes the direction, then a
date-prefixed entry under [`docs/plans/`](../plans/README.md) carries the
implementation roadmap and tracks shipped/active/superseded status. Plans say
*how and when*; ADRs say *what and why*.

Statuses: **proposed** (under discussion), **accepted** (the direction is
fixed; implementation may be pending), **superseded** (a later ADR wins on
conflict; the successor is named).

| ADR | Status | Notes |
|---|---|---|
| [0003-launch-evidence-and-work-tracking](0003-launch-evidence-and-work-tracking.md) | proposed | Goals as live commitments belong to a service the organization binds; `my` records local, lock-guarded evidence of each `my ai` launch and claims nothing beyond what it observed. The section 9 non-goals stand. Number 0002 is reserved for the service-backed fleet record in review on another branch. Implementation plan: [2026-11-09-launch-attempts](../plans/2026-11-09-launch-attempts.md). |
| [0001-launch-scoped-skill-composition](0001-launch-scoped-skill-composition.md) | accepted | `my ai` composes a per-launch skill **profile** as disposable derived state in the launch root; no user-level org installs for launch-root-capable harnesses; `.agents/skills` is the cross-harness center with per-harness mirrors only where a launch-root seam is proven. Skill scope != context scope. Implementation plan: [2026-06-14-launch-scoped-skill-composition](../plans/2026-06-14-launch-scoped-skill-composition.md). |
