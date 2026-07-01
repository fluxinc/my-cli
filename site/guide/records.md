# Records: Customers, Meetings, Support, Fleet

Workspace content is markdown records with frontmatter, living in mounted
content repos. Customer identities are read from mounted `customers/*.md`
records, and each record domain exposes write-oriented commands. `my sync
--push` publishes adopted changes.

```sh
my customers list [--json]
my customers add <domain|slug> [--name TEXT] [--domain DOMAIN] [--domain-confirmed] [--alias TEXT] [--partner ID]

my meetings list [--since DATE] [--customer ID] [--partner ID] [--json]
my meetings search <text>
my meetings get <id|path>
my meetings add <slug> [--date DATE] [--title TEXT] [--customer ID] [--attendees NAME] [--partner ID]

my support list [--since DATE] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
my support search <text>
my support get <id|path>
my support add <slug> [--customer ID] [--identifier ID]... [--claimed-by MEMBER] [--observed-by MEMBER]... [--product ID] [--area TEXT] [--status open|workaround|resolved]

my fleet list [--status TEXT] [--customer ID] [--identifier ID] [--branch NAME] [--where KEY=VALUE] [--json]
my fleet search <text>
my fleet get <id|identifier|path>
my fleet add <id> [--customer ID] [--status TEXT] [--device TEXT] [--serial TEXT] [--identifier ID]...
my fleet set <id> KEY=VALUE...
```

When the `qmd` tool is installed, `search` uses it for higher-quality
retrieval; otherwise a built-in scan applies. Single keywords match best.

## Customer records

Customer identities live under `customers/*.md` in a mounted content repo, not
in the manifest. Use `my customers add <domain|slug>` to scaffold one. A
minimal record is:

```md
---
id: sampleco.example.com
name: SampleCo
domain: sampleco.example.com
domain_confirmed: true
aliases:
  - sampleco
  - sc
partners:
  - integratorco
---

# SampleCo
```

`my customers list` reads these records, and customer filters accept IDs,
domains, names, and aliases.

## Meetings

A meeting note is a dated journal entry: what was discussed, decided, and
assigned. `my meetings add <slug>` scaffolds the record in the meetings
content root; a slug that starts with `YYYY-MM-DD` sets the date. Attendees
and partners are repeatable flags, each occurrence one literal value.

## Support records

A support record is an anonymized problem-to-solution story: problem, context,
solution, validation. The body stays anonymized; linkable attribution lives in
frontmatter:

- `--customer` — the canonical customer ID (see `my customers list`).
- `--identifier`, repeatable — every device, order, workstation, or asset
  identifier that applies, so recurrence on the same equipment is
  discoverable later.
- `--claimed-by` — the org member who worked it; `--observed-by`, repeatable,
  for others involved.
- `approved_by` stays empty unless the operator explicitly signs off.

## Fleet records

The fleet is a registry, not a journal: one record per deployed instance,
keyed by a stable id, updated in place. State history is the record's git log.

`my fleet get` resolves *any* identifier — sales order, functional location,
serial, hostname — and lists related support records found by shared
identifiers. Its output ends with the concrete next step: a seeded
`my support add` command carrying the customer and every identifier, ready to
run when no relevant record exists yet.

Record workflow transitions with `my fleet set <id> status=<value>`, then
publish with the suggested `my sync --push --message` command so each transition is
a readable git commit.

The built-in fleet work contract ties these together — see
[Guidance and Contract](./guidance-and-contract.md).

## Adoption: why your file did not publish

`my sync --push` only publishes content it knows is intentional. Records created
by the CLI are adopted automatically (Git intent-to-add). A file you created by
hand stays held until you adopt it:

```sh
my record adopt <path>
```

This is the difference between "scratch file that happens to be in a content
mount" and "record the organization should see."

## Sessions and records

When your current directory is inside an active session (`sessions/<id>`),
record commands write to that session's mount worktree instead of the base
mount — session work does not leak until you finish the session. See
[Sessions](./sessions.md).
