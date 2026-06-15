# Model-Driven Onboarding

Status: **active (draft)**, 2026-06-15. Brainstormed between Claude and Codex
over Talking Stick; scope decisions made by Wojtek. Not yet implemented.

## Problem

`our onboard` (v0.26.0) is a deterministic human tour: it explains the model
with press-enter prompts and delegates every durable change to
`our setup --interactive`. By design it **refuses to author a manifest** — with
no registered manifest it just prints `our manifests add <name> <git-url>` and
exits. `our init` covers the other end (create a new org) but assumes the
operator already knows they want one and can drive flags.

The genuinely *dynamic* part has no path: interviewing a person about their
business and **building the initial control plane** — which mounts, which
roles, which repos, which skills, which tools, what contract — then wiring the
workspace and publishing it. That is judgement work over an open-ended space,
exactly what a model is good at and a fixed prompt flow is bad at. Two people
onboarding the same way is the wrong default; their orgs differ.

We want a **model-driven onboarding**: launch a harness with an onboarding
skill and an instruction prompt, let it ask the right questions for *this*
person, and have it set up the workspace/manifest/repos through the CLI's
existing deterministic, validated commands.

## What this is not

- **Not** a replacement for the deterministic paths. `our setup` stays
  flag-driven and scriptable; `our onboard` (no flag) stays the press-enter
  human tour. The model mode is additive.
- **Not** a separate tutorial feature. Teaching is **folded inline**: the
  onboarding skill explains each command as it runs it ("I'm running
  `our sync` now — that's how workspace content gets published"). No
  `our tutorial` verb, no second skill. (Decision, Wojtek.)
- **Not** a new top-level verb. Per the v0.26.0 "two verbs, no third" rule,
  this is a **flag/mode** on the existing `our onboard`, not `our author` or
  `our wizard`.

## Shape

One adaptive flow, two branches, selected by whether a manifest is already
registered (Decision, Wojtek — "both, one adaptive flow"):

```
our onboard --agent [--harness NAME]
   │
   ├─ no manifest registered ─────────►  AUTHOR branch
   │     model interviews the user, builds a new org:
   │     our init → admin mounts/roles/services/skills/tools/contract
   │     → repos add → doctor/compile gates → review → publish
   │
   └─ manifest registered ────────────►  JOIN branch
         model walks a teammate through setup against the existing
         manifest: pick role → setup --role → sync → inline teaching
```

The branch is a *default*, not a cage: a returning founder can still add
mounts/roles to an existing org (AUTHOR-style edits on a registered manifest),
and the skill should offer that. Detection just picks the most likely intent.

## Division of responsibility (the core principle)

**CLI = deterministic mechanism. Skill = conversational brain.** This keeps the
public repo "generic mechanism code" and puts all the open-ended judgement in
the skill, where it belongs.

- The **CLI** never asks open-ended questions and never guesses business
  structure. It exposes validated, idempotent commands and honest checks
  (`our doctor`, `our compile`, `our admin manifests validate`). It already
  owns: `init`, `publish`, `setup`, `sync`, `repos add`, and
  `admin {mounts,skills,tools,contract} add`.
- The **skill** (bundled, like the `our` self-skill) is the brain: it holds the
  question script, the ordering, the inline teaching copy, and the
  decision-making about what *this* org needs. It only mutates state by calling
  CLI commands — never by hand-editing the manifest.

`our onboard --agent` itself still **mutates nothing durable**. It composes the
onboarding skill into the launch root and execs the chosen harness with an
instruction prompt. All durable change happens inside the model session,
through the same validated commands a human could run. This preserves the
v0.26.0 invariant ("onboard mutates nothing durable except by delegating") —
here it delegates to a model that delegates to setup/admin/init.

## The launcher: `our onboard --agent`

A flag on the existing command (Decision, Claude+Codex — a flag is not a verb;
skill-only is too easy to miss on first run and loses prompt/guardrail
control):

- `our onboard --agent` — compose the onboarding skill into the launch root,
  pick a harness (prompt the human when interactive; honor `--harness`), and
  exec `our ai <harness>` with an **instruction prompt** that points the model
  at the onboarding skill and the current branch (author vs join).
- `our onboard --agent --harness codex` — non-interactive harness selection for
  scripted/repeat use.
- Reuses the existing `our ai` launch path (skill composition, refresh, update
  check) rather than inventing a second launcher. The onboarding skill is
  installed/composed the same way the `our` self-skill is.
- Zero-manifest is the AUTHOR entry point — unlike plain `our onboard`, the
  `--agent` mode works from truly nothing because authoring a manifest is the
  whole point.

Open question O1 (for Codex): does the instruction prompt go through a
harness-specific initial-prompt mechanism (`claude -p`, `codex` exec prompt),
or do we drop a launch-root `AGENTS.md`/prompt file the skill keys off? The
former is cleaner but harness-shaped; the latter is uniform but indirect.

## Guardrails (the part that makes model authoring safe)

Biggest risk (Codex): model-authored control-plane drift, premature publish,
and leaked secrets. Containment is layered and **all deterministic**:

1. **Command-driven mutations only.** The skill is instructed to never hand-edit
   manifest JSON. Every change goes through a validated admin/init/setup/repos
   command. This is why we add the missing role/service verbs (below) instead
   of letting the model write that JSON.
2. **Schema validation at every write.** Admin `add` commands validate their
   input and reject malformed manifests; `our admin manifests validate` is the
   explicit gate the skill runs after a batch of edits.
3. **`our doctor` + `our compile` gates.** Before publish, the skill runs
   `our doctor` (must be clean) and, when roles exist, `our compile --role <r>`
   (must produce a valid projection). A failing gate blocks the publish step.
4. **Publish held to the very end.** Everything is `local-only` until an
   explicit, human-confirmed `our publish`. The model presents a review
   (what mounts/roles/repos it created) and asks before publishing. `our sync`
   already holds manifest/admin changes from auto-publish, so an accidental
   `our sync` mid-onboarding cannot leak the half-built control plane.
5. **Secrets never in the manifest.** The skill must put credentials in the
   environment/local descriptors the way `services` already expect (URL-only
   MCP descriptors, env-var references), never inline. `our doctor` already
   reports unset referenced env vars, so this is checkable.
6. **Human confirmation on irreversible/outward steps.** `our init` (creates
   local repos) and `our publish` (creates remotes, pushes) are confirmed with
   the human before the model runs them.

## New CLI surface: role & service authoring verbs

The one real gap (Codex confirmed; Wojtek chose to close it now): there is no
`our admin roles` or `our admin services` verb today — only read-only
`our roles list/get`, `our services list/get`, and `our setup --role`. To keep
the AUTHOR branch command-driven, add a thin authoring slice:

Surface below is **Codex's spec**, grounded in the current manifest structs and
validation (`internal/manifest`, `internal/cli/admin.go`, `services.go`). It
mirrors the existing `our admin {tools,contract}` add/edit/remove shape exactly.

**Roles:**

- `our admin roles add <id> --manifest-dir DIR --purpose TEXT [--guidance PATH]...
  [--mount ID]... [--skill namespace:name]... [--tool ID]... [--service ID]...
  [--force] [--json]`
- `our admin roles edit <id> --manifest-dir DIR [--purpose TEXT]
  [--guidance PATH]... [--clear-guidance] [--mount ID]... [--clear-mounts]
  [--skill namespace:name]... [--clear-skills] [--tool ID]... [--clear-tools]
  [--service ID]... [--clear-services] [--force] [--json]`
- `our admin roles remove <id> --manifest-dir DIR [--force] [--json]`

Semantics: `add` fails on an existing id unless `--force` replaces. `edit` keeps
fields absent from flags; when any repeated list flag is present it **replaces**
that whole list; `--clear-*` flags set the list to nil and conflict with the
corresponding list flag. `remove` just deletes the role (no manifest references
point at roles today, so validation suffices). Output mirrors admin
tools/contract: `action id manifest_path`, message, next commands; `--json`
returns the role. Role validation already rejects: id not lowercase
kebab-case, duplicate id, blank purpose, guidance paths that aren't relative
paths inside the manifest repo, unknown mount ids, skill refs not
`namespace:name` lowercase kebab-case or unknown, unknown tool ids, unknown
service ids.

**Services:**

- `our admin services add <id> --manifest-dir DIR --kind http|mcp --purpose TEXT
  --auth-ref REF [--describe-ref REF] [--connection-type TYPE]
  [--connection-command CMD | --connection-url URL] [--connection-arg ARG]...
  [--connection-env KEY=VALUE]... [--connection-header KEY=VALUE]... [--force]
  [--json]`
- `our admin services edit <id> --manifest-dir DIR [--kind http|mcp]
  [--purpose TEXT] [--auth-ref REF] [--describe-ref REF|--clear-describe-ref]
  [--connection-type TYPE] [--connection-command CMD | --connection-url URL]
  [--connection-arg ARG]... [--connection-env KEY=VALUE]...
  [--connection-header KEY=VALUE]... [--clear-connection] [--force] [--json]`
- `our admin services remove <id> --manifest-dir DIR [--prune-roles] [--force]
  [--json]`

Semantics: `add` requires `--kind`, `--purpose`, `--auth-ref`, and either
`--describe-ref` or a connection. Connection flags are only allowed for
`--kind mcp`; `--connection-command` and `--connection-url` are CLI-mutually-
exclusive even though the low-level validator only requires at least one.
`connection-arg/env/header` replace those lists/maps when supplied on `edit`;
`--clear-connection` conflicts with any connection flag. `remove` fails if any
role selects the service, unless `--prune-roles` first removes that service id
from `role.services` (mirroring `skills remove --prune-related`).

Service validation already rejects: bad/duplicate id, blank purpose,
unsupported kind (only `http`/`mcp`), missing/invalid `auth_ref`. Crucially
**`auth_ref` must be `none`, `env://NAME`, `op://…`, or `broker://…` — never a
literal secret** (this is guardrail #5 enforced at the schema layer).
`describe_ref` must be an `http(s)` URL or a relative path inside the manifest
repo; a service needs `describe_ref` or a connection; connection is mcp-only and
must include a command or `http(s)` url with no surrounding whitespace.

Implementation note (Codex): reuse the existing `optionalStringFlag` pattern
from admin tools for scalar edits and `stringListFlag` for repeated values; add
a `key=value` parser for connection env/header; always run
`ensureAdminManifestClean` unless `--force`, then `manifest.ValidateDocument`
after mutation, then `manifest.SaveDocument`.

These verbs are independently useful outside onboarding (any admin can now
script role/service authoring), and they let the skill treat roles/services
like every other command-driven control-plane noun.

## AUTHOR branch — question script (skill content, illustrative)

The skill owns this; it is copy, not code. Rough arc:

1. "What does your organization do, in a sentence?" → org id/name for
   `our init`.
2. "What kinds of shared knowledge do you keep?" → mounts (handbook,
   customers, meetings, support, fleet, policy, docs) via `admin mounts add`.
3. "What roles/teams work differently here?" → roles via `admin roles add`,
   each scoped to relevant mounts/services.
4. "Any code repositories to track?" → `repos add`.
5. "Any external tools/services the org depends on?" → `admin tools add` /
   `admin services add`.
6. "Any binding rules everyone must follow?" → `admin contract add`.
7. Run `our setup` to materialize the umbrella, `our doctor`/`compile` to
   verify, present a review, then confirm and `our publish`.

Each step is taught inline as it happens. The model adapts ordering and
question depth to the person — a solo founder and a 200-person company get
different conversations from the same script.

## JOIN branch — question script (skill content, illustrative)

1. Detect the registered manifest; summarize the org from it.
2. "Which role fits you?" (from `our roles list`) → `our setup --role <r>`.
3. `our sync` to pull mounts; teach the content verbs
   (`meetings/support/fleet`) inline by example.
4. Point at `our ai <harness>` for daily work.

## Phasing

- **P1 — admin verbs.** `our admin roles` + `our admin services` add/edit/remove
  with validation and tests. Independently shippable; unblocks the skill.
- **P2 — onboarding skill.** Bundle the model-driven onboarding skill
  (AUTHOR + JOIN branches, inline teaching, guardrail instructions). Install
  alongside the `our` self-skill.
- **P3 — launcher.** `our onboard --agent [--harness NAME]`: skill composition,
  branch detection, instruction prompt, harness exec. Resolve O1.
- **P4 — docs + changelog.** Site onboarding page, README roadmap, plans index.

## Testing

- Go unit tests for the new admin verbs (valid/invalid input, manifest
  validation rejects malformed roles/services, no inline secrets accepted).
- Boundary tests that `our onboard --agent` mutates nothing durable itself and
  composes the skill into the launch root (mirror `onboard_boundary_test.go`).
- A `/tmp` sandbox smoke test of the AUTHOR arc end-to-end with a stub harness,
  asserting publish is held until explicit confirmation and `doctor` is clean
  before publish.

## Open questions

- **O1** Instruction-prompt delivery (harness initial-prompt vs launch-root
  prompt file). Codex to weigh in.
- **O2** Should the AUTHOR branch's `our init` confirmation and `our publish`
  confirmation be CLI-enforced (the command refuses without a flag) or
  skill-enforced (the model asks)? Leaning skill-enforced for `publish` (the
  human is in the loop anyway) and CLI-as-is for `init` (already local-only).
- **O3** Do we need `our admin roles`/`services` `list` too, or do the existing
  read-only `our roles/services list` cover the skill's read needs? (Likely
  yes, reuse the read verbs.)
