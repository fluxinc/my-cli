# Changelog

## 0.19.0 - 2026-06-12

### Changed

- Split the `internal/cli` implementation into per-domain files without
  behavior changes: `cli.go` now holds only the app core, dispatcher, usage,
  version, and update plumbing, while command implementations live with their
  domains. The matching CLI tests were split into per-domain `_test.go`
  files, leaving `cli_test.go` for shared helpers and cross-cutting tests.

## 0.18.0 - 2026-06-12

### Added

- Manifest `services` and `roles` sections: services describe the
  organization's remote surfaces (`kind: http|mcp`, `describe_ref`, URI
  `auth_ref` such as `op://`, `env://`, `broker://`, or `none`, optional
  server.json-shaped inline `connection` for MCP); roles grant mounts,
  skills, tools, and services. Validation covers ids, kinds, auth/describe
  references, connection shape, and grant resolution.
- Skills may declare `requires: ["service:<id>"]` alongside the existing
  `workspace:` and `tool:` forms; `our skills show` surfaces declared
  requirements.
- `our services list|get` and `our roles list|get` inspection commands with
  `--json`.
- `our setup --role <id>` persists the selected role in `.our/state.json`,
  appends role-specific guidance fragments to generated `AGENTS.md`, and
  makes the selected role available for role-scoped service materialization.
- `our setup` (and the derived reconcile in `our sync`/`our manifests sync`)
  now materializes an umbrella-root `.mcp.json` from MCP services with local
  connection data (inline `connection` or a checked-in descriptor), scoped to
  the selected role. Values pass through as `${VAR}` placeholders — never
  resolved secrets, never network fetches. A hand-written `.mcp.json` is
  never overwritten without `--force`.
- `our doctor` gained a `service` section: it reports MCP services without
  local connection data (URL-only `describe_ref`), missing checked-in
  descriptors, unset environment variables referenced by `auth_ref` or
  connection placeholders, and `op://` references without the op CLI on
  PATH.

## 0.17.0 - 2026-06-11

### Fixed

- Recording commands (`our meetings add`, `our support add`, and
  `our fleet add`) now detect when the current directory is inside an active
  work session and write records to that session's mount worktree instead of
  silently writing to the base umbrella checkout.

### Changed

- `our ai` now launches from the base umbrella by default. Work sessions are
  explicit with `our ai --new-session` or `our ai --session <id>`; when run
  from inside an active session, plain `our ai <harness>` continues launching
  from that current session. `--no-session` remains available to ignore a
  current session for base inspection/admin work.

## 0.16.0 - 2026-06-11

### Fixed

- `our ai` no longer creates an orphan work session when the requested
  harness binary is missing from PATH: the binary is resolved before a new
  default session is created, and the error says no session was created.
  Resume (`--session`) and base (`--no-session`) launches still print the
  exact fallback command. `our ai --print` keeps creating a session, as
  documented.

### Added

- `our doctor` now reports work sessions: each active session shows its
  live state (`ok` when clean, `warning` with dirty/unlanded counts and the
  matching `our work finish` command, `error` when a worktree is missing or
  git inspection fails), and finished/discarded registry records roll up
  into a single archived count. The JSON report gains a `sessions` section.
- Added `our work list` as an alias for `our work status`. Human work-session
  output now includes session-specific follow-up commands: `work start`
  prints `our work finish <id> ...`, and `work finish` prints a `next` line
  for the natural follow-up (`our sync`, `our sync --print`, or
  `our work status`).

## 0.15.0 - 2026-06-11

### Changed

- Split catalog products from repository checkouts: products in
  `catalog/products.json` are now pure business entities (no `git_url`) that
  may link implementing repos via `repos: ["<repo-id>"]`, and the
  organization's repositories live in a new `catalog/repos.json` inventory.
  New `our repos list|add|remove` verbs manage clones under `repos/<id>`
  (`add` is idempotent: an existing clone of the same remote is adopted;
  conflicting paths hold with remediation). `our root`/`our ai` take `--repo`
  (`--product` now errors with the migration hint); `our mounts add
  product:<id>` is removed; the sync scope is `repos` (the `products`
  spelling is gone); sync/doctor report repo checkouts with the `repo` role.
  Records keep `--product` as a business reference. Umbrella state migrates
  automatically (`selected_products` to `selected_repos`, `product:` mount
  entries to `repo:`); clones under `repos/<id>` are untouched. Manifest
  mount kind `repo` is removed — repos.json is the single declaration path.
  A product entry still carrying `git_url` fails validation with the
  migration message, so migrate private manifests before installing this
  release.
- The bundled `our` self-skill and the docs site now document work sessions
  fully: the Session concept in the model, the `our work
  start/status/resume/finish` verbs in the skill's operational surface and
  task list, and quickstart/launch guidance reflecting that `our ai` starts
  in a fresh session by default.

## 0.14.0 - 2026-06-11

### Added

- Added `our work start [--slug]` and `our work status [--all]`: isolated work
  sessions as visible `work/<id>/` directories with a git worktree per synced
  content mount on a fresh `our/work/<id>` branch, session-local `scratch/`,
  a `SESSION.md` summary, and a first-class session registry under
  `.our/sessions/`. Repo-kind mounts and selected products are not included
  in session worktrees yet.
- Added `our work finish [session-id] --land|--publish|--discard`. Landing
  commits intentional dirty session content, rejects unadopted `??` files and
  non-content changes, merges clean session branches into the base checkout,
  removes session worktrees/branches, and records the session outcome.
- `our sync` (and the targeted sync inside `our work finish --publish`) now
  reads the session registry and holds outbound publish of a content mount
  while any active session on it has dirty files or unlanded commits, naming
  the session id, path, and the `our work finish` remediation. Inbound
  fast-forward pulls are unaffected.
- `our ai` now launches from a fresh work session by default, supports
  `--session <id>` for explicit resume, and requires `--no-session` for base
  umbrella or product checkout launches. Added `our work resume [session-id]`
  to print a resumable session path.

## 0.13.2 - 2026-06-10

### Changed

- Removed the conflated self-mount compatibility path: mount `git_url: "."`
  is now invalid, mounts always materialize as separate umbrella checkouts,
  and sync emits separate manifest/content entries instead of a merged
  workspace role.
- Restructured `examples/acme-workspace` into separate `manifest/` and
  `content/` fixture directories.

## 0.13.1 - 2026-06-10

### Added

- Added `our record adopt <path>` to mark an existing file under a declared
  content mount as intentional publish content using Git intent-to-add.

### Fixed

- Recording commands (`our meetings/support/fleet add`) now work in a
  freshly initialized, unpublished organization: `local-only` mounts count
  as usable content roots instead of being skipped.

### Changed

- `our meetings add`, `our support add`, and `our fleet add` now mark created
  records with Git intent-to-add so `our sync` can distinguish Our-created
  records from stray untracked drafts.
- `our sync` now holds plain untracked (`??`) files under content paths and
  names the `our record adopt <path>` remediation instead of auto-committing
  arbitrary new files.

## 0.13.0 - 2026-06-10

### Added

- Added `our publish`: one idempotent command to take a local organization
  online. It creates private remotes for the content and manifest
  repositories (or adopts existing origins and pushes, verifying GitHub
  remotes are private), rewrites local mount URLs to the published remotes in
  a commit scoped to `manifest.json`, updates the registry, and prints the
  teammate join command. `--print` shows the plan without changing anything.
- Checkouts without an `origin` remote now report `local-only` (pointing at
  `our publish`) across `our manifests sync`, `our mounts sync`, and
  `our sync`, instead of failing.
- `our sync` refuses to publish a manifest whose mounts still reference local
  paths, and `our doctor` names each local-path mount with the `our publish`
  remediation, so a machine-local URL can never leak to teammates.

### Changed

- The manifest is now a control plane separate from workspace content:
  `our init` creates two local repositories — a private manifest repo
  (manifest, catalog, skills, agent guidance) at the registry path, and a
  content repo at `<umbrella>/workspace` with the handbook directories.
  The workspace never contains `manifest.json`, and hosting permissions can
  restrict manifest pushes to admins while the whole organization pushes
  content. Published repos default to `<org>-manifest` and `<org>-workspace`.
  `our init --path` now selects the content repo location.
- A mount whose git URL matches the manifest's own remote (or the `"."`
  marker) remains supported as a compatibility layout: it resolves to the
  single registered checkout (no duplicate clone, sparse-checkout skipped,
  one merged sync entry). New organizations get the two-repo layout.

### Fixed

- Content publishing in self-hosted organizations is no longer held back by
  `another checkout of the same remote has pending changes`: the layouts that
  kept duplicate checkouts of one remote are gone.
- Gemini skill installs and uninstalls now respect `--home` isolation instead
  of writing to the real `~/.gemini` (#16).

## 0.12.0 - 2026-06-10

### Added

- Added `our init <org-id>` to create a small local manifest/handbook repo,
  commit it, register it, sync the manifest cache, and print the next setup,
  launch, and optional GitHub publish commands.
- Mount `git_url: "."` now resolves to the Git URL or local path used to
  register the manifest, so self-hosted handbook mounts survive publishing.

### Fixed

- Reworked the quickstart and manifest docs so the first-run path no longer
  points at a dead example manifest URL.

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
