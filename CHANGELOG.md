# Changelog

## 0.11.0 - 2026-06-10

### Changed

- `our doctor` now reads as a repair dry run: every finding that
  `our doctor --fix` can repair is marked `would fast-forward`,
  `would reconcile derived guidance and skills`, or
  `would reinstall the our self-skill` (also `would_fix` in `--json`), with a
  closing `fixable` count pointing at `our doctor --fix`. Findings doctor
  cannot repair keep their explanatory remediation text.

## 0.10.0 - 2026-06-10

### Changed

- Product repositories now clone under `repos/<id>` instead of `products/<id>`.
  `our setup` migrates an existing `products/` directory automatically, and
  legacy `products/<id>` checkouts keep resolving until migrated. The sync
  scope accepts `repos` (the `products` spelling still works).
- Startup commands (`our root`, `our ai`, `our setup`) now print a stderr
  `notice` line for checkouts the auto-refresh cannot converge — dirty, ahead,
  behind, or diverged — naming the repository and the command that reconciles
  it. Stdout is unchanged, so `cd "$(our root)"` remains safe.

### Fixed

- `our manifests sync` now reconciles generated guidance and manifest skills
  after pulling or cloning a changed manifest for an existing matching umbrella;
  pass `--no-derived` for a cache-only refresh.
- `our ai` now ensures the bundled `our` self-skill is installed before it
  execs a filesystem harness, and manifest skill sync/purge no longer removes
  that self-skill.

## 0.9.0 - 2026-06-10

### Added

- Added `our support list/search/get/add` for anonymized support records under
  `support/`, with qmd-first search, a built-in markdown search fallback, and
  linkable frontmatter attribution: an optional canonical customer ID, a
  repeatable `--identifier` list for device, order, or asset identifiers, and
  org member fields (`claimed_by`, `observed_by`, and the human sign-off
  `approved_by`).
- Added `support` as a manifest mount kind; handbook mounts without explicit
  `include_paths` now treat `support/` as approved content for sync publishing.
- Added `our fleet list/search/get/add/set` and the `fleet` mount kind: a
  registry of deployed instances with one record per stable id under
  `fleet/<id>.md`, updated in place. `our fleet get` resolves any entry in a
  record's `identifiers` list and reports related support records;
  `our fleet set` updates scalar frontmatter while preserving everything else
  and suggests an `our sync --message` command for the transition; and
  `our support add` warns when an `--identifier` is unknown to the registry.
- Extracted the shared `internal/record` engine behind meetings, support, and
  fleet records (frontmatter parsing now ignores inline YAML comments in
  unquoted values).

### Fixed

- `our doctor` now reports an absent or stale `our` self-skill on present
  harnesses instead of claiming no skill drift, and `our doctor --fix`
  reinstalls it (#13).

## 0.8.0 - 2026-06-09

### Changed

- Renamed the CLI from `flux` to `our` and the project to Our AI
  (`github.com/fluxinc/our-ai`). Commands now read as possessive English:
  `our meetings list`, `our customers list`, `our sync`.
- Renamed `flux launch` to `our ai` and `flux onboard` to `our setup`;
  `our launch` and `our onboard` remain as deprecated aliases that warn on
  stderr. The `--onboard` flag on `our ai` is now `--setup`.
- Pluralized noun command groups: `our manifests`, `our mounts`,
  `our workspaces`; `flux catalog list products` is now `our products list`.
- Renamed the built-in sync backend from `flux` to `builtin`
  (`our sync --backend auto|gnit|builtin`).
- Renamed the umbrella marker directory from `.flux/` to `.our/`, the data
  home from `~/.local/share/flux` to `~/.local/share/our`, and environment
  variables from `FLUX_*` to `OUR_*`.
- Release archives are now `our-ai_<version>_<os>_<arch>.tar.gz` containing
  the `our` binary; `install.sh` installs `our` from `fluxinc/our-ai`.
- The bundled self-skill is now `our` (id `our:self`).
- `our doctor` reports legacy Flux state — `.flux/` directories,
  `~/.local/share/flux`, `~/.config/flux/manifests.json`, `FLUX_*`
  environment variables, a `flux` binary on `PATH`, and installed `flux`
  self-skills — with migration remediation.

## 0.7.0 - 2026-06-09

### Added

- Added `flux tools list` to enumerate manifest-declared tools with mode,
  purpose, and install hints.
- Added `flux admin tools add|edit|remove` to manage manifest tool
  declarations, with manifest validation and a reference check that blocks
  removing a tool still referenced by a skill.

### Changed

- Renamed the multi-repo sync backend from Nit to Gnit: `flux sync` now takes
  `--backend gnit`, detects `.gnit/roster.yaml`, invokes the `gnit` binary,
  and reports `gnit_root` in JSON output.

## 0.6.0 - 2026-06-09

### Added

- Added `flux update` for checksum-verified self-updates from GitHub release
  tarballs, with `--check`, `--version`, `--json`, and managed-install refusal
  guidance.
- `flux root`, `flux launch`, and `flux onboard` now emit a non-blocking stderr
  notice when a newer Flux release is available, with `--no-update-check`,
  `FLUX_NO_UPDATE_CHECK`, and `FLUX_UPDATE_CHECK_TTL` controls.
- `flux doctor` now reports the running Flux version and latest known release.

## 0.5.0 - 2026-06-08

### Added

- `flux doctor` now reports per-checkout Git freshness with behind/ahead/dirty
  counts, fetches refs by default, and supports `--no-fetch` for offline
  local-tracking-ref checks.
- `flux doctor` now reports derived drift for manifest skills and generated
  workspace guidance.
- Added `flux doctor --fix` to fast-forward clean stale manifest/content
  checkouts and reconcile generated guidance plus manifest skills while leaving
  dirty, diverged, product, and remote-unknown checkouts untouched.
- `flux sync` now records non-print runs to `.flux/last-sync.json`, and
  `flux doctor` surfaces the last sync/publish audit.
- `flux sync` now reconciles generated guidance and manifest skills after
  manifest checkout changes; pass `--no-derived` to skip that step.
- `flux root`, `flux launch`, and `flux onboard` now run a best-effort,
  TTL-gated refresh for clean manifest/content checkouts, with `--no-refresh`,
  `FLUX_NO_AUTO_REFRESH`, and `FLUX_REFRESH_TTL` controls.
- Manifests can set `sync.publish_policy` to `auto`, `never`, or `pr` as the
  default for `flux sync` when `--publish` is omitted.
- Added `flux admin customers add` and `flux admin customers edit` for explicit
  maintainer writes to `catalog/customers.json`.
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

- Added `flux sync`: gnit-first bidirectional umbrella reconciliation. It pulls
  inbound updates and publishes safe local changes outbound, with a
  conservative auto policy that direct-pushes only private, content-only changes
  and holds admin/manifest, public, mixed, divergent, and duplicate-remote
  checkouts.
- Gnit is the multi-repo publish backend when the umbrella is a Gnit control
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
