# our

`our` is a small, dependency-free CLI that bootstraps an AI agent's working
environment from a single organization manifest. One command turns a fresh
machine into one where every installed AI harness — Claude Code, Codex,
OpenCode, Gemini — has the same skills, the same company context, and the same
local tooling.

It is built for a world where **agents are the primary operators**. Humans own
intent — goals, products, decisions — and express it as content in a Git repo.
`our` is the deterministic, machine-friendly bridge that gets that content and
those capabilities onto every agent surface, the same way, every time.

Documentation: https://fluxinc.github.io/our-ai/

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/our-ai/master/install.sh | sh

our init acme --name "Acme"
our setup
our ai codex
```

That's the whole setup. `our ai codex` resolves the umbrella, verifies the
generated guidance, and starts Codex in the base umbrella. Agents that need
isolated content work opt in with `our ai --new-session codex` or resume a
known session with `our ai --session <id> codex`.
`our init` creates two local repos — a private manifest repo (the control
plane: manifest, catalog, skills) and a content repo at `~/acme/workspace`
(the actual workspace) — registers them, and works offline. When ready to
share, `our publish` creates the private remotes, points the manifest at the
published content repo, and pushes both; teammates join with a single
`our manifests add acme <manifest-url>`.
Run `our update` to update an install from the latest GitHub release; re-running
`install.sh` still works as a fallback. Developers can still install from source
with `go install github.com/fluxinc/our-ai/cmd/our@latest`. The installer also
installs Our AI's bundled `our` skill into existing harnesses
so agents know how to use the CLI itself.

## The Model

`our` has eight concepts. Everything in the CLI is one of these:

| Concept | What it is |
|---|---|
| **Manifest** | An organization's configuration, stored in its own private Git repo — the control plane. Declares skills, mounts, catalog, services, roles, and tool hints. The single source of truth; it is not the workspace, and day-to-day work never touches it. |
| **Skill** | A capability installed into harness skill directories. *Organization* skills are *static* (a directory in the manifest repo) or *tool-provided* (materialized by an external tool's own installer). The CLI also ships one public, organization-neutral *self-skill* named `our`, embedded in the binary, that teaches harnesses how to use `our` itself. |
| **Umbrella** | A per-user operating envelope (e.g. `~/acme`): a `.our/` identity namespace plus mounts and local scratch as peers. When initialized for sync publishing, this is the Gnit control workspace so multi-repo commits and pushes have one substrate. |
| **Mount** | A Git-backed content folder cloned into the umbrella (handbook, meeting notes, policy, docs). Can be path-scoped so only the relevant subtree lands. |
| **Session** | An isolated unit of work under `work/<id>`: a git worktree per content mount on a fresh branch, plus session-local scratch. Create one with `our work start` or `our ai --new-session`; inspect it with `our work status` or `our work list`; work leaves only through `our work finish --land\|--publish\|--discard`. |
| **Catalog** | JSON inventories for products (business entities, which may link repos), repos (the organization's repositories), and canonical customers. Users opt specific repos into their umbrella on demand. |
| **Guidance** | Generated root `AGENTS.md` instructions for agents, built from a public baseline plus manifest-declared and role-specific fragments. `CLAUDE.md` points to the same file. |
| **Tool** | An external executable the org depends on. `our` reports presence and install hints — it never silently installs tools. |

Skills arrive from two places, split by a public/private line. The `our`
self-skill is **public** and travels **inside the CLI binary** — it is
organization-neutral, carries no company content, and the binary keeps it
current on its own. **Organization skills** are **private** to a manifest repo
you control and appear only once you add and sync that manifest, so they can
carry guidance specific to your team. Nothing organization-specific is ever
baked into the public CLI.

## Commands

Run `our --help` for the authoritative surface. The essentials:

### Onboarding

```sh
our setup [harness...] | --all   # create umbrella, write guidance/MCP config, install skills, sync mounts
                                    # [--manifest NAME] [--umbrella DIR] [--role ROLE] [--copy] [--link] [--print]
                                    # [--no-refresh] [--no-update-check]
```

`setup` is the normal path: idempotent, non-interactive, safe to re-run.

### Startup

```sh
our root [--repo ID] [--no-refresh] [--no-update-check]
                                             # print the umbrella or repo path
our ai [--new-session|--session ID|--no-session] [--repo ID] [--setup] [--no-refresh] [--no-update-check] [harness]
                                             # verify guidance, then start a harness
our ai codex --model gpt-5              # pass harness flags after the harness name
our ai --new-session codex
our ai --session 2026-06-11-work-ab12 codex
our ai --repo sample-service codex
our ai --print codex                    # print cd <umbrella> && codex
```

`ai` refuses to start against missing or stale generated guidance. Pass
`--setup` to reconcile first, or run `our setup` directly. By default it
launches from the base umbrella, or from the current active work session when
run inside `work/<id>`. Use `--new-session` to create a fresh isolated session,
`--session` to resume a known active session, and `--no-session` to ignore a
current session for base inspection/admin/debug.
`root`, `ai`, and `setup` also run a best-effort, TTL-gated refresh of
clean manifest/content checkouts so startup sees current context without
touching dirty, diverged, repo, or remote-unknown checkouts. Use
`--no-refresh` for one command, `OUR_NO_AUTO_REFRESH=1` globally, or
`OUR_REFRESH_TTL=30m` to tune the default six-hour refresh window.

Startup commands also print stderr `notice` lines for dirty, ahead, behind, or
diverged checkouts, each with the remediation command, keeping stdout clean.
They additionally check, at most once per day, whether a newer Our AI
release exists. Notices are stderr-only so `cd "$(our root)"` stays path-pure.
Use `--no-update-check` for one command, `OUR_NO_UPDATE_CHECK=1` globally, or
`OUR_UPDATE_CHECK_TTL=12h` to tune the check window.

### Updating Our AI

```sh
our update --check                  # compare this binary with the latest release
our update                          # download, verify, and replace this binary
our update --version 0.5.0          # install a specific release
```

`our update` verifies the release tarball against `checksums.txt` before
replacing the binary. It refuses package-managed or non-writable installs and
prints the matching follow-up, such as `brew upgrade our`,
`go install github.com/fluxinc/our-ai/cmd/our@latest`, or re-running
`install.sh`.

### Manifests

```sh
our init <org-id> [--name NAME] [--path DIR] # create manifest + content repos locally
our publish [--manifest NAME] [--print]      # create private remotes, rewrite mount URLs, push
our manifests add <name> <git-url>          # register an org manifest
our manifests sync <name...> | --all        # refresh checkout and derived artifacts
our manifests list                          # list registered manifests
our manifests validate <name|path>          # schema + reference checks
```

When a non-print manifest sync pulls or clones exactly one manifest, `our`
reconciles derived workspace artifacts for an existing matching umbrella:
generated guidance, umbrella MCP config, and manifest skills. Pass
`--no-derived` for a cache-only refresh or `--umbrella DIR` when the intended
umbrella is not the current one.

### Services and roles

```sh
our services list [--json]
our services get <id> [--json]
our roles list [--json]
our roles get <id> [--json]
our setup --role operator
```

Manifest `services` describe remote organization surfaces such as HTTP APIs and
MCP servers. Manifest `roles` grant services and optional role-specific
guidance without pruning mounts. `our setup --role <id>` stores the local role
selection in `.our/state.json`, appends that role's guidance fragments to
`AGENTS.md`, and materializes umbrella-root `.mcp.json` for locally described
MCP services visible to the role.

### Skills

```sh
our skills self status [--json]            # installed/absent status for the bundled our skill
our skills self install [harness...] | --all
our skills list [--json]                   # manifest/source skills available to install
our skills show <id|slug> [--json]         # one skill's metadata and source path
our skills status [--skill ID_OR_SLUG]     # installed/absent status across harnesses
our skills install [harness...] | --all    # materialize skills into harness dirs
our skills uninstall <harness...> | --all  # remove materialized skills
our skills sync [harness...] | --all       # install/update and prune stale Our AI-managed skills
our skills purge <harness...> | --all      # remove Our AI-managed materializations
```

`our skills self ...` manages the bundled, public-safe `our` CLI skill. It is
installed by `install.sh`, refreshed during `our setup`, ensured for the
selected filesystem harness before `our ai` execs it, and quietly kept current
for already-installed file-based harness copies when a newer binary runs.

Use `--skill ID_OR_SLUG` on manifest skill `install`, `uninstall`, `sync`,
`purge`, or `status` to target a single declared skill. Manifest skills install
as symlinks by default (`--copy` to vendor a copy). `our` records provenance
and refuses to clobber a directory it did not place. `skills sync` prunes stale
Our AI-managed manifest skills by default, but does not remove the bundled
`our` self-skill; pass `--no-prune` to only install/update. Skill commands only
refresh harness skill directories; run `our setup` when manifest guidance or
the generated umbrella `AGENTS.md` should change without a manifest sync.

Manifest authoring is explicit admin work:

```sh
our admin skills add <skill-dir> --id org:name --manifest-dir <checkout>
our admin skills remove <id|slug> --manifest-dir <checkout> [--prune-orphans]
our admin tools add <id> --manifest-dir <checkout> --mode required|optional --purpose "..."
our admin tools edit <id> --manifest-dir <checkout> [--purpose "..."]
our admin tools remove <id> --manifest-dir <checkout>
```

Admin commands write a maintainer checkout, not the synced cache. They
refuse dirty git checkouts unless `--force` is supplied, never commit or push,
and require explicit flags for duplicate-prone or destructive cleanup such as
`--keep-original`, `--remove-original`, `--delete-source`, or product
`related_skills` pruning. Removing a skill reports now-orphaned tools and
allowed namespaces; `--prune-orphans` removes those too. Tool removal refuses
manifest skills that still reference the tool. After a write they print the
relevant `git status` and `git diff` follow-up commands.

`our admin` is the home for shared/workspace configuration. Mutating or
configuration commands are reachable there too, with the top-level forms
retained as quiet compatibility aliases:

```sh
our admin setup ...                 # alias of our setup
our admin manifests add|sync|validate  # alias of our manifests ...
our admin mounts add|remove|sync       # alias of our mounts ...
our admin meetings add                # alias of our meetings add
our admin support add                 # alias of our support add
our admin customers add|edit          # edit catalog/customers.json
our admin tools add|edit|remove       # edit manifest tools[]
```

Admin aliases are intentionally limited to those mutating/configuration
subcommands. Operational reads (`list`/`show`/`status`/`search`/`get`) stay
under their top-level commands.

### Umbrella mounts

```sh
our mounts list                             # manifest content mounts
our mounts add <kind:id|id>                 # opt an optional content mount in
our mounts sync <mount...> | --all          # clone or fast-forward mounts
our mounts remove <mount...> [--force]
```

Repo clones are managed with `our repos add <id>` and land under `repos/<id>`
in the umbrella; legacy `products/` checkouts migrate automatically at
`our setup`.

### Sync

```sh
our sync --print                           # plan inbound refresh and outbound publish
our sync [--backend auto|gnit|builtin]         # auto prefers Gnit once the umbrella is initialized
our sync --publish auto|never|direct|pr    # explicit override; direct is CLI-only
our sync --scope all|local|content|manifest|repos  # limit to one repo class; repos = catalog repo clones
our sync --no-derived                      # skip derived guidance/MCP/skill reconcile after manifest changes
```

`our sync` is the routine reconciliation command. Our AI classifies changes,
handles private/public and content/admin policy, and blocks duplicate checkouts
of the same remote until they are collapsed to one canonical checkout. Gnit is
the intended backend for real multi-repo Change creation, ordered push, and
resume; Pins are reserved for intentional recorded workspace states. The Our AI
backend is a guarded bootstrap fallback when a workspace has not been
initialized as a Gnit control workspace. `--publish direct` can publish existing
local commits directly, but dirty non-content/admin files are still held back
instead of being quietly committed. A manifest can set top-level
`sync.publish_policy` to `auto`, `never`, or `pr` as the default when
`--publish` is omitted; an explicit CLI flag always wins. Non-print syncs write
`.our/last-sync.json` so `our doctor` can show the last sync/publish audit.
When sync pulls or publishes a manifest checkout, it reconciles generated
guidance, umbrella MCP config, and manifest skills unless `--no-derived` is
passed.

### Catalog

```sh
our products list [--json]         # the org's product inventory
our customers list [--json]                # canonical customer IDs, aliases, and partners
```

### Meeting notes

```sh
our meetings list   [--since DATE] [--customer ID] [--partner ID] [--product ID] [--json]
our meetings search <text> [--customer ID] [--partner ID] [--product ID] [--json]
our meetings get    <id|path> [--json]
our meetings add    <slug> [--date DATE] [--title TEXT] [--customer ID]
                     [--attendees NAME] [--partner ID] [--source-id ID]
```

A markdown-first operational record (YAML frontmatter), resolved against the
umbrella by default, including the configured umbrella from the registered
manifest when the command is run outside the umbrella. Search uses `qmd` when it
is present and falls back to built-in token-AND markdown search.

### Support records

```sh
our support list   [--since DATE] [--customer ID] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
our support search <text> [--customer ID] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
our support get    <id|path> [--json]
our support add    <slug> [--date DATE] [--title TEXT] [--customer ID]
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
our fleet list   [--status TEXT] [--customer ID] [--partner ID] [--identifier ID]
                  [--branch NAME] [--where KEY=VALUE] [--json]
our fleet search <text> [same filters] [--json]
our fleet get    <id|identifier|path> [--json]
our fleet add    <id> [--customer ID] [--partner ID] [--status TEXT]
                  [--device TEXT] [--serial TEXT] [--identifier ID]
                  [--config-repo NAME] [--config-branch NAME]
                  [--deployed-site TEXT] [--ship-to TEXT] [--contact TEXT]
                  [--install-date DATE] [--print] [--json]
our fleet set    <id|identifier> KEY=VALUE... [--json]
```

A registry record per deployed instance under `fleet/<id>.md`, keyed by a
stable id (hostname or node name) and updated in place. `get` resolves any
entry in the record's `identifiers` list — a sales order, functional location,
or serial — and lists support records sharing an identifier. `set` updates
scalar frontmatter fields while preserving everything else, and suggests an
`our sync --message` command so workflow transitions stay readable in git
history. The status vocabulary is organization-defined.

### Diagnostics

```sh
our tools list                             # declared tools across selected manifests
our tools info <name>                      # install hints for a declared tool
our doctor [--no-fetch] [--fix]            # git freshness, sessions, services, derived drift, last sync, manifests, tools
```

Data-returning commands expose `--json` where shown. Structured errors use a
machine-readable `{error, message, remediation}` with a concrete next command,
so an agent that hits a wall can recover without a human.
`our doctor` fetches refs before reporting behind/ahead counts by default; use
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
| Gemini | via `gemini skills link` |

Missing harnesses are skipped silently — `our` configures what is present and
never fails because a harness is absent.

## The Toolchain Around `our`

`our` is the organization layer of a broader agentic toolchain. Each piece is
its own project with one job, and they compose without depending on each
other's internals:

- **`our` (this repo)** — org tooling, primarily for agents: the manifest
  defines the organization; umbrellas and workspaces materialize it so humans
  and AI operators work from the same context with the same commands.
- **[gnit](https://github.com/mostlydev/gnit)** — git-native multi-repo
  workspaces. The umbrella's publish substrate for mounts: cross-repo
  changes, ordered push, reproducible pins.
- **[clawdapus](https://github.com/mostlydev/clawdapus)** — materialization:
  governed agent containers ("claws") compiled from declarative pod files.
  The compile target for turning manifest roles into contained fleet agents,
  with the `our` CLI inside as a governed work surface.
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

- **`our` (this repo, public)** — the CLI: onboarding, manifest, skill,
  mount, catalog, and meeting mechanics. Generic. No customer data, no
  proprietary skills, no internal strategy.
- **`<org>-manifest` (private, control plane)** — the org's definition layer:
  `manifest.json`, proprietary skills, catalog JSON, tool declarations, and
  agent guidance fragments. Admin-writable.
- **`<org>-workspace` (private, data plane)** — the org's operating content:
  meetings, support, fleet, decisions, policy, projects, people. Pushed by
  the whole organization.

The manifest repo stays outside the umbrella entirely; the workspace a user
or agent browses is a mount of the content repositories the manifest defines.
See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design
rationale.

## Roadmap

`our` is pre-alpha and evolving quickly. The phases, with detailed plans
indexed in [docs/plans/](docs/plans/README.md):

- **Shipped — control/data-plane split (v0.13.x).** A private manifest repo
  (the control plane) separate from workspace content repos (the data plane);
  `our publish` creates the private remotes; auto-publishing is gated on
  record adoption (`our record adopt`, Git intent-to-add). Plans:
  [single-checkout workspace](docs/plans/2026-06-10-single-checkout-workspace.md),
  [execution plane](docs/plans/2026-06-10-execution-plane.md) (safety patch).
- **Shipped — sessions (v0.14.0; opt-in launch revision v0.17.0).** `our work start|status|list|resume|finish`: visible
  `work/<id>` git worktrees per session, a session registry consulted by
  `our sync`, explicit `our ai --new-session`/`--session` launch paths, and
  session-aware content commands so record writes from inside `work/<id>` stay
  in the session worktree instead of the base workspace.
  Plan: [execution plane](docs/plans/2026-06-10-execution-plane.md), Mode A.
- **Shipped — products/repos split (v0.15.0).** Catalog products are pure
  business entities (no `git_url`) that may link implementing repos;
  organization repositories live in `catalog/repos.json` with an `our repos`
  noun and `--repo` launch flags, cloned under `repos/<id>`. Plan:
  [products/repos split](docs/plans/2026-06-11-products-repos-split.md).
- **Shipped — session stability and ergonomics (v0.16.0).** Hardening of the
  Mode A session loop: `our ai` no longer leaves an orphan session when the
  harness binary is missing, `our doctor` reports work-session health
  (active state, missing worktrees, archived counts), and `our work list`
  plus session-specific follow-up commands round out the session surface.
  Plan: [execution plane](docs/plans/2026-06-10-execution-plane.md), Mode A.
- **Next — roles and services (v0.18, Mode A).** Manifest `roles` and
  `services` extensions describing the organization's remote surfaces (APIs,
  MCP servers, gated brokers), inspection verbs, `our setup --role`, and
  umbrella-root MCP config materialized from checked-in or inline connection
  data — references only, never secrets or network fetches. Plans:
  [execution plane](docs/plans/2026-06-10-execution-plane.md),
  [v0.18 scope](docs/plans/2026-06-12-v018-scope.md) (converged).
- **Later — contained runners (Mode B) and substrate upgrades.** Org-side
  launch-artifact compilation (`our launch compile`) and descriptor
  fetch/cache, compiling manifests into container launch artifacts for
  governed fleet agents, a gnit backend for sessions, and managed read-only
  base mounts. Plan:
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
