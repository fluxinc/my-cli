# Data Types over Data Surfaces

Status: active, 2026-06-13. Slice 1 shipped in v0.22.0; later slices remain
draft. Supersedes the earlier RBAC-oriented draft.

## Problem

Customers live in the manifest (`catalog/customers.json`, admin-writable via
`our admin customers`). That conflates **operational data** (the customers we
work with) with **control-plane configuration** (what the org loads and
calls), and forces an admin bottleneck on ordinary business records. The
deeper smell: `our` has only two write audiences (admin manifest repo vs org
workspace repo), so "who can change a customer" is welded to "which repo
holds the file."

An earlier draft tried to fix this by adding Principals/Grants — an authority
layer inside `our`. That direction is out of scope by design: **`our` is a
light manifest/loader CLI, not an authorization gateway.** Modeling rights in
`our` creates a second
rights system that can disagree with the real one (GitHub, a CRM, clawdapus).
Split-brain authority is worse than none.

## What `our` is (the altitude)

`our` loads skills, loads MCPs, mounts files, installs company-wide CLI
dependencies, and describes things to call — for both local harnesses and the
claw pods it compiles. It declares **what to mount and call**, never **who
may**. Access control belongs to the backend:

- Git-backed domain → the Git host (GitHub/GitLab) enforces repo & branch
  permissions; `gnit` only **composes** the per-domain repos into the
  umbrella (ordered publish/pins), it is not an access-control layer.
- API/MCP-backed domain → the service's own token/scope/RBAC enforces.
- `our` may carry a credential **reference** (`op://`, `env://`, `auth_ref`)
  — a description of how to authenticate a call, never an `our`-modeled right.
  `our` does not carry "grants": the vestigial `Service.Grant` field is
  removed in this model (if a backend like clawdapus needs opaque passthrough,
  it is named `descriptor_options` and is explicitly not authority).

Granularity is a backend concern. The small-business answer: split the data
plane into per-domain repos so the Git host's per-repo permissions supply the
granularity, composed without submodules by `gnit`. `our` mounts what the
running identity can already reach; the backend rejects the rest.

## The model: three layers

### 1. Data types — the stable commands

Every business has the same nouns. They stay first-class commands and do not
change when the backend changes. Two families, treated differently:

- **Operational record domains** — `our customers`, `our meetings`,
  `our support`, `our fleet` (deployments). These hold records that live in a
  backend and are the targets of the binding work below.
- **Catalog inventory** — `our products`, `our repos`. These keep their
  existing catalog metadata + local-clone-selection semantics; they gain a
  binding only if a real need appears, not by default.

`our customers list` works identically whether customers are markdown files in
a git mount or rows behind a CRM API.

### 2. Surfaces and data bindings — the pluggable backend

Keep the two primitives `our` already has, unchanged:

- **mounts** — git-backed content (gnit-composable). Maps to clawdapus
  `host://` / `volume://`.
- **services** — callable remote things (MCP/HTTP + descriptor). Maps to
  clawdapus `service://` + descriptor.

"**Surface**" is the umbrella term for *a thing a data type can be backed by*
— realized as a `mount` **or** a `service`. There is no new merged type and no
`Service → Surface` rename: collapsing the two primitives would blur
git-content vs callable-remote and churn the codebase for no gain. The
clawdapus-aligned vocabulary comes from the mapping above, not a rename.

A thin **data binding** maps a data type to a surface:

```
"data_bindings": {
  "customers": { "surface": "mount:customers" }, # small-business default
  "meetings": { "surface": "service:crm" }       # when a CRM/API exists
}
```

The manifest holds only the binding plus the reach details already present on
mounts/services (URL + `include_paths`, or endpoint + `describe_ref` +
`auth_ref`). The binding value is an object rather than a bare string so future
non-authority metadata can be added without a shape migration. Records and
rights live in the backend, not in `our`.

### 3. Surface bundles — what a backend contributes

A surface (mount or service) may contribute, for its domain:

- **skills** — how to work with the domain (markdown guidance).
- **mcps** — MCP servers exposing the domain's operations (→ `.mcp.json`).
- **tools** — described callables / company CLI dependencies to install.
- **guidance fragments** — domain norms rendered into `AGENTS.md` under a
  **labeled, source-attributed** section (e.g. `## Domain Notes: customers`),
  **separate** from the top-level org `contract`.

The top-level manifest `contract` list and `our contract list|add|remove`
stay authoritative and untouched — surface guidance is rendered as its own
attributed section, never merged silently into the binding org contract, so
`our contract list` never misrepresents what it owns. This is the one
genuinely new structural idea, and it is small: surfaces gain a bundle. It
mirrors clawdapus, where a service's `.claw-describe.json` advertises its
tools/skill and a consuming agent auto-mounts that skill.

## Roles become loadouts, not authority

`manifest.Role` already selects `Mounts + Skills + Tools + Services +
GuidancePaths`. Reframe it as a **loadout/profile**: which surfaces are active
for a given harness or claw — nothing more. Activating a role wires in its
surfaces' bundles (skills + mcps + tools + guidance fragments). A role is
*what is loaded*, never *what is permitted*; the Git host / the API still gate
real access. `our setup --role` keeps working; only the framing tightens.

## Materialization is recursive (one model, both directions)

`our` is used **by** the claws it compiles, so the surface model drives both
directions from one source:

- **Local harness** (`our setup` / `our ai`): active surfaces → mounts checked
  out + `.mcp.json` from surface MCPs + tools installed + `AGENTS.md`
  (baseline + skills + org contract + per-surface domain notes).
- **Claw pod** (`our launch compile`, the Mode B slice in
  [execution plane](2026-06-10-execution-plane.md)): active surfaces → pod
  `host://` / `service://` + descriptors + skills + contract → a per-agent
  context dir mirroring clawdapus `/claw/context/<id>/`.

In-pod, `our customers` reads the same materialized bindings.
`our launch compile` and clawdapus's context materialization are the same act
from two angles.

## The original worry, reconciled without RBAC

"Employees can delete customers/meetings" splits into two controls, neither of
which is authority inside `our`:

- **Hard:** make `customers` its own git repo with restricted write at the Git
  host. Backend-enforced; coarse but real today. `gnit` composes that repo
  into the umbrella alongside the others.
- **Soft:** the customers surface contributes a **guidance fragment**
  ("archive customers; never hard-delete; handle PII per policy") rendered
  under its labeled domain-notes section. A binding norm for agents, not a
  gate, and distinct from the org contract.

When the org graduates to a CRM with real RBAC, the same data type rebinds to
a `service` and the CRM's RBAC takes over — zero change to commands or model.

## Implementation slices (conservative; near-term is mostly subtraction)

**Slice 1 — Customers leave the manifest (subtractive, ships first).**
- Remove `Customer` from the manifest catalog; delete `our admin customers
  add|edit`; drop `catalog/customers.json` as a canonical store and
  `LoadCustomers` from manifest refs.
- `our customers` sources records from a mount (`customers/*.md`), identical
  to `our meetings`. The Git host's per-repo perms supply granularity; `gnit`
  composes.
- The private fluxinc manifest moves its customer rows into a `customers`
  workspace domain in the same step (private repo, not here).

**Slice 2 — Data bindings over existing mounts/services.**
- Add a top-level `data_bindings` map keyed by stable data noun
  (`customers`, `meetings`, `support`, `fleet`) whose value is
  `{ "surface": "mount:<id>" }` or `{ "surface": "service:<id>" }`.
  Keep `mounts` and `services` as separate primitives. Mount bindings narrow
  today's local markdown commands to the selected mount; service bindings are
  valid declarations but command invocation waits for Slice 4. Document the
  clawdapus mapping (mount ↔ host/volume, service ↔ service+descriptor).
- Remove the vestigial `Service.Grant` field. If a backend passthrough is ever
  needed, it must be named `descriptor_options` and explicitly non-authority.
  `our` carries no grants.

**Slice 3 — Surface bundles + domain guidance fragments.**
- A surface may carry `skills`, `mcps`, `tools`, and `guidance` fragments.
  Render active surfaces' fragments into labeled, source-attributed
  `AGENTS.md` sections; leave the top-level org `contract` and `our contract`
  verbs untouched.

**Slice 4 — Service-backed domains (future; YAGNI until a backend exists).**
- A binding may target a `service` whose descriptor (CLI / MCP / HTTP, with a
  stable declared shape) `our` invokes; the backend enforces and implements.
  No provider-framework abstraction. Build when a real API exists — flux-store
  today exposes only a narrow HMAC activation endpoint, so order/customer
  access needs a purpose-built broker, not an admin session.

**Slice 5 — Compile (the recursion; tracked under Mode B).**
- `our launch compile` consumes active surfaces → claw pod + context dir.
  Detailed in the execution-plane plan.

## Non-goals

- No principals, grants, capabilities, or any authority/RBAC model in `our`;
  no `grant` field carrying authority.
- No record storage in the manifest; it holds bindings + descriptors, not
  data.
- `gnit` composes repos; it is not an access-control layer.
- No new container/daemon machinery; compilation targets clawdapus.
