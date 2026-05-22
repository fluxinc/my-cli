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

```sh
go install github.com/fluxinc/flux/cmd/flux@latest

flux manifest add acme https://github.com/example/acme-workspace.git
flux manifest sync acme
flux onboard --manifest acme
```

That's the whole setup. Open any AI harness and it already has the org's skills
installed and the org's knowledge synced locally.

## The Model

`flux` has seven concepts. Everything in the CLI is one of these:

| Concept | What it is |
|---|---|
| **Manifest** | An organization's configuration, stored in a Git repo. Declares skills, mounts, catalog, and tool hints. The single source of truth. |
| **Skill** | A capability installed into harness skill directories. Either *static* (a directory in the manifest repo) or *tool-provided* (materialized by an external tool's own installer). |
| **Umbrella** | A per-user, non-Git directory (e.g. `~/acme`) that is the operating envelope: a `.flux/` identity namespace plus mounts and local scratch as peers. |
| **Mount** | A Git-backed content folder cloned into the umbrella (handbook, meeting notes, policy, docs). Can be path-scoped so only the relevant subtree lands. |
| **Catalog** | JSON inventories for products and canonical customers. Users opt specific products into their umbrella on demand. |
| **Guidance** | Generated root `AGENTS.md` instructions for agents, built from a public baseline plus manifest-declared fragments. `CLAUDE.md` points to the same file. |
| **Tool** | An external executable the org depends on. `flux` reports presence and install hints — it never silently installs tools. |

## Commands

Run `flux --help` for the authoritative surface. The essentials:

### Onboarding

```sh
flux onboard [harness...] | --all   # create umbrella, write guidance, install skills, sync mounts
                                    # [--manifest NAME] [--umbrella DIR] [--copy] [--link] [--print]
```

`onboard` is the normal path: idempotent, non-interactive, safe to re-run.

### Manifests

```sh
flux manifest add <name> <git-url>          # register an org manifest
flux manifest sync <name...> | --all        # fetch/refresh the manifest cache
flux manifest list                          # list registered manifests
flux manifest validate <name|path>          # schema + reference checks
```

### Skills

```sh
flux skills install [harness...] | --all    # materialize skills into harness dirs
flux skills uninstall <harness...> | --all
flux skills list [--json]                   # what is installed, where, and its provenance
```

Skills install as symlinks by default (`--copy` to vendor a copy). `flux`
records provenance and refuses to clobber a directory it did not place.
`skills install` only refreshes harness skill directories; rerun `flux onboard`
when manifest guidance or the generated umbrella `AGENTS.md` should change too.

### Umbrella mounts

```sh
flux mount list                             # manifest mounts + opted-in products
flux mount add <kind:id|id>                 # opt a catalog product / mount in
flux mount sync <mount...> | --all          # clone or fast-forward mounts
flux mount remove <mount...> [--force]
```

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
flux doctor                                 # manifest validity, mount state, tool presence
```

Every command accepts `--json`. Errors are structured: a machine-readable
`{error, message, remediation}` with a concrete next command, so an agent that
hits a wall can recover without a human.

## Supported Harnesses

| Harness | Install path |
|---|---|
| Claude Code | `~/.claude/skills/<skill>` |
| Codex | `~/.codex/skills/<skill>` |
| OpenCode | `~/.opencode/skills/<skill>` |
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
