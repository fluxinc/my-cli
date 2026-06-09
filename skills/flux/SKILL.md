---
name: flux
description: Use when working inside a Flux umbrella (a per-user operating dir, e.g. ~/flux, containing a .flux/ directory and a generated AGENTS.md), or when the user asks about the `flux` CLI, organization manifests, workspace skills, mounts, meeting notes, customers/catalog, onboarding a harness, or syncing/publishing local workspace changes. Also use when an AGENTS.md says the workspace is Flux-managed.
---

This skill teaches a harness how to operate inside a Flux workspace.

`flux` is a small, dependency-free CLI that bootstraps an AI agent's working
environment from a single organization **manifest**. One command gives every
installed harness (Claude Code, Codex, OpenCode, Gemini) the same skills, the
same company context, and the same local tooling.

## When To Use

Use this skill when any of these are true:

- the working directory is, or sits under, a Flux **umbrella** (a `.flux/`
  marker directory and a generated `AGENTS.md` are present)
- the user mentions `flux`, an organization manifest, workspace skills, mounts,
  meeting notes, customers, the product catalog, onboarding, or syncing the
  workspace
- the user wants to record a meeting/decision or publish local workspace changes

Prefer the `flux` CLI over hand-rolled git or file edits for anything it owns.
Run `flux --help` (or `flux <command> --help`) for the authoritative surface.

## The Model

`flux` has seven concepts. Everything in the CLI is one of these:

- **Manifest** — an organization's configuration in a Git repo: declares skills,
  mounts, catalog, and tool hints. The single source of truth. Registered
  locally with `flux manifest add <name> <git-url>` and refreshed with
  `flux manifest sync`.
- **Skill** — a capability installed into harness skill directories. Either
  *static* (a directory in the manifest repo) or *tool-provided*.
- **Umbrella** — a per-user operating envelope (e.g. `~/flux` or `~/acme`): a
  `.flux/` identity namespace plus mounts and local scratch. Launch harnesses
  from here so they pick up the generated `AGENTS.md` context.
- **Mount** — a Git-backed content folder cloned into the umbrella (handbook,
  meeting notes, policy, docs).
- **Catalog** — JSON inventories for products and canonical customers.
- **Guidance** — the generated root `AGENTS.md` (and `CLAUDE.md` pointer) built
  from a public baseline plus manifest fragments.
- **Tool** — an external executable the org depends on; `flux` reports presence
  and install hints, it never silently installs tools.

## Operational vs Admin

`flux` splits its surface by risk. This boundary matters for an agent:

- **Operational** commands are read-only or only touch local per-user state.
  They are safe to run freely: `flux skills list/show/status`,
  `flux meetings list/search/get`, `flux customers list`,
  `flux catalog list products`, `flux tools list/info`, `flux root`,
  `flux launch`, `flux doctor`, `flux manifest list`, `flux mount list`, and
  `flux sync --print`.
  `flux update --check` is also safe for inspection. Run `flux update` itself
  only when the user explicitly asks to update the local CLI binary.
- **Admin** commands mutate the shared source of truth (the manifest, catalog,
  guidance, skills declarations). They live under `flux admin ...`
  (`flux admin skills add/remove`, `flux admin customers add/edit`,
  `flux admin tools add/edit/remove`,
  `flux admin manifest/mount/meetings/onboard`) and require explicit intent.
  Do not run them to "fix" something unless the user asked to change the
  organization's configuration.

When unsure, reach for the operational form first; it cannot damage shared
state.

## Common Tasks

Bootstrap / refresh the workspace:

```sh
flux onboard [--manifest NAME] [--no-refresh] [--no-update-check]
                                    # create umbrella, write guidance, install skills, sync mounts
flux root [--product ID] [--no-refresh] [--no-update-check]
                                    # print the umbrella (or product) path
flux launch [--no-refresh] [--no-update-check] [harness]
                                    # verify guidance is current, then start a harness
flux doctor [--no-fetch] [--fix]   # git freshness, derived drift, last sync, manifests, tools
```

`root`, `launch`, and `onboard` make a best-effort, TTL-gated refresh of clean
manifest/content checkouts before reading workspace context. They do not touch
dirty, diverged, product, or remote-unknown repositories. Use `--no-refresh`
for one command, `FLUX_NO_AUTO_REFRESH=1` globally, or `FLUX_REFRESH_TTL=30m`
to tune the default six-hour window.

These startup commands also make a best-effort, stderr-only check for a newer
Flux release. The notice never changes stdout, so `cd "$(flux root)"` remains
safe. Use `--no-update-check`, `FLUX_NO_UPDATE_CHECK=1`, or
`FLUX_UPDATE_CHECK_TTL=12h` when the user needs deterministic/offline startup.

Update Flux when explicitly requested:

```sh
flux update --check                 # compare this binary with the latest release
flux update                         # download, checksum-verify, and replace it
flux update --version 0.5.0         # install a specific release
```

`flux update` refuses package-managed or non-writable installs and prints the
right follow-up command, such as `brew upgrade flux`,
`go install github.com/fluxinc/flux/cmd/flux@latest`, or re-running
`install.sh`.

Find and record knowledge:

```sh
flux meetings list   [--since DATE] [--customer ID] [--partner ID] [--json]
flux meetings search <text>        # single keywords match best
flux meetings get    <id|path>
flux meetings add    <slug> [--date DATE] [--title TEXT] [--customer ID] [--attendees NAME] [--partner ID] [--source-id ID]
                     # --attendees/--partner repeatable; each occurrence is one literal value, commas preserved
                     # a slug that starts with YYYY-MM-DD sets the date and is not double-prefixed
flux customers list  [--json]      # canonical customer IDs, aliases, partners
```

Manage skills on this machine:

```sh
flux skills list                   # manifest/source skills available to install
flux skills status                 # what's installed across harnesses, and where
flux skills install [harness...] | --all
flux skills sync                   # reconcile installs with the manifest (prune stale)
flux tools list                    # manifest-declared external tools
flux tools info <name>             # install hints for one external tool
```

## Sync: reconcile and publish

`flux sync` is the routine "make this workspace current and publish what is safe
to publish" command. It pulls inbound updates and, by default (`--publish
auto`), direct-pushes only **private, content-only** changes (e.g. new meeting
notes); manifest/catalog/admin changes, public repos, divergent branches, and
unsafe duplicate-remote checkouts are held back.

```sh
flux sync --print                  # plan only: show what would pull/push/hold (always safe)
flux sync                          # reconcile + publish per the auto policy
flux sync --no-derived             # skip skill/guidance reconcile after manifest changes
flux sync --publish never          # explicit local-only reconcile
flux sync --publish pr             # currently holds changes and reports PR-mode follow-up
```

`flux sync` uses **Gnit** as its multi-repo publish backend once the umbrella is
a Gnit control workspace; otherwise it uses a guarded built-in Git path. Run
`flux sync --print` first to see the plan before publishing. GitHub PR creation
is a Flux policy layer planned on top of Gnit and `gh`; it is not implemented in
the current CLI yet. A manifest can set top-level `sync.publish_policy` to
`auto`, `never`, or `pr` as the default when `--publish` is omitted; an
explicit CLI flag always wins. A non-print sync writes `.flux/last-sync.json`;
use `flux doctor` to review the last publish/sync audit. `flux doctor` fetches
refs before reporting behind/ahead counts by default; pass `--no-fetch` for an
offline view labeled as of the last fetch. `flux doctor --fix` only
fast-forwards clean stale manifest/content checkouts and reconciles generated
guidance plus manifest skills; it reports dirty, diverged, product, and
remote-unknown checkouts instead of touching them.

## Tips

- Launch harnesses from the umbrella root (`cd "$(flux root)"`) so generated
  guidance is in scope.
- Data-returning commands accept `--json`; structured errors carry a concrete
  remediation command — read it and follow it.
- To record what happened in a meeting, use `flux meetings add` and then
  `flux sync` to publish it, rather than editing files and pushing by hand.
- This skill is installed and kept current by the `flux` CLI itself; do not
  hand-edit the installed copy.
