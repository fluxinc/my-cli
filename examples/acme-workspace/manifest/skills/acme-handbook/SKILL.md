---
name: acme-handbook
description: >
  Use when answering questions from the Acme workspace, including meetings,
  customer identity records, support notes, decisions, projects, policy, and
  people content synced by the our CLI.
---

# Acme Handbook

Use the local our umbrella as the source of truth for Acme operational
knowledge. Prefer `our meetings list`, `our meetings search <text>`, and
`our meetings get <id>` over ad hoc file searches when the question is about
meetings or commitments.

Use `our customers list` when a task needs the canonical customer ID before
adding or filtering meeting notes.

When a substantive support problem is resolved, record an anonymized support
note under the mounted handbook's `support/` directory unless the operator says
not to. Capture the problem, context, diagnosis, solution, validation, and any
feature signal. Use canonical customer IDs in frontmatter when recurrence
evidence matters, and record every applicable device, order, or asset
identifier in the `identifiers` frontmatter list — a workstation name, an
equipment ID, a functional location, a sales order number — so later incidents
on the same equipment can be linked. Record the org members involved in
frontmatter as well: `claimed_by` for whoever worked the problem and
`observed_by` for others involved; leave `approved_by` empty unless the
operator explicitly approves the record. Keep the body free of customer names,
credentials, raw identifying logs, and personal data.

Use `our fleet get <id-or-identifier>` to resolve a deployed instance — a
hostname, sales order, functional location, or serial all work — before
linking support records or editing site configuration. When an instance's
workflow state changes, update it with `our fleet set <id> status=<value>` and
publish the transition with the suggested `our sync --message` command so the
registry's git history stays a readable event log.

If no umbrella is available, ask the operator to run:

```sh
our setup --manifest acme
```
