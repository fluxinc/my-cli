# Changelog

## Unreleased

### Added

- `flux admin skills remove` now reports orphaned tool declarations and allowed
  skill namespaces, and can remove them with `--prune-orphans`.

### Fixed

- `flux meetings add` no longer double-prefixes ids when the slug already starts
  with a date, and repeatable list flags keep comma-containing values intact.
- Manifest writes no longer add zero-value JSON noise such as empty `source`,
  `requires`, `workspaces`, or `skill_install` fields.

## 0.4.0 - 2026-06-08

### Added

- Added a bundled `flux` CLI self-skill and `flux skills self
  install|uninstall|status` so installs can teach agent harnesses how to use
  Flux itself, separate from organization manifest skills.

### Changed

- `install.sh` now runs the installed binary to install the bundled Flux
  self-skill into existing harness skill directories, and normal human CLI runs
  quietly refresh already-installed file-based copies.
- Documented the public/private skill model across the README and docs site: the
  `flux` self-skill ships in the public binary, while organization skills stay
  private to a manifest you control.

## 0.3.0 - 2026-06-08

### Added

- Added `flux sync`: nit-first bidirectional umbrella reconciliation. It pulls
  inbound updates and publishes safe local changes outbound, with a
  conservative auto policy that direct-pushes only private, content-only changes
  and holds admin/manifest, public, mixed, divergent, and duplicate-remote
  checkouts.
- Nit is the multi-repo publish backend when the umbrella is a Nit control
  workspace; a guarded Flux Git path is the fallback. Same-remote duplicate
  checkouts are detected, tolerated when clean, and held when a sibling has
  pending changes.

## 0.2.0 - 2026-06-03

### Added

- Added `flux skills show` and `flux skills status` for operational skill
  inspection.
- Added `--skill` filtering for install, uninstall, sync, purge, and status.
- Added `flux skills sync` and `flux skills purge` for local harness
  reconciliation with Flux-managed provenance.
- Added `flux admin skills add` and `flux admin skills remove` for explicit
  manifest skill authoring.
- Added admin aliases for mutating/configuration commands while keeping
  operational reads top-level.
- Added GoReleaser packaging, a checksum-verified `install.sh`, and a
  VitePress GitHub Pages documentation site.

### Changed

- Clarified the split between operational skill materialization and admin
  source-of-truth changes.
- Kept top-level mutating forms as quiet compatibility aliases.

## 0.1.0 - 2026-05-21

### Added

- Added `flux customers list` for canonical customer IDs, aliases, domains, and
  partner metadata from manifest customer catalogs.
- Added customer alias resolution for `flux meetings list`, `search`, and `add`
  so shorthand values can resolve to canonical customer IDs.
- Added `--partner`, `--attendees`, and `--source-id` meeting metadata support.
- Added qmd-first meeting search with built-in token-AND markdown search as a
  fallback.
- Added configured umbrella discovery for meeting commands run outside the
  umbrella directory.
- Added `flux version` and `flux --version`.

### Changed

- Updated generated workspace guidance and the example handbook skill to direct
  agents through `flux customers list` before customer-specific meeting work.
- Documented that `flux skills install` refreshes harness skill directories and
  `flux onboard` regenerates workspace guidance such as `AGENTS.md`.
