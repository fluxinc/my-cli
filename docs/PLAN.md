# flux Implementation Plan

Status: public-safe working plan for the published CLI repository.

## Repository Shape

`flux` is the generic mechanism. It stays public and contains the Go CLI,
tests, neutral fixtures, and public architecture docs. It must not contain
organization skills, private manifests, customer notes, internal catalogs, or
company-specific operating content.

Each organization provides one private workspace repository. That repository is
both the manifest source and the handbook/content source. The manifest cache is
authoritative for skills, catalog, tool declarations, and mount definitions.
The local umbrella directory, such as `~/acme`, is not a Git repository; it is
the per-user operating envelope containing `.flux/`, scoped content mounts,
detached product clones, and local scratch.

Recovery directories and session reconstructions are local scratch. They are
not part of the public product surface.

## Current Baseline

- Go standard library only.
- Public-safe docs in `README.md` and `docs/ARCHITECTURE.md`.
- Manifest registry, sync, validation, and GitHub auth checks.
- Manifest-declared static skills and tool-provided skills.
- Umbrella creation with `.flux/workspace.json` and `.flux/state.json`.
- Sparse scoped mounts through `include_paths`.
- Product catalog opt-in through `flux mount add product:<id>`.
- Customer catalog listing and alias resolution for meeting filters/adds.
- Markdown meeting commands: `list`, `search`, `get`, and `add`, with qmd-first
  search when `qmd` is installed.
- Neutral example workspace fixture under `examples/acme-workspace/`.
- Public tests use neutral `acme`, `sampleco`, and `sample-product` fixtures.

## Milestones

1. Installer path: add a public install script or release workflow that installs
   the `flux` binary without baking in any one organization's manifest.
2. Onboarding polish: make `flux onboard` the clearest zero-to-go command after
   a manifest is registered, with structured remediation for missing sync,
   missing auth, missing tools, or absent harnesses.
3. Manifest refresh: keep manifest sync explicit as a command, but make normal
   agent flows self-healing where safe. If a cache is stale or missing, return a
   concrete next command or perform a safe sync during onboarding.
4. Testbed loop: keep generic behavior covered by public synthetic fixtures, and
   validate real organization behavior against a private workspace repository
   outside public CI.
5. Entity growth: extend the `meetings` shape to other markdown-first content
   kinds only when the on-disk convention is stable.
6. Release hygiene: before every public push, scan the public tree and history
   for organization content. Before every private push, confirm the private repo
   visibility and avoid mirroring recovered scratch files.

## Non-Goals

- No separate repo just for a manifest unless a specific organization proves it
  needs that split.
- No public org-specific skills or sample data.
- No daemon or MCP surface in v1.
- No silent installation of external tools; manifests provide install hints and
  optional tool skill installers.
