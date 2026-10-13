# Records: Customers, Meetings, Support, Fleet

Workspace content is markdown records with frontmatter, living in mounted
content repos. Customer identities are read from mounted `customers/*.md`
records, and each record domain exposes write-oriented commands. `my sync
--push` publishes adopted changes.

```sh
my customers list [--json]
my customers list --identity --json
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
my fleet search <text> [--no-semantic]
my fleet get <id|identifier|path>
my fleet add <id> [--customer ID] [--status TEXT] [--device TEXT] [--serial TEXT] [--identifier ID]...
my fleet set <id> KEY=VALUE... [--confirm ID]...
my fleet transition <id> <operation> [KEY=VALUE...]   # unreleased
my fleet index [--rebuild]                            # unreleased
```

Bare `my meetings` and `my support` use their safest useful action: they list
records and accept the same filters as `list`. Subcommands remain available
for search, retrieval, and creation.

When the `qmd` tool is installed, `search` uses it for higher-quality
retrieval; otherwise a built-in scan applies. Single keywords match best.
Service-backed fleet records are searched by the service itself, plus the
private index described under [Service-backed fleet](#service-backed-fleet).

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

`my customers list --identity --json > customer-identities.json` generates a
deterministic, least-privilege artifact for detached repository CI. The artifact
contains only canonical IDs, domains, aliases, and source revision/freshness
metadata; it omits names, partner links, note bodies, and local paths. Generate
it where the customer mount is available, then provide only the JSON artifact
to the detached job. Generation fails distinctly when the source is
unavailable, dirty, or out of sync, so CI can distinguish an empty identity set
from an untrustworthy projection.

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

For markdown records, record workflow transitions with
`my fleet set <id> status=<value>`, then publish with the suggested
`my sync --push --message` command so each transition is a readable git commit.
For service-backed records, use `my fleet transition`: the change is live in
the service and there is nothing to publish.

The built-in fleet work contract ties these together — see
[Guidance and Contract](./guidance-and-contract.md).

### Service-backed fleet

::: warning Unreleased
This section describes `master`. The latest release (v0.42.0) does not have
`my fleet transition`, `my fleet index`, `--confirm`, `--no-semantic` or
service-backed fleet; they arrive with the next release.
:::

When an organization's registry lives in a service with an API, the manifest
binds fleet to it. Records of other kinds can stay in a mount:

```json
"services": [{
  "id": "registry", "kind": "http", "purpose": "Instance registry",
  "connection": { "type": "http", "url": "https://registry.example.test/api/v1" },
  "auth_ref": "op://Example/registry-api/token"
}],
"data_bindings": {
  "fleet": { "surfaces": ["service:registry", "mount:workspace"],
             "field_aliases": { "device": "model" } }
}
```

Then `my fleet` reads and writes the service live, through the
`records-http/v1` protocol:

- **`get`**: the reference resolves in the service and in the mount.
  - One match is shown with its notes and provenance.
  - Several matches, on either surface, are listed and the command exits non-zero.
  - Related support records match every identifier the live record carries.
- **`list` and `search`**: merge both surfaces.
- **`set`**: sends every `KEY=VALUE` in one request, against the revisions just read. The service saves all of them or none. A conflict, a refused field or a change that needs confirmation is reported as such.
  - Nothing is left to publish.
  - A change the service shares across other records (one order number on several instances) is refused at first, listing them. Run the same `set` again with one `--confirm <id>` per listed record (the refusal prints the exact, shell-quoted command) to apply it to exactly those records.
  - `field_aliases` maps fleet names to the service's names.
- **`transition`**: `my fleet transition <id> <operation> [KEY=VALUE...]` changes workflow state. `set status=` is refused for a service record.
- **`add`**: creates markdown only, and needs `--workspace` once fleet is service-backed.
- **Pointers**: a markdown record with `managed_by: service:registry` (and optionally `managed_id`) points to the service. It is hidden from lists, `get` follows it, and its old values are never shown as current.
- **Failures**: if the service cannot answer, the command fails and names it. No local copy stands in.
- **Semantic search**: `my fleet index` builds a private, per-user index of the service's records, kept in `~/.local/state/my-cli/records/` and used by qmd in isolation from your own qmd index and configuration (qmd's downloaded models are reused read-only). `my fleet search` then also finds records by meaning.
  - Every match is re-read from the service and shown with its current values, never cached text.
  - Deleted and archived records drop out.
  - Before each search the index catches up from the service's change feed (a few pages). If it is further behind, if qmd is missing, or if it was never built, the search says so and shows the service's own results only.
  - `--no-semantic` skips it.
- **`--where`**: markdown records match on any frontmatter field; a service record answers only for the registry fields it carries (`status`, `customer`, `partner`, `device`, `serial`, `config_repo`, `config_branch`, `deployed_site`, `ship_to`, `install_date`).
- **Connection**: the service's `connection.url` is its API base: https, or http on loopback only, with no credentials, query or fragment. The protocol is described in the [service-backed fleet plan](https://github.com/fluxinc/my-cli/blob/master/docs/plans/2026-10-12-service-backed-fleet.md).
- **Credentials**: `auth_ref` (`env://`, `op://` or `none`; `broker://` is not supported here) is resolved per invocation, sent only to the service's own origin, and never printed.

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

Pending session work only holds a base publish or land when the changed file
paths overlap. Unrelated base and session records can proceed independently;
if Git cannot prove the path sets, My AI keeps the existing fail-closed hold.
