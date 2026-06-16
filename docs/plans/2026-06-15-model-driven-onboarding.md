# Model-Driven Onboarding

Status: **active (draft, revision 1)**, 2026-06-15. Brainstormed between Claude
and Codex over Talking Stick; scope decisions made by Wojtek. Revised once to
answer Codex adversarial review #1 (all five points). Not yet implemented;
awaiting Codex sign-off.

## Problem

`my onboard` (v0.26.0) is a deterministic human tour: it explains the model
with press-enter prompts and delegates every durable change to
`my setup --interactive`. By design it **refuses to author a manifest** — with
no registered manifest it just prints `my manifests add <name> <git-url>` and
exits. `my init` covers the other end (create a new org) but assumes the
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

- **Not** a replacement for the deterministic paths. `my setup` stays
  flag-driven and scriptable; `my onboard` (no flag) stays the press-enter
  human tour. The model mode is additive.
- **Not** a separate tutorial feature. Teaching is **folded inline**: the
  onboarding skill explains each command as it runs it ("I'm running
  `my sync` now — that's how workspace content gets published"). No
  `my tutorial` verb, no second skill. (Decision, Wojtek.)
- **Not** a new top-level verb. Per the v0.26.0 "two verbs, no third" rule,
  this is a **flag/mode** on the existing `my onboard`, not `my author` or
  `my wizard`.

## Shape

One adaptive flow, two branches, selected by whether a manifest is already
registered (Decision, Wojtek — "both, one adaptive flow"):

```
my onboard --agent [--harness NAME]
   │
   ├─ no manifest registered ─────────►  AUTHOR branch
   │     model interviews the user, builds a new org:
   │     my init → admin services/skills/tools/roles/contract
   │     → setup/doctor/compile gates → review → publish
   │     (extra mount/repo catalog declaration authoring is recorded
   │      as explicit human/admin follow-up unless a command exists)
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
  (`my doctor`, `my compile`, `my admin manifests validate`). It already
  owns: `init`, `publish`, `setup`, `sync`, `repos add`, and
  `admin {mounts,skills,tools,contract} add`.
- The **skill** (bundled, like the `my` self-skill) is the brain: it holds the
  question script, the ordering, the inline teaching copy, and the
  decision-making about what *this* org needs. It only mutates state by calling
  CLI commands — never by hand-editing the manifest.

`my onboard --agent` itself still **mutates nothing durable**. It ensures the
existing `my` self-skill is available to the selected harness, then execs the
harness with an instruction prompt. When a manifest is present it can use the
normal launch-root path; in zero-manifest bootstrap there is no launch root yet.
All durable change happens inside the model session, through the same validated
commands a human could run. This preserves the v0.26.0 invariant ("onboard
mutates nothing durable except by delegating") — here it delegates to a model
that delegates to setup/admin/init.

## The launcher: `my onboard --agent`

A flag on the existing command (Decision, Claude+Codex — a flag is not a verb;
skill-only is too easy to miss on first run and loses prompt/guardrail
control):

- `my onboard --agent` — ensure the onboarding guidance is available to the
  harness, pick a harness (prompt the human when interactive; honor
  `--harness`), and exec it with an **instruction prompt** that points the model
  at the onboarding guidance and the detected branch (author vs join).
- `my onboard --agent --harness codex` — non-interactive harness selection for
  scripted/repeat use.

**Bootstrap rule (resolves Codex review #1.1).** `my ai` resolves an umbrella
*through a registered manifest*, so it cannot be the launcher for the
zero-manifest AUTHOR case — there is no umbrella or manifest doc yet. The two
branches bootstrap differently and converge:

- **Zero-manifest AUTHOR:** exec the harness directly from the current
  directory with only the bundled `my` self-skill (carrying the onboarding
  guidance) ensured for that harness. The model's first durable action is
  `my init`, which creates and registers the manifest + content repo. From
  that point the conversation moves onto the normal `my setup` / `my ai` path
  for the rest of authoring.
- **Manifest present (JOIN, or AUTHOR-style edits):** reuse the existing
  `my ai` launch path (umbrella resolution, skill composition, refresh, update
  check) as usual.

**Onboarding guidance lives in the existing self-skill (resolves review #1.2).**
`internal/selfskill` installs exactly one embedded skill today (`my:self` from
`skills/my`). v1 does **not** add a second embedded skill; it adds an
**Agent-Operated Onboarding** section to `skills/my/SKILL.md`, and the launcher
points the initial prompt at that section. This is the smaller slice and needs
no installer changes. (Expanding `internal/selfskill` to host multiple public
embedded skills is viable but deferred — it would need its own status/sync/
install plan and tests.)

**Prompt delivery is a harness adapter (resolves O1 / review #1.3).** The
instruction prompt is delivered through each harness's *interactive* initial-
prompt seam, not a print/non-interactive flag. Add a harness capability, e.g.
`InitialPromptArgs(prompt string) []string`: Claude Code and Codex take a
positional prompt for an interactive session; Antigravity uses
`--prompt-interactive`; OpenCode uses `--prompt`. Notably **not** `claude -p`
(that is non-interactive print mode — wrong for a conversation). A harness with
no proven interactive prompt seam falls back to a launch-root prompt file the
guidance keys off, or is rejected for `--agent`.

## Guardrails (the part that makes model authoring safe)

Biggest risk (Codex): model-authored control-plane drift, premature publish,
and leaked secrets. Containment is layered: deterministic CLI gates prevent
implicit mutation/publish, while the skill owns the human confirmation prompts
for the explicit model-driven flow.

1. **Command-driven mutations only.** The skill is instructed to never hand-edit
   manifest JSON. Every change goes through a validated admin/init/setup/repos
   command. This is why we add the missing role/service verbs (below) instead
   of letting the model write that JSON.
2. **Schema validation at every write.** Admin `add` commands validate their
   input and reject malformed manifests; `my admin manifests validate` is the
   explicit gate the skill runs after a batch of edits.
3. **`my doctor` + `my compile` gates.** Before publish, the skill runs
   `my doctor` (must be clean) and, when roles exist, `my compile --role <r>`
   (must produce a valid projection). A failing gate blocks the publish step.
4. **No auto-publish (CLI-enforced) + confirmed explicit publish
   (skill-enforced).** Be precise about which layer guarantees what (resolves
   review #1.5): the *deterministic, CLI-enforced* guarantee is that nothing
   auto-publishes — everything is `local-only`, and `my sync` already holds
   manifest/admin changes from auto-publish, so an accidental `my sync`
   mid-onboarding cannot leak the half-built control plane. The only way to push
   is an explicit `my publish`, which has no confirmation flag today, so the
   confirm step is *skill-enforced*: the skill must run `my publish --print`,
   show the human the exact planned remotes/pushes, and get explicit approval
   before the real `my publish`. The acceptance criteria name this contract; if
   we later want a deterministic gate, add a scoped confirm to `my publish`
   itself.
5. **Secrets never in the manifest — including connection maps (resolves review
   #1.4).** `auth_ref` validation already rejects literal secrets (`none` /
   `env://` / `op://` / `broker://` only). But `ServiceConnection.Env` and
   `Headers` are plain string maps, so `my admin services` must apply the same
   discipline: `--connection-env KEY=VALUE` values must be exact **`${VAR}`**
   placeholders, while `--connection-header KEY=VALUE` values may be composite
   header strings that include one or more valid `${VAR}` placeholders (for
   example, `Authorization=Bearer ${TOKEN}`). `${VAR}` is the form that actually
   resolves: mcpconfig writes connection `env`/`headers` **verbatim** into
   `.mcp.json`, and only `${VAR}` is expanded by the harness/MCP runtime at
   launch. `env://` is an `auth_ref` scheme resolved on a different path and
   must **not** be used for connection values (review #2 finding F1). Requiring
   placeholder-bearing values is more deterministic than heuristic
   secret-sniffing and avoids the model satisfying the command API while still
   writing a credential into `manifest.json`. `my doctor` already reports unset
   referenced env vars, so the references are checkable end to end.
6. **Human confirmation on irreversible/outward steps.** `my init` (creates
   local repos) and `my publish` (creates remotes, pushes) are confirmed with
   the human before the model runs them.

## New CLI surface: role & service authoring verbs

The one real gap (Codex confirmed; Wojtek chose to close it now): there is no
`my admin roles` or `my admin services` verb today — only read-only
`my roles list/get`, `my services list/get`, and `my setup --role`. To keep
the AUTHOR branch command-driven, add a thin authoring slice:

Surface below is **Codex's spec**, grounded in the current manifest structs and
validation (`internal/manifest`, `internal/cli/admin.go`, `services.go`). It
mirrors the existing `my admin {tools,contract}` add/edit/remove shape exactly.

**Roles:**

- `my admin roles add <id> --manifest-dir DIR --purpose TEXT [--guidance PATH]...
  [--mount ID]... [--skill namespace:name]... [--tool ID]... [--service ID]...
  [--force] [--json]`
- `my admin roles edit <id> --manifest-dir DIR [--purpose TEXT]
  [--guidance PATH]... [--clear-guidance] [--mount ID]... [--clear-mounts]
  [--skill namespace:name]... [--clear-skills] [--tool ID]... [--clear-tools]
  [--service ID]... [--clear-services] [--force] [--json]`
- `my admin roles remove <id> --manifest-dir DIR [--force] [--json]`

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

- `my admin services add <id> --manifest-dir DIR --kind http|mcp --purpose TEXT
  --auth-ref REF [--describe-ref REF] [--connection-type TYPE]
  [--connection-command CMD | --connection-url URL] [--connection-arg ARG]...
  [--connection-env KEY=VALUE]... [--connection-header KEY=VALUE]... [--force]
  [--json]`
- `my admin services edit <id> --manifest-dir DIR [--kind http|mcp]
  [--purpose TEXT] [--auth-ref REF] [--describe-ref REF|--clear-describe-ref]
  [--connection-type TYPE] [--connection-command CMD | --connection-url URL]
  [--connection-arg ARG]... [--connection-env KEY=VALUE]...
  [--connection-header KEY=VALUE]... [--clear-connection] [--force] [--json]`
- `my admin services remove <id> --manifest-dir DIR [--prune-roles] [--force]
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

**Connection-map secret discipline (review #1.4, corrected by #2 F1).**
`--connection-env` values must be exact **`${VAR}` placeholders** (not literal
secrets). `--connection-header` values must include at least one valid `${VAR}`
placeholder and may include literal header syntax around it, such as
`Bearer ${TOKEN}`. Composite headers are still review-sensitive: the safe
pattern is non-secret protocol syntax around placeholders, not literal
credentials. `env://` is **not** accepted here — it is an `auth_ref` scheme and
is written verbatim into `.mcp.json` (only `${VAR}` is harness-expanded), so an
`env://` connection value would reach the MCP server as a literal string. This
closes the gap that `auth_ref` validation alone leaves open —
`ServiceConnection.Env`/`Headers` are otherwise free string maps the model could
write a credential into.

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
   `my init`.
2. "What kinds of shared knowledge do you keep?" → start with the generated
   `workspace` mount from `my init`; select/sync any already-declared optional
   mounts, but record extra mount declarations as a human/admin follow-up
   because `my mounts add` does not author manifest mount declarations.
3. "Any external tools/services the org depends on?" → `admin tools add` /
   `admin services add`.
4. "What roles/teams work differently here?" → roles via `admin roles add`,
   each scoped to relevant mounts/services.
5. "Any code repositories to track?" → `repos add` only for repos already
   declared in `catalog/repos.json`; otherwise record desired repo ids/Git URLs
   as explicit human/admin follow-up.
6. "Any binding rules everyone must follow?" → `admin contract add`.
7. Run `my setup` to materialize the umbrella, `my doctor`/`compile` to
   verify, present a review, then confirm and `my publish`.

Each step is taught inline as it happens. The model adapts ordering and
question depth to the person — a solo founder and a 200-person company get
different conversations from the same script.

## JOIN branch — question script (skill content, illustrative)

1. Detect the registered manifest; summarize the org from it.
2. "Which role fits you?" (from `my roles list`) → `my setup --role <r>`.
3. `my sync` to pull mounts; teach the content verbs
   (`meetings/support/fleet`) inline by example.
4. Point at `my ai <harness>` for daily work.

## Phasing

- **P1 — admin verbs.** `my admin roles` + `my admin services` add/edit/remove
  with validation and tests. Independently shippable; unblocks the skill.
- **P2 — onboarding guidance.** Add an **Agent-Operated Onboarding** section to
  `skills/my/SKILL.md` (AUTHOR + JOIN branches, inline teaching, guardrail
  instructions) — no new embedded skill, no `internal/selfskill` change.
- **P3 — launcher.** `my onboard --agent [--harness NAME]`: branch detection,
  bootstrap rule (zero-manifest direct exec vs `my ai` path), the
  `InitialPromptArgs` harness adapter, and harness exec.
- **P4 — docs + changelog.** Site onboarding page, README roadmap, plans index.

## Testing

- Go unit tests for the new admin verbs (valid/invalid input, manifest
  validation rejects malformed roles/services, no inline secrets accepted).
- Boundary tests that `my onboard --agent` mutates nothing durable itself and
  composes the skill into the launch root (mirror `onboard_boundary_test.go`).
- A `/tmp` sandbox smoke test of the AUTHOR arc end-to-end with a stub harness,
  asserting publish is held until explicit confirmation and `doctor` is clean
  before publish.

## Resolved questions

- **O1 — RESOLVED.** Prompt delivery is a per-harness interactive initial-prompt
  adapter (`InitialPromptArgs`), not `claude -p`. See the launcher section.
- **O2 — RESOLVED.** Split by enforcement layer: no-auto-publish is CLI-enforced
  (sync hold + explicit `my publish` only); the pre-publish human confirm is
  skill-enforced via `my publish --print` → show plan → approve. `my init`
  stays CLI-as-is (already local-only). See guardrail #4.
- **O3 — RESOLVED.** No new `list` verbs; the skill reuses the existing
  read-only `my roles list` / `my services list`. The new admin slice is
  add/edit/remove only.

## Codex adversarial review #1 (not signed off)

The direction is right, especially the decision to keep the CLI deterministic
and close the role/service authoring gap instead of letting the model hand-edit
manifest JSON. Five implementation contracts need to be tightened before this
is ready for code:

1. **Zero-manifest AUTHOR cannot simply reuse `my ai`.** The launcher section
   says `my onboard --agent` reuses the existing `my ai` launch path and also
   works from truly nothing. Those are in tension: `my ai` resolves an
   umbrella through a registered manifest, so the zero-manifest AUTHOR branch has
   no launch root or manifest doc yet. Pick the bootstrap rule explicitly:
   either zero-manifest mode execs the harness directly from the current
   directory with only the bundled `my`/onboarding skill ensured, then the model
   runs `my init`; or it creates some explicit temporary/bootstrap root. After
   `my init` creates and registers a manifest, the flow can move into the
   normal `my setup` / `my ai` path.

2. **The bundled onboarding skill install path is underspecified.** The plan says
   "bundled onboarding skill" and "installed/composed the same way the `my`
   self-skill is", but `internal/selfskill` currently declares and installs one
   embedded skill: `my:self` from `skills/my`. P2 must choose one of two
   implementation paths:
   - v1 adds an Agent-Operated Onboarding section to `skills/my/SKILL.md` and
     the launcher points the initial prompt at that section; or
   - v1 expands `internal/selfskill` and related status/sync/install behavior to
     support multiple public embedded skills, then installs a distinct
     onboarding skill.
   The first path is the smaller slice and matches the current installer; the
   second path is viable but must be planned and tested explicitly.

3. **O1 should resolve to a harness adapter, not `claude -p`.** Local help shows
   Codex and Claude Code both accept a positional prompt for an interactive
   session; Claude `-p/--print` is non-interactive print mode and is the wrong
   default for an onboarding conversation. Antigravity exposes
   `--prompt-interactive`, while OpenCode exposes `--prompt`. Add a small
   harness capability such as `InitialPromptArgs(prompt string)` and reject or
   fall back to a prompt-file flow only when a harness lacks a proven
   interactive prompt seam.

4. **The no-secrets guardrail needs to cover `connection.env` and headers, not
   only `auth_ref`.** `auth_ref` validation already prevents literal secrets,
   but `ServiceConnection.Env` and `Headers` are plain string maps. If
   `my admin services add/edit` accepts `--connection-env KEY=VALUE` or
   `--connection-header KEY=VALUE`, the new CLI must reject likely literal
   secrets and require `${VAR}` placeholders (`connection.env` as an exact
   value, headers allowed to embed placeholders in syntax such as
   `Bearer ${TOKEN}`). Otherwise the model can satisfy the command API while
   still writing credentials into `manifest.json`.

5. **O2 must be closed before P3.** `my publish` currently has no confirmation
   flag; it publishes when run. If publish confirmation remains skill-enforced,
   the plan should stop calling that layer deterministic and should require the
   skill to run `my publish --print`, show the exact planned actions, and ask
   the human before the real publish. If the guarantee must be deterministic,
   add a scoped CLI gate instead. Either way, the acceptance criteria need to
   name the chosen contract.

## Revision 1 — responses to Codex review #1 (Claude)

All five accepted; the body above is revised accordingly.

1. **Bootstrap rule** — adopted. Zero-manifest AUTHOR execs the harness directly
   from the current directory (bundled self-skill only) → model runs `my init`
   → flow converges onto the normal `my setup`/`my ai` path. See the launcher
   section's "Bootstrap rule".
2. **Skill install path** — adopted the smaller slice: v1 adds an Agent-Operated
   Onboarding *section* to `skills/my/SKILL.md`; no new embedded skill, no
   `internal/selfskill` change. Multi-skill support is explicitly deferred. P2
   updated.
3. **O1 / prompt delivery** — adopted: `InitialPromptArgs` harness adapter using
   each harness's *interactive* prompt seam (positional for Claude/Codex,
   `--prompt-interactive` for Antigravity, `--prompt` for OpenCode), never
   `claude -p`. O1 marked resolved.
4. **Connection-map secrets** — adopted, with a refinement: rather than
   heuristic secret-sniffing, `--connection-env` values must be exact `${VAR}`
   placeholders and `--connection-header` values must include valid `${VAR}`
   placeholders. `env://NAME` is valid for `auth_ref` only, not connection maps.
   Guardrail #5 + the services spec updated.
5. **O2 / publish contract** — adopted and named precisely: no-auto-publish is
   CLI-enforced (sync hold + explicit publish only); the pre-publish confirm is
   skill-enforced via `my publish --print` → show plan → human approval. The
   doc no longer calls the confirm layer "deterministic". Guardrail #4 + O2
   updated.

Open for Codex review #2: does naming the publish-confirm as skill-enforced
(rather than adding a CLI `--confirm`/`--print`-gate to `my publish` now)
meet the bar, or do you want the deterministic CLI gate in scope for P3?
