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
| [0001-launch-scoped-skill-composition](0001-launch-scoped-skill-composition.md) | accepted | `our ai` composes a per-launch skill **profile** as disposable derived state in the launch root; no user-level org installs for launch-root-capable harnesses; `.agents/skills` is the cross-harness center with per-harness mirrors only where a launch-root seam is proven. Skill scope != context scope. Implementation plan: [2026-06-14-launch-scoped-skill-composition](../plans/2026-06-14-launch-scoped-skill-composition.md). |
