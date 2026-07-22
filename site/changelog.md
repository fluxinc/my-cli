# Changelog

## Unreleased

### Fixed

- Automatic publication is target-aware in partial Gnit umbrellas: exact roster
  members use coordinated publishing, while unrostered My AI checkouts use the
  guarded built-in path. Session publication uses the same planner, previews
  run the same local preflight, and only rostered targets can be reported as
  published by Gnit.
- Coordinated publication now holds before mutation when the roster is invalid,
  a member identity or control root is unpublishable, a member checkout is
  missing, or whole-workspace Gnit push would exceed the selected My AI scope.

### Changed

- `my doctor` reports partial Gnit topology, unmanaged or missing members,
  invalid rosters, and unpublishable control roots without turning supported
  unrostered content mounts into operator errors.

## 0.35.0 - 2026-07-01

### Added

- `my publish --manifest NAME` is the deliberate control-plane publish path: it
  commits and pushes dirty manifest/catalog control files (`manifest.json`,
  `catalog/`, `skills/`, `guidance/`, `agent-guidance/`), ending the
  permanently-dirty manifest checkout without routing the control plane through
  content auto-publish. The low-level form is
  `my sync --publish direct --scope manifest`; dirty files outside the
  control-plane paths still hold.
- `my customers add <domain|slug>` scaffolds a mounted `customers/<id>.md`
  record (`--name`, `--domain`, `--domain-confirmed`, repeatable `--alias` and
  `--partner`); "unknown customer" warnings now point at it instead of keeping a
  literal slug.
- Held-back `my sync` results carry a stable `reason_code` and, where
  actionable, a `next_command` (both surfaced by `my doctor`), including a real
  first recovery step for dirty-behind and diverged mounts.

### Changed

- Handbook mounts default `customers/` and `fleet/` into their publish paths,
  and record writes warn at creation time when the new file lands outside the
  mount's declared publish paths, so records no longer sit unpublished silently.
- Duplicate role/global guidance is de-duplicated in generated guidance and the
  launch projection.

### Fixed

- `my sync`'s active-session hold no longer bounces you toward
  `my session finish --land` when the base checkout is dirty (which
  `requireBaseReady` would refuse): it now names the dirty base files and tells
  you to resolve them first.
- Finished or migrated sessions replace their local `AGENTS.md`/`CLAUDE.md` with
  a finished-session stub and surface concrete stale/inactive-session
  diagnostics, so a shell left inside a landed session no longer dead-ends on
  stale `my work` guidance.

## 0.34.0 - 2026-06-22

### Added

- `my session start|join|resume|status|list|finish` is the primary surface for
  isolated work units (a git worktree per content mount). `my ai --new-session`,
  `--session ID`, and `-r`/`--resume` remain as launch shortcuts; `--new-session`
  now prints join/finish hints. Creating a session surfaces its id, path, and
  mounts plus hints for joining another harness and finishing, and `--json`
  carries `launch_command`, `join_command`, and `finish_command`.

### Changed

- The worktree work-unit is consolidated onto the noun "session" end to end:
  on-disk `work/<id>` becomes `sessions/<id>`, branches `my/work/<id>` become
  `my/session/<id>`, and default ids are noun-free (`YYYY-MM-DD-<hex>`). A lazy,
  idempotent, safe migration moves legacy layouts on session commands and
  `my doctor --fix` (never during `my ai`), skipping ambiguous mounts without
  mutating them. `my work` remains as a deprecated alias.

### Fixed

- `my ai` no longer falsely reports workspace guidance as stale (and no longer
  loops you back to `my setup`) when the selected role contributes guidance: the
  launch freshness check is now role-aware, matching `my setup` and `my doctor`.
  Repairable managed guidance (missing, stale, or broken `CLAUDE.md` alias) is
  reconciled in place before launch; unmanaged guidance still requires
  `my setup --force`.

## 0.33.0 - 2026-06-17

### Added

- Default manifest resolution: with more than one registered manifest, `my`
  commands no longer require `--manifest`. Precedence is explicit `--manifest`,
  then the current umbrella's manifest, then a registry default (initially the
  first-added manifest).
- `my manifests default [<name>] [--clear]` shows or repoints the global default
  manifest, and `my manifests list` marks the active default.

### Changed

- Renamed the bundled self-skill from `my` to `my-cli`; existing managed
  installs migrate automatically on the next CLI run or explicit self-skill
  install, while the canonical id remains `my:self`.

## 0.32.1 - 2026-06-16

### Fixed

- Release CI: CLI Git test clones now get a local commit identity, so the test
  suite that goreleaser runs no longer fails on runners without a global Git
  identity. (v0.32.0 was tagged but failed to publish for this reason.)

### Changed

- Docs site: the header and homepage hero now use the text wordmark `> my ai`
  instead of an image logo.

## 0.32.0 - 2026-06-16

### Changed

- `my sync` is pull-only by default. Publishing local changes now requires
  `my sync --push` or an explicit `--publish` mode; manifest
  `sync.publish_policy` selects the mode for `--push` rather than making bare
  sync publish.
- Human output for setup, sync, and work-session finish is shorter by default,
  with `--verbose` available for full row-level detail.
- Agent-operated onboarding now anchors walkthrough commands at the umbrella
  root and treats explicit "file it" requests as authorization to file a
  public-safe project issue.
- Work-session guidance now embeds concrete startup context for launched
  harnesses: umbrella root, organization, selected role, session id/path,
  mount worktrees, exact resume/finish commands, and the generated base
  umbrella guidance including organization contract rules. Resuming a session
  with `my ai --session` or `my ai -r` rewrites stale session guidance before
  launch.

### Fixed

- `my doctor` no longer reports an older cached latest release as if the
  installed CLI were stale.

## 0.31.0 - 2026-06-16

### Added

- `my ai -r` / `my ai --resume` launches a harness in an active work session,
  with single-session auto-selection, an interactive picker, and deterministic
  non-interactive errors that list active session ids.

### Changed

- Model-driven onboarding now starts a split-pane learn-by-example walkthrough
  instead of asking for an `OK` handshake. The operator runs small command
  sets, the assistant pauses after each set, and onboarding focuses on basic
  workflows while deeper record and admin work stays agent-operated.
- `my work resume` is documented as a shell `cd` helper; use
  `my ai -r [session-id] [harness]` to resume work in a harness.

### Fixed

- Repo-session documentation now states that catalog code repos launch with
  `my ai --repo` and are not landed by `my work finish` yet.

## 0.30.1 - 2026-06-16

### Changed

- `my update` now resolves the latest release through GitHub's public
  `releases/latest` redirect instead of the rate-limited REST API.

### Fixed

- Interactive model-driven onboarding now prompts to replace or skip an existing
  non-My AI launch skill entry instead of aborting before the harness starts.

## 0.30.0 - 2026-06-16

### Added

- `my onboarding --no-agent` to force the deterministic walkthrough/status
  review instead of launching a harness.

### Changed

- `my onboarding` is now the primary guided onboarding command. In an
  interactive terminal it launches model-driven onboarding by default,
  auto-detecting a logged-in or singly installed harness when possible;
  `my onboard` remains as a compatibility alias.
- Onboarding output now uses readable human prompts and next-step commands
  instead of tabular status rows.

## 0.29.0 - 2026-06-16

### Added

- `my admin roles add|edit|remove` and `my admin services add|edit|remove`
  for command-driven manifest authoring of role loadouts and service surfaces.
- `my onboard --agent [--harness NAME]` to launch a harness with the bundled
  Agent-Operated Onboarding guidance. Zero-manifest runs start the harness from
  the current directory for AUTHOR bootstrap; registered-manifest runs reuse the
  normal `my ai --setup --no-session` launch path for JOIN onboarding.

### Changed

- The project is now **My AI**: the CLI is `my`, installed from
  `fluxinc/my-cli` (https://my-cli.com), with umbrella state under `.my-cli/`
  and `MYCLI_*` environment variables. Release archives are
  `my-cli_<version>_<os>_<arch>.tar.gz`.

## 0.28.0 - 2026-06-15

### Added

- Windows release builds: archives now include `windows/amd64` and
  `windows/arm64`, so `my update` can fetch a Windows package.

### Fixed

- `my update` on Windows: extract the `my.exe` binary from the release archive,
  and replace the running executable with a rename-aside (Windows locks a running
  binary, so the old one is moved to `<path>.old` and cleaned up on a later
  update).

## 0.27.0 - 2026-06-14

### Added

- Launch-scoped skill composition: `my ai` composes the organization skills for
  a launch and materializes them as disposable derived state in the launch
  root's `.agents/skills/` (copied, with `.my-cli-managed.json` markers and per-slug
  ownership), instead of installing organization skills into harness user config
  directories. Non-My AI entries are never clobbered.
- `my ai --skills all|none|<id,...>` and `my ai --profile <id>` skill
  selectors, plus a manifest `profiles` list of named loadouts. `--skills` and
  `--profile` are mutually exclusive; unsatisfiable skill requirements fail with
  a precise closure error.
- Harness launch-root skill capability model: Codex and Antigravity read
  `.agents/skills` directly; Claude Code gets a generated `.claude/skills`
  mirror.
- Antigravity (`agy`) harness support.

### Changed

- `my setup`, `my sync`, `my manifests sync`, and `my doctor --fix` no longer
  install organization skills into harness user config directories for
  launch-root-capable harnesses; those skills are now launch-scoped. `my doctor
  --fix` removes leftover user-global organization skills. The bundled `my`
  self-skill stays on the global ensure path during migration.
- OpenCode keeps organization skills user-global (it has no launch-root skill
  discovery); `my ai --skills/--profile` is rejected for OpenCode.

### Removed

- Removed Gemini harness support entirely. Antigravity (`agy`) is the
  replacement; `my ai gemini` and Gemini skill management are no longer
  supported.

## 0.26.0 - 2026-06-14

### Added

- `my onboard`: a human walkthrough that explains the model, handles the
  no-manifest case with registration guidance, offers interactive setup on
  first run, and records umbrella-local tour completion.
- `my setup --interactive`: explicit prompt mode for manifest and role
  selection, including role clearing via `none`.

### Removed

- Removed the deprecated `my onboard` -> `my setup` alias. `onboard` is now
  a real tour command; `setup` remains the deterministic machine configurator.

## 0.25.0 - 2026-06-14

### Added

- `my compile --role <id> [--manifest NAME] [--home DIR]`: emits a
  deterministic manifest-to-Clawdapus launch projection JSON artifact without
  launching containers, resolving credentials, or fetching service descriptors.

### Removed

- Removed the deprecated `my launch` dispatch alias. Use `my ai` for local
  harness startup and `my compile` for the contained-runner projection.

## 0.24.0 - 2026-06-13

### Added

- Data bindings may carry domain-notes `guidance` fragments. Those fragments
  render into generated `AGENTS.md` under a labeled, source-attributed
  `## Domain Notes: <data type>` section, kept separate from the organization
  contract and the `my contract` verbs.

## 0.23.0 - 2026-06-13

### Added

- Manifest `data_bindings`: map stable operational data nouns (`customers`,
  `meetings`, `support`, `fleet`) to declared `mount:<id>` or `service:<id>`
  surfaces. Mount-backed bindings narrow existing local record commands;
  service-backed bindings are validated but deferred until service-domain
  invocation ships.

### Changed

- Roles are documented and reported as local loadouts/selections rather than
  authority grants.

### Removed

- Removed the vestigial service `grant` field from the manifest schema and
  `my services` output. Existing manifest JSON that still contains `grant`
  remains load-tolerant because unknown fields are ignored.

## 0.22.0 - 2026-06-13

### Changed

- `my customers list` now reads mounted `customers/*.md` customer identity
  records instead of manifest `catalog/customers.json`, so customer data lives
  in the workspace data plane and follows the backing Git/API permissions.
- `my init` scaffolds a workspace `customers/` directory instead of an empty
  manifest customer catalog.

### Removed

- Removed `my admin customers add|edit` and manifest customer catalog
  validation/loading. Customers are operational records, not manifest admin
  data.

## 0.21.0 - 2026-06-12

### Added

- `my contract list`: inspect the organization contract rules in force, with
  manifest name and the 1-based index used for removal.
- `my admin contract add "RULE" --manifest-dir DIR` and
  `my admin contract remove <index|"RULE"> --manifest-dir DIR`: edit the
  manifest contract through the standard admin review-commit-push flow, with
  duplicate, empty, and multiline rules rejected.
- Documentation: new guide pages for Guidance and Contract, Records
  (meetings/support/fleet), Work Sessions, Services and Roles, and
  Sync/Doctor/Updates; the site sidebar now covers the full command surface.

## 0.20.0 - 2026-06-12

### Added

- Manifest `contract`: a list of short, binding organization rules rendered
  as an `## Organization Contract` section in generated `AGENTS.md`, between
  the public baseline and manifest guidance fragments. Validation rejects
  empty, multiline, and duplicate rules; existing guidance drift detection and
  derived reconcile cover contract changes automatically.
- Added a built-in Fleet Work Contract to generated guidance and the bundled
  `my` self-skill: agents should start substantive fleet work with
  `my fleet get`, continue a related support record or create a new dated one
  with `my support add`, carry fleet identifiers with repeated
  `--identifier`, and publish through `my sync`.
- `my fleet get` human output now ends with a support-record next step,
  including an `my support add` command seeded with the fleet id, known
  identifiers, and customer when available. JSON output is unchanged.

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
  `workspace:` and `tool:` forms; `my skills show` surfaces declared
  requirements.
- `my services list|get` and `my roles list|get` inspection commands with
  `--json`.
- `my setup --role <id>` persists the selected role in `.my-cli/state.json`,
  appends role-specific guidance fragments to generated `AGENTS.md`, and
  makes the selected role available for role-scoped service materialization.
- `my setup` (and the derived reconcile in `my sync`/`my manifests sync`)
  now materializes an umbrella-root `.mcp.json` from MCP services with local
  connection data (inline `connection` or a checked-in descriptor), scoped to
  the selected role. Values pass through as `${VAR}` placeholders — never
  resolved secrets, never network fetches. A hand-written `.mcp.json` is
  never overwritten without `--force`.
- `my doctor` gained a `service` section: it reports MCP services without
  local connection data (URL-only `describe_ref`), missing checked-in
  descriptors, unset environment variables referenced by `auth_ref` or
  connection placeholders, and `op://` references without the op CLI on
  PATH.

## 0.17.0 - 2026-06-11

### Fixed

- Recording commands (`my meetings add`, `my support add`, and
  `my fleet add`) now detect when the current directory is inside an active
  work session and write records to that session's mount worktree instead of
  silently writing to the base umbrella checkout.

### Changed

- `my ai` now launches from the base umbrella by default. Work sessions are
  explicit with `my ai --new-session` or `my ai --session <id>`; when run
  from inside an active session, plain `my ai <harness>` continues launching
  from that current session. `--no-session` remains available to ignore a
  current session for base inspection/admin work.

## 0.16.0 - 2026-06-11

### Fixed

- `my ai` no longer creates an orphan work session when the requested
  harness binary is missing from PATH: the binary is resolved before a new
  default session is created, and the error says no session was created.
  Resume (`--session`) and base (`--no-session`) launches still print the
  exact fallback command. `my ai --print` keeps creating a session, as
  documented.

### Added

- `my doctor` now reports work sessions: each active session shows its
  live state (`ok` when clean, `warning` with dirty/unlanded counts and the
  matching `my work finish` command, `error` when a worktree is missing or
  git inspection fails), and finished/discarded registry records roll up
  into a single archived count. The JSON report gains a `sessions` section.
- Added `my work list` as an alias for `my work status`. Human work-session
  output now includes session-specific follow-up commands: `work start`
  prints `my work finish <id> ...`, and `work finish` prints a `next` line
  for the natural follow-up (`my sync`, `my sync --print`, or
  `my work status`).

## 0.15.0 - 2026-06-11

### Changed

- Split catalog products from repository checkouts: products in
  `catalog/products.json` are now pure business entities (no `git_url`) that
  may link implementing repos via `repos: ["<repo-id>"]`, and the
  organization's repositories live in a new `catalog/repos.json` inventory.
  New `my repos list|add|remove` verbs manage clones under `repos/<id>`
  (`add` is idempotent: an existing clone of the same remote is adopted;
  conflicting paths hold with remediation). `my root`/`my ai` take `--repo`
  (`--product` now errors with the migration hint); `my mounts add
  product:<id>` is removed; the sync scope is `repos` (the `products`
  spelling is gone); sync/doctor report repo checkouts with the `repo` role.
  Records keep `--product` as a business reference. Umbrella state migrates
  automatically (`selected_products` to `selected_repos`, `product:` mount
  entries to `repo:`); clones under `repos/<id>` are untouched. Manifest
  mount kind `repo` is removed — repos.json is the single declaration path.
  A product entry still carrying `git_url` fails validation with the
  migration message, so migrate private manifests before installing this
  release.
- The bundled `my` self-skill and the docs site now document work sessions
  fully: the Session concept in the model, the `my work
  start/status/resume/finish` verbs in the skill's operational surface and
  task list, and quickstart/launch guidance reflecting that `my ai` starts
  in a fresh session by default.

## 0.14.0 - 2026-06-11

### Added

- Added `my work start [--slug]` and `my work status [--all]`: isolated work
  sessions as visible `work/<id>/` directories with a git worktree per synced
  content mount on a fresh `my/work/<id>` branch, session-local `scratch/`,
  a `SESSION.md` summary, and a first-class session registry under
  `.my-cli/sessions/`. Repo-kind mounts and selected products are not included
  in session worktrees yet.
- Added `my work finish [session-id] --land|--publish|--discard`. Landing
  commits intentional dirty session content, rejects unadopted `??` files and
  non-content changes, merges clean session branches into the base checkout,
  removes session worktrees/branches, and records the session outcome.
- `my sync` (and the targeted sync inside `my work finish --publish`) now
  reads the session registry and holds outbound publish of a content mount
  while any active session on it has dirty files or unlanded commits, naming
  the session id, path, and the `my work finish` remediation. Inbound
  fast-forward pulls are unaffected.
- `my ai` now launches from a fresh work session by default, supports
  `--session <id>` for explicit resume, and requires `--no-session` for base
  umbrella or product checkout launches. Added `my work resume [session-id]`
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

- Added `my record adopt <path>` to mark an existing file under a declared
  content mount as intentional publish content using Git intent-to-add.

### Fixed

- Recording commands (`my meetings/support/fleet add`) now work in a
  freshly initialized, unpublished organization: `local-only` mounts count
  as usable content roots instead of being skipped.

### Changed

- `my meetings add`, `my support add`, and `my fleet add` now mark created
  records with Git intent-to-add so `my sync` can distinguish My-created
  records from stray untracked drafts.
- `my sync` now holds plain untracked (`??`) files under content paths and
  names the `my record adopt <path>` remediation instead of auto-committing
  arbitrary new files.

## 0.13.0 - 2026-06-10

### Added

- Added `my publish`: one idempotent command to take a local organization
  online. It creates private remotes for the content and manifest
  repositories (or adopts existing origins and pushes, verifying GitHub
  remotes are private), rewrites local mount URLs to the published remotes in
  a commit scoped to `manifest.json`, updates the registry, and prints the
  teammate join command. `--print` shows the plan without changing anything.
- Checkouts without an `origin` remote now report `local-only` (pointing at
  `my publish`) across `my manifests sync`, `my mounts sync`, and
  `my sync`, instead of failing.
- `my sync` refuses to publish a manifest whose mounts still reference local
  paths, and `my doctor` names each local-path mount with the `my publish`
  remediation, so a machine-local URL can never leak to teammates.

### Changed

- The manifest is now a control plane separate from workspace content:
  `my init` creates two local repositories — a private manifest repo
  (manifest, catalog, skills, agent guidance) at the registry path, and a
  content repo at `<umbrella>/workspace` with the handbook directories.
  The workspace never contains `manifest.json`, and hosting permissions can
  restrict manifest pushes to admins while the whole organization pushes
  content. Published repos default to `<org>-manifest` and `<org>-workspace`.
  `my init --path` now selects the content repo location.
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

- Added `my init <org-id>` to create a small local manifest/handbook repo,
  commit it, register it, sync the manifest cache, and print the next setup,
  launch, and optional GitHub publish commands.
- Mount `git_url: "."` now resolves to the Git URL or local path used to
  register the manifest, so self-hosted handbook mounts survive publishing.

### Fixed

- Reworked the quickstart and manifest docs so the first-run path no longer
  points at a dead example manifest URL.

## 0.11.0 - 2026-06-10

### Changed

- `my doctor` now reads as a repair dry run: every finding that
  `my doctor --fix` can repair is marked `would fast-forward`,
  `would reconcile derived guidance and skills`, or
  `would reinstall the my self-skill` (also `would_fix` in `--json`), with a
  closing `fixable` count pointing at `my doctor --fix`. Findings doctor
  cannot repair keep their explanatory remediation text.

## 0.10.0 - 2026-06-10

### Changed

- Product repositories now clone under `repos/<id>` instead of `products/<id>`.
  `my setup` migrates an existing `products/` directory automatically, and
  legacy `products/<id>` checkouts keep resolving until migrated. The sync
  scope accepts `repos` (the `products` spelling still works).
- Startup commands (`my root`, `my ai`, `my setup`) now print a stderr
  `notice` line for checkouts the auto-refresh cannot converge — dirty, ahead,
  behind, or diverged — naming the repository and the command that reconciles
  it. Stdout is unchanged, so `cd "$(my root)"` remains safe.

### Fixed

- `my manifests sync` now reconciles generated guidance and manifest skills
  after pulling or cloning a changed manifest for an existing matching umbrella;
  pass `--no-derived` for a cache-only refresh.
- `my ai` now ensures the bundled `my` self-skill is installed before it
  execs a filesystem harness, and manifest skill sync/purge no longer removes
  that self-skill.

## 0.9.0 - 2026-06-10

### Added

- Added `my support list/search/get/add` for anonymized support records under
  `support/`, with qmd-first search, a built-in markdown search fallback, and
  linkable frontmatter attribution: an optional canonical customer ID, a
  repeatable `--identifier` list for device, order, or asset identifiers, and
  org member fields (`claimed_by`, `observed_by`, and the human sign-off
  `approved_by`).
- Added `support` as a manifest mount kind; handbook mounts without explicit
  `include_paths` now treat `support/` as approved content for sync publishing.
- Added `my fleet list/search/get/add/set` and the `fleet` mount kind: a
  registry of deployed instances with one record per stable id under
  `fleet/<id>.md`, updated in place. `my fleet get` resolves any entry in a
  record's `identifiers` list and reports related support records;
  `my fleet set` updates scalar frontmatter while preserving everything else
  and suggests an `my sync --message` command for the transition; and
  `my support add` warns when an `--identifier` is unknown to the registry.
- Extracted the shared `internal/record` engine behind meetings, support, and
  fleet records (frontmatter parsing now ignores inline YAML comments in
  unquoted values).

### Fixed

- `my doctor` now reports an absent or stale `my` self-skill on present
  harnesses instead of claiming no skill drift, and `my doctor --fix`
  reinstalls it (#13).

## 0.8.0 - 2026-06-09

### Changed

- Renamed the CLI from `flux` to `my` and the project to My AI
  (`github.com/fluxinc/my-cli`); commands read as possessive English.
- `flux launch` is now `my ai`; `flux onboard` is now `my setup`
  (deprecated aliases warn on stderr).
- Pluralized noun command groups (`manifests`, `mounts`, `workspaces`);
  `catalog list products` is now `products list`.
- Built-in sync backend renamed from `flux` to `builtin`.
- `.flux/` is now `.my-cli/`, `~/.local/share/flux` is now `~/.local/share/my-cli`,
  `~/.config/flux/manifests.json` is now `~/.config/my-cli/manifests.json`, and
  `FLUX_*` environment variables are now `MYCLI_*`.
- Release archives are `my-cli_<version>_<os>_<arch>.tar.gz` with the `my`
  binary inside; the bundled self-skill is `my` (id `my:self`).
- `my doctor` detects legacy Flux state and prints migration remediation.

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
