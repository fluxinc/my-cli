# Manifest contract rules

Status: debate — Claude position drafted, awaiting Codex critique. Both agents
amend this file in place; the converged section at the bottom wins.

## Goal (operator)

Design, debate, converge on, implement, review, test, and release a way to add
to the basic `our` contract — e.g. "Always create and update a support record
when working on any fleet member." Explicit constraint: **don't
over-complicate.**

## Problem

The "basic contract" is the operating orientation in the generated `AGENTS.md`
(public baseline in `internal/guidance/baseline/AGENTS.md`). Organizations can
extend guidance today only via `agent_guidance.paths` — whole markdown
fragment files appended as `## Manifest Guidance: <path>` sections. For short
imperative rules that is the wrong shape:

- A one-sentence rule requires creating and registering a file.
- Rules end up buried in prose; nothing marks them as binding rather than
  background reading.
- There is no way to enumerate the rules in force (`--json` or otherwise), so
  harnesses and humans cannot inspect or reference them individually.

## Claude position (v1)

Add a structured, org-wide rule list to the manifest. Nothing else.

### Manifest

```json
"contract": [
  {
    "id": "support-record-on-fleet-work",
    "rule": "Always create and update a support record when working on any fleet member."
  }
]
```

- `Contract []ContractRule` on `manifest.Document`; `ContractRule{ID, Rule string}`.
- Validation (wired into `validateOrgManifest`): IDs required, kebab-case-ish
  (same identifier rule as services/roles), unique; `rule` non-empty
  single-paragraph text.

### Rendering

`ComposeWithOptions` (internal/guidance/guidance.go) renders a dedicated
section **immediately after the baseline**, before any `## Manifest Guidance:`
fragments:

```markdown
## Organization Contract

These rules are binding for all agents in this workspace:

- (support-record-on-fleet-work) Always create and update a support record
  when working on any fleet member.
```

Stable IDs in the rendering let handoffs and reviews cite "per contract rule
`support-record-on-fleet-work`". Zero rules → no section (no litter).
Existing drift machinery (Check/Ensure, doctor, `our sync` derived reconcile)
picks this up for free because the contract changes composed bytes.

### Inspection

`our contract list [--json]` — operational, read-only, lists id + rule per
registered manifest (manifest-prefixed like other nouns). No `get` (rules are
one-liners; list shows everything). No `add/edit` admin verbs in v1 — the
existing admin path for guidance (edit manifest JSON, sync) applies; verbs can
come later if editing friction proves real.

### Explicitly out of scope (v1)

- **Enforcement.** No doctor checks that a support record was actually
  created, no hooks. The contract is guidance-level; harnesses follow it the
  way they follow the rest of AGENTS.md.
- **Role scoping.** Rules are org-wide. Roles already filter guidance_paths;
  if per-role rules prove needed, a `contract` grant slot on Role is a
  compatible later addition.
- **Severity/levels/categories.** One flat list. Add taxonomy only when a
  real manifest needs it.

### Why not alternatives

- **Status quo (guidance fragments):** works for essays, wrong shape for
  rules; not enumerable; nothing marks rules as binding.
- **Skill-based ("install an org-rules skill"):** skills are capabilities,
  not obligations; also per-harness install state, while the contract must be
  unconditional workspace context.
- **Enforcement engine:** violates "don't over-complicate"; rules like the
  example need judgment ("working on a fleet member") that mechanical checks
  cannot decide.

### Slices

1. Manifest schema + validation + tests (`internal/manifest`).
2. Guidance rendering + tests (`internal/guidance`), example workspace fixture.
3. `our contract list` + tests, baseline AGENTS.md mention, docs
   (site cli-reference + the-model), skills/our/SKILL.md, CHANGELOG ×2.
4. Cross-review, live smoke in /tmp sandbox, release vNEXT.

## Codex critique

_(yours)_

## Converged scope

_(filled after debate)_
