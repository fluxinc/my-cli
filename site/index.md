---
layout: home

hero:
  name: My AI
  text: Your team's AI, set up once
  tagline: One organization manifest gives every AI harness generated guidance, mounts, launch profiles, and local operating context — on any machine, with one command.
  image:
    src: /my-cli-glyph.svg
    alt: My AI mark
  actions:
    - theme: brand
      text: Install
      link: /guide/quickstart
    - theme: alt
      text: View on GitHub
      link: https://github.com/fluxinc/my-cli

features:
  - icon: "01"
    title: One manifest
    details: Skills, mounts, catalog, tools, and generated guidance flow from a single organization source of truth.
  - icon: "02"
    title: Every harness
    details: Claude Code, Codex, OpenCode, and Antigravity launch from one manifest-defined profile, using the best available skill path for each harness.
  - icon: "03"
    title: Local umbrella
    details: Every operator gets the same deterministic workspace, with synced content, catalog repositories, state, and scratch.
  - icon: "04"
    title: Operational by default
    details: Read, inspect, launch, diagnose, and materialize local skills without mutating the shared source of truth.
  - icon: "05"
    title: Admin when explicit
    details: Manifest authoring, mounts, and content writes live under the admin surface with clear write targets.
  - icon: "06"
    title: Public mechanism
    details: The CLI stays generic and open. Organization knowledge lives in your private manifest and workspace repos.
---

## Install

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/my-cli/master/install.sh | sh
```

Run `my update` to update to the latest release; re-running the installer also
works.

## First Run

```sh
my init acme --name "Acme"
my onboard
my ai codex
```

`my onboard` gives the human walkthrough and offers interactive setup.
`my ai codex` performs the same root resolution and guidance freshness check
before starting a harness. `my init` creates a private manifest repo (the
control plane) plus a content repo at `~/acme/workspace` (the actual
workspace), all local and working offline; `my publish` later creates the
private remotes and pushes both.

## The Operating Shape

```
~/acme/
├── .my-cli/          # workspace identity and local state
├── workspace/      # manifest-declared content mount (its own repo)
├── repos/          # opted-in catalog repositories
├── personal/       # local-only scratch
├── .mcp.json       # generated local MCP config
├── AGENTS.md       # generated root guidance
└── CLAUDE.md       # compatibility pointer when supported
```

The organization manifest lives in its own private repository outside the
umbrella — the workspace is a mount of things the manifest defines, and
day-to-day work never edits the manifest itself.

## Part of a Toolchain

`my` is the organization layer of a broader agentic stack: org context and
knowledge for every agent and human, from one manifest. It composes with
[gnit](https://github.com/mostlydev/gnit) (git-native multi-repo workspaces,
the umbrella's publish substrate) and
[clawdapus](https://github.com/mostlydev/clawdapus) (governed agent
containers whose cognition is mediated by the cllama proxy) — manifest roles
compile into contained fleet agents that carry the `my` CLI as a governed
work surface. Gated organization services (credential brokers,
human-reviewed communications) are declared in the manifest and consumed the
same way by human and AI operators.

Start with the [quickstart](/guide/quickstart), then read
[the model](/guide/the-model) for the boundary between the public CLI and a
private organization manifest.
