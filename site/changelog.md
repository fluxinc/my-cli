# Changelog

## 0.3.0 - 2026-06-08

### Added

- Added `flux sync`: nit-first bidirectional umbrella reconciliation with a
  conservative auto policy (direct-push only private, content-only changes).
- Nit is the multi-repo publish backend once the umbrella is a Nit control
  workspace; a guarded Flux Git path is the fallback. Same-remote duplicate
  checkouts are detected and held when unsafe.

## 0.2.0 - 2026-06-03

### Added

- Added `flux skills show` and `flux skills status`.
- Added `--skill` filtering for skill install, uninstall, sync, purge, and
  status commands.
- Added `flux skills sync` and `flux skills purge` for local harness
  reconciliation.
- Added `flux admin skills add` and `flux admin skills remove` for manifest
  skill authoring against a maintainer checkout.
- Added admin aliases for mutating/configuration commands.
- Added release packaging, checksum-verified install script, and GitHub Pages
  documentation site.

### Changed

- Clarified that operational reads stay top-level while admin commands mutate
  shared or workspace configuration.
- Preserved top-level mutating forms as quiet compatibility aliases.

## 0.1.0 - 2026-05-21

### Added

- Added `flux customers list` for canonical customer IDs, aliases, domains, and
  partner metadata from manifest customer catalogs.
- Added customer alias resolution for `flux meetings list`, `search`, and
  `add`.
- Added meeting metadata for partners, attendees, and source IDs.
- Added qmd-first meeting search with built-in markdown search fallback.
- Added configured umbrella discovery for meeting commands run outside the
  umbrella directory.
- Added `flux version` and `flux --version`.

### Changed

- Updated generated workspace guidance and the example handbook skill.
- Documented the split between skill materialization and regenerated workspace
  guidance.
