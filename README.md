# my

`my` is a small, dependency-free CLI that bootstraps an AI agent's working
environment from a single organization manifest. One command turns a fresh
machine into one where installed AI harnesses — Claude Code, Codex, OpenCode,
Antigravity — share the same company context, manifest-defined launch profiles,
and local tooling.

It is built for a world where **agents are the primary operators**. Humans own
intent — goals, products, decisions — and express it as content in a Git repo.
`my` is the deterministic, machine-friendly bridge that gets that content and
those capabilities onto every agent surface, the same way, every time.

Documentation: https://my-cli.com/

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/my-cli/master/install.sh | sh

my init acme --name "Acme"
my onboarding --harness codex
my ai codex
```

That's the whole first run. `my onboarding` launches a harness in an interactive
terminal, greets the operator, and starts a split-pane walkthrough where the
operator runs small sets of validated `my` commands while the model explains
and pauses after each set.
Use `my onboarding --no-agent` for the deterministic walkthrough that explains
the model and points at `my setup --interactive`. `my setup` remains the
scriptable machine configurator. `my ai codex` resolves the umbrella, verifies
the generated guidance, and starts Codex in the base umbrella. Agents that need
isolated content work opt in with `my ai --new-session codex` or resume a known
session with `my ai -r <id> codex`.

`my init` creates two local repos — a private manifest repo (the control
plane: manifest, product/repo catalog, skills) and a content repo at
`~/acme/workspace` (the actual workspace, including customer records) —
registers them, and works offline. When ready to
share, `my publish` creates the private remotes, points the manifest at the
published content repo, and pushes both; teammates join with a single
`my manifests add acme <manifest-url>`.
Run `my update` to update an install from the latest GitHub release; re-running
`install.sh` still works as a fallback. Developers can still install from source
with `go install github.com/fluxinc/my-cli/cmd/my@latest`. The installer also
installs My AI's bundled `my` skill into existing harnesses
so agents know how to use the CLI itself.

## The Model

`my` has eight concepts. Everything in the CLI is one of these:

| Concept | What it is |
|---|---|
| **Manifest** | An organization's configuration, stored in its own private Git repo — the control plane. Declares skills, mounts, data bindings, catalog, services, roles, and tool hints. The single source of truth; it is not the workspace, and day-to-day work never touches it. |
| **Skill** | A capability exposed to harnesses. *Organization* skills are *static* (a directory in the manifest repo) or *tool-provided* (materialized by an external tool's own installer); `my ai` composes them into the launch root for harnesses with a project-local skill seam. The CLI also ships one public, organization-neutral *self-skill* named `my`, embedded in the binary, that teaches harnesses how to use `my` itself. |
| **Umbrella** | A per-user operating envelope (e.g. `~/acme`): a `.my-cli/` identity namespace plus mounts and local scratch as peers. When initialized for sync publishing, this is the Gnit control workspace so multi-repo commits and pushes have one substrate. |
| **Mount** | A Git-backed content folder cloned into the umbrella (handbook, customers, meeting notes, policy, docs). Can be path-scoped so only the relevant subtree lands. |
| **Session** | An isolated unit of work under `work/<id>`: a git worktree per content mount on a fresh branch, plus session-local scratch. Create one with `my work start` or `my ai --new-session`; inspect it with `my work status` or `my work list`; work leaves only through `my work finish --land\|--publish\|--discard`. |
| **Catalog** | JSON inventories for products (business entities, which may link repos) and repos (the organization's repositories). Users opt specific repos into their umbrella on demand. Customer identities are mounted workspace records, not manifest catalog rows. |
| **Guidance** | Generated root `AGENTS.md` instructions for agents, built from a public baseline plus manifest-declared and role-specific fragments. `CLAUDE.md` points to the same file. |
| **Tool** | An external executable the org depends on. `my` reports presence and install hints — it never silently installs tools. |

Skills arrive from two places, split by a public/private line. The `my`
self-skill is **public** and travels **inside the CLI binary** — it is
organization-neutral, carries no company content, and the binary keeps it
current on its own. **Organization skills** are **private** to a manifest repo
you control and appear only once you add and sync that manifest, so they can
carry guidance specific to your team. Nothing organization-specific is ever
baked into the public CLI.

## Commands

Run `my --help` for the authoritative surface. The essentials:

### Onboarding

```sh
my onboarding [--harness codex]
                               # model-driven onboarding; auto-detects a harness when unambiguous
my onboarding --no-agent       # deterministic walkthrough; offers interactive setup
my setup [harness...] | --all # create umbrella, write guidance/MCP config, install self-skill, sync mounts
                                    # [--manifest NAME] [--umbrella DIR] [--role ROLE] [--copy] [--link] [--print]
                                    # [--interactive] [--no-refresh] [--no-update-check]
```

`setup` is the normal machine path: idempotent, non-interactive, safe to
re-run. Use `setup --interactive` when you want prompts for manifest and role
selection. Use `my onboarding` when you want a harness to run the adaptive
AUTHOR/JOIN onboarding flow; `my onboard` remains a compatibility alias.
Publish still requires `my publish --print` and explicit human approval.

### Startup

```sh
my root [--repo ID] [--no-refresh] [--no-update-check]
                                             # print the umbrella or repo path
my ai [--new-session|--session ID|--resume [ID]|--no-session] [--repo ID] [--skills all|none|ID,...] [--profile ID] [--setup] [--no-refresh] [--no-update-check] [harness]
                                             # verify guidance, then start a harness
my ai codex --model gpt-5              # pass harness flags after the harness name
my ai --new-session codex
my ai --session 2026-06-11-work-ab12 codex
my ai -r codex                         # resume the only active session, or pick in a TTY
my ai -r 2026-06-11-work-ab12 codex
my ai --repo sample-service codex
my ai --print codex                    # print cd <umbrella> && codex
```

`ai` refuses to start against missing or stale generated guidance. Pass
`--setup` to reconcile first, or run `my setup` directly. By default it
launches from the base umbrella, or from the current active work session when
run inside `work/<id>`. Use `--new-session` to create a fresh isolated session,
`--session` or `-r <id>` to resume a known active session, `-r <harness>` to
resume the single active session or pick one interactively, and `--no-session`
to ignore a current session for base inspection/admin/debug.
`root`, `ai`, and `setup` also run a best-effort, TTL-gated refresh of
clean manifest/content checkouts so startup sees current context without
touching dirty, diverged, repo, or remote-unknown checkouts. Use
`--no-refresh` for one command, `MYCLI_NO_AUTO_REFRESH=1` globally, or
`MYCLI_REFRESH_TTL=30m` to tune the default six-hour refresh window.

Startup commands also print stderr `notice` lines for dirty, ahead, behind, or
diverged checkouts, each with the remediation command, keeping stdout clean.
They additionally check, at most once per day, whether a newer My AI
release exists, using GitHub's public release redirect rather than the
rate-limited REST API. Notices are stderr-only so `cd "$(my root)"` stays
path-pure.
Use `--no-update-check` for one command, `MYCLI_NO_UPDATE_CHECK=1` globally, or
`MYCLI_UPDATE_CHECK_TTL=12h` to tune the check window.

### Updating My AI

```sh
my update --check                  # compare this binary with the latest release
my update                          # download, verify, and replace this binary
my update --version 0.5.0          # install a specific release
```

`my update` verifies the release tarball against `checksums.txt` before
replacing the binary. It refuses package-managed or non-writable installs and
prints the matching follow-up, such as `brew upgrade my`,
`go install github.com/fluxinc/my-cli/cmd/my@latest`, or re-running
`install.sh`.

### Manifests

```sh
my init <org-id> [--name NAME] [--path DIR] # create manifest + content repos locally
my publish [--manifest NAME] [--print]      # create private remotes, rewrite mount URLs, push
my manifests add <name> <git-url>          # register an org manifest
my manifests sync <name...> | --all        # refresh checkout and derived artifacts
my manifests list                          # list registered manifests
my manifests validate <name|path>          # schema + reference checks
```

When a non-print manifest sync pulls or clones exactly one manifest, `my`
reconciles derived workspace artifacts for an existing matching umbrella:
generated guidance, umbrella MCP config, and launch-scoped skill reconciliation
notices. Pass
`--no-derived` for a cache-only refresh or `--umbrella DIR` when the intended
umbrella is not the current one.

### Services and roles

```sh
my services list [--json]
my services get <id> [--json]
my roles list [--json]
my roles get <id> [--json]
my admin services add|edit|remove ...
my admin roles add|edit|remove ...
my setup --role operator
my compile --role operator [--manifest NAME] [--home DIR]
```

Manifest `data_bindings` map stable data nouns (`customers`, `meetings`,
`support`, `fleet`) to an existing `mount:<id>` or `service:<id>`.
Manifest `services` describe remote organization surfaces such as HTTP APIs and
MCP servers. Manifest `roles` are local loadouts: they select services and
optional role-specific guidance without granting authority or pruning mounts.
`my setup --role <id>` stores the local role selection in `.my-cli/state.json`,
appends that role's guidance fragments to `AGENTS.md`, and materializes
umbrella-root `.mcp.json` for locally described MCP services visible to the
role. `my compile --role <id>` is the read-only Mode B handoff: it prints a
deterministic manifest-to-Clawdapus launch projection as JSON, without
launching containers or resolving credentials. A role is required when the
manifest declares roles; manifests with no roles compile unscoped.

### Contract rules

```sh
my contract list [--json]
my admin contract add "RULE TEXT" --manifest-dir <checkout>
my admin contract remove <index|"RULE TEXT"> --manifest-dir <checkout>
```

Manifest `contract` entries are short, binding organization rules rendered
into generated `AGENTS.md`. Reads stay top-level; edits go through the admin
review-commit-push flow against a maintainer manifest checkout.

### Skills

```sh
my skills self status [--json]            # installed/absent status for the bundled my skill
my skills self install [harness...] | --all
my skills list [--json]                   # manifest/source skills available to install
my skills show <id|slug> [--json]         # one skill's metadata and source path
my skills status [--skill ID_OR_SLUG]     # installed/absent status across harnesses
my skills install [harness...] | --all    # explicit user-global materialization
my skills uninstall <harness...> | --all  # remove materialized skills
my skills sync [harness...] | --all       # install/update and prune stale My AI-managed skills
my skills purge <harness...> | --all      # remove My AI-managed materializations
```

`my skills self ...` manages the bundled, public-safe `my` CLI skill. It is
installed by `install.sh`, refreshed during `my setup`, ensured for the
selected filesystem harness before `my ai` execs it, and quietly kept current
for already-installed file-based harness copies when a newer binary runs.

Use `--skill ID_OR_SLUG` on manifest skill `install`, `uninstall`, `sync`,
`purge`, or `status` to target a single declared skill. These commands are
explicit manual user-global materialization surfaces; managed launches get
organization skills from `my ai` in the launch root when the harness supports
that. OpenCode is currently compatibility-global: present or explicit OpenCode
setup/launch keeps org skills in `~/.config/opencode/skills`, and `my ai
opencode --skills/--profile` is rejected until OpenCode has a proven
project-local seam. Manual manifest skills install as symlinks by default
(`--copy` to vendor a copy). `my` records provenance and refuses to clobber a
directory it did not place. `skills sync` prunes stale My AI-managed manual
manifest skills by default, but does not remove the bundled `my` self-skill;
pass `--no-prune` to only install/update. Skill commands only refresh harness
skill directories; run `my setup` when manifest guidance or the generated
umbrella `AGENTS.md` should change without a manifest sync.

Manifest authoring is explicit admin work:

```sh
my admin skills add <skill-dir> --id org:name --manifest-dir <checkout>
my admin skills remove <id|slug> --manifest-dir <checkout> [--prune-orphans]
my admin tools add <id> --manifest-dir <checkout> --mode required|optional --purpose "..."
my admin tools edit <id> --manifest-dir <checkout> [--purpose "..."]
my admin tools remove <id> --manifest-dir <checkout>
my admin services add <id> --manifest-dir <checkout> --kind http|mcp --purpose "..." --auth-ref REF
my admin services edit <id> --manifest-dir <checkout> [--purpose "..."]
my admin services remove <id> --manifest-dir <checkout> [--prune-roles]
my admin roles add <id> --manifest-dir <checkout> --purpose "..."
my admin roles edit <id> --manifest-dir <checkout> [--purpose "..."]
my admin roles remove <id> --manifest-dir <checkout>
my admin contract add "RULE TEXT" --manifest-dir <checkout>
my admin contract remove <index|"RULE TEXT"> --manifest-dir <checkout>
```

Admin commands write a maintainer checkout, not the synced cache. They
refuse dirty git checkouts unless `--force` is supplied, never commit or push,
and require explicit flags for duplicate-prone or destructive cleanup such as
`--keep-original`, `--remove-original`, `--delete-source`, or product
`related_skills` pruning. Removing a skill reports now-orphaned tools and
allowed namespaces; `--prune-orphans` removes those too. Tool removal refuses
manifest skills that still reference the tool. After a write they print the
relevant `git status` and `git diff` follow-up commands.

`my admin` is the home for shared/workspace configuration. Mutating or
configuration commands are reachable there too, with the top-level forms
retained as quiet compatibility aliases:

```sh
my admin setup ...                 # alias of my setup
my admin manifests add|sync|validate  # alias of my manifests ...
my admin mounts add|remove|sync       # alias of my mounts ...
my admin meetings add                # alias of my meetings add
my admin support add                 # alias of my support add
my admin tools add|edit|remove       # edit manifest tools[]
my admin contract add|remove         # edit manifest contract[]
```

Admin aliases are intentionally limited to those mutating/configuration
subcommands. Operational reads (`list`/`show`/`status`/`search`/`get`) stay
under their top-level commands.

### Umbrella mounts

```sh
my mounts list                             # manifest content mounts
my mounts add <kind:id|id>                 # opt an optional content mount in
my mounts sync <mount...> | --all          # clone or fast-forward mounts
my mounts remove <mount...> [--force]
```

Repo clones are managed with `my repos add <id>` and land under `repos/<id>`
in the umbrella; legacy `products/` checkouts migrate automatically at
`my setup`.

### Sync

```sh
my sync --print                           # plan inbound refresh and outbound publish
my sync [--backend auto|gnit|builtin]         # auto prefers Gnit once the umbrella is initialized
my sync --publish auto|never|direct|pr    # explicit override; direct is CLI-only
my sync --scope all|local|content|manifest|repos  # limit to one repo class; repos = catalog repo clones
my sync --no-derived                      # skip derived guidance/MCP/skill reconcile after manifest changes
```

`my sync` is the routine reconciliation command. My AI classifies changes,
handles private/public and content/admin policy, and blocks duplicate checkouts
of the same remote until they are collapsed to one canonical checkout. Gnit is
the intended backend for real multi-repo Change creation, ordered push, and
resume; Pins are reserved for intentional recorded workspace states. The My AI
backend is a guarded bootstrap fallback when a workspace has not been
initialized as a Gnit control workspace. `--publish direct` can publish existing
local commits directly, but dirty non-content/admin files are still held back
instead of being quietly committed. A manifest can set top-level
`sync.publish_policy` to `auto`, `never`, or `pr` as the default when
`--publish` is omitted; an explicit CLI flag always wins. Non-print syncs write
`.my-cli/last-sync.json` so `my doctor` can show the last sync/publish audit.
When sync pulls or publishes a manifest checkout, it reconciles generated
guidance, umbrella MCP config, and launch-scoped skill reconciliation notices
unless `--no-derived` is passed.

### Catalog and customer records

```sh
my products list [--json]         # the org's product inventory
my customers list [--json]        # mounted customer identity records
```

### Meeting notes

```sh
my meetings list   [--since DATE] [--customer ID] [--partner ID] [--product ID] [--json]
my meetings search <text> [--customer ID] [--partner ID] [--product ID] [--json]
my meetings get    <id|path> [--json]
my meetings add    <slug> [--date DATE] [--title TEXT] [--customer ID]
                     [--attendees NAME] [--partner ID] [--source-id ID]
```

A markdown-first operational record (YAML frontmatter), resolved against the
umbrella by default, including the configured umbrella from the registered
manifest when the command is run outside the umbrella. Search uses `qmd` when it
is present and falls back to built-in token-AND markdown search.

### Support records

```sh
my support list   [--since DATE] [--customer ID] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
my support search <text> [--customer ID] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
my support get    <id|path> [--json]
my support add    <slug> [--date DATE] [--title TEXT] [--customer ID]
                    [--product ID] [--area TEXT] [--tag TEXT]
                    [--status open|workaround|resolved] [--feature-candidate]
                    [--print] [--json]
```

An anonymized problem-solving record under `support/`. Use optional canonical
customer IDs in frontmatter when recurrence evidence matters, and keep the body
free of identifying details. Search uses `qmd` when present and falls back to
built-in token-AND markdown search.

### Fleet registry

```sh
my fleet list   [--status TEXT] [--customer ID] [--partner ID] [--identifier ID]
                  [--branch NAME] [--where KEY=VALUE] [--json]
my fleet search <text> [same filters] [--json]
my fleet get    <id|identifier|path> [--json]
my fleet add    <id> [--customer ID] [--partner ID] [--status TEXT]
                  [--device TEXT] [--serial TEXT] [--identifier ID]
                  [--config-repo NAME] [--config-branch NAME]
                  [--deployed-site TEXT] [--ship-to TEXT] [--contact TEXT]
                  [--install-date DATE] [--print] [--json]
my fleet set    <id|identifier> KEY=VALUE... [--json]
```

A registry record per deployed instance under `fleet/<id>.md`, keyed by a
stable id (hostname or node name) and updated in place. `get` resolves any
entry in the record's `identifiers` list — a sales order, functional location,
or serial — and lists support records sharing an identifier. `set` updates
scalar frontmatter fields while preserving everything else, and suggests an
`my sync --message` command so workflow transitions stay readable in git
history. The status vocabulary is organization-defined.

### Diagnostics

```sh
my tools list                             # declared tools across selected manifests
my tools info <name>                      # install hints for a declared tool
my doctor [--no-fetch] [--fix]            # git freshness, sessions, services, derived drift, last sync, manifests, tools
```

Data-returning commands expose `--json` where shown. Structured errors use a
machine-readable `{error, message, remediation}` with a concrete next command,
so an agent that hits a wall can recover without a human.
`my doctor` fetches refs before reporting behind/ahead counts by default; use
`--no-fetch` for an offline view labeled as of the last fetch. It also reports
service materialization health, active work sessions, missing session
worktrees, and archived session counts. `--fix` only fast-forwards clean stale
manifest/content checkouts and reconciles derived guidance, MCP config, and
skills; dirty, diverged, repo, remote-unknown checkouts, and session work are
reported rather than touched.

## Supported Harnesses

| Harness | Install path |
|---|---|
| Claude Code | `~/.claude/skills/<skill>` |
| Codex | `~/.codex/skills/<skill>` |
| OpenCode | `~/.config/opencode/skills/<skill>` |
| Antigravity | `~/.agents/skills/<skill>` |

Managed org-skill launches use the project-local seam where available: Claude
Code receives a launch-root `.claude/skills` mirror, Codex and Antigravity read
launch-root `.agents/skills`, and OpenCode stays on its global path as a
compatibility exception.

Missing harnesses are skipped silently — `my` configures what is present and
never fails because a harness is absent.

## The Toolchain Around `my`

`my` is the organization layer of a broader agentic toolchain. Each piece is
its own project with one job, and they compose without depending on each
other's internals:

- **`my` (this repo)** — org tooling, primarily for agents: the manifest
  defines the organization; umbrellas and workspaces materialize it so humans
  and AI operators work from the same context with the same commands.
- **[gnit](https://github.com/mostlydev/gnit)** — git-native multi-repo
  workspaces. The umbrella's publish substrate for mounts: cross-repo
  changes, ordered push, reproducible pins.
- **[clawdapus](https://github.com/mostlydev/clawdapus)** — materialization:
  governed agent containers ("claws") compiled from declarative pod files.
  The compile target for turning manifest roles into contained fleet agents,
  with the `my` CLI inside as a governed work surface.
- **cllama** (part of clawdapus) — containment: the governance proxy that
  holds real provider credentials and mediates every model and tool call.
  Agents get scoped bearer tokens, never keys.
- **Policy and audit** sit behind that proxy: behavioral rules compiled from
  the organization's manifest, enforced outside the agent process, with every
  intervention auditable.
- **Gated organization services** — credential brokers and human-reviewed
  communication pipelines — are declared in the manifest and consumed
  identically by human and AI operators: gating is a property of the service,
  not of who is asking.

The shared design principle: external, inward-facing mechanisms (directories,
mounts, proxies, repos) govern agents at boundaries they cannot avoid — never
through any harness's internal machinery.

## Public/Private Boundary

**This repository is the generic mechanism and is public-safe. It must never
contain organization content.**

- **`my` (this repo, public)** — the CLI: onboarding, manifest, skill,
  mount, catalog, and meeting mechanics. Generic. No customer data, no
  proprietary skills, no internal strategy.
- **`<org>-manifest` (private, control plane)** — the org's definition layer:
  `manifest.json`, proprietary skills, product/repo catalog JSON, tool
  declarations, and agent guidance fragments. Admin-writable.
- **`<org>-workspace` (private, data plane)** — the org's operating content:
  customers, meetings, support, fleet, decisions, policy, projects, people.
  Pushed by the whole organization.

The manifest repo stays outside the umbrella entirely; the workspace a user
or agent browses is a mount of the content repositories the manifest defines.
See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design
rationale.

## Roadmap

`my` is pre-alpha and evolving quickly. The phases, with detailed plans
indexed in [docs/plans/](docs/plans/README.md):

- **Shipped — control/data-plane split (v0.13.x).** A private manifest repo
  (the control plane) separate from workspace content repos (the data plane);
  `my publish` creates the private remotes; auto-publishing is gated on
  record adoption (`my record adopt`, Git intent-to-add). Plans:
  [single-checkout workspace](docs/plans/2026-06-10-single-checkout-workspace.md),
  [execution plane](docs/plans/2026-06-10-execution-plane.md) (safety patch).
- **Shipped — work sessions, Mode A (v0.14.0–v0.17.0).** `my work
  start|status|list|resume|finish`: visible `work/<id>` git worktrees per
  session, a session registry consulted by `my sync` and `my doctor`,
  session-aware content commands, and opt-in launches via
  `my ai --new-session`, `--session`, and `-r`/`--resume` (base umbrella
  remains the default).
  Plan: [execution plane](docs/plans/2026-06-10-execution-plane.md), Mode A.
- **Shipped — products/repos split (v0.15.0).** Catalog products are pure
  business entities (no `git_url`) that may link implementing repos;
  organization repositories live in `catalog/repos.json` with an `my repos`
  noun and `--repo` launch flags, cloned under `repos/<id>`. Plan:
  [products/repos split](docs/plans/2026-06-11-products-repos-split.md).
- **Shipped — roles and services, Mode A (v0.18.0).** Manifest `roles` and
  `services` sections describing the organization's remote surfaces (APIs,
  MCP servers, gated brokers), `my services`/`my roles` inspection verbs,
  `my setup --role`, umbrella-root `.mcp.json` materialized from checked-in
  or inline connection data — references only, never secrets or network
  fetches — and doctor service-health checks. Plans:
  [execution plane](docs/plans/2026-06-10-execution-plane.md),
  [v0.18 scope](docs/plans/2026-06-12-v018-scope.md).
- **Shipped — CLI package refactor (v0.19.0).** The `internal/cli` package
  and its tests are split into cohesive per-domain files, leaving `cli.go`
  as a small app core/dispatcher/update shell and `cli_test.go` as shared
  helpers plus cross-cutting tests. Plan:
  [CLI package refactor](docs/plans/2026-06-12-cli-package-refactor.md).
- **Shipped — contract rules and verbs (v0.20.0-v0.21.0).** A built-in Fleet
  Work Contract in generated guidance and the bundled self-skill (start fleet
  work from `my fleet get`, record it in support records, carry identifiers),
  a support-record next-step hint in `my fleet get` output, a manifest
  `contract` list of short, binding org rules rendered as an
  `## Organization Contract` section in `AGENTS.md`, and
  `my contract list` plus `my admin contract add|remove` for the standard
  inspect/review-commit-push workflow. Plan:
  [contract rules](docs/plans/2026-06-12-contract-rules.md).
- **Shipped — customer records move to the data plane (v0.22.0).**
  `my customers list` now reads mounted `customers/*.md` records, customer
  alias resolution still feeds meetings/support/fleet filters, and
  `my admin customers add|edit` plus manifest `catalog/customers.json`
  loading/validation are removed.
  Plan: [data surfaces](docs/plans/2026-06-13-data-surfaces.md), Slice 1.
- **Shipped — data bindings over surfaces (v0.23.0).** Manifest `data_bindings`
  maps stable operational data nouns (`customers`, `meetings`, `support`,
  `fleet`) to existing `mount:<id>` or `service:<id>` surfaces. Mount-backed
  bindings narrow today's local record commands; service-backed domain
  invocation remains deferred. Plan:
  [data surfaces](docs/plans/2026-06-13-data-surfaces.md), Slice 2.
- **Shipped — domain notes over bound surfaces (v0.24.0).** Data bindings can
  carry labeled guidance fragments for their backing surfaces without changing
  the top-level org contract. This completes the near-term data-surface scope;
  service-backed domain invocation and contained runners remain future/YAGNI.
  Plan:
  [data surfaces](docs/plans/2026-06-13-data-surfaces.md), Slice 3.
- **Shipped — contained runner launch projection (v0.25.0).** Org-side
  launch-artifact projection (`my compile`): manifest + role + skills +
  mounts compile into a deterministic Clawdapus-facing JSON artifact for
  governed fleet agents, with baseline and manifest contract blocks preserved
  as enforce-level inputs. The Clawdapus pod/context emitter and descriptor
  fetch/cache remain later phases.
  Plans: [compile launch projection](docs/plans/2026-06-14-compile-launch-plan.md),
  [execution plane](docs/plans/2026-06-10-execution-plane.md).
- **Shipped (v0.26.0) — human onboarding walkthrough.** `my onboard` introduced
  a minimal human tour; `my setup` stays the deterministic machine configurator,
  with explicit `my setup --interactive` for prompting. Tour completion is
  stored umbrella-local; no new top-level verbs such as `configuration`,
  `configure`, or `tour`. Plan:
  [onboarding walkthrough](docs/plans/2026-06-14-onboarding-walkthrough.md).
- **Shipped (v0.27.0) — launch-scoped skill composition.** `my ai` composes
  manifest profile/skill selectors into disposable `.agents/skills` state under
  the launch root, with harness mirrors where a launch-root seam exists.
  Automatic setup/sync/doctor paths stop installing organization skills globally
  for launch-root-capable harnesses; OpenCode remains compatibility-global until
  a project-local skill seam is proven; the global `my` self-skill remains
  during migration. Gemini harness support was removed entirely in favor of
  Antigravity (`agy`). Plans:
  [launch-scoped skill composition](docs/plans/2026-06-14-launch-scoped-skill-composition.md),
  [ADR 0001](docs/decisions/0001-launch-scoped-skill-composition.md).
- **Shipped (v0.29.0), refined after dogfood — model-driven onboarding.**
  `my onboarding [--harness NAME]` launches a harness with the bundled `my`
  self-skill's Agent-Operated Onboarding guidance, and `my onboard` remains a
  compatibility alias. The launcher chooses AUTHOR vs JOIN from manifest state,
  uses direct harness exec for zero-manifest bootstrap, reuses `my ai --setup`
  when a manifest exists, and now teaches through split-pane command sets
  instead of an `OK` handshake. Onboarding stays focused on basic human
  workflows: launch harnesses, start/resume/finish sessions, check sync/doctor,
  and paste raw context into harness chat for agents to operate deeper record
  and admin commands. Publish remains behind `my publish --print` plus explicit
  human approval. Plan:
  [model-driven onboarding](docs/plans/2026-06-15-model-driven-onboarding.md).
- **Later — substrate upgrades.** A gnit backend for sessions once umbrellas
  bootstrap as gnit control workspaces, and managed read-only base mounts
  for contained launches. Plan:
  [execution plane](docs/plans/2026-06-10-execution-plane.md).

This section is kept current with every release and direction change; the
per-plan status lives in [docs/plans/README.md](docs/plans/README.md).

## Design Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — the design: concepts, the
  manifest schema, umbrella shape, mount scoping, skill resolution, the
  agents-primary philosophy, and the public/private boundary.
- [docs/PLAN.md](docs/PLAN.md) — public-safe implementation plan: current
  baseline, active direction, and non-goals.
- [docs/plans/](docs/plans/README.md) — long-form design plans with a status
  index (active / shipped / superseded). Start with
  [the execution plane](docs/plans/2026-06-10-execution-plane.md) for where
  the CLI is headed: sessions, contained runners, and organization services.
- [examples/acme-workspace](examples/acme-workspace) — neutral split
  manifest/content fixture for local development.

## Dependencies

Go standard library only. No third-party Go dependencies, by policy — supply
chain surface is part of the threat model for a tool that installs things.

## Contributing

The public repo carries the generic CLI and its tests only. Fixtures and
examples use neutral placeholders (`acme`, `example`, `sampleco`). If a change
would require organization-specific data to test, the test belongs against a
private manifest, not here. `go test ./...` and `go vet ./...` must pass.

## License

MIT — see [LICENSE](LICENSE).
