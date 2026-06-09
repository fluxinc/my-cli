# Changelog

## 0.7.0 - 2026-06-09

### Added

- Added `flux tools list` to enumerate manifest-declared tools.
- Added `flux admin tools add|edit|remove` for manifest tool declarations,
  with validation and skill-reference checks on removal.

### Changed

- Renamed the multi-repo sync backend from Nit to Gnit (`--backend gnit`,
  `.gnit/roster.yaml`, `gnit_root` in JSON output).

## 0.6.0 - 2026-06-09

### Added

- Added `flux update` for checksum-verified self-updates from GitHub release
  tarballs, with `--check`, `--version`, `--json`, and managed-install guidance.
- `flux root`, `flux launch`, and `flux onboard` emit a non-blocking stderr
  notice when a newer Flux release is available, with `--no-update-check`,
  `FLUX_NO_UPDATE_CHECK`, and `FLUX_UPDATE_CHECK_TTL` controls.
- `flux doctor` reports the running Flux version and latest known release.

## 0.5.0 - 2026-06-08

### Added

- `flux doctor` reports per-checkout Git freshness, derived skill/guidance
  drift, and the last sync/publish audit.
- Added `flux doctor --fix` for guarded fast-forward remediation of clean
  stale manifest/content checkouts plus derived skill/guidance reconcile.
- `flux sync` records non-print runs to `.flux/last-sync.json`; doctor fetches
  refs by default and supports `--no-fetch` for offline freshness checks.
- `flux sync` reconciles generated guidance and manifest skills after manifest
  checkout changes, with `--no-derived` as an escape hatch.
- `flux root`, `flux launch`, and `flux onboard` perform a best-effort,
  TTL-gated refresh for clean manifest/content checkouts, with `--no-refresh`,
  `FLUX_NO_AUTO_REFRESH`, and `FLUX_REFRESH_TTL` controls.
- Manifests can set `sync.publish_policy` to `auto`, `never`, or `pr` as the
  default for `flux sync` when `--publish` is omitted.
- Added `flux admin customers add` and `flux admin customers edit` for customer
  catalog writes.
- `flux admin skills remove` reports orphaned tools and allowed skill namespaces,
  and can remove them with `--prune-orphans`.

### Fixed

- `flux meetings add` handles date-prefixed slugs and comma-containing
  attendees/partners correctly.
- Manifest writes omit zero-value JSON fields instead of adding unrelated
  serialization noise.

## 0.4.0 - 2026-06-08

### Added

- Added the bundled `flux` CLI self-skill and `flux skills self
  install|uninstall|status`.

### Changed

- The installer and `flux onboard` now install the bundled Flux self-skill into
  existing harnesses, and human CLI runs quietly refresh already-installed
  file-based copies.
- Documented the public/private skill model: the `flux` self-skill ships in the
  public binary; organization skills stay private to a manifest you control.

## 0.3.0 - 2026-06-08

### Added

- Added `flux sync`: gnit-first bidirectional umbrella reconciliation with a
  conservative auto policy (direct-push only private, content-only changes).
- Gnit is the multi-repo publish backend once the umbrella is a Gnit control
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
