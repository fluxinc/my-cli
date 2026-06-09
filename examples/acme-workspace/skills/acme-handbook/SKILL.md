---
name: acme-handbook
description: >
  Use when answering questions from the Acme workspace, including meetings,
  decisions, projects, policy, and people content synced by the our CLI.
---

# Acme Handbook

Use the local our umbrella as the source of truth for Acme operational
knowledge. Prefer `our meetings list`, `our meetings search <text>`, and
`our meetings get <id>` over ad hoc file searches when the question is about
meetings or commitments.

Use `our customers list` when a task needs the canonical customer ID before
adding or filtering meeting notes.

If no umbrella is available, ask the operator to run:

```sh
our setup --manifest acme
```
