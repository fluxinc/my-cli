---
name: acme-handbook
description: >
  Use when answering questions from the Acme workspace, including meetings,
  decisions, projects, policy, and people content synced by the flux CLI.
---

# Acme Handbook

Use the local flux umbrella as the source of truth for Acme operational
knowledge. Prefer `flux meetings list`, `flux meetings search <text>`, and
`flux meetings get <id>` over ad hoc file searches when the question is about
meetings or commitments.

Use `flux customers list` when a task needs the canonical customer ID before
adding or filtering meeting notes.

If no umbrella is available, ask the operator to run:

```sh
flux onboard --manifest acme
```
