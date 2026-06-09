# flux

`flux` is a small, dependency-free CLI that bootstraps an AI agent's working
environment from a single organization manifest. One command turns a fresh
machine into one where every installed AI harness — Claude Code, Codex,
OpenCode, Gemini — has the same skills, the same company context, and the same
local tooling.

It is built for a world where **agents are the primary operators**. Humans own
intent — goals, products, decisions — and express it as content in a Git repo.
`flux` is the deterministic, machine-friendly bridge that gets that content and
those capabilities onto every agent surface, the same way, every time.

Documentation: https://fluxinc.github.io/flux/

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/flux/master/install.sh | sh

flux manifest add acme https://github.com/example/acme-workspace.git
flux manifest sync acme
flux onboard --manifest acme
cd "$(flux root --manifest acme)" && claude
```

That's the whole setup. Launch AI harnesses from the umbrella root so they see
the generated workspace context; `flux launch --manifest acme codex` performs
the same root resolution and verifies the generated guidance before starting.
Re-run `install.sh` to update to the latest GitHub release. Developers can
still install from source with `go install github.com/fluxinc/flux/cmd/flux@latest`.
The installer also installs Flux's bundled `flux` skill into existing harnesses
so agents know how to use the CLI itself.

## The Model

`flux` has seven concepts. Everything in the CLI is one of these:

| Concept | What it is |
|---|---|
| **Manifest** | An organization's configuration, stored in a Git repo. Declares skills, mounts, catalog, and tool hints. The single source of truth. |
| **Skill** | A capability installed into harness skill directories. *Organization* skills are *static* (a directory in the manifest repo) or *tool-provided* (materialized by an external tool's own installer). The CLI also ships one public, organization-neutral *self-skill* named `flux`, embedded in the binary, that teaches harnesses how to use `flux` itself. |
| **Umbrella** | A per-user operating envelope (e.g. `~/acme`): a `.flux/` identity namespace plus mounts and local scratch as peers. When initialized for sync publishing, this is the Nit control workspace so multi-repo commits and pushes have one substrate. |
| **Mount** | A Git-backed content folder cloned into the umbrella (handbook, meeting notes, policy, docs). Can be path-scoped so only the relevant subtree lands. |
| **Catalog** | JSON inventories for products and canonical customers. Users opt specific products into their umbrella on demand. |
| **Guidance** | Generated root `AGENTS.md` instructions for agents, built from a public baseline plus manifest-declared fragments. `CLAUDE.md` points to the same file. |
| **Tool** | An external executable the org depends on. `flux` reports presence and install hints — it never silently installs tools. |

Skills arrive from two places, split by a public/private line. The `flux`
self-skill is **public** and travels **inside the CLI binary** — it is
organization-neutral, carries no company content, and the binary keeps it
current on its own. **Organization skills** are **private** to a manifest repo
you control and appear only once you add and sync that manifest, so they can
carry guidance specific to your team. Nothing organization-specific is ever
baked into the public CLI.

## Commands

Run `flux --help` for the authoritative surface. The essentials:

### Onboarding

```sh
flux onboard [harness...] | --all   # create umbrella, write guidance, install skills, sync mounts
                                    # [--manifest NAME] [--umbrella DIR] [--copy] [--link] [--print]
```

`onboard` is the normal path: idempotent, non-interactive, safe to re-run.

### Startup

```sh
flux root [--product ID]                     # print the umbrella or product path
flux launch [--product ID] [harness]         # verify guidance, then start a harness
flux launch codex --model gpt-5              # pass harness flags after the harness name
flux launch --print codex                    # print cd <umbrella> && codex
```

`launch` refuses to start against missing or stale generated guidance. Pass
`--onboard` to reconcile first, or run `flux onboard` directly.

### Manifests

```sh
flux manifest add <name> <git-url>          # register an org manifest
flux manifest sync <name...> | --all        # fetch/refresh the manifest cache
flux manifest list                          # list registered manifests
flux manifest validate <name|path>          # schema + reference checks
```

### Skills

```sh
flux skills self status [--json]            # installed/absent status for the bundled flux skill
flux skills self install [harness...] | --all
flux skills list [--json]                   # manifest/source skills available to install
flux skills show <id|slug> [--json]         # one skill's metadata and source path
flux skills status [--skill ID_OR_SLUG]     # installed/absent status across harnesses
flux skills install [harness...] | --all    # materialize skills into harness dirs
flux skills uninstall <harness...> | --all  # remove materialized skills
flux skills sync [harness...] | --all       # install/update and prune stale Flux-managed skills
flux skills purge <harness...> | --all      # remove Flux-managed materializations
```

`flux skills self ...` manages the bundled, public-safe `flux` CLI skill. It is
installed by `install.sh`, refreshed during `flux onboard`, and quietly kept
current for already-installed file-based harness copies when a newer binary
runs.

Use `--skill ID_OR_SLUG` on manifest skill `install`, `uninstall`, `sync`,
`purge`, or `status` to target a single declared skill. Manifest skills install
as symlinks by default (`--copy` to vendor a copy). `flux` records provenance
and refuses to clobber a directory it did not place. `skills sync` prunes stale
Flux-managed skills by default; pass `--no-prune` to only install/update. Skill
commands only refresh harness skill directories; rerun `flux onboard` when
manifest guidance or the generated umbrella `AGENTS.md` should change too.

Manifest authoring is explicit admin work:

```sh
flux admin skills add <skill-dir> --id org:name --manifest-dir <checkout>
flux admin skills remove <id|slug> --manifest-dir <checkout> [--prune-orphans]
```

Admin skill commands write a maintainer checkout, not the synced cache. They
refuse dirty git checkouts unless `--force` is supplied, never commit or push,
and require explicit flags for duplicate-prone or destructive cleanup such as
`--keep-original`, `--remove-original`, `--delete-source`, or product
`related_skills` pruning. Removing a skill reports now-orphaned tools and
allowed namespaces; `--prune-orphans` removes those too. After a write they
print the relevant `git status` and `git diff` follow-up commands.

`flux admin` is the home for shared/workspace configuration. Mutating or
configuration commands are reachable there too, with the top-level forms
retained as quiet compatibility aliases:

```sh
flux admin onboard ...                 # alias of flux onboard
flux admin manifest add|sync|validate  # alias of flux manifest ...
flux admin mount add|remove|sync       # alias of flux mount ...
flux admin meetings add                # alias of flux meetings add
flux admin customers add|edit          # edit catalog/customers.json
```

Admin aliases are intentionally limited to those mutating/configuration
subcommands. Operational reads (`list`/`show`/`status`/`search`/`get`) stay
under their top-level commands.

### Umbrella mounts

```sh
flux mount list                             # manifest mounts + opted-in products
flux mount add <kind:id|id>                 # opt a catalog product / mount in
flux mount sync <mount...> | --all          # clone or fast-forward mounts
flux mount remove <mount...> [--force]
```

### Sync

```sh
flux sync --print                           # plan inbound refresh and outbound publish
flux sync [--backend auto|nit|flux]         # auto prefers Nit once the umbrella is initialized
flux sync --publish auto|never|direct|pr    # auto is private content-only
```

`flux sync` is the routine reconciliation command. Flux classifies changes,
handles private/public and content/admin policy, and blocks duplicate checkouts
of the same remote until they are collapsed to one canonical checkout. Nit is
the intended backend for real multi-repo Change creation, ordered push, and
resume; Pins are reserved for intentional recorded workspace states. The Flux
backend is a guarded bootstrap fallback when a workspace has not been
initialized as a Nit control workspace. `--publish direct` can publish existing
local commits directly, but dirty non-content/admin files are still held back
instead of being quietly committed.

### Catalog

```sh
flux catalog list products [--json]         # the org's product inventory
flux customers list [--json]                # canonical customer IDs, aliases, and partners
```

### Meeting notes

```sh
flux meetings list   [--since DATE] [--customer ID] [--partner ID] [--product ID] [--json]
flux meetings search <text> [--customer ID] [--partner ID] [--product ID] [--json]
flux meetings get    <id|path> [--json]
flux meetings add    <slug> [--date DATE] [--title TEXT] [--customer ID]
                     [--attendees NAME] [--partner ID] [--source-id ID]
```

A markdown-first operational record (YAML frontmatter), resolved against the
umbrella by default, including the configured umbrella from the registered
manifest when the command is run outside the umbrella. Search uses `qmd` when it
is present and falls back to built-in token-AND markdown search.

### Diagnostics

```sh
flux tools info <name>                      # install hints for a declared tool
flux doctor                                 # guidance freshness, manifest validity, mounts, tools
```

Data-returning commands expose `--json` where shown. Structured errors use a
machine-readable `{error, message, remediation}` with a concrete next command,
so an agent that hits a wall can recover without a human.

## Supported Harnesses

| Harness | Install path |
|---|---|
| Claude Code | `~/.claude/skills/<skill>` |
| Codex | `~/.codex/skills/<skill>` |
| OpenCode | `~/.config/opencode/skills/<skill>` |
| Gemini | via `gemini skills link` |

Missing harnesses are skipped silently — `flux` configures what is present and
never fails because a harness is absent.

## Public/Private Boundary

**This repository is the generic mechanism and is public-safe. It must never
contain organization content.**

- **`flux` (this repo, public)** — the CLI: onboarding, manifest, skill,
  mount, catalog, and meeting mechanics. Generic. No customer data, no
  proprietary skills, no internal strategy.
- **`<org>-workspace` (private)** — the org's operating layer: `manifest.json`,
  proprietary skills, catalog JSON, tool declarations, and handbook content
  (meetings, decisions, policy, projects).

The manifest repo is private and is also mounted as the org's handbook content,
**scoped** so only content directories land in the umbrella — the manifest and
skill sources stay in the manifest cache and are never exposed as a second,
drifting copy. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full
design rationale.

## Design Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — the design: concepts, the
  manifest schema, umbrella shape, mount scoping, skill resolution, the
  agents-primary philosophy, and the public/private boundary.
- [docs/PLAN.md](docs/PLAN.md) — public-safe implementation plan and repo
  boundaries for continuing work while this repo remains published.
- [docs/plans/2026-05-28-startup-context-ergonomics.md](docs/plans/2026-05-28-startup-context-ergonomics.md)
  — converged design for `flux root`, `flux launch`, `flux doctor` guidance
  freshness, and the post-launch orientation section in generated `AGENTS.md`.
- [examples/acme-workspace](examples/acme-workspace) — neutral manifest,
  catalog, skill, and handbook fixture for local development.

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
