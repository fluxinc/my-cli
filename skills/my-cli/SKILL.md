---
name: my-cli
description: Use when working inside a My AI umbrella (a per-user operating dir, e.g. ~/my, containing a .my-cli/ directory and a generated AGENTS.md), or when the user asks about the `my` CLI, organization manifests, launch-scoped skills, mounts, meeting notes, customers, the product catalog, onboarding a harness, or syncing/publishing local workspace changes. Also use when an AGENTS.md says the workspace is My AI-managed.
---

This skill teaches a harness how to operate inside a My AI workspace.

`my` is a small, dependency-free CLI that bootstraps an AI agent's working
environment from a single organization **manifest**. One command gives installed
harnesses (Claude Code, Codex, OpenCode, Antigravity) the same company context,
manifest-defined launch profiles, and local tooling.

## When To Use

Use this skill when any of these are true:

- the working directory is, or sits under, a My AI **umbrella** (a `.my-cli/`
  marker directory and a generated `AGENTS.md` are present)
- the user mentions `my`, an organization manifest, launch-scoped skills, mounts,
  meeting notes, customers, the product catalog, onboarding, or syncing the
  workspace
- the user wants to record a meeting/decision or publish local workspace changes

Prefer the `my` CLI over hand-rolled git or file edits for anything it owns.
Run `my --help` (or `my <command> --help`) for the authoritative surface.

## The Model

`my` has eight concepts. Everything in the CLI is one of these:

- **Manifest** — an organization's configuration in its own private Git repo:
  declares skills, mounts, data bindings, catalog, services, roles, and tool
  hints. The single source of truth, and the control plane only — the manifest
  is not the workspace; the
  workspace is a mount of things the manifest defines, and day-to-day work
  never edits the manifest. Registered locally with `my init <org-id>` for a
  new organization or `my manifests add <name> <git-url>` for an existing
  repo, then refreshed with `my manifests sync`.
- **Skill** — a capability exposed to harnesses. Organization skills are
  *static* (a directory in the manifest repo) or *tool-provided*; `my ai`
  composes them into the launch root's `.agents/skills/` (with a `.claude/skills/`
  mirror for Claude Code) for harnesses with a project-local skill seam (Claude
  Code, Codex, Antigravity). Pick the loadout with `my ai --skills all|none|<id,...>`
  or `my ai --profile <id>` (mutually exclusive); with no selector, `my ai`
  uses the default for the launch target: selected role skills for a base
  umbrella, workspace-satisfied skills for a session, all org skills for an
  unscoped umbrella, and no org skills for a repo launch. Named loadouts come
  from the manifest's `profiles` list. OpenCode is compatibility-global until
  that seam is proven, so its org skills stay user-global and `--skills`/`--profile`
  are rejected for it.
- **Umbrella** — a per-user operating envelope (e.g. `~/my` or `~/acme`): a
  `.my-cli/` identity namespace plus mounts and local scratch. Launch harnesses
  from here so they pick up the generated `AGENTS.md` context.
- **Mount** — a Git-backed content folder cloned into the umbrella (handbook,
  customers, meeting notes, policy, docs).
- **Session** — an isolated unit of work under `<umbrella>/work/<id>`: a git
  worktree per content mount on a fresh `my/work/<id>` branch, plus
  session-local `scratch/`, with a registry record under `.my-cli/sessions/`.
  Create one with `my work start` or `my ai --new-session`; work leaves a
  session only through `my work finish --land | --publish | --discard`.
- **Catalog** — JSON inventories: products (business entities, which may link
  repos) and repos (the organization's repositories, cloned on demand under
  `repos/<id>` via `my repos add`). Customer identities are mounted workspace
  records, not manifest catalog rows.
- **Guidance** — the generated root `AGENTS.md` (and `CLAUDE.md` pointer) built
  from a public baseline plus manifest and selected-role fragments. A manifest
  `contract` list adds short, binding org rules rendered as an
  `## Organization Contract` section; treat those rules as obligations.
- **Tool** — an external executable the org depends on; `my` reports presence
  and install hints, it never silently installs tools.

## Operational vs Admin

`my` splits its surface by risk. This boundary matters for an agent:

- **Operational** commands are read-only or only touch local per-user state.
  They are safe to run freely: `my skills list/show/status`,
  `my meetings list/search/get`, `my support list/search/get`,
  `my fleet list/search/get`,
  `my customers list`,
  `my products list`, `my repos list/add/remove`, `my tools list/info`,
  `my services list/get`, `my roles list/get`, `my contract list`,
  `my compile`, `my root`,
  `my ai`, `my doctor`, `my manifests list`, `my mounts list`,
  `my work start/status/list/resume/finish` (sessions are local execution-plane
  state; `finish --publish` only publishes what the sync policy allows),
  `my sync`, and `my sync --print`.
  `my update --check` is also safe for inspection. Run `my update` itself
  only when the user explicitly asks to update the local CLI binary.
- **Admin** commands mutate the shared source of truth (the manifest,
  product/repo catalog, guidance, skills declarations). They live under
  `my admin ...`
  (`my admin skills add/remove`, `my admin tools add/edit/remove`,
  `my admin roles add/edit/remove`, `my admin services add/edit/remove`,
  `my admin contract add/remove`,
  `my admin manifests/mounts/meetings/support/setup`) and require explicit
  intent.
  Do not run them to "fix" something unless the user asked to change the
  organization's configuration.

When unsure, reach for the operational form first; it cannot damage shared
state.

## Common Tasks

Bootstrap / refresh the workspace:

```sh
my init <org-id> [--name NAME] [--path DIR] [--umbrella DIR]
                                    # create manifest + content repos locally and register them
my publish [--manifest NAME] [--print]
                                    # create private remotes, rewrite local mount URLs, push both repos
my onboarding [--manifest NAME] [--home DIR] [--umbrella DIR] [--agent|--no-agent] [--harness NAME]
                                    # launch guided onboarding; --no-agent prints the deterministic walkthrough
my setup [--manifest NAME] [--role ROLE] [--interactive] [--no-refresh] [--no-update-check]
                                    # create umbrella, write guidance/MCP config, install self-skill, sync mounts
my root [--repo ID] [--no-refresh] [--no-update-check]
                                    # print the umbrella (or repo) path
my ai [--new-session|--session ID|--resume [ID]|--no-session] [--repo ID] [--skills all|none|ID,...] [--profile ID] [--setup] [--print] [--no-refresh] [--no-update-check] [harness]
                                    # verify guidance is current, compose launch-scoped org skills, then start a harness
                                    # --skills / --profile pick the org skill loadout (mutually exclusive); see the Skill model above
                                    # --setup reconciles the umbrella first when guidance is stale or missing
my compile --role ROLE [--manifest NAME] [--home DIR]
                                    # print deterministic manifest-to-Clawdapus launch projection JSON
my doctor [--no-fetch] [--fix]   # git freshness, sessions, services, derived drift, last sync, manifests, tools
```

Use `my onboarding` when a human wants the guided tour. In an interactive
terminal it launches a harness by default, introduces the operator to My AI,
and configures the workspace through validated `my` commands. Use
`my onboarding --no-agent` for the deterministic walkthrough: it writes no state
until setup actually runs, and with no registered manifest it prints the
`my manifests add <name> <git-url>` next step. Completed tour state is local to
the umbrella under `.my-cli/state.json`. `my onboard` remains a compatibility
alias.

Use `my init` only when the user explicitly wants to create a new
organization. It creates two local repos — a private manifest repo at the
registry path and a content repo at `<umbrella>/workspace` — commits and
registers them, and prints the next `my setup`, `my ai`, and `my publish`
commands. Everything reports `local-only` until published. Run `my publish`
only when the user wants the organization shared: it creates private remotes
(`<org>-manifest`, `<org>-workspace`), rewrites the manifest's local mount
URLs to the published repos, and pushes both. Never hand-edit mount URLs and
never push a manifest that still references local paths — `my sync` holds it
and `my doctor` names the offending mounts.

`root`, `ai`, and `setup` make a best-effort, TTL-gated refresh of clean
manifest/content checkouts before reading workspace context. They do not touch
dirty, diverged, repo, or remote-unknown repositories. Use `--no-refresh`
for one command, `MYCLI_NO_AUTO_REFRESH=1` globally, or `MYCLI_REFRESH_TTL=30m`
to tune the default six-hour window. `my ai` also ensures the bundled `my`
self-skill is installed for the selected filesystem harness before exec.

Use `my setup --role <id>` when the manifest declares local operating roles.
The selected role is stored in `.my-cli/state.json`; generated `AGENTS.md`
includes that role's guidance fragments, and umbrella-root `.mcp.json` is
materialized only for locally described MCP services selected by that role.
Inspect available role and service declarations with `my roles list|get` and
`my services list|get`. Roles never prune mounts.

Use `my compile --role <id>` to inspect the contained-runner handoff for a
role. It is read-only and prints deterministic JSON only: no container launch,
no service invocation, no credential resolution, and no descriptor fetch.
When a manifest declares roles, `--role` is required; a manifest with no roles
compiles an unscoped full projection. A local mount `git_url` is a compile
error because contained launches must not leak host paths.

By default, `my ai` starts the harness from the base umbrella, or from the
current active session when run inside `<umbrella>/work/<id>`. Treat the base
umbrella as inspection/admin space; do not create shared content directly in
base mounts unless the operator explicitly asks for a base edit. Use
`my ai --new-session <harness>` to create an isolated content session,
`my ai -r <id> <harness>` to resume an active session, and
`my ai --no-session <harness>` to ignore a current session for base
inspection/admin/debug. Repo launches are base checkouts in this release, so
use `my ai --repo <id> <harness>` for them. Products are business catalog
entries, not checkouts: records reference them with `--product`, while code
lives in catalog repos managed by `my repos`.

When the refresh cannot converge a checkout, these commands print a stderr
line per repository in the form `notice\t<repo>\t<state>; run ...` (dirty,
ahead, behind, or diverged, with the reconciling command). On seeing one,
finish the current step, then run the suggested command — usually `my sync`,
or `my doctor` for diverged checkouts. Repo clones live under
`repos/<id>` (legacy `products/<id>` keeps resolving until `my setup`
migrates it).

These startup commands also make a best-effort, stderr-only check for a newer
My AI release. The notice never changes stdout, so `cd "$(my root)"` remains
safe. Use `--no-update-check`, `MYCLI_NO_UPDATE_CHECK=1`, or
`MYCLI_UPDATE_CHECK_TTL=12h` when the user needs deterministic/offline startup.

Work in sessions:

```sh
my work start [--slug SLUG]      # create an isolated session: worktree per content mount + scratch/
my work status [--all]           # list sessions with per-mount dirty and unlanded state
my work list [--all]             # alias for status
my ai -r [session-id] [harness]  # launch a harness in an active session; prompts only in an interactive TTY
my work resume [session-id]      # print the cd command for manual shell navigation
my work finish [session-id] --land     # commit session content, merge into base, remove worktrees
my work finish [session-id] --publish  # land, then publish landed content per the sync policy
my work finish [session-id] --discard  # delete the session's worktrees, branches, and directory
```

Use `my ai -r <id> <harness>` to run multiple harnesses in the same session;
for example, `my ai -r <id> claude-code` and `my ai -r <id> codex` launch into
the same session directory. With exactly one active session, `my ai -r codex`
auto-selects it. With multiple active sessions, an interactive terminal gets a
picker; non-interactive agent use must pass an explicit id and never waits on a
prompt. `my work resume` is only a shell helper that prints `cd <path>`.

If you are running inside a session (the working directory is under
`<umbrella>/work/<id>`), keep all edits in the session's mount worktrees and
`scratch/`; never edit the base mounts directly. Content record commands
(`my meetings/support/fleet add`) automatically target the current session's
mount worktree when run from inside the session. Finish is the only exit:
`--land` holds unadopted `??` files and non-content changes instead of
committing them, so adopt records first (`my meetings/support/fleet add` do
this automatically). While a session is dirty or unlanded, `my sync` holds
outbound publish of that mount and names the session — finish or discard the
session rather than working around the hold.

Catalog code repos are not included in work sessions yet. Use
`my ai --repo <id> <harness>` for a base repo checkout, and land code changes
through that repository's normal Git or pull-request workflow.

Update My AI when explicitly requested:

```sh
my update --check                 # compare this binary with the latest release
my update                         # download, checksum-verify, and replace it
my update --version 0.5.0         # install a specific release
```

`my update` refuses package-managed or non-writable installs and prints the
right follow-up command, such as `brew upgrade my`,
`go install github.com/fluxinc/my-cli/cmd/my@latest`, or re-running
`install.sh`.

Find and record knowledge:

```sh
my meetings list   [--since DATE] [--customer ID] [--partner ID] [--json]
my meetings search <text>        # single keywords match best
my meetings get    <id|path>
my meetings add    <slug> [--date DATE] [--title TEXT] [--customer ID] [--attendees NAME] [--partner ID] [--source-id ID]
                     # --attendees/--partner repeatable; each occurrence is one literal value, commas preserved
                     # a slug that starts with YYYY-MM-DD sets the date and is not double-prefixed
my support list    [--since DATE] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
my support search  <text>        # qmd-first support record search when available
my support get     <id|path>
my fleet list      [--status TEXT] [--customer ID] [--partner ID] [--identifier ID] [--branch NAME] [--where KEY=VALUE] [--json]
my fleet search    <text>        # qmd-first fleet registry search when available
my fleet get       <id|identifier|path>   # resolves any identifier; lists related support records
my fleet set       <id|identifier> KEY=VALUE...  # scalar frontmatter updates; preserves the rest
my fleet add       <id> [--customer ID] [--partner ID] [--status TEXT] [--device TEXT] [--serial TEXT] [--identifier ID] [--config-repo NAME] [--config-branch NAME] [--deployed-site TEXT] [--ship-to TEXT] [--contact TEXT] [--install-date DATE] [--print] [--json]
my support add     <slug> [--date DATE] [--title TEXT] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--observed-by MEMBER] [--approved-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--status open|workaround|resolved] [--feature-candidate] [--print] [--json]
                     # --identifier repeatable: record every device, order, or asset
                     # identifier that applies (workstation name, equipment/box ID,
                     # functional location, sales order) so records link later
                     # --claimed-by = org member who worked it; --observed-by repeatable
                     # for others involved; never set --approved-by without explicit
                     # operator approval — it is the human sign-off field
my customers list  [--json]      # mounted customer IDs, aliases, partners
```

Manage skills on this machine:

```sh
my skills list                   # manifest/source skills available to install
my skills status                 # what's installed across harnesses, and where
my skills install [harness...] | --all  # explicit user-global org skill materialization
my skills sync                   # reconcile manual materializations (prune stale)
my tools list                    # manifest-declared external tools
my tools info <name>             # install hints for one external tool
my services list|get             # manifest-declared remote surfaces
my roles list|get                # manifest-declared operating roles
my contract list                 # binding organization contract rules
```

## Sync: reconcile, review, and publish

`my sync` is the routine "make this workspace current" command. Bare `my sync`
pulls inbound updates and reconciles local guard state; it never publishes local
changes. Publishing requires an explicit operator action: use `my sync --push`
to publish eligible local changes according to the manifest policy (or the
default auto policy when none is set). Auto policy only direct-pushes **private,
content-only** changes (e.g. new meeting notes or support records);
manifest/catalog/admin changes, public repos, divergent branches, and unsafe
duplicate-remote checkouts are held back.

```sh
my sync                          # pull/reconcile only; never publishes local changes
my sync --print                  # plan the pull-only default
my sync --push --print           # preview explicit publish work
my sync --push                   # publish eligible local changes per policy
my sync --scope repos            # limit to catalog repo clones (all|local|content|manifest|repos)
my sync --no-derived             # skip derived guidance/MCP/skill reconcile after manifest changes
my sync --publish never          # explicit local-only reconcile
my sync --publish pr             # currently holds changes and reports PR-mode follow-up
```

Plain untracked (`??`) files under declared content paths are held instead of
being published. Use `my meetings add`, `my support add`, or `my fleet add`
for new records; those commands mark the created file with Git intent-to-add.
For a manually created file that should publish, run `my record adopt <path>`
after checking that it belongs in the shared content repo. Explicit `git add`
also counts as adoption.

"Derived" means the artifacts generated from the manifest: root guidance
(`AGENTS.md` plus the `CLAUDE.md` pointer), umbrella-root `.mcp.json`, and
launch-scoped skill reconciliation notices. `my ai` materializes the actual
organization skill loadout into each launch root where the harness supports it;
OpenCode keeps org skills on its global compatibility path for now.
During interactive onboarding, if an existing launch-scope skill entry collides
with the selected manifest skill, the CLI asks whether to replace or skip it and
then continues. Treat this as an operator choice, not a fatal setup condition.

Rule of thumb for the similar verbs: `my sync` converges inbound state and
local guards (use it by default); `my sync --push` is the explicit publish
step; `my doctor` is the repair dry run — it diagnoses,
marks each repairable finding with `would ...`, and prints a fixable count,
while `my doctor --fix` applies exactly that plan; `my manifests sync`
refreshes the registered manifest checkout. Use `my manifests sync` before an
umbrella exists or for multi-manifest administration; when exactly one
manifest changes and an umbrella is known, it also reconciles generated
guidance, umbrella `.mcp.json`, and launch-scoped skill notices.

`my sync --push` uses **Gnit** as its multi-repo publish backend once the
umbrella is a Gnit control workspace; otherwise it uses a guarded built-in Git
path. Run `my sync --push --print` first to see the explicit publish plan.
GitHub PR creation is a My AI policy layer planned on top of Gnit and `gh`; it
is not implemented in the current CLI yet. A manifest can set top-level
`sync.publish_policy` to `auto`, `never`, or `pr` as the default mode for
`--push`; an explicit `--publish` flag always wins. Bare `my sync` ignores that
policy and stays pull-only. A non-print sync writes `.my-cli/last-sync.json`;
use `my doctor` to review the last publish/sync audit. `my doctor` fetches
refs before reporting behind/ahead counts by default; pass `--no-fetch` for an
offline view labeled as of the last fetch. It also reports service
materialization health (URL-only MCP descriptors, missing checked-in
descriptors, unset referenced environment variables, and missing optional
resolver tools such as `op`), active work-session health, missing session
worktrees, and archived session counts. `my doctor --fix` only
fast-forwards clean stale manifest/content checkouts and reconciles generated
guidance, umbrella `.mcp.json`, plus legacy global org-skill cleanup; it reports dirty,
diverged, repo, remote-unknown checkouts, and session work instead of touching
them.

## Agent-Operated Onboarding

When a harness is launched via `my onboarding`, you are the onboarding
assistant. Start by greeting the operator and immediately begin a
learn-by-example walkthrough. Tell the operator to open a second terminal window
or split pane, then explicitly move it to the umbrella root with
`cd "$(my root)"` once a registered manifest/root exists. Do not assume the
operator's current directory is already the umbrella. The operator runs the
commands; you guide, explain, and pace the tour. Build or join the organization
**only through the validated `my` commands** below - never by hand-editing the
manifest or any generated file.

**Walkthrough discipline.** Present one small command set at a time, usually one
to three commands in a fenced `sh` block. Before the block, say in one short
line what the set teaches or changes. After every set, stop and ask whether it
worked, whether there were errors, and whether the operator has questions before
the next set. Do not chain multiple sets without confirmation.

**Human runs commands.** Do not perform the main onboarding commands yourself
while the operator watches. If the operator reports trouble or uncertainty,
offer a read-only verification step such as `my doctor`, `my root`,
`my manifests list --json`, or inspecting generated state. Verification is
optional support, not a hard gate; the normal path is ask-and-proceed.

**Issue filing.** If the operator says to file an issue about a generic,
public-safe CLI problem, treat that as authorization to file it in the project
tracker. Do not write a personal scratch note or memory as a substitute. Ask at
most once when the target repository or privacy boundary is genuinely ambiguous.

**One adaptive flow.** Detect state first, then branch. Start with a command set
for the operator:

```sh
my manifests list --json
```

With **no manifest registered**, take the **AUTHOR** branch (create a new org).
With a **manifest already registered**, take the **JOIN** branch (set this
person up against the existing org). When a manifest exists, have the operator
run `my root` to find the umbrella; if it succeeds, the next command set starts
with `cd "$(my root)"`. If `my root` fails, JOIN still applies but setup is
needed. A returning admin may also want AUTHOR-style edits on an existing
manifest — offer that when it fits.

**Conversation discipline.** Ask one question at a time; prefer concrete choices
over open prompts; match depth to the person (a solo founder and a 200-person
company need different conversations). Do not dump the whole plan up front.

### AUTHOR branch (no manifest yet)

Create the smallest useful local organization and stop there. Do not teach the
whole admin/catalog CLI during onboarding; deeper manifest design belongs in a
normal harness conversation after the operator is oriented.

1. Confirm intent and get a one-line description of the org. With explicit human
   go-ahead, give the first durable command set:

   ```sh
   my init <org-id> --name "<Name>" --json
   ```

   This creates local-only manifest + content repos; nothing is shared yet. Ask
   the operator to confirm success and keep the returned manifest checkout for
   any later agent-guided admin work.
2. Materialize and verify as small sets. First:

   ```sh
   my manifests validate <manifest-dir>
   my setup
   ```

   Then pause. If that worked, give:

   ```sh
   my doctor
   ```

3. Teach the everyday harness/session loop from the new umbrella:

   ```sh
   cd "$(my root)"
   my ai <harness>
   my ai --new-session <harness>
   my work status
   my ai -r <session-id> <harness>
   my work finish <session-id> --land
   ```

   Explain that operators can paste transcripts, notes, screenshots, or issue
   context into the harness chat; the agent will choose the right deeper `my`
   commands when records need to be created.

### JOIN branch (manifest already registered)

1. Have the operator inspect the basic workspace state, then summarize it back:

   ```sh
   cd "$(my root)"
   my mounts list
   my doctor
   ```

2. Help the person pick a role when roles exist. Then give the setup command:

   ```sh
   my setup --role <id>
   ```

   If there are no roles, use:

   ```sh
   my setup
   ```

3. Teach the basic daily loop in small sets:

   ```sh
   my sync
   my ai <harness>
   ```

   Then:

   ```sh
   my ai --new-session <harness>
   my work status
   my ai -r <session-id> <harness>
   my work finish <session-id> --land
   ```

   Then:

   ```sh
   my sync --push --print
   my sync --push
   ```

4. Explain the operator/agent split: humans provide intent and raw context in
   harness chat. For example, if the operator has a new meeting transcript or
   fleet/support context, they paste it into the harness conversation; the agent
   decides whether and how to create records. Do not teach the full content,
   support, fleet, catalog, or admin CLI in onboarding.

### Hard rules (do not skip)

- **Command-driven only.** Every manifest change goes through a
  validated `my` command. Never edit `manifest.json` or generated
  `AGENTS.md`/`.mcp.json` by hand.
  Do not use local operational commands such as `my mounts add` or
  `my repos add` as if they authored manifest or catalog declarations.
- **No secrets in the manifest.** `auth_ref` must be `none`, `env://NAME`,
  `op://...`, or `broker://...`. Inline connection env values must be exact
  `${VAR}` placeholders; connection header values may be composite but must
  contain a `${VAR}` (e.g. `Authorization=Bearer ${TOKEN}`). Never paste a
  literal credential.
- **Publish is held to the end.** Everything stays `local-only` until the last
  step. Before sharing anything, run `my publish --print`, show the human the
  exact remotes/pushes it plans, and get explicit approval — then run
  `my publish`. `my init` (local repos) and `my publish` (remotes) are the
  irreversible/outward steps; confirm both with the human first.
- **Gates before publish.** `my manifests validate <manifest-dir>`,
  `my setup`, and a clean `my doctor` before you offer to publish.
- **Stay generic and public-safe.** Teach the model and the shape; never bake
  one org's private data into reusable guidance.

## Tips

- Launch harnesses with `my ai <harness>` for base inspection/admin work, or
  `my ai --new-session <harness>` for isolated content work. If you are
  already inside a session, plain `my ai <harness>` keeps launching from that
  session; use `--no-session` only to ignore the current session.
- Data-returning commands accept `--json`; structured errors carry a concrete
  remediation command — read it and follow it.
- To record what happened in a meeting, use `my meetings add` and then
  `my sync --push` to publish it, rather than editing files and pushing by hand.
- To record a resolved support problem, use `my support add` with anonymized
  body text; put linkable attribution in frontmatter — the canonical customer
  ID, every applicable device, order, or asset identifier (`--identifier`,
  repeatable), and the org members involved (`--claimed-by` for who worked it,
  `--observed-by` for others) — so recurrence on the same equipment or by the
  same people is discoverable later. Leave `approved_by` empty unless the
  operator explicitly approves the record.
- To look up a deployed instance, prefer `my fleet get <id-or-identifier>` —
  any sales order, functional location, serial, or hostname resolves — and use
  the related support records it lists. Before substantive fleet work,
  continue a relevant support record from that list or create a new dated
  anonymized one with `my support add`; put the fleet record id and every
  useful device, order, or asset identifier on the support record with repeated
  `--identifier` flags. Record workflow transitions with `my fleet set <id>
  status=<value>`, then publish with the suggested `my sync --push --message`
  command so each transition stays a readable git commit.
- This skill is installed and kept current by the `my` CLI itself; do not
  hand-edit the installed copy.
