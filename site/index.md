---
layout: home

hero:
  name: flux
  text: Manifest-backed AI workspace tooling
  tagline: One command gives every installed AI harness the same skills, generated guidance, mounts, and local operating context.
  image:
    src: /flux-glyph.svg
    alt: flux
  actions:
    - theme: brand
      text: Install
      link: /guide/quickstart
    - theme: alt
      text: View on GitHub
      link: https://github.com/fluxinc/flux

features:
  - icon: "01"
    title: One manifest
    details: Skills, mounts, catalog, tools, and generated guidance come from a single organization source of truth.
  - icon: "02"
    title: Every harness
    details: Claude Code, Codex, OpenCode, and Gemini receive the same declared capabilities without per-harness setup.
  - icon: "03"
    title: Local umbrella
    details: Each operator gets a deterministic workspace envelope with synced content, products, state, and scratch.
  - icon: "04"
    title: Operational by default
    details: Read, inspect, launch, diagnose, and materialize local skills without mutating shared source of truth.
  - icon: "05"
    title: Admin when explicit
    details: Manifest authoring, onboarding, mounts, and content writes live under the admin surface with clear write targets.
  - icon: "06"
    title: Public mechanism
    details: The CLI stays generic and public-safe. Organization knowledge lives in private manifest and workspace repos.
---

## Install

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/flux/master/install.sh | sh
```

Re-run the installer to update to the latest release.

## First Run

```sh
flux manifest add acme https://github.com/example/acme-workspace.git
flux manifest sync acme
flux onboard --manifest acme
cd "$(flux root --manifest acme)" && claude
```

`flux launch --manifest acme codex` performs the same root resolution and
guidance freshness check before starting a harness.

## The Operating Shape

```
~/acme/
├── .flux/          # workspace identity and local state
├── handbook/       # manifest-declared content mount
├── products/       # opted-in product repos
├── personal/       # local-only scratch
├── AGENTS.md       # generated root guidance
└── CLAUDE.md       # compatibility pointer when supported
```

Start with the [quickstart](/guide/quickstart), then read
[the model](/guide/the-model) for the boundary between the public CLI and a
private organization manifest.
