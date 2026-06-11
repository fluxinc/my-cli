---
name: our
description: Use when working inside an Our AI umbrella (a per-user operating dir, e.g. ~/our, containing a .our/ directory and a generated AGENTS.md), or when the user asks about the `our` CLI, organization manifests, workspace skills, mounts, meeting notes, customers/catalog, onboarding a harness, or syncing/publishing local workspace changes. Also use when an AGENTS.md says the workspace is Our AI-managed.
---

This skill teaches a harness how to operate inside an Our AI workspace.

`our` is a small, dependency-free CLI that bootstraps an AI agent's working
environment from a single organization **manifest**. One command gives every
installed harness (Claude Code, Codex, OpenCode, Gemini) the same skills, the
same company context, and the same local tooling.

## When To Use

Use this skill when any of these are true:

- the working directory is, or sits under, an Our AI **umbrella** (a `.our/`
  marker directory and a generated `AGENTS.md` are present)
- the user mentions `our`, an organization manifest, workspace skills, mounts,
  meeting notes, customers, the product catalog, onboarding, or syncing the
  workspace
- the user wants to record a meeting/decision or publish local workspace changes

Prefer the `our` CLI over hand-rolled git or file edits for anything it owns.
Run `our --help` (or `our <command> --help`) for the authoritative surface.

## The Model

`our` has eight concepts. Everything in the CLI is one of these:

- **Manifest** — an organization's configuration in its own private Git repo:
  declares skills, mounts, catalog, and tool hints. The single source of
  truth, and the control plane only — the manifest is not the workspace; the
  workspace is a mount of things the manifest defines, and day-to-day work
  never edits the manifest. Registered locally with `our init <org-id>` for a
  new organization or `our manifests add <name> <git-url>` for an existing
  repo, then refreshed with `our manifests sync`.
- **Skill** — a capability installed into harness skill directories. Either
  *static* (a directory in the manifest repo) or *tool-provided*.
- **Umbrella** — a per-user operating envelope (e.g. `~/our` or `~/acme`): a
  `.our/` identity namespace plus mounts and local scratch. Launch harnesses
  from here so they pick up the generated `AGENTS.md` context.
- **Mount** — a Git-backed content folder cloned into the umbrella (handbook,
  meeting notes, policy, docs).
- **Session** — an isolated unit of work under `<umbrella>/work/<id>`: a git
  worktree per content mount on a fresh `our/work/<id>` branch, plus
  session-local `scratch/`, with a registry record under `.our/sessions/`.
  `our ai` starts one by default; work leaves a session only through
  `our work finish --land | --publish | --discard`.
- **Catalog** — JSON inventories: products (business entities, which may link
  repos), canonical customers, and repos (the organization's repositories,
  cloned on demand under `repos/<id>` via `our repos add`).
- **Guidance** — the generated root `AGENTS.md` (and `CLAUDE.md` pointer) built
  from a public baseline plus manifest fragments.
- **Tool** — an external executable the org depends on; `our` reports presence
  and install hints, it never silently installs tools.

## Operational vs Admin

`our` splits its surface by risk. This boundary matters for an agent:

- **Operational** commands are read-only or only touch local per-user state.
  They are safe to run freely: `our skills list/show/status`,
  `our meetings list/search/get`, `our support list/search/get`,
  `our fleet list/search/get`,
  `our customers list`,
  `our products list`, `our repos list/add/remove`, `our tools list/info`, `our root`,
  `our ai`, `our doctor`, `our manifests list`, `our mounts list`,
  `our work start/status/resume/finish` (sessions are local execution-plane
  state; `finish --publish` only publishes what the sync policy allows), and
  `our sync --print`.
  `our update --check` is also safe for inspection. Run `our update` itself
  only when the user explicitly asks to update the local CLI binary.
- **Admin** commands mutate the shared source of truth (the manifest, catalog,
  guidance, skills declarations). They live under `our admin ...`
  (`our admin skills add/remove`, `our admin customers add/edit`,
  `our admin tools add/edit/remove`,
  `our admin manifests/mounts/meetings/support/setup`) and require explicit
  intent.
  Do not run them to "fix" something unless the user asked to change the
  organization's configuration.

When unsure, reach for the operational form first; it cannot damage shared
state.

## Common Tasks

Bootstrap / refresh the workspace:

```sh
our init <org-id> [--name NAME] [--path DIR] [--umbrella DIR]
                                    # create manifest + content repos locally and register them
our publish [--manifest NAME] [--print]
                                    # create private remotes, rewrite local mount URLs, push both repos
our setup [--manifest NAME] [--no-refresh] [--no-update-check]
                                    # create umbrella, write guidance, install skills, sync mounts
our root [--repo ID] [--no-refresh] [--no-update-check]
                                    # print the umbrella (or repo) path
our ai [--session ID|--no-session] [--repo ID] [--setup] [--no-refresh] [--no-update-check] [harness]
                                    # verify guidance is current, then start a harness in a fresh work session by default
                                    # --setup reconciles the umbrella first when guidance is stale or missing
our doctor [--no-fetch] [--fix]   # git freshness, derived drift, last sync, manifests, tools
```

Use `our init` only when the user explicitly wants to create a new
organization. It creates two local repos — a private manifest repo at the
registry path and a content repo at `<umbrella>/workspace` — commits and
registers them, and prints the next `our setup`, `our ai`, and `our publish`
commands. Everything reports `local-only` until published. Run `our publish`
only when the user wants the organization shared: it creates private remotes
(`<org>-manifest`, `<org>-workspace`), rewrites the manifest's local mount
URLs to the published repos, and pushes both. Never hand-edit mount URLs and
never push a manifest that still references local paths — `our sync` holds it
and `our doctor` names the offending mounts.

`root`, `ai`, and `setup` make a best-effort, TTL-gated refresh of clean
manifest/content checkouts before reading workspace context. They do not touch
dirty, diverged, product, or remote-unknown repositories. Use `--no-refresh`
for one command, `OUR_NO_AUTO_REFRESH=1` globally, or `OUR_REFRESH_TTL=30m`
to tune the default six-hour window. `our ai` also ensures the bundled `our`
self-skill is installed for the selected filesystem harness before exec.

By default, `our ai` creates a fresh session under `<umbrella>/work/<id>` and
starts the harness there. Treat the base umbrella as inspection/admin space; do
not create shared content directly in base mounts unless the operator explicitly
asks for a base edit. Use `our ai --session <id> <harness>` to resume an active
session, and `our ai --no-session <harness>` for base inspection/admin/debug.
Repo launches are base checkouts in this release, so use
`our ai --no-session --repo <id> <harness>` for them. Products are business
catalog entries, not checkouts: records reference them with `--product`,
while code lives in catalog repos managed by `our repos`.

When the refresh cannot converge a checkout, these commands print a stderr
line per repository in the form `notice\t<repo>\t<state>; run ...` (dirty,
ahead, behind, or diverged, with the reconciling command). On seeing one,
finish the current step, then run the suggested command — usually `our sync`,
or `our doctor` for diverged checkouts. Product clones live under
`repos/<id>` (legacy `products/<id>` keeps resolving until `our setup`
migrates it).

These startup commands also make a best-effort, stderr-only check for a newer
Our AI release. The notice never changes stdout, so `cd "$(our root)"` remains
safe. Use `--no-update-check`, `OUR_NO_UPDATE_CHECK=1`, or
`OUR_UPDATE_CHECK_TTL=12h` when the user needs deterministic/offline startup.

Work in sessions:

```sh
our work start [--slug SLUG]      # create an isolated session: worktree per content mount + scratch/
our work status [--all]           # list sessions with per-mount dirty and unlanded state
our work resume [session-id]      # print the cd command for an active session
our work finish [session-id] --land     # commit session content, merge into base, remove worktrees
our work finish [session-id] --publish  # land, then publish landed content per the sync policy
our work finish [session-id] --discard  # delete the session's worktrees, branches, and directory
```

If you are running inside a session (the working directory is under
`<umbrella>/work/<id>`), keep all edits in the session's mount worktrees and
`scratch/`; never edit the base mounts directly. Finish is the only exit:
`--land` holds unadopted `??` files and non-content changes instead of
committing them, so adopt records first (`our meetings/support/fleet add` do
this automatically). While a session is dirty or unlanded, `our sync` holds
outbound publish of that mount and names the session — finish or discard the
session rather than working around the hold.

Update Our AI when explicitly requested:

```sh
our update --check                 # compare this binary with the latest release
our update                         # download, checksum-verify, and replace it
our update --version 0.5.0         # install a specific release
```

`our update` refuses package-managed or non-writable installs and prints the
right follow-up command, such as `brew upgrade our`,
`go install github.com/fluxinc/our-ai/cmd/our@latest`, or re-running
`install.sh`.

Find and record knowledge:

```sh
our meetings list   [--since DATE] [--customer ID] [--partner ID] [--json]
our meetings search <text>        # single keywords match best
our meetings get    <id|path>
our meetings add    <slug> [--date DATE] [--title TEXT] [--customer ID] [--attendees NAME] [--partner ID] [--source-id ID]
                     # --attendees/--partner repeatable; each occurrence is one literal value, commas preserved
                     # a slug that starts with YYYY-MM-DD sets the date and is not double-prefixed
our support list    [--since DATE] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
our support search  <text>        # qmd-first support record search when available
our support get     <id|path>
our fleet list      [--status TEXT] [--customer ID] [--partner ID] [--identifier ID] [--branch NAME] [--where KEY=VALUE] [--json]
our fleet search    <text>        # qmd-first fleet registry search when available
our fleet get       <id|identifier|path>   # resolves any identifier; lists related support records
our fleet set       <id|identifier> KEY=VALUE...  # scalar frontmatter updates; preserves the rest
our fleet add       <id> [--customer ID] [--partner ID] [--status TEXT] [--device TEXT] [--serial TEXT] [--identifier ID] [--config-repo NAME] [--config-branch NAME] [--deployed-site TEXT] [--ship-to TEXT] [--contact TEXT] [--install-date DATE] [--print] [--json]
our support add     <slug> [--date DATE] [--title TEXT] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--observed-by MEMBER] [--approved-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--status open|workaround|resolved] [--feature-candidate] [--print] [--json]
                     # --identifier repeatable: record every device, order, or asset
                     # identifier that applies (workstation name, equipment/box ID,
                     # functional location, sales order) so records link later
                     # --claimed-by = org member who worked it; --observed-by repeatable
                     # for others involved; never set --approved-by without explicit
                     # operator approval — it is the human sign-off field
our customers list  [--json]      # canonical customer IDs, aliases, partners
```

Manage skills on this machine:

```sh
our skills list                   # manifest/source skills available to install
our skills status                 # what's installed across harnesses, and where
our skills install [harness...] | --all
our skills sync                   # reconcile installs with the manifest (prune stale)
our tools list                    # manifest-declared external tools
our tools info <name>             # install hints for one external tool
```

## Sync: reconcile and publish

`our sync` is the routine "make this workspace current and publish what is safe
to publish" command. It pulls inbound updates and, by default (`--publish
auto`), direct-pushes only **private, content-only** changes (e.g. new meeting
notes or support records); manifest/catalog/admin changes, public repos,
divergent branches, and unsafe duplicate-remote checkouts are held back.

```sh
our sync --print                  # plan only: show what would pull/push/hold (always safe)
our sync                          # reconcile + publish per the auto policy
our sync --scope repos            # limit to catalog repo clones (all|local|content|manifest|repos)
our sync --no-derived             # skip skill/guidance reconcile after manifest changes
our sync --publish never          # explicit local-only reconcile
our sync --publish pr             # currently holds changes and reports PR-mode follow-up
```

Plain untracked (`??`) files under declared content paths are held instead of
auto-published. Use `our meetings add`, `our support add`, or `our fleet add`
for new records; those commands mark the created file with Git intent-to-add.
For a manually created file that should publish, run `our record adopt <path>`
after checking that it belongs in the shared content repo. Explicit `git add`
also counts as adoption.

"Derived" means the artifacts generated from the manifest: root guidance
(`AGENTS.md` plus the `CLAUDE.md` pointer) and manifest-declared skills. Sync
reconciles them automatically after a manifest checkout changes.

Rule of thumb for the three similar verbs: `our sync` converges everything
(use it by default); `our doctor` is the repair dry run — it diagnoses,
marks each repairable finding with `would ...`, and prints a fixable count,
while `our doctor --fix` applies exactly that plan; `our manifests sync`
refreshes the registered manifest checkout. Use `our manifests sync` before an
umbrella exists or for multi-manifest administration; when exactly one
manifest changes and an umbrella is known, it also reconciles generated
guidance and manifest skills.

`our sync` uses **Gnit** as its multi-repo publish backend once the umbrella is
a Gnit control workspace; otherwise it uses a guarded built-in Git path. Run
`our sync --print` first to see the plan before publishing. GitHub PR creation
is an Our AI policy layer planned on top of Gnit and `gh`; it is not implemented in
the current CLI yet. A manifest can set top-level `sync.publish_policy` to
`auto`, `never`, or `pr` as the default when `--publish` is omitted; an
explicit CLI flag always wins. A non-print sync writes `.our/last-sync.json`;
use `our doctor` to review the last publish/sync audit. `our doctor` fetches
refs before reporting behind/ahead counts by default; pass `--no-fetch` for an
offline view labeled as of the last fetch. `our doctor --fix` only
fast-forwards clean stale manifest/content checkouts and reconciles generated
guidance plus manifest skills; it reports dirty, diverged, product, and
remote-unknown checkouts instead of touching them.

## Tips

- Launch harnesses with `our ai <harness>`: it starts from a fresh work
  session by default so generated guidance is in scope and the base umbrella
  stays clean. Reserve `cd "$(our root)"` plus `our ai --no-session` for
  inspection/admin/debug.
- Data-returning commands accept `--json`; structured errors carry a concrete
  remediation command — read it and follow it.
- To record what happened in a meeting, use `our meetings add` and then
  `our sync` to publish it, rather than editing files and pushing by hand.
- To record a resolved support problem, use `our support add` with anonymized
  body text; put linkable attribution in frontmatter — the canonical customer
  ID, every applicable device, order, or asset identifier (`--identifier`,
  repeatable), and the org members involved (`--claimed-by` for who worked it,
  `--observed-by` for others) — so recurrence on the same equipment or by the
  same people is discoverable later. Leave `approved_by` empty unless the
  operator explicitly approves the record.
- To look up a deployed instance, prefer `our fleet get <id-or-identifier>` —
  any sales order, functional location, serial, or hostname resolves — and use
  the related support records it lists. Record workflow transitions with
  `our fleet set <id> status=<value>`, then publish with the suggested
  `our sync --message` command so each transition stays a readable git commit.
- This skill is installed and kept current by the `our` CLI itself; do not
  hand-edit the installed copy.
