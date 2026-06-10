# The execution plane: sessions and contained materialization

Status: decision packet for operator review (drafted by Claude, critiqued and
co-shaped by Codex), 2026-06-10. Pre-alpha: rethinking is cheap and
encouraged. Builds on the control/data-plane split shipped in v0.13.0.

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
- `our ai` **defaults into a fresh session**. Reuse is explicit only
  (`our work resume <id>` or `our ai --session <id>`), because implicit reuse
  recreates successive-run inheritance. `--no-session` launches on the base
  umbrella for admin/debug. The ergonomic path must be the safe path —
  guidance alone does not change default agent behavior.
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
convention: a container materialized **from the manifest**, with volume,
tool, MCP, and service mounts compiled from what the manifest declares.

The manifest already describes nearly everything such a pod needs — the
mapping is close to 1:1:

| manifest concept            | container/pod concept                      |
|-----------------------------|--------------------------------------------|
| guidance (+ role)           | behavioral contract (read-only AGENTS.md)  |
| mounts (role-scoped subset) | volume mounts into the agent home          |
| skills                      | skill installation directives              |
| tools + org MCP servers     | managed/mediated tool plane                |
| members / roles             | agent identity + persona seed              |
| catalog products            | repo mounts                                |

A future `our launch compile [--role R]` (verb TBD) emits the org-side launch
artifacts — contract text, role-scoped mount list, tool grants, MCP server
declarations, persona seed — that container tooling (for example, `claw up`)
consumes. Inside the container the agent gets a role-scoped
umbrella pulled into its home, the `our` CLI installed, and **only the
operational verb set** reachable through the mediated tool plane; `our
admin *` and `our sync` remain operator-only — the privilege split the CLI
already enforces. Cross-pollution is impossible by construction: every
container has its own filesystem.

Mode B implies manifest extensions that are useful standalone:

- `roles`: named role definitions — guidance fragments, mount scoping, tool
  and skill grants;
- `mcp_servers`: organization MCP servers, exposed to interactive harness
  setups and to the mediated tool plane alike.

### Immediate safety patch (independent of mode work)

`our sync` stops auto-publishing arbitrary dirty content files:

- **Untracked** files under content paths auto-publish only with explicit
  provenance: created by `our meetings/support/fleet add` (recorded in
  umbrella state at creation) or adopted with `our record adopt <path>`.
  Everything else is held and **named** in the sync report with the adopt /
  explicit-publish remediation. Schema validity is deliberately not enough —
  an agent half-draft can be schema-valid.
- **Modifications to tracked files** publish as today; the human
  edit-a-record flow is untouched. This is a compatibility compromise, not
  full isolation: the patch buys immediate safety for new stray files, while
  default sessions are what prevent tracked-file half-edits from being made in
  the base checkout.

## Options

### O1 — Safety patch only (now)

- Pro: fast, narrow; closes the litter-publishing hole immediately.
- Pro: preserves human direct edits to tracked records.
- Con: agents still share one checkout — tracked-file half-edits,
  cross-pollution, and inheritance remain; introduces provenance state to
  explain and maintain.
- When it wins: as v0.13.1, while the execution plane is built.

### O2 — Patch + sessions, opt-in (`our work start`, `our ai --session`)

- Pro: structural isolation exists; daily single-command flow unchanged.
- Con: the dangerous path stays the ergonomic default; agents won't opt in
  reliably; partial protection in practice.
- When it wins: if the operator decides default sessionization is too much
  ceremony for the daily driver.

### O3 — Patch + sessions, default-on for `our ai` (recommended)

- Pro: the ergonomic path is the safe path — the only design that changes
  default agent behavior; concurrent/successive runs can't interact; base
  stays pristine; human-symmetric (humans get and can take over the same
  sessions); `--no-session` keeps the escape hatch.
- Con: adds a finish step to the flow (mitigations: `our work finish
  --publish` doubles as the session's sync; prompt on harness exit); more
  lifecycle to document and GC.
- When it wins: default interactive use, including the operator's own.

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

1. **v0.13.1** — provenance-gated publishing for untracked content files,
   plus `our record adopt`.
2. **v0.14** — `our work` sessions (visible `work/<id>`, plain git
   worktrees, first-class registry, sync session-awareness), with `our ai`
   defaulting into a fresh session, explicit resume only, and `--no-session`
   as the escape hatch.
3. **v0.15** — manifest `roles` + `mcp_servers`; org-side launch-artifact
   compilation for contained runners.
4. Later — gnit backend for sessions once umbrellas bootstrap as gnit
   control workspaces; managed read-only base mounts for contained launches.
