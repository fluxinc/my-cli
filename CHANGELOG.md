# Changelog

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
