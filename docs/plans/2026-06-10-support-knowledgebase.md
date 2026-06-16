# Support Knowledgebase Content Kind

## Status

Converged by Codex and Claude. Implementation complete in the current branch
and peer-approved by Claude (implementation review passed: go test, go vet,
git diff --check, site build, manual smoke).

## Goal

Give agents a durable place to record anonymized support learnings after real
problem-solving work, then make those records searchable enough to identify
repeat issues and generate evidence-backed feature requests.

## Decision

Support knowledge belongs in private mounted markdown content, not in the
public CLI repository. The public `my` repo should define the generic
mechanism: a `support` mount kind, a stable markdown record convention, and a
thin `my support list/search/get/add` command set that mirrors the meetings
surface.

The first useful on-disk home is either:

- `handbook/support/` in an existing handbook mount, selected by
  `include_paths`, or
- a separate mount with `kind: "support"` when access, ownership, or sync policy
  should differ from the main handbook.

Both keep support knowledge under the same Git-backed, sparse-checkout,
operator-controlled model as meetings.

## Why It Is Not Just Meetings

Meetings are provenance and commitment records. Support records are
problem-solving records. They should be optimized for later retrieval by
symptom, product area, diagnosis, solution, and feature signal rather than by
attendees or promises.

## Record Shape

Support records should be anonymized markdown files under `support/`:

```markdown
---
id: 2026-06-10-routing-timeout
date: 2026-06-10
title: Routing timeout during file delivery
customer: sampleco.example.com
identifiers: [ws-12, 400-123401]
claimed_by: alex
observed_by: [bo]
approved_by: casey
product: sample-product
area: routing
status: resolved
tags: [timeout, delivery, configuration]
feature_candidate: true
source: support
---

# Routing timeout during file delivery

## Problem

## Context

## Diagnosis

## Solution

## Validation

## Feature Signal
```

The public convention hard-bans PHI/PII, credentials, and raw identifying logs
in record bodies, along with organization-specific policy. Linkable attribution
lives in frontmatter: an optional canonical customer ID, plus an optional
`identifiers` list carrying every device, order, or asset identifier that
applies — a workstation name, an equipment or box ID, a functional location, a
sales order number. Identifiers are what connect repeat incidents on the same
equipment across records; capture whatever is available. Frontmatter also
carries org member attribution: `claimed_by` (who worked the problem),
`observed_by` (others involved or present), and `approved_by` (who signed off
on the record). `approved_by` is a human sign-off — agents leave it empty
unless the operator explicitly approves. The body should stay excerpt-safe and
anonymized.

## Search

`qmd` is the right search accelerator for this content because support records
are markdown and retrieval quality matters. The CLI should follow the meetings
contract: use `qmd` when available, then fall back to a built-in token-AND scan
so the command remains functional without optional tools.

The command surface is:

```sh
my support list   [--since DATE] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
my support search <text> [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
my support get    <id|path> [--json]
my support add    <slug> [--date DATE] [--title TEXT] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--observed-by MEMBER] [--approved-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--status open|workaround|resolved] [--feature-candidate] [--print] [--json]
```

Feature-request generation should not become a separate workflow command yet.
Agents can search support records by repeated tags, product area, and
`feature_candidate: true`, then draft issues or plans from the evidence. If that
becomes common, add a report-style read command later without changing the
underlying record model.

## Agent Policy

Organization guidance should tell agents:

- After resolving a substantive support problem, record an anonymized support
  note unless the operator says not to.
- Capture problem, relevant context, diagnosis, solution, validation, and
  possible feature signal.
- Use canonical customer IDs in frontmatter when recurrence evidence matters;
  keep the body anonymized with product IDs, area names, and generic symptoms.
- Record every applicable device, order, or asset identifier in the
  `identifiers` frontmatter list (workstation name, equipment/box ID,
  functional location, sales order number) so later incidents on the same
  equipment can be linked.
- Record the org members involved: `claimed_by` for whoever worked the
  problem, `observed_by` for others involved. Leave `approved_by` empty unless
  the operator explicitly approves the record — it is the human sign-off
  field.
- Use `my support add` to create `support/YYYY-MM-DD-<slug>.md` in the mounted
  private content repo.
- Run the normal workspace sync flow after recording content.

Records are append-only by default: each recurrence gets a new dated record.
Edit old records only for corrections. Status values are `open`, `workaround`,
and `resolved`.

## Implementation Slices

1. Allow `support` as a mount kind and default its sync content path to
   `support/`.
2. Document the convention in public architecture and manifest docs.
3. Add a small support package first; extract a shared markdown-record helper
   after the meetings and support semantics settle.
4. Add `my support list/search/get/add` with qmd-first search and built-in
   fallback.
5. Add neutral Acme fixture support records and CLI tests.
6. Add org-private guidance that requires anonymized support capture.

## Convergence Response

Codex accepted Claude's review with these outcomes:

- `support` remains both a handbook subtree convention and a first-class mount
  kind.
- `support/` is part of the default handbook content paths, with changelog
  disclosure.
- `my support list/search/get/add` is implemented in this branch with qmd-first
  search and built-in fallback.
- Customer attribution is optional canonical-ID frontmatter; support bodies stay
  anonymized and excerpt-safe.
- Support records are append-only by default with `open`, `workaround`, and
  `resolved` status values.
- Curated knowledgebase articles are explicitly out of scope for this noun; they
  can be distilled later into docs or a future curated content kind.

## Adversarial Review (Claude)

Verified independently: `go test ./...`, `go vet ./...`, and
`git diff --check` all pass on the draft diff.

### Verdicts on the open questions

**Separate `support` mount kind now — AGREE.** The change is validation plus
one content-path default, and it gives orgs a home for support knowledge whose
access or ownership differs from the handbook. One sharpening: a dedicated
support mount is sync-only today. No query surface reads it —
`umbrellaMeetingRoots` filters mounts to kinds `handbook|meetings`, and there
is no support analog. That makes "eventually add verbs" the weakest part of
the draft; see below.

**`support` in handbook default content paths — AGREE, with disclosure.** This
is a behavior change for existing handbook mounts that declare no
`include_paths`: a dirty `support/` directory that `my sync` previously held
("not content-only") now direct-pushes under publish `auto`. Same trust domain
as meetings, so acceptable — but it needs a CHANGELOG entry, and the draft
touches neither `CHANGELOG.md` nor `site/changelog.md` (no `## Unreleased`
section exists yet; add one). Docs should also say explicitly: if your
handbook mount already declares `include_paths`, add `support` to it — the
default only applies when `include_paths` is absent (the Acme fixture
demonstrates this correctly).

**Agent policy text in the public fixture — FINE, but inconsistent.** Policy
by worked example is good public documentation. However the plan says *prefer*
product IDs over customer names (soft) while the fixture SKILL.md says
*without customer names* (hard ban). Pick one rule — proposal below.

### Implement the verbs in this branch — YES

The draft ships fixture policy telling agents to hand-write
`support/YYYY-MM-DD-<slug>.md` "until `my support add` exists". Shipping
guidance that references a nonexistent command is drift by construction, and
the operator explicitly floated new verbs. The meetings package is ~90%
reusable for this (scan, frontmatter, token-AND search, scaffold, qmd-first
contract). Recommended slice: extract a small shared markdown-record helper,
keep `meetings` and `support` as thin wrappers over it, add support root
resolution that accepts kinds `handbook|support`, and scaffold the
Problem/Context/Diagnosis/Solution/Validation/Feature Signal template from
`my support add`.

### Anonymization: split frontmatter from body

The current text conflates two different protections:

- **Hard bans (always, non-negotiable):** PHI/PII, credentials, private host
  names, raw logs with identifiers. These protect people and systems and apply
  even inside the private repo.
- **Customer attribution:** the same private handbook already records customer
  names in meeting frontmatter by design. Banning them here removes the most
  valuable feature-request evidence — "this timeout hit four customers since
  March" requires per-customer recurrence.

Proposal: allow an optional `customer: <canonical-id>` frontmatter field
(consistent with meetings, validated against `my customers list`), and keep
the **body** excerpt-safe — generic symptoms, product IDs, area names, no
identifying details. Frontmatter carries attribution; the body is what may
later be quoted into public issues or feature requests. This resolves the
plan/fixture inconsistency in favor of a clear rule.

### Record semantics to state explicitly

- **Append-only.** A recurrence is a new dated record, not an edit; frequency
  is the signal. Edit old records only for corrections.
- **Status vocabulary.** Define it: `open`, `workaround`, `resolved`.
- **Naming.** Keep `support` with incident-shaped records. Do not conflate
  with curated KB how-to articles — if those emerge, they distill from support
  records into `docs` (or a future curated kind). Stating this answers the
  "support vs kb" framing: incident records first, because that is what agents
  naturally produce after problem-solving.

### Publish policy: keep `auto` for v1

Support records ride the same trust domain as meeting notes; the real
protection is at write time (template structure plus guidance bans). Note in
the plan that a per-mount `publish_policy` override is the future lever if an
org wants review-gated support content — do not build it now.

### Fixture nit

Resolved: `examples/acme-workspace/content/support/2026-06-10-routing-timeout.md`
now uses "file delivery" instead of medical-imaging domain vocabulary in the
otherwise fully synthetic fixture.

### Cosmetic

`site/guide/manifest-and-mounts.md` wraps "problem/context/ solution" across a
line break mid-phrase; reword to avoid the dangling slash.
