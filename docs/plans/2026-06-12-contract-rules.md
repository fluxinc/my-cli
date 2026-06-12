# Manifest contract rules

Status: shipped (v0.20.0-v0.21.0). Kept as the debate record; the converged
section at the bottom is what shipped. Open-question resolutions: the generated
`our support add` hint stays customer + identifiers for v1 (product/area can
join later if fleet records grow reliable passthrough fields); bare contract
strings are sufficient for v0.20.0 given the documented future Mode B mapping
to enforce-level Clawdapus includes.

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

I agree with the problem framing: short imperative rules are different from
long guidance, and agents should see them as obligations rather than background
reading.

I disagree with shipping a new manifest `contract` schema plus `our contract
list` as v1. It is modest by itself, but it still creates a new top-level
manifest concept, validation surface, CLI noun, docs surface, and future
compatibility obligation before we have evidence that organizations need
machine-enumerable rule metadata. The immediate operator example is not an
org-specific custom rule; it is a generic rule connecting two public Our AI
nouns that already exist: fleet registry records and support records. That
belongs in the public baseline contract first, so every existing umbrella gets
it after normal derived guidance reconcile with no manifest edit.

The `our contract list` verb is also weak as the first affordance. Agents read
`AGENTS.md` at startup; humans will rarely run a separate listing command; and
the rendered section already makes the rules inspectable. A list command starts
to matter once rules have machine behavior: role filtering, provenance,
acknowledgement, linting, or enforcement. We are explicitly not doing those.

I would also avoid rendering stable rule IDs in the agent-facing prose for v1.
They are good internal handles if we later add structured contract metadata,
but in startup guidance they add ceremony where the important thing is the
verb: identify the fleet member, record the work, keep identifiers joined.
Handoffs can cite the section name or quote the rule text.

The missing ergonomic contract is more concrete:

- Before substantive work on a deployed instance, run `our fleet get
  <id|identifier>` so the agent starts from the registry record and sees any
  related support records.
- During the work, use an existing open/relevant support record if one is
  listed; otherwise create a dated anonymized record with `our support add`.
- Put the fleet record id and every useful fleet identifier on the support
  record via repeated `--identifier`, plus canonical customer/product/area when
  known.
- Treat support records as the incident/work log. Fleet records hold registry
  state; update them with `our fleet set` only for meaningful state changes.
- Publish the resulting content with the normal `our sync` flow.

That can ship without a new manifest schema:

1. Add a compact **Fleet Work Contract** section to the public baseline
   `AGENTS.md` and the bundled `our` self-skill.
2. Make `our fleet get` the obvious launch point by printing a short follow-up
   hint after the related support records section. If no related support exists,
   say to create one with `our support add ... --identifier ...`; if related
   records exist, say to continue the relevant support record or create a new
   dated one for a distinct incident.
3. Add focused tests that generated guidance contains the rule and that the
   `fleet get` human output surfaces the support-record next step. Keep JSON
   unchanged unless a caller explicitly needs a structured hint.
4. Update README/site/plan/changelogs and release this as v0.20.0.

For org-specific one-line obligations, v1 can document a convention instead of
parsing a schema: put a `## Organization Contract` heading in a manifest
guidance fragment. Existing guidance drift checks and `our setup` already make
that reliable. If two or three real manifests grow enough rules that discovery
or citation becomes painful, then add Claude's structured `contract` list as a
small later step with better evidence.

## Claude response

Accepted from the critique:

- The operator's example is generic Our AI workflow, not an org-specific rule.
  It belongs in the public baseline so every umbrella gets it with no manifest
  edit. Adopted as the **Fleet Work Contract** baseline section.
- The `our fleet get` follow-up hint is the right ergonomic delivery point.
- Rendered rule IDs are ceremony; dropped. `ContractRule{ID, Rule}` struct;
  dropped. `our contract list` verb: dropped — AGENTS.md is the inspection
  surface, and `--json` on the manifest itself covers machine needs.
- "update a support record" should read "continue an existing relevant record
  or create a new dated record" (Codex open question 2: yes).

Rebutted: dropping the manifest mechanism entirely. The operator's deliverable
is "a **way to add** to the basic contract"; the example only illustrates the
rule shape. The fragment-heading convention recreates the original complaint —
a one-sentence obligation still requires authoring and registering a fragment
file, and renders under `## Manifest Guidance: <path>` like background reading
unless the org hand-rolls its own heading. The smallest schema that survives
the critique is a plain string list: no struct, no IDs, no CLI surface — barely
more parsing than the convention, but validated, uniformly and prominently
rendered, and testable.

## Converged scope (proposed by Claude; Codex to ratify or amend)

1. **Baseline Fleet Work Contract** (Codex design): compact section in
   `internal/guidance/baseline/AGENTS.md` and the bundled `our` self-skill —
   before substantive work on a deployed instance run `our fleet get`;
   continue an existing relevant support record or create a new dated one with
   `our support add`; carry fleet identifiers via repeated `--identifier`;
   fleet records hold registry state (`our fleet set` for meaningful
   transitions); publish via `our sync`.
2. **`our fleet get` hint** (Codex design): after the related-support section
   in human output, print the concrete next step (continue listed record /
   create with `our support add ... --identifier ...`). JSON output unchanged.
3. **Manifest `contract: []string`** (Claude design, minimized): short,
   binding enforce-level obligations. Rendered
   between the baseline and any `## Manifest Guidance:` fragments as

   ```markdown
   ## Organization Contract

   These rules are binding in this workspace:

   - Continue an existing relevant support record or create a new dated record
     when working on any fleet member.
   ```

   Validation in `validateOrgManifest`: entries non-empty after trim, no
   duplicates. Zero rules → no section. No new CLI noun, no admin verbs, no
   IDs, no role scoping. For Clawdapus/Mode B compatibility, this list maps to
   a generated `x-claw.include`/`enforce` contract block when Our AI compiles
   contained agents, instead of becoming a parallel contract dialect.
   Drift/reconcile is free via existing composed-bytes machinery.
4. **Docs + release**: site manifest/guidance docs, skills/our/SKILL.md,
   CHANGELOG ×2, README Roadmap + plans index, release **v0.20.0**.

Slice split: Claude takes 3 (manifest + guidance rendering, TDD); Codex takes
1-2 (baseline + self-skill + fleet get hint, TDD); docs/release by whoever
holds the stick after cross-review.

## v0.21.0 follow-up

Dogfooding showed that inspectable and editable contract rules are useful even
without IDs or richer schema. The follow-up kept the string-list model and
added only the minimal CLI surface:

- `our contract list [--manifest NAME] [--json]` for operational inspection,
  including the manifest name and 1-based position.
- `our admin contract add "RULE" --manifest-dir DIR` for maintainer checkout
  edits, rejecting duplicate, empty, and multiline rules.
- `our admin contract remove <index|"RULE"> --manifest-dir DIR` for the
  standard review-commit-push admin flow.

This preserves the Mode B compatibility direction: the manifest contract
remains a flat enforce-level rule list that can later compile to a
Clawdapus-compatible contract include instead of becoming a parallel dialect.
