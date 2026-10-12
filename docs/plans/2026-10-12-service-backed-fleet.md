# Service-backed fleet records

Status: first slice and fan-out confirmation implemented (2026-10-12); semantic search follows

This plan delivers Slice 4 of the data-surfaces plan
(`2026-06-13-data-surfaces.md`) for the `fleet` data type. It applies once an
organization's deployed-instance registry lives in a service with a real API,
not in Markdown. `my fleet` then reads and writes that service live. The
service enforces access, revisions and workflow; `my` invokes it and never
caches values as a second source of truth.

The organizing constraint is the same as the rest of `my`: no provider
framework. One HTTP record protocol and one binding shape, built because a
real backend now exists.

## Binding

A data binding may name several surfaces. An organization moving one product
line into a service keeps its other products' records in Markdown:

```json
"services": [{
  "id": "registry",
  "kind": "http",
  "purpose": "Deployed-instance registry",
  "connection": { "type": "http", "url": "https://registry.example.test/api/v1" },
  "auth_ref": "op://Example/registry-api/token"
}],
"data_bindings": {
  "fleet": { "surfaces": ["service:registry", "mount:workspace"] }
}
```

- `surface` (one) stays valid. `surfaces` (an ordered list) is new. At most one service surface is allowed per data type in this slice.
- An `http` service's `connection.url` is its API base. `connection` is currently MCP-only, and this slice allows it for `http` too, with a URL alone. `describe_ref` keeps its meaning: a description reference for compile and other consumers.
- The URL must be https (http only for loopback in tests). It must not contain userinfo or query parameters.
- `auth_ref` resolves at invocation, and is never stored or printed.
  - `env://NAME` reads the environment.
  - `op://...` runs `op read`.
  - `broker://` stays unsupported here, with an actionable error.
  - `none` sends no credential.

### Records that moved

A Markdown fleet record whose frontmatter has `managed_by: service:<id>` is a
pointer, not a record:
- It is hidden from `list` and `search`.
- `get` on it, or on one of its identifiers, follows the service.
  It is resolved by `managed_id` when present, otherwise by its own id through `resolve`; the service keeps historical source ids.
  If that fails or the service is down, the command fails and names the service. It never shows the pointer's stale values.
- `set` on it is refused, and the refusal names the service.

Its file stays for history, and its identifiers still link support records.
Records without `managed_by` behave exactly as today.

## Protocol (`records-http/v1`)

Paths are relative to the service base URL. A record is JSON with at least
`id` (a stable string), `revision` and `archived`. Writes echo back every
revision field the record carried (`revision` and any `*_revision`), so a
backend can guard more than one owner, for example a row and its parent.

| Call | Use |
|---|---|
| `GET records/{id}` | Exact read by stable id. |
| `GET records/resolve?identifier=X` | `{candidates}`: every record an exact identifier names, archived included. |
| `GET records?q=&include_archived=&after=` | Search, and keyset pages (`next_after`) for bootstraps. |
| `GET changes?after=&limit=` | `{changes:[{id, op: upsert\|tombstone}], cursor, more}`, commit-safe. |
| `PATCH records/{id}` | `{changes:{field: value}, <revision fields>}`. All or nothing: 409 stale, 422 refused. |
| `POST records/{id}/transitions` | `{operation, <revision fields>, ...}`: workflow moves that the backend gates. |

## Commands

- **`get <x>`**: every reference goes through `resolve`. A stable id is only used directly when it came from this service (for example from a `list` result or a pointer's `managed_id`). Nothing is guessed from a reference's shape or a vendor prefix.
  - Unmanaged Markdown records are matched as today.
  - One match across both surfaces is printed.
  - Several matches (remote, Markdown, or one of each) are listed with their surface and id, and the command exits non-zero. It never prefers one surface.
  - Related support records are found by every identifier on the live record, including historical source ids.
- **`list` / `search`**: the service's search (`q`) and the Markdown surfaces, merged.
  Each result carries its surface, so nothing pretends to be the other. If the service can't be reached, the command fails and names it. It does not quietly return the Markdown half.
- **`set <x> KEY=VALUE...`**: on a service record, one PATCH with every pair and the revisions from a fresh read.
  - A 409 prints the current values and exits non-zero.
  - `status=` is refused with a pointer to `transition`.
  - No `my sync` suggestion is printed, because nothing local changed.
- **`transition <x> <operation> [KEY=VALUE...]`** (new): POST a transition with the revisions from a fresh read. The backend's refusals are printed as given.
- **`add`**: creates Markdown only. If the type is bound to a service and the target mount is not selected (`--workspace`), it is refused, with a pointer to the service's UI for its records. A selected mount adds an unmanaged local record, as today.

## Projection

The service's JSON is projected into `fleet.Record`. Every field is named; nothing is inferred.

| fleet.Record | records-http/v1 |
|---|---|
| `ID` | `id`, opaque |
| `Customer` | `customer.workspace_customer_id` (canonical id), else the name |
| `Status` | `stage` |
| `Identifiers` | every exact identifier: `order.so_number`, `po_number`, `po_number_2`, `box.serial_number`, `label`, `hostname`, `tailscale_name`, the remote-access ids, `guid`, every value in `identifiers{}`, shipment tracking numbers and `sources[].source_id` (historical ids). Support linking and unknown-identifier warnings rely on this list. |
| `Partner` | `order.partner` |
| `ShipTo`, `InstallDate` | `ship_to`, `box.installed_on` |
| `DeployedSite` | `site` |
| `Device`, `Serial` | `box.device_model`, `box.serial_number` |
| `ConfigRepo`, `ConfigBranch` | `box.config_repo`, `box.config_branch` |
| `Contacts` | `contacts[]`, as `role: name` |
| `Source`, `Surface` | `service`, and `service:<id>` |
| `URL` (new) | `url` when the service gives one |

`get` also prints notes and provenance. `set` keys go through an alias table from fleet keys to service fields: `serial`, `device`→`model`, `ship_to`, `config_branch`, `install_date`→`installed`, and `deployed_site`→`site_name`. Other keys pass through as service field names, and the atomic backend refuses unknown ones (422 names the field), so nothing is saved. `my` keeps no copy of the service's field list.

## Required follow-ups

The integration is not complete until both of these ship:
- **Fan-out confirmation** (done): a write that changes other records, such as a shared order number, is refused with "confirmation required", the ids it would affect and a link to the service's UI. It is never labelled stale. `my fleet set ... --confirm <ids>` sends those ids as `confirmed_ids`. The service applies the change only if they are exactly the records it covers, so nothing is confirmed implicitly.
- **Semantic search** (below).

## Later slices

- **Semantic search:** a per-user derived index keyed by (service, id, content fingerprint). The fingerprint is over every revision field, or the hydrated content, because a parent edit fans out without bumping the record's own revision. A local QMD index over derived records is the vendor-free first choice. It is bootstrapped from keyset pages using a cursor taken first, then follows `changes`. Every hit is hydrated through `GET records/{id}` before output. The index is never a display source, a write target or an offline fallback.

## Tests

All fixtures are fictional (`examples/acme-workspace`, `registry.example.test`), with an `httptest` server implementing the protocol. The tests cover:
- Resolve with zero, one and several candidates.
- Pointers hiding and following.
- An atomic `set` that maps a 409 and a 422.
- Refusing `status=` and `add`.
- Surfacing a down service.
- `auth_ref` resolution for env:// and an op:// stub, with no secret in any output.
- Transport safety:
  - A redirect to another origin never carries the credential.
  - env and op failures, and server error bodies, never print the secret.
  - A URL with userinfo or a query string is refused.
- Ambiguity across surfaces, and a pointer whose service is down failing closed.
