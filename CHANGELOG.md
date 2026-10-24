# Changelog

## Unreleased

### Added

- Opt-in `my admin contract review` evaluates the complete contract and verified
  committed policy text with an explicit model. Deterministic format, duplicate
  and size lint precedes bounded, tool-free Claude review; missing sources,
  unavailable runners and partial output are reported distinctly.
- Contract authoring can attach advisory findings to the exact proposed PR
  commit with `--review` or manifest `contract_review` configuration. Findings
  never rewrite, block, approve or merge changes. `--review=false` overrides
  manifest opt-in. The new manifest field has a v0.50.0 compatibility floor.
- Saved review evidence binds the contract head/blob, policy digest set, prompt
  version, harness and model. Local `review --check` and doctor notes detect
  stale or incomplete evidence, including policy-only changes, without model
  calls. An optional manifest-repository CI example is included. Use a
  versioned model identifier: `--check` cannot see an alias retargeted
  upstream. Policy text that is not valid UTF-8 makes the review incomplete
  before any model call.

## 0.49.0 - 2026-10-24

### Added

- Per-launch organization contract delivery state, separate from installed
  guidance. Each harness keeps its own emitted snapshot and native conversation
  binding, including resume/fork handling, explicit retractions, final-rule
  removal, and full snapshots after compaction. Receipts record completed output
  into a hook path, never model acceptance or compliance.
- `my contract pending` reads pending context locally without recording receipt;
  native `--hook claude|codex` adapters record emission only after complete output.
  Hooks never fetch. Context above 64 KiB is reported incomplete without advancing
  the cursor. Unsupported harnesses receive a stderr fallback from `my` commands.
- Launch-scoped Claude Code (2.1.310+) and Codex (0.163.0+) adapters keep
  existing settings and hooks. Their command definitions are stable across
  launches, and they never bypass hook trust. Codex uses an unlimited hook
  context setting, bounded by the CLI's own 64 KiB output cap. Native Windows
  remains unsupported. Claude Code was verified live for startup, prompt,
  mid-run tool, resume, compaction and fork delivery. Codex was verified live
  for the delivery mechanics and for skipping an untrusted hook. Approving the
  hook once in Codex's `/hooks` persists across launches; this was verified from
  the Codex 0.163.0 source, not from a live approval.
- Resuming or forking a conversation by ID starts from what that conversation
  was last given, so forks list the rules retracted since then. Every update
  states that it is the complete contract. Closed launch records are pruned 30
  days after their last hook. A pruned or reopened record gets a full snapshot
  on its next hook.
- `my contract register --manifest NAME --harness NAME --root DIR` is the explicit
  runtime registration seam for contained runners. Compilation stays deterministic
  and does not register a launch or claim live delivery. State upgrades to schema 2
  when instances are added, preventing older clients from discarding receipts.

## 0.48.0 - 2026-10-24

### Added

- Sync failures with `--json` retain one complete report on stdout (including
  contract tracking errors), with a nonzero exit status. They no longer append
  a second JSON error document after the report.
- Contract revision tracking and per-root installation state. Sync shows a
  line diff on stderr and `contract_change` in JSON when the active contract
  changes. `--no-derived` preserves pending work; later syncs retry even
  without a new pull. Umbrellas and active sessions reconcile independently.
- `my contract status [--json]` reports active and installed revisions, local
  unpublished changes, pending roots, and the last successful remote check.
  `my contract diff [--from REV] [--to REV] [--json]` compares local revisions.
  Both commands are local-only and do not fetch or update state.
- Revision state is recorded for inline and file-backed contracts, including
  final-rule retractions and rollbacks. Never-configured manifests create no
  contract state. Rewriting guidance does not prove a running agent read it;
  live delivery remains unsupported until the separate adapter release.
- Each root records a hash of the AGENTS.md it installed. A later sync, or
  `my contract status`, treats an edited or missing file as pending and
  rewrites it, even with no new pull.

## 0.47.0 - 2026-10-24

### Added

- File-backed organization contract. A manifest can set
  `"contract_file": "guidance/contract.md"` instead of the inline `contract`
  list (one or the other, never both). The file is a Markdown list: one
  single-line `- rule` per entry, optional unique `## ` groups that render as
  `### ` headings under `## Organization Contract`, and one optional `# `
  title that is not rendered. Anything else, plus duplicates, invalid UTF-8,
  control characters, paths outside `guidance/` and symlinks, is a validation
  error. Validation warns above 150 rules or 16 KiB. The feature floor is
  v0.47.0, so authoring stamps `min_my_version` and v0.46.x asks for
  `my update`.
- `my contract show [--json]` prints the contract as written, with its Git
  blob id. `my contract list --json` adds each rule's heading.
- `my admin contract replace <index|"OLD"> "NEW"` rewrites one rule as a
  single edit, and `my admin contract add --under HEADING` adds to a group,
  creating the heading if needed. Edits to a contract file change only the
  lines they touch.
- `my admin contract migrate` moves an inline contract into
  `guidance/contract.md`. It refuses if the file exists. It patches only
  `contract`, `contract_file` and `min_my_version`, keeping unknown keys and
  key order and never lowering a higher floor, and the generated `AGENTS.md`
  is byte-identical. It reminds the admin that releases before v0.46.0 ignore
  the new keys.
- Contract edits guard their base. Registered-manifest PRs re-fetch just
  before pushing and refuse (`base_moved`) if the upstream moved. Local
  `--manifest-dir` edits refuse if the files changed while the edit was
  prepared. `--expect-blob SHA` refuses an edit unless the contract file is
  still at the blob you read.
- `my compile` carries a contract file's rules with its `path`, `sections` and
  `revision` (manifest commit and blob id). An emptied file still projects its
  block.

## 0.46.0 - 2026-10-21

### Added

- Manifest `min_my_version` (a plain `X.Y.Z` release): the oldest `my` that
  reads the manifest. It is checked before every other validation rule, so an
  older `my` reports only `requires my vX or later (running vY); run my update`
  instead of rejecting newer features as invalid. Releases before this one
  ignore the field. Unstamped source builds warn and skip the check.
- `my admin min-version --manifest-dir DIR [--set X.Y.Z]` declares it: by
  default it raises the value to the newest feature the manifest uses (now
  mount mode `role`, v0.45.0) and never lowers a higher one; `--set` refuses a
  version below that floor. It patches only that key, keeping fields this `my`
  does not know. Validation warns when a manifest uses a gated feature without
  declaring a covering `min_my_version`, and every `my admin` manifest write
  raises it automatically.
- `my ai`, `my root`, and `my setup` add the cached update notice to an
  invalid-manifest failure ("a newer my (vX) is available and may support this
  manifest: run `my update`"), honoring `--no-update-check` and
  `MYCLI_NO_UPDATE_CHECK`.

## 0.45.0 - 2026-10-21

### Added

- Manifest mount mode `role`: a role-only mount that must be selected by at
  least one role. Unscoped setup, mount sync and listing, `my ai` launch scope,
  and `my compile` exclude it; a selecting role clones it during `my setup`
  (clone-if-accessible) and projects it with mode `role`. `required`, `default`
  and `optional` behave as before.
- `my admin manifest-mounts add|remove` declares or removes manifest mounts in
  a maintainer checkout, re-validating before writing. `--role ID` adds the
  mount to existing roles; removal refuses while a role still selects it
  unless `--prune-roles` removes the mount and its role references in one
  validated write.

## 0.44.0 - 2026-10-20

### Added

- Personal service access with `my services auth set/status/unset`: references
  stay local, keys are verified before saving, and shared credentials remain
  unchanged. Onboarding follows each service's `auth_setup_url`; doctor checks
  personal references and warns before expiry.
- `my fleet comment` posts service conversation comments with an explicit
  retry key and revision checks, including service-defined mention delivery.

### Tests

- Disable background Git maintenance in recovery fixtures so their byte-for-byte
  source checks cannot race setup commits. Runtime recovery behavior is unchanged.

## 0.43.0 - 2026-10-16

### Added

- `my support link <id> --record <stable-id>` notes a published support record
  on service-backed fleet records, named by stable id. It writes nothing unless
  the record's current text is on its workspace's default branch (freshly
  fetched from the manifest's git URL), sends only the record's id,
  and is safe to rerun: each link carries a deterministic idempotency key.
- Author data bindings with validated `my admin bindings set/remove`, including service/mount surfaces, guidance and field aliases. HTTP service authoring now accepts an API connection URL.
- `my fleet index` and semantic `my fleet search` for service-backed fleet:
  a private per-user qmd index of the service's records, isolated from your
  own qmd setup. It follows the service's change feed, and every match is
  re-read from the service and shown with current values only. When the
  index is behind, missing or qmd is unavailable, search says so and shows
  the service's results only. `--no-semantic` skips it.
- `my fleet set --confirm <id>` (repeated, one exact id each) applies a shared change (one order number
  on several records) after the service listed exactly which records it
  covers. Without it, the refusal prints the ids and the shell-quoted command to run.
- Serve fleet records from a service: `data_bindings` may name several
  `surfaces` (one service plus mounts) and `field_aliases`. An `http` service
  takes its API base from `connection.url` (https, or http on loopback only; no credentials, query or fragment).
  `my fleet get/list/search/set` read and write it live through
  `records-http/v1`. Writes save all fields or none. Workflow moves go through
  the new `my fleet transition`. Markdown records with `managed_by` become
  pointers. A service that cannot answer fails the command. Credentials from
  `env://` or `op://` (or `none`) never leave the service's origin or reach output.

### Fixed

- `my fleet list/search --where` now matches service-backed records on the
  registry fields they carry; it used to drop every service record.
- `my fleet index --json` uses snake_case keys like every other command.

## 0.42.0 - 2026-09-12

### Fixed

- Start every agent onboarding branch before Git/GitHub authentication, setup,
  or organization policy gates, using a neutral temporary workspace. Normal
  `my ai` launches retain their access and policy checks.
- Preserve actual terminal input through piped installation, avoiding the
  macOS Codex event-reader crash caused by reopening `/dev/tty`. Failed harness
  launches now report their exit status and explicit retry/alternate/manual
  options without silently switching away from the assistant.
- Let the onboarding assistant execute routine local setup while explaining,
  retaining human authentication, privilege, policy, and publication boundaries.

### Tests

- Add native terminal/input tests to macOS and Linux CI and offline clean-system
  cases with both Git and GitHub CLI absent at assistant entry.

## 0.41.1 - 2026-08-27

### Fixed

- Built-in `my sync` Git fetch, pull, and push operations now use the same
  invocation-local `gh auth git-credential` helper as manifest and setup
  clones, with terminal prompts disabled. Private GitHub HTTPS mounts no longer
  fall through to the obsolete username/password prompt after setup, and My AI
  still does not rewrite global Git configuration.

## 0.41.0 - 2026-08-25

### Added

- A `ci` GitHub Actions workflow runs `gofmt`, `go vet`, `go build`, and
  `go test ./...` on every push to `master` and every pull request (#17).
- `my customers list --identity --json` emits a deterministic portable
  customer identity projection with source revision/freshness metadata while
  excluding names, partner links, note bodies, and local paths. Dirty, stale,
  or unavailable sources fail with distinct error codes (#36).

### Changed

- Manifest-derived read verbs (`my contract list`, `my roles list|get`,
  `my services list|get`, `my tools list|info`, `my policy list|show|status`)
  now share the same TTL-bounded
  auto-refresh as launches, so a merged manifest change is no longer served
  silently from a stale cache. Existing JSON result shapes are unchanged (#33).
- A Gnit control-mode umbrella root with no origin remote (and no roster
  remote) is treated as a deliberately local root: `my doctor` reports it as
  `info` instead of a permanent warning, and `my sync --push` publishes member
  content mounts through guarded built-in publication instead of holding them.
  A roster that declares a root remote the checkout lacks still holds, and
  `--backend gnit` still fails closed (#34).
- Session docs and `my session resume` usage now state that a work session is
  a persistent workspace, not a harness chat, and that resuming it starts a
  fresh conversation (#24).
- Session publish and land guards now compare exact changed paths. Unrelated
  base and session records no longer block one another; overlapping work and
  any path-inspection failure remain held. Base intent-to-add entries (which
  `git merge` refuses even when unrelated) are named before the merge is
  attempted, with the exact `git add`/`git reset` remediation (#30).

## 0.40.1 - 2026-08-25

### Fixed

- The retained onboarding matrix summary now records the actual UTC run date,
  and the release plan correctly reports all 20 arm64/amd64 scenario runs.

## 0.40.0 - 2026-08-25

### Added

- `my admin repos add` authors validated `catalog/repos.json` declarations
  through the same dirty-checkout and governed-admin gates as other manifest
  control-plane commands.
- A stable `https://my-cli.com/install.sh` entrypoint can register an existing
  organization manifest and start guided onboarding in the same one-line
  install. Fresh interactive installs hand off to an already-installed agent;
  `--no-onboarding` keeps automation noninteractive. The installer also prints a
  prerequisite checklist (git, `gh` login, an installed harness) with the
  install command for the platform, and supports fish and macOS bash profiles.
- `my manifests add --no-replace` refuses to re-point a registered name at a
  different URL; the installer uses it so a second team handout cannot silently
  replace an organization.
- `my doctor` reports a `prereq` section: `git`, `gh` (installed and logged
  in), the `my` binary directory on PATH, and installed harness CLIs, each with
  remediation.
- `my setup` and `my onboarding --no-agent` clone a registered-but-unsynced
  manifest on first use instead of failing with "run my manifests sync".
- `my onboarding --complete` and `--status` provide an explicit local
  completion seam so re-running the one-line installer resumes interrupted
  onboarding and leaves completed installations quiet. Completion refuses a
  missing required tool; optional tools do not block it.
- `my tools list` and `my tools info` report each declared executable as
  `present` (with its resolved path) or `missing`, alongside required/optional
  mode. `my doctor` includes the mode in tool details.
- A reproducible Ubuntu container matrix exercises agent-backed AUTHOR and JOIN
  onboarding on arm64 and amd64, preserving JSONL transcripts.

### Changed

- The release installer persists `~/.local/bin` in the operator's shell profile
  and no longer leaves a fresh install dependent on a temporary PATH export.
- Agent onboarding treats a registered-but-unsynced manifest as a resumable
  JOIN bootstrap, walking GitHub authentication, manifest sync, setup, and
  required organization tools instead of failing before the agent launches.
- Installer manifest registration fails closed when the same name already
  points at a different URL. Invalid existing checkouts launch a JOIN_REPAIR
  agent handoff with the exact load error instead of failing before help starts.

### Fixed

- Authenticated GitHub HTTPS manifest and mount operations now supply
  `gh auth git-credential` to Git per invocation, preventing obsolete password
  prompts without modifying global Git configuration. Git runs with
  `GIT_TERMINAL_PROMPT=0` so agents and installers never hang on a prompt, and
  authentication failures carry remediation (`gh auth login` or the SSH URL).
  A missing or logged-out `gh` is reported as `gh_missing` /
  `authentication_failed` with the install/login command instead of a raw
  `exec` error.
- Quickstart no longer mixes `#` comment lines into paste blocks and documents
  that `gh` is required for github.com-hosted manifests and mounts.

## 0.39.0 - 2026-08-14

### Added

- `my session leftovers` inventories registered and raw Git worktrees across
  exact umbrella content mounts and cloned catalog repositories; `my doctor`
  reports the same findings without mutating Git state. Sync emits a
  non-blocking informational row when a content-mount leftover has commits not
  present in its base checkout.
- `my session close-worktree <path>` safely removes one clean named-branch
  leftover after confirmation while preserving its branch. It refuses base,
  active-session, locked, missing, detached, dirty, and untracked worktrees and
  never force-removes or prunes.
- First-run documentation now covers prerequisites, creating and publishing a
  new manifest, joining an existing manifest, setup, operational checks, and a
  Windows WSL installation path that keeps Linux Git and the umbrella together.

### Changed

- Generated guidance and the bundled self-skill direct agents to prefer My AI
  sessions and inspect leftover worktrees before stopping work.

## 0.38.0 - 2026-08-13

### Added

- First-class, separate Grok and Cursor CLI harness support across `my ai`,
  onboarding, session joins/resumes, self-skill management, launch-scoped
  organization skills, detection, help, and documentation. Cursor execution
  prefers the unambiguous `cursor-agent` compatibility command and validates
  the current official `agent` fallback so Grok's same-named executable is
  never launched by mistake.
- `my admin skills add --require TYPE:ID` records `workspace:`, `tool:`, or
  `service:` dependencies on import.

## 0.37.0 - 2026-07-31

### Added

- Policy-at-invocation for governed organizations: generated guidance now
  carries a compact, role-scoped `## Organization Policies` consultation index
  with policy summaries, topics, and digest-verifying `my policy show` actions.
  Real AI launches prove every applicable required or optional committed policy
  blob locally before emitting launch context or starting a harness.
- `my compile` projects applicable policies as digest-bound `policies[]`
  references and fails when a policy's mount is outside the selected role.
  Non-governed projections and guidance remain unchanged.
- Policy declarations accept optional one-line `summary` and repeatable `topics`
  metadata, including `my admin policy add --summary/--topic` authoring.
- An approachable governance walkthrough documents the employee `my ai`
  experience, agent consultation contract, admin authoring, durable acceptance
  ledger, and the beta versus experimental boundary.

### Changed

- Bare `my meetings` and `my support` now list records, including list filters,
  instead of requiring employees or agents to spell out the default action.
- Active session guidance is regenerated from current organization policy when
  a session starts, joins a harness, or resumes; harness paths retain governed
  freshness and digest proof immediately before execution.
- Organization skills keep mutable runtime state under
  `~/.local/state/my-cli/skills/<manifest>/<install-slug>/`. Doctor warns about
  unexpected skill-source files, and publish/sync holds unadopted files under
  every declared skill source to prevent credentials or caches entering the
  manifest.

## 0.36.0 - 2026-07-21

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

### Added

- An opt-in beta governed-organization foundation: manifest governance
  declarations, GitHub-backed authorization, digest-bound policy acceptance and
  launch gates, PR-only governed publication, trusted-base CI validation, and
  live enforcement audits. Governance completion, record-domain/outbox work,
  and organization dogfood remain on their active plan and are not claimed
  complete by this release.
- Experimental access monitoring, recovery capsules, and quarantine machinery
  behind explicit per-machine activation. Policy and publication flows never
  activate it; it remains not recommended for real umbrellas until its separate
  disposable-private-repository revocation drill passes.

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
  directories. Non-My entries are never clobbered.
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
  (`github.com/fluxinc/my-cli`). Commands now read as possessive English:
  `my meetings list`, `my customers list`, `my sync`.
- Renamed `flux launch` to `my ai` and `flux onboard` to `my setup`;
  `my launch` and `my onboard` remain as deprecated aliases that warn on
  stderr. The `--onboard` flag on `my ai` is now `--setup`.
- Pluralized noun command groups: `my manifests`, `my mounts`,
  `my workspaces`; `flux catalog list products` is now `my products list`.
- Renamed the built-in sync backend from `flux` to `builtin`
  (`my sync --backend auto|gnit|builtin`).
- Renamed the umbrella marker directory from `.flux/` to `.my-cli/`, the data
  home from `~/.local/share/flux` to `~/.local/share/my-cli`, and environment
  variables from `FLUX_*` to `MYCLI_*`.
- Release archives are now `my-cli_<version>_<os>_<arch>.tar.gz` containing
  the `my` binary; `install.sh` installs `my` from `fluxinc/my-cli`.
- The bundled self-skill is now `my` (id `my:self`).
- `my doctor` reports legacy Flux state — `.flux/` directories,
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
