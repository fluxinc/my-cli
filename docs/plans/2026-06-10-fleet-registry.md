# Fleet Registry Content Kind

## Status

Converged, implemented in the current branch, and peer-approved by Codex
(adversarial implementation review passed after one fix round: hash-safe
scalar quoting, duplicate-key rejection, and CRLF preservation in
`record.SetScalars`). Codex opened with an
adversarial review (arguing the design under-scopes: a box registry alone
does not replace a fulfillment tracker's workflow). Claude countered with a
registry-first design. The convergence is: build one `fleet` registry entity
for v1, but make the mutation tooling strong enough that it behaves like a
workflow surface rather than a markdown archive. The public tracking issue is
fluxinc/our-ai#15 (a generic registry proposal filed in parallel; its open
questions — noun naming, mount kind vs manifest-declared kind, status
taxonomy — are answered below and in the implementation).

## Goal

Give organizations that deploy product instances at customer sites a durable,
Git-backed registry of those instances — the join table between support
incidents (`our support --identifier`), per-site configuration branches, the
customer catalog, and commercial/fulfillment facts — and make it good enough
to replace a hosted task tracker as the fulfillment system of record.

## The Disagreement

Codex's review (Talking Stick turn 11) holds that one-record-per-box
list/get/search merely "reproduces tracker fields in markdown" and that the
design needs three entities — durable assets, fulfillment orders/jobs/events,
and support incidents — with subtrees like `assets/`, `orders/`, `events/`,
plus atomic transition verbs and unresolved-work views.

Claude's counter: the third entity already shipped (support), and the other
two collapse into one for the workload this serves. The expansion to
orders-and-events is concept debt in the other direction — modeling a ticket
system we just decided to leave.

## Claude's Verdicts on the Five Questions

### 1. Record taxonomy — one registry entity, not three

- **Orders fold into the instance record.** The org's own tracker history is
  empirically ~1:1 task-to-box (174 tasks → 169 records in the worked
  export). An order that never produces a deployed instance is sales
  pipeline — out of scope. A repeat order against an existing instance is
  rare enough to be frontmatter (`identifiers` gains the new order number) or
  a new record when hardware is actually replaced. Build the `orders/`
  subtree only when a real N:1 case appears; the schema loses nothing by
  waiting.
- **Events are git history.** A registry record mutated in place gets its
  event log for free: every status transition is a commit. Storing event
  files would duplicate `git log` with worse tooling. If a queryable view is
  wanted later, `our fleet log <id>` can surface the record's git history —
  a read verb, not a storage format.
- **Due dates, assignment, queues** are frontmatter keys filtered generically
  (see `--where` below), not schema. Payment *facts* live in the
  `commercial:` block; payment *workflow* (invoicing, AR) stays in accounting
  tooling — absorbing it would be scope creep with no agent payoff.

### 2. CLI verbs — the slice that beats the tracker

```sh
our fleet list   [--status S] [--customer C] [--partner P] [--identifier I]
                 [--branch B] [--where key=value]... [--json]
our fleet search <text> [same filters] [--json]
our fleet get    <id|identifier> [--json]
our fleet add    <id> [field flags] [--print] [--json]
our fleet set    <id> key=value... [--json]
```

- The killer verb is `our fleet get <any-identifier>`: hostname, sales
  order, functional location, or serial in — full identity out (customer,
  partner, config branch, status, contacts), **plus related support records**
  by identifier intersection. That is the join that previously took an
  expedition across naming conventions and tracker custom fields, answered
  offline in milliseconds.
- `our fleet set` is the workflow verb: atomic field updates
  (`our fleet set acme-box-1 status=live`) that rewrite frontmatter while
  **preserving unknown keys and the body**. This replaces dragging a card
  across a board. It is operational, not admin — registry records are mount
  content in the same trust domain as meetings and support notes.
- Because event provenance is git history, `set` must make that history
  legible: it should report the exact field changes and print a suggested
  follow-up such as
  `our sync --message "Update fleet acme-box-1: status=live"`. Guidance
  should prefer one logical transition per sync when the operator wants an
  auditable workflow history. Generic `Sync Our AI content` commits are
  acceptable for bulk cleanup, not for meaningful status transitions.
- `--where key=value` gives typed-flag ergonomics for the org-specific long
  tail (assignment, due dates, anything) without baking org schema into the
  public CLI. Typed flags cover only the join/filter core.
- Status board equivalent: `our fleet list --status build`. At registry
  volumes (tens to low hundreds of records), a filtered list beats a board.

### 3. Sync, concurrency, publish

- New mount kind `fleet`; default sync content path `fleet/`. It does
  **not** join the handbook default content paths — the registry's value is
  a separately scoped repo (remote-access and commercial fields want a
  tighter boundary than the handbook). A handbook-resident `fleet/` remains
  possible via explicit `include_paths`.
- Publish stays `auto` for v1, same reasoning as support: private mount,
  content-only direct push, protection is at write time. Per-mount
  `publish_policy` remains the documented future lever.
- Mutate-in-place raises conflict odds relative to append-only journals, but
  at a few transitions per month the existing pull-then-push sync flow is
  enough. Document "run `our sync` before and after `our fleet set`" in
  guidance; build nothing.
- Notifications on status change: acknowledged real gap vs a hosted tracker;
  explicitly deferred (a watch routine later, not a CLI concern now).
- Importer story: **not public CLI surface.** Bulk migration is write files +
  commit; the scaffold verb covers steady-state creation.

## Codex Convergence Response

1. **Taxonomy:** accept one registry entity for v1. The 174-task → 169-record
   export is enough evidence that `orders/` would model hypothetical shape,
   not the current workload. Add `orders/` later only when one deployed
   instance has multiple independent active commercial/fulfillment lifecycles
   that cannot be represented as identifiers plus frontmatter.
2. **Events:** accept no event files for v1, with the tooling condition above.
   If git history is the event log, `fleet set` has to shape the operator's
   next sync message and preserve unknown frontmatter so that transitions are
   both safe and readable later.
3. **Record engine:** extract `internal/record` before adding `fleet`, ideally
   in a separate no-behavior-change commit followed by the fleet noun. The
   frontmatter-preserving writer is the real new mechanism; adding a third
   copy of meetings/support would make that harder to review.
4. **Support join:** agree that `fleet get <identifier>` is the primary join
   surface and that `support add --identifier` should warn, not fail, on
   unknown identifiers. Support records keep narrative and identifiers only;
   registry facts remain in fleet and are computed at read time.

### 4. What stays out of support

Unchanged and sharpened: support records carry incident narrative plus
`identifiers` — never registry facts (status, branch, commercial,
remote-access). The join is computed at read time by identifier
intersection, in both directions (`our fleet get` shows related support
records; a future `our support get` enrichment can show the instance).
`our support add --identifier` gains a soft resolution check against the
registry when a fleet mount is present: unknown identifier → warning, not
failure (the registry may legitimately lag reality).

### 5. Naming — `fleet`, not `fulfillment`

Fulfillment is a process phase; the record outlives it by years, and most
reads (support joins, branch⇄instance⇄customer lookups) happen long after
fulfillment ends. Naming the noun after its first month optimizes for the
wrong half of its life. "Fulfillment" also frames the feature as a task
tracker, which invites rebuilding one. Records live flat at `fleet/<id>.md`
— one entity, one directory; no `boxes/` subtree (org slang) and no
`assets/` subtree until a second entity exists.

## Registry vs Journal

The structural insight under all of the above: `our` now has two record
lifecycles.

| | journal (meetings, support) | registry (fleet) |
|---|---|---|
| key | `YYYY-MM-DD-<slug>` | stable `<id>` |
| mutation | append-only; recurrence = new record | updated in place |
| sort | date desc | id / status |
| history | the records themselves | git log of the record |
| verbs | list/search/get/add | list/search/get/add/**set** |

`fleet` is the first registry noun. The CLI should model the distinction
explicitly rather than pretend fleet is another journal (no `--since`, no
date-prefixed filenames).

## Record Shape

One record per deployed instance, `fleet/<id>.md`. Typed core grounded in
the fields a device-deploying org actually joins on; everything else is
preserved passthrough.

```markdown
---
id: acme-box-1                    # stable key; convention: hostname/node name
customer: sampleco.example.com    # canonical `our customers` ID
partner: samplepartner            # optional resale/service partner
status: live                      # org-defined vocabulary; free-form in v1
device: "Sample Scanner X"
serial: SN-0001
identifiers: ["SO 100045", "PO 200031", "FL 400-123401", "SN-0001"]
config_repo: acme/sample-configs
config_branch: partner/site-1
deployed_site: "Springfield"      # where it runs
ship_to: "Centerville"            # where it shipped; freight ≠ deployment
contacts: ["IT: ops@sampleco.example.com"]
install_date: 2026-06-01
access:                           # remote-access facts; CLI passthrough
  node: acme-box-1
  ip: 10.0.0.12
commercial:                       # ACL-sensitive; CLI passthrough,
  revenue: 0                      # excluded from default list columns
  payment_due: 2026-07-01
source: fleet
---

# acme-box-1

Free-form notes. Incident history belongs in support records; state history
belongs in git log — this body should not accrete either.
```

- `status` vocabulary is org-defined (guidance carries it); the public CLI
  validates non-empty only. A manifest-declared vocabulary is a future
  option, not v1.
- `identifiers` is the same join currency as support records: sales order,
  purchase order, invoice, functional location, serial, asset tag — capture
  everything available.
- `deployed_site` vs `ship_to` is deliberately two fields; conflating them
  is a known failure mode (shipments via freight forwarders).

## Implementation: extract the record engine first

`internal/meetings` (594 lines) and `internal/support` (660 lines) are
near-identical. Landing fleet as a third copy locks in ~1,800 lines of
triplicated scan/frontmatter/filter/search/scaffold code. This branch should:

1. Extract `internal/record`: roots, scan, frontmatter parse/write, token-AND
   search with qmd-first contract, snippet, filter primitives, scaffold
   helpers — parameterized by a noun definition (directory, typed fields,
   journal vs registry keying).
2. Re-express `meetings` and `support` as thin definitions over it (no
   behavior change; existing tests prove it).
3. Add `fleet` as the first registry-mode definition, including the
   frontmatter-preserving rewrite needed by `our fleet set`.

The frontmatter rewrite (preserve unknown keys, comments aside, body intact)
is the only genuinely new machinery and deserves the most test attention.

## Out of Scope (v1)

- `orders/` and `events/` subtrees (triggers documented above).
- Payment/invoicing workflow.
- Notifications on status change.
- Per-mount publish policy.
- Tracker importers in the public CLI.
- Liveness probing (deriving `status` from network presence) — migration
  tooling, org-private.

## Resolution

All four convergence questions were accepted by Codex (see the Codex
Convergence Response above) and are implemented:

1. One registry entity; `orders/`/`events/` deferred with documented triggers.
2. `our fleet set` reports exact field changes and suggests
   `our sync --message "Update fleet <id>: status=live"` (the flag already
   exists on `our sync`), so transitions stay readable git history.
3. `internal/record` was extracted first; `meetings` and `support` are thin
   noun definitions over it with no behavior change, and `fleet` is the first
   registry-mode noun. The frontmatter-preserving rewrite lives in the engine
   as `record.SetScalars` with focused tests.
4. `our fleet get <id|identifier>` is the join surface and lists related
   support records; `our support add --identifier` warns (never blocks) on
   identifiers unknown to a populated registry.
