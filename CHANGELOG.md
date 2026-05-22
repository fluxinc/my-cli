# Changelog

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
