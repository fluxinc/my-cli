# flux-ai

`flux` is a small CLI for bootstrapping agent workspaces from an organization
manifest. A manifest can declare skills, tool hints, content mounts, product
catalog entries, and local meeting-note folders that AI harnesses can use.

This repository contains the generic CLI mechanics. Organization-specific
skills, catalogs, handbook content, and meeting notes should live in a separate
private manifest repository.

## Quick Start

```sh
# install the CLI
go install github.com/fluxinc/flux-ai/cmd/flux@latest

# register an organization manifest
flux manifest add acme https://github.com/example/acme-workspace.git
flux manifest sync acme

# create the local umbrella, install declared skills, and sync default mounts
flux onboard --manifest acme

# inspect what was installed
flux skills list --manifest acme
```

Run `flux --help` for the full surface.

## What It Manages

- **Manifests**: organization configuration stored in a Git repo.
- **Skills**: static manifest skills and tool-provided skills.
- **Umbrellas**: local workspace envelopes such as `~/acme` or `~/flux`.
- **Mounts**: Git-backed content folders inside an umbrella.
- **Catalogs**: JSON product catalog entries from the manifest repo.
- **Tools**: manifest-declared diagnostics and install hints.

## Supported Harnesses

| Harness | Install path |
|---|---|
| Claude Code | `~/.claude/skills/<skill>` |
| Codex | `~/.codex/skills/<skill>` |
| OpenCode | `~/.opencode/skills/<skill>` |
| Gemini | via `gemini skills link` |

Missing harnesses are skipped silently.

## Public/Private Boundary

Keep this repo public-safe. Put organization knowledge, customer notes, product
runbooks, private catalogs, and proprietary skills in a private manifest repo.

## Dependencies

Go stdlib only. No third-party Go dependencies.

## License

MIT (see [LICENSE](LICENSE)).
