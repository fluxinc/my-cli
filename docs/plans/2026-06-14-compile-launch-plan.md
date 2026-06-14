# Compile: the manifest→Clawdapus launch projection (Mode B, phase 1)

Status: **active implementation**, drafted by Claude 2026-06-14, **revised after
Codex adversarial reviews #1–#2**, and **signed off by Codex after review #3**
(same day). **Hard gate: no release until Claude and Codex both sign off on the
implementation.** Elaborates step 4 ("Next") of
[`2026-06-10-execution-plane.md`](2026-06-10-execution-plane.md) and Slice 5 of
[`2026-06-13-data-surfaces.md`](2026-06-13-data-surfaces.md). Pre-1.0, no
userbase: back-compat is not a constraint.

### Review history

- **Draft (Claude):** generic runner-neutral JSON IR, `our compile`, drop both
  deprecated aliases, O1–O5 open.
- **Review #1 (Codex), not signed off:** narrow the IR to a Clawdapus-targeted
  projection (no second-runner overpromise / third DSL); fix stale "drop both
  aliases" text (onboard is reclaimed, not dropped); answer O1–O5; tighten the
  schema (preserve `Mount.Kind`, materializable skills, role-closure validation,
  contract provenance + include modes).
- **Revision #1 (Claude):** all review-#1 findings folded in; O1–O5 resolved.
- **Review #2 (Codex), not signed off:** O2 fixed; five deltas — partial roles
  must be legal (omit out-of-role bindings, don't error); drop redundant
  `--json`; local/absolute mount `git_url` is a compile error; precise
  `warnDeprecated` dependency wording; add a partial-role filtering golden test.
- **Revision #2 (Claude):** all five folded in. Pending Codex review #3.
- **Review #3 (Codex), signed off:** plan is internally consistent and ready for
  TDD implementation; implementation still returns to Claude for hard review.

## Problem

Mode B — contained materialization — is the live frontier both active plans
converge on. The manifest already describes nearly everything a contained
runner needs (the manifest↔pod mapping table in the execution-plane plan is
close to 1:1), but nothing yet **emits** that description as a consumable
artifact. Until a deterministic, inspectable compile output exists, there is
nothing to hand the container runtime, nothing to diff in review, nothing to
test.

The execution-plane plan's principle is fixed: **the manifest stays semantic;
Clawdapus is the compile target, not a vocabulary source.** This slice builds
the first half of that — a deterministic projection of the manifest into a
flat, inspectable launch document **whose fields map directly onto the
Clawdapus emitter** (phase 2). It is:

- **not** Clawdapus's native pod-file/`x-claw` DSL emitted verbatim (that is the
  phase-2 emitter's job), and
- **not** a generic runner-neutral IR for hypothetical future runtimes (there is
  exactly one consumer; a second emitter is YAGNI until real demand exists, and
  an over-general IR risks becoming a third DSL).

It is `our`'s own semantic projection, shaped to compile cleanly into Clawdapus.
Phase 2 is the Clawdapus pod-file/context emitter that consumes this projection.

## Converged decision (Claude + Codex, 2026-06-14, post review #1)

1. **Artifact** — `our compile` emits a deterministic **manifest→Clawdapus
   launch projection** (JSON): a role-scoped projection of the manifest into a
   flat "what to materialize" document with provenance preserved.
2. **Determinism is a hard requirement** — sorted keys, no timestamps, no
   randomness, stable slice ordering. Acceptance is a golden test that compiles
   `examples/acme-workspace` and diffs the output **byte-for-byte**. Same
   discipline enforced on domain-notes ordering in v0.24.0.
3. **Command noun** — new top-level `our compile` (read-only emitter).
4. **Drop only the `launch` alias** — no userbase, so this slice removes the
   deprecated `our launch`→`our ai` dispatch alias (`internal/cli/cli.go:137`)
   and its `warnDeprecated("our launch", …)` call site. The `warnDeprecated`
   helper itself is **not** necessarily deleted here: `onboard` still calls it,
   so the helper survives this slice unless the onboarding plan has already
   landed. `runLaunch`/`launch.go` stay as the `our ai` impl. **`onboard` is
   NOT dropped** — it is reclaimed as a new human-facing onboarding walkthrough
   handled in a **separate plan** (`2026-06-14-onboarding-walkthrough.md`),
   which owns removing the `onboard`→`setup` deprecation, the
   `runOnboard`→`runSetup` rename, and the final deletion of `warnDeprecated`.
   **None of that onboard work belongs in this compile slice.**
5. **Defines a seam, not a build** — the JSON is the handoff boundary to the
   phase-2 Clawdapus pod/context emitter. We spec the seam; the emitter is
   phase 2.
6. **Out of scope (loader-only)** — no execution, no service invocation, no
   connector/credential materialization, no describe-endpoint fetching. Access
   control stays in the backend.

## The artifact: `our compile`

```
our compile --role <id> [--manifest <name>]
```

- **Read-only.** Resolves the manifest, projects it, prints the launch
  projection JSON to stdout. Writes nothing, fetches nothing, launches nothing.
- **JSON is the only output.** The projection *is* the artifact; there is no
  `--json` flag or alternate format in v0.25 (add a human summary later only if
  it earns its place).
- **`--role <id>` is required when the manifest declares any roles** (O1). The
  projection is scoped to that role's selections. A manifest that declares **no**
  roles compiles an unscoped full projection without `--role`. A future `--all`
  can be added if whole-manifest inspection is needed when roles exist; not now.
- **Unknown role is an error** (O5).
- `--manifest <name>` selects among configured manifests, consistent with the
  other inspection verbs.

### Projection shape (Clawdapus-targeted; settled enough for review #2)

```json
{
  "compile_version": 1,
  "target": "clawdapus",
  "organization": { "id": "acme", "name": "Acme Example" },
  "role": "operator",
  "contract": [
    { "source": "baseline:fleet-work-contract", "mode": "enforce",
      "rules": ["Before substantive work on a deployed instance, continue or open a dated support record."] },
    { "source": "manifest:contract", "mode": "enforce",
      "rules": ["Continue an existing relevant support record or create a new dated record when working on any fleet member."] }
  ],
  "guidance": [
    { "id": "agent-guidance-base", "source": "agent_guidance", "mode": "guide", "path": "agent-guidance/base.md" },
    { "id": "role-operator",       "source": "role:operator",  "mode": "guide", "path": "agent-guidance/operator.md" },
    { "id": "domain-customers",    "source": "domain:customers", "surface": "mount:handbook", "mode": "reference", "path": "agent-guidance/customers-domain.md" }
  ],
  "mounts": [
    { "id": "handbook", "kind": "handbook", "mode": "rw", "git_url": "…", "include_paths": ["customers", "meetings"] }
  ],
  "data_bindings": {
    "customers": { "surface": "mount:handbook", "kind": "mount", "ref": "handbook" }
  },
  "services": [
    { "id": "comms", "kind": "mcp", "purpose": "…", "describe_ref": "…",
      "auth_ref": "op://vault/comms/token", "connection": { "type": "stdio", "command": "…" } }
  ],
  "skills": [
    { "id": "our", "install_slug": "our", "path": "…", "source": { "type": "…", "tool": "…" },
      "capabilities": ["…"], "requires": ["workspace:handbook"] }
  ],
  "tools": [
    { "id": "op", "mode": "optional", "purpose": "secret resolver" }
  ]
}
```

Each section is a projection of the matching manifest concept, in the
manifest↔Clawdapus mapping the execution-plane plan already drew:

| section         | source                                              | Clawdapus target (phase 2)            |
|-----------------|-----------------------------------------------------|----------------------------------------|
| `contract`      | baseline Fleet Work Contract + `Document.Contract`, each block tagged `source`+`mode` | `x-claw.include` entries (enforce)    |
| `guidance`      | `agent_guidance` + role guidance + bound-surface domain notes, **in render order**, each tagged `id`/`source`/`mode` | `x-claw.include` (guide / reference)  |
| `mounts`        | role-scoped subset of `Document.Mounts`, **`Mount.Kind` preserved** | volume / bind mounts                  |
| `data_bindings` | `Document.DataBindings`, surface resolved; out-of-role bindings **omitted** (partial roles legal) | declared backends                     |
| `services`      | role-selected `Document.Services`, non-secret refs only | `SURFACE`s + mediated tool wiring     |
| `skills`        | role-scoped `Document.Skills`, with `requires`/`source`/`capabilities` | mounted skill install directives      |
| `tools`         | role-scoped `Document.Tools`                        | local tool requirements                |

## Open questions — resolved in review #1

- **O1 — no-role behavior.** **Resolved:** require `--role` when the manifest
  declares roles; unscoped full projection only when no roles are declared.
  Defer `--all`.
- **O2 — IR vs native-first.** **Resolved:** neither extreme — a deterministic
  manifest→Clawdapus *projection*. Drop the runner-neutral / second-runner
  framing; fields map directly to the phase-2 Clawdapus emitter. The projection
  stays `our`-semantic (not raw `x-claw`) so the manifest remains the source of
  truth.
- **O3 — schema surface.** **Resolved:** preserve `Mount.Kind` (no
  `kind: git`); skills carry `requires`/`source`/`capabilities` (materializable,
  not just `id`/`install_slug`); out-of-role `data_bindings` are **omitted**
  (partial roles legal) — only an *emitted* binding that cannot resolve, or an
  unsatisfied skill `requires`, errors.
- **O4 — baseline contract.** **Resolved:** include the baseline Fleet Work
  Contract in the compiled `contract`, but **with `source` + `mode`
  provenance** (not flattened to indistinguishable strings), so the emitter can
  map each block to the right `x-claw.include` mode.
- **O5 — unknown role / empty sections.** **Resolved:** unknown role is an
  error; empty sections are fine when a role intentionally selects nothing.
  Out-of-role bindings are omitted, not errors; only an *emitted* binding that
  cannot resolve, or an unsatisfied skill `requires`, errors (role-closure
  validation, below).

## Role-closure validation (O3 + O5)

Partial roles are legal. Compile **omits** (does not emit) any data binding
whose backing `mount:`/`service:` is not in the selected role's scope — a role
that excludes fleet/support simply has no fleet/support binding. Compile
**fails** (non-zero, named error) only when:

- an **emitted** data binding cannot resolve (its in-scope backing surface is
  missing or malformed), or
- a selected skill's `requires` names a `workspace:`/`service:`/`tool:` surface
  not visible to the role.

This makes the projection self-consistent before it ever reaches the emitter,
without punishing intentionally narrow roles.

## Determinism contract

- **Field order:** fixed by Go struct field order (a dedicated `compile` output
  type, not `manifest.Document` re-marshaled).
- **Map keys:** `data_bindings` and any nested maps emit in sorted key order.
- **Slices:** preserve manifest declaration order; never reorder
  non-deterministically.
- **Encoding:** `json.MarshalIndent(v, "", "  ")` + trailing newline.
- **No volatile fields:** no timestamps, host paths, absolute local paths,
  randomness, or environment-dependent values.
- **Acceptance:** a golden test compiles `examples/acme-workspace` for a
  declared role and asserts the bytes equal a checked-in `testdata/*.json`
  fixture. The fixture is the spec.

## Mode-aware policy (from the execution-plane plan)

Compilation emits **topology and non-secret references only**:

- `auth_ref` travels verbatim (`op://…`, `env://…`, `broker://…`, `none`); it
  is **never resolved**.
- `describe_ref` travels as a reference; it is **never fetched**; no
  derived-snapshot caching during compile.
- `connection.env` `${VAR}` placeholders travel verbatim; never expanded.
- No credentials, fetched schemas, or resolved secrets ever appear in output.
- Mount `git_url` must be a remote URL; a local or absolute filesystem path is a
  **compile error** — an unpublished manifest must not leak host paths into a
  contained-launch projection.

## Scope

**In (phase 1):**
- `our compile` read-only emitter + the launch-projection output type.
- Role scoping (required when roles exist) + role-closure validation.
- Contract/guidance provenance (`source` + `mode`).
- Deterministic JSON + golden ACME fixture test.
- Remove the deprecated `launch` dispatch alias and its `warnDeprecated` call
  site (the helper itself is deleted only once `onboard`'s call site is gone —
  onboarding plan).

**Out (later phases / other plans):**
- Phase-2 Clawdapus pod-file/context emitter.
- Any execution, session creation, or container launch.
- Service-backed *domain invocation* (data-surfaces Slice 4).
- Credential/describe resolution or caching (governance plane).
- `onboard` reclamation + `runOnboard`→`runSetup` rename (onboarding plan).
- `--all` / multi-role / second-runner emitters.

## Acceptance criteria

1. `our compile --role <r>` prints deterministic launch-projection JSON for
   `examples/acme-workspace`; byte-for-byte golden fixture.
2. `--role` required when roles are declared; unknown role errors; no-roles
   manifest compiles unscoped.
3. Partial-role filtering: a role that excludes a surface **omits** its data
   binding — a golden fixture asserts the omission (not an error). An *emitted*
   binding that cannot resolve, and an unsatisfied skill `requires`, both error.
4. A local or absolute mount `git_url` is a compile error.
5. Contract carries baseline + manifest blocks with `source`+`mode`; mounts
   preserve `Mount.Kind`; skills carry materialization fields.
6. No secret/describe resolution occurs (output contains reference strings
   verbatim).
7. `our launch` dispatch alias + its `warnDeprecated` call site gone;
   `our ai`/`our setup` unaffected (existing tests green); the `warnDeprecated`
   helper may remain while `onboard` still calls it; no `onboard` changes in
   this slice.
8. Plan sign-off happened before implementation; implementation review by both
   agents remains the release gate.
