# The execution plane: sessions and contained materialization

Status: decision packet for operator review (drafted by Claude, critiqued and
co-shaped by Codex), 2026-06-10. Pre-alpha: rethinking is cheap and
encouraged. Builds on the control/data-plane split shipped in v0.13.0.

Update 2026-06-11 (v0.17.0): Mode A sessions shipped, but the launch default
changed after dogfood. `our ai` now launches from the base umbrella by default;
sessions are opt-in with `our ai --new-session` or `our ai --session <id>`.
When a command runs from inside an active session, content verbs resolve to the
session mount worktree so records do not leak into the base checkout.

## Problem

Agents working inside the umbrella write files: drafts, scripts, notes,
half-finished records. Three failure modes follow:

1. **Litter publishing.** `our sync` auto-publishes any dirty file under a
   mount's content paths. One session's half-draft can be published by
   whichever session runs sync next.
2. **Cross-pollution.** Concurrent agents share one writable mount checkout
   and one scratch area; they read and build on each other's uncommitted
   attempts.
3. **Inheritance.** Successive runs start where the last one left off,
   litter included.

Hard constraints from the operator:

- Humans must be able to jump in just like the models do — the umbrella
  stays browsable, and the isolation mechanism must be plain directories and
  ordinary git, not machine-only plumbing.
- Workspace content repos must never collect agent litter.
- The design must fit the broader architecture: organization manifests
  ultimately compile into contained, governed agent containers, where the
  `our` CLI itself is exposed as a governed tool.

## Architecture: three planes, two modes of operation

v0.13.0 separated the **control plane** (the private manifest repo) from the
**data plane** (content repos mounted in the umbrella). This plan adds the
**execution plane**: where work actually happens, in two modes.

```
control plane    <org>-manifest repo      defines everything; admin-writable
data plane       <org>-workspace repo(s)  durable content; org-writable
execution plane  Mode A: sessions          interactive humans + harnesses
                 Mode B: contained runners governed fleet agents
```

The unifying thesis: **`our` is operator-controlled self-materialization of
what the manifest describes.** Mode A materializes a working context on the
operator's machine; Mode B compiles the same manifest into container launch
artifacts and materializes the context inside a contained runner. Same
definitions, two materializers. `our` owns manifest semantics, umbrella and
session UX, and safe publish; container tooling owns materialization and
containment; the governance proxy owns mediated model/tool access. `our`
does not become a runtime.

### Mode A: sessions (interactive)

The umbrella base becomes a clean, read-mostly reference: mounted content
repos at their published state, generated guidance, registry state. Work
happens in **sessions** — visible, ordinary directories:

```
~/acme/                       base umbrella (clean)
  workspace/                  content repo @ master (read-mostly by policy)
  work/
    2026-06-10-codex-a1b2/    one session
      workspace/              git worktree @ our/work/2026-06-10-codex-a1b2
      scratch/                session-local scratch (never committed)
      AGENTS.md               generated, session-aware guidance
      SESSION.md              what this session is for, who started it
  personal/                   durable user scratch (unchanged)
.our/sessions/<id>.json       session registry metadata (not the work itself)
```

- `our work start [--slug s]` creates a session: a git **worktree** of each
  writable mount on a fresh `our/work/<id>` branch, plus scratch, plus
  generated session guidance. Cheap (worktrees share the object store),
  plain (a human can `cd` in, run git, take over), isolated (branches are
  per-session; the base checkout never sees uncommitted session state).
- `our ai --new-session` creates a fresh session; `our ai --session <id>`
  resumes one. Plain `our ai` launches from the base umbrella, except when the
  current directory is already inside an active session, where it keeps using
  that session. `--no-session` ignores a current session for admin/debug.
- `our work status` lists sessions: branch, dirty paths, age, harness.
- `our work finish --land | --publish | --discard` is the only way work
  leaves a session: land merges to the base branch locally; publish lands
  and syncs; discard removes worktrees and branch. Records pass validation
  and provenance adoption on the way out.
- The **session registry is first-class**: `our sync` reads
  `.our/sessions/*.json` and holds base-mount auto-publish when any active
  session on the same mount remote is dirty or unlanded — naming the
  session id, path, mount, dirty files, and the `our work finish|discard|
  status` remediation. This must not rely on accidental duplicate-remote
  detection. Clean, closed sessions are GC candidates by age.
- Humans use the same verbs, or take over an agent's session by cd-ing into
  it — full symmetry. The base `AGENTS.md` should describe the base umbrella
  as inspection/admin space, not the default place for agent scratch; session
  guidance points harnesses at the session's own `scratch/`.

Substrate alignment: gnit (the umbrella's intended multi-repo substrate) has
this exact workflow designed as `gnit worktree add <path> --pin` — isolated
workspaces from a pin, members on fresh per-workspace branches, per-agent
attempts without branch explosion. `our work` ships first on plain git
worktrees with the same shape and delegates to gnit when the umbrella is a
gnit control workspace; gnit is the substrate, not a prerequisite.

### Mode B: contained materialization (fleet)

For unattended/role agents, isolation comes from containment, not
convention: a container materialized **from the manifest**, with volume
mounts, local tool requirements, and service surfaces compiled from what the
manifest declares.

The manifest already describes nearly everything such a pod needs — the
mapping is close to 1:1:

| manifest concept            | container/pod concept                      |
|-----------------------------|--------------------------------------------|
| guidance (+ role)           | behavioral contract (read-only AGENTS.md)  |
| mounts (role-scoped subset) | volume mounts into the agent home          |
| skills                      | skill installation directives              |
| tools                       | local tool requirements                    |
| members / roles             | agent identity + persona seed              |
| catalog products            | repo mounts                                |
| services (role-selected / data-bound) | remote surfaces + mediated tool wiring     |

`our compile --role R` emits the org-side launch projection — contract blocks,
role-scoped mounts, tool selections, data bindings, and service declarations.
The later Clawdapus emitter consumes that projection and writes the native pod
and context artifacts that container tooling (for example, `claw up`) consumes.
Inside the container the agent gets a role-scoped umbrella pulled into its home,
the `our` CLI installed, and **only the operational verb set** reachable through
the mediated tool plane; `our admin *` and `our sync` remain operator-only —
the privilege split the CLI already enforces. Cross-pollution is impossible by
construction: every container has its own filesystem.

Mode B builds on manifest extensions that are useful standalone:

- `roles`: named local loadouts — guidance fragments, mount scoping, tool,
  skill, and service selections. Roles select what `our` materializes; they
  do not grant authority.
- `data_bindings`: stable operational data nouns mapped to `mount:<id>` or
  `service:<id>`, so local harnesses and contained agents use the same
  declared backend for customers, meetings, support, and fleet.
- `services`: the organization's remote surfaces — APIs, MCP servers, gated
  brokers — the analog of container surface descriptions. Per entry: id,
  kind (`http`, `mcp`, …), purpose, base URL or discovery, `describe_ref`
  (a self-description endpoint or a checked-in description file;
  descriptions are **references by default**, with fetched snapshots cached
  as derived local state under `.our`, never silently promoted to manifest
  truth), and `auth_ref` (a credential reference, never a secret). An MCP
  server is simply a service of kind `mcp`. Skills may declare `service:<id>`
  dependencies alongside `workspace:` and `tool:`.

Two gated-service shapes anchor the services design, and both are consumed
identically by human and AI operators — gating is a property of the
service, not of the consumer: a **credential broker** (asking is free; each
read requires out-of-band approval; returned credentials are scoped and never
committed to the manifest) and a **communications platform** (operators draft
freely; every send passes human review through an approval-gated,
idempotent pipeline).

Surfacing is mode-aware and conservative. In Mode A, `our setup`
materializes harness MCP config and guidance opt-in, only for services
visible to the current role/operator, never fetching describe endpoints by
default, with doctor warnings for unresolved auth references. In Mode B,
compilation emits topology and non-secret references only — never credentials or fetched
schemas; refresh belongs to the runtime/governance plane.

### Immediate safety patch (independent of mode work)

`our sync` stops auto-publishing arbitrary dirty content files:

- **Untracked** files under content paths publish only when the Git index
  already records adoption: `our meetings/support/fleet add` runs
  `git add -N` after creating the record, `our record adopt <path>` does the
  same for a manually created file, and an explicit human `git add <path>`
  also counts. Plain `??` paths are held and **named** in the sync report
  with the adopt remediation. Schema validity is deliberately not enough — an
  agent half-draft can be schema-valid.
- **Modifications to tracked files** publish as today; the human
  edit-a-record flow is untouched. This is a transition compromise, not
  full isolation: the patch buys immediate safety for new stray files, while
  opt-in sessions plus session-aware content commands give agents an isolated
  path when tracked-file half-edits should not touch the base checkout.

## Prior art: adopt vs build

A four-area sweep (worktree session managers, service descriptor standards,
publish gating, secret references) found that most of this plan decomposes
onto existing, boring mechanisms — and sharpened where the genuine novelty
is. The plan adopts accordingly.

### Adopt (exists; do not reinvent)

- **Secret references are URIs.** `auth_ref` becomes a scheme-addressed
  reference: `op://vault/item/field` accepted verbatim (resolved by the op
  CLI — `op run`-style env injection at launch, never to disk), `env://VAR`,
  `broker://<credential-id>` for push-gated brokers, or `none`. The separate
  resolver-mode enum is dropped — it conflated storage backends with
  mediation planes. Mode policy replaces it: Mode A resolves locally at
  launch; Mode B compilation refuses to emit locally-resolvable references
  into container artifacts (only service id + auth reference travel; the governance
  plane resolves). Precedent: op:// (already used in fleet records),
  helmfile/vals `ref+` across 30+ backends, Berglas, Kong; Docker Compose is
  adopting the same convention (docker/compose#13821).
- **Service descriptions reuse existing schemas by reference.** For
  `kind: mcp`, `describe_ref` targets a server.json document (the MCP
  registry schema every major harness ecosystem now consumes); inline
  connection material reuses server.json vocabulary rather than coining
  fields. For `kind: http`, `describe_ref` targets OpenAPI/AsyncAPI.
  Container-native services may point at a `claw.describe` descriptor.
  `kind: a2a` (agent cards) is reserved. Backstage catalog-info was
  evaluated and rejected — inventory without connection material.
- **Mode A materialization is the ecosystem-standard move.** `our setup`
  emits each harness's native config dialect (`.mcp.json` with `${VAR}`
  placeholders, `.vscode/mcp.json` with inputs, etc.), never secret values —
  exactly what org MCP registries (e.g. GitHub Copilot's) already do.
- **The adoption verb copies Jujutsu and speaks Git.** jj hit this exact
  litter problem (it auto-snapshots like our sync auto-publishes) and solved
  it with `snapshot.auto-track = "none()"` plus `jj file track`. `our record adopt`
  is the same polarity, implemented with Git's own intent-to-add bit:
  `git add -N` records local adoption without staging file content. Sync
  reports plain `??` files (named, with remediation) instead of hiding them.
  No in-file markers are trusted; the `@generated` convention proves those
  are forgeable by the agent being gated. Explicit `git add <path>` is also
  adoption, which preserves human symmetry.
- **Session conventions imitate the field, without harness coupling.**
  Worktree-per-session is now mainstream harness UX, which validates the
  model — but `our work` deliberately does NOT integrate with any harness's
  internal worktree mechanisms (hooks, flags, lifecycle APIs): that locks
  the design to each harness and creates a permanent compatibility
  treadmill. Sessions are owned by `our` on plain git; `our ai` launches
  any harness with its working directory inside the session, which is the
  only integration surface every harness already supports. The cleanup
  conventions the ecosystem converged on (auto-remove clean, prompt on
  dirty, age-based GC) are imitated, not wired. This is the same principle
  the containment layer already established for fleet agents: external,
  inward-facing mechanisms (directory layout, mounts, process environment)
  govern the workload at boundaries it cannot avoid, removing any mechanical
  dependency on the internals of a plethora of harnesses. Sessions are that
  principle applied to the interactive case.
- **Belt-and-braces from git operations practice:** `.gitignore` for
  `work/` and `scratch/`; server-side push rulesets blocking those paths on
  content repos; session/agent trailers stamped on landed commits.

### Dependency policy

External tool dependencies are limited to mature, well-maintained projects:
git, gh, and optionally the op CLI as a secret resolver (declared as a
manifest tool; its absence degrades to held references with doctor
warnings). Surveyed niche or venture-backed session managers and resolver
frameworks are treated as precedent to imitate, never as dependencies; the
multi-repo substrate remains in-house (gnit) by design.

### Build (genuinely unserved)

- **Multi-repo sessions.** Every surveyed manager — claude-squad, Conductor,
  Nimbalyst, vibe-kanban, container-use, gwq, amux, GitButler — is
  single-repo; Conductor's linked-directories multi-repo is documented as
  "not yet perfect". One session spanning worktrees of N writable mounts,
  with a registry consulted by org-level sync, exists nowhere.
- **The services/data policy envelope**: role-selected visibility plus
  data-binding topology in a git-native, serverless registry consumed
  identically by humans and agents. server.json stops at `isSecret`;
  org-registry products assume a hosted service.
- **Mode-aware compile policy** (refusing local secret references in
  container artifacts) — the property no off-the-shelf resolver has.

### Simplification adopted for v0.13.1

jj's history suggests auto-publishing untracked files is the unusual
feature, not the gate. v0.13.1 adopts **"never auto-publish plain `??`
content; adopt or finish to publish"**. Records created by `our ... add` are
adopted at creation via `git add -N`; everything else waits for
`our record adopt`, explicit `git add`, or future `our work finish`. Same UX
for the happy path, no separate provenance ledger.

## Boundary with container tooling ("our speaks claw")

The manifest↔pod mapping table raised a real concern: vocabulary overlap
and drift between the org manifest and container tooling DSLs (Clawfile /
pod-file `x-claw` blocks). Resolution: **the manifest stays semantic; claw
is a compile target, not a vocabulary source.**

- The manifest's sections describe organization truth (roles, mounts,
  skills, tools, services, guidance) in `our`'s own minimal schema.
- A later Mode B emitter consumes `our compile` output and emits the container
  tooling's native formats — pod-file fragments and contract documents — the
  same way Mode A emits each harness's native MCP config dialect. Speaking the
  consumer's dialect at the boundary avoids both a parallel DSL and schema
  coupling: container tooling evolves independently, and a new runner format
  becomes a new emitter, not a manifest migration.
- Where a concept is a genuine published standard (server.json for MCP
  connection material, OpenAPI for HTTP, op:// for secret references), the
  manifest adopts the standard directly rather than wrapping it.

## Options

### O1 — Safety patch only (now)

- Pro: fast, narrow; closes the litter-publishing hole immediately.
- Pro: preserves human direct edits to tracked records.
- Con: agents still share one checkout — tracked-file half-edits,
  cross-pollution, and inheritance remain; introduces an adoption rule that
  depends on Git index state.
- When it wins: as v0.13.1, while the execution plane is built.

### O2 — Patch + sessions, opt-in (`our work start`, `our ai --new-session`, `our ai --session`)

- Pro: structural isolation exists; daily single-command flow unchanged.
- Con: agents must choose the session path for isolated work; partial
  protection in practice.
- When it wins: this became the post-dogfood direction because humans mostly
  want base launch, while bots can carry explicit session flags.

### O3 — Patch + sessions, default-on for `our ai` (superseded)

- Pro: the ergonomic path is the safe path — the only design that changes
  default agent behavior; concurrent/successive runs can't interact; base
  stays pristine; human-symmetric (humans get and can take over the same
  sessions); `--no-session` keeps the escape hatch.
- Con: adds a finish step to the flow (mitigations: `our work finish
  --publish` doubles as the session's sync; prompt on harness exit); more
  lifecycle to document and GC.
- When it wins: originally recommended for agent-only launch ergonomics, but
  superseded by the 2026-06-11 opt-in decision.

### O4 — Sessions on gnit now (block on `gnit worktree add`)

- Pro: one isolation mechanism across the stack; pins give reproducible
  session baselines and attempt review.
- Con: blocks v0.14 on a feature in another project; umbrellas aren't gnit
  control workspaces yet.
- Resolution adopted: ship `our work` on plain git worktrees shaped for gnit
  delegation; switch the backend when the umbrella is gnit-controlled.

### O5 — Read-only base mounts

- Pro: reinforces sessions; accidental base writes become impossible.
- Con: permission games are brittle cross-platform and punish legitimate
  edits; never sufficient alone.
- Resolution adopted: read-mostly **by policy** (sessions + sync gating)
  first; managed read-only later for contained launches.

### O6 — Contained role/fleet launches (Mode B)

- Pro: strongest scoped execution and tool governance; role-scoped mounts
  and mediated model/tool access fit the broader ecosystem.
- Con: heavier dependencies and moving parts; less casual human takeover
  unless directories stay plainly mounted.
- When it wins: governed bots and role/fleet deployments — a second
  execution mode, not a replacement for human-friendly sessions.

## Recommendation (Claude + Codex)

The combined path:

1. **v0.13.1** — Git intent-to-add gated publishing for untracked content
   files, plus `our record adopt`.
2. **v0.14-v0.17** — `our work` sessions (visible `work/<id>`, plain git
   worktrees, first-class registry, sync session-awareness), revised in v0.17.0 so
   `our ai` defaults to base launch while sessions are explicit with
   `--new-session`/`--session` and current-session cwd is honored.
3. **v0.18.0** — manifest `roles` + `services` (MCP servers fold in as
   `kind: mcp`), `our services`/`our roles` inspection, `our setup --role`,
   umbrella-root `.mcp.json` from local connection data, doctor service
   checks (see `docs/plans/2026-06-12-v018-scope.md`).
4. **Next** — org-side launch-artifact projection for contained runners
   (`our compile`): manifest + role + skills + mounts compile into a
   deterministic Clawdapus-facing launch artifact; the manifest `contract`
   list (v0.20.0) maps to the artifact's enforce-level contract block rather
   than a parallel dialect. Descriptor fetch/cache remains a later derived-state
   phase.
5. Later — gnit backend for sessions once umbrellas bootstrap as gnit
   control workspaces; managed read-only base mounts for contained launches.
