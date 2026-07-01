# my-cli footgun audit from private operator sessions

Status: **active (implementation ready; operator decision pending)**. Date: 2026-06-30.

Two agents adversarially reviewed a private operator transcript corpus covering
Claude Code and Codex work inside My AI-managed umbrellas. The goal was to find
recurring footguns, repeated learning, needless difficulty, and architectural
problems that should be resolved in the public `my` CLI.

## Method

- Evidence source: private operator transcripts and live My AI-managed workspace
  state.
- Verification: candidate findings were cross-checked against transcripts,
  current source, live CLI behavior where safe, and relevant git history.
- Baseline: installed `my` = v0.34.0. Items listed as already fixed were
  confirmed in current source; items listed as open were not found to have a
  complete fix.

Adversarial review materially changed the result: several plausible findings
were downgraded to already-fixed or rejected, and one initially proposed fix was
rejected as unsound because it would have routed control-plane publishing
through the content auto-publish path.

Current implementation status: the working tree includes the fixes listed
below. The core patch received adversarial Claude/Codex review; a final
Codex-only cleanup suppresses self-referential `my doctor` detail noise. The
change is awaiting the operator decision to commit/push and optionally file
follow-up issues.

## Confirmed-open findings

### Tier 1 - compact self-heal fixes

1. **Manifest/catalog repo needed an explicit publish path for dirty catalog files.**
   Catalog and admin commands can leave the manifest checkout dirty, while
   `my sync --push` holds manifest entries because auto-publish applies only to
   content mounts. Direct publish also treated catalog changes as outside
   declared content paths. The durable fix must be deliberate control-plane
   publishing, not a content auto-publish shortcut.

   **Fix implemented in this change:** `my publish --manifest NAME` now acts as the
   human-facing path for reviewed manifest control-plane edits, committing and
   pushing dirty files under `manifest.json`, `catalog/`, `skills/`,
   `guidance/`, and `agent-guidance/`. The equivalent low-level sync form is
   `my sync --publish direct --scope manifest`. `my sync --push --scope
   manifest` still holds because auto-publish remains content-only, and dirty
   files outside those control-plane paths still hold. `my publish --print`
   also reports the planned manifest rewrite and commit when a local mount
   already has a known publish remote, so the dry run does not hide the
   reviewed control-plane step. Rename and delete staging is scoped through
   those same control-plane roots, so a rename inside an allowed root publishes
   atomically, while a rename from outside the allowlist still holds.

2. **#28 - sync/finish deadlock on a dirty base checkout.**
   `my sync --push` held outbound publish because an active session had pending
   work and pointed at `my session finish --land`; `finish --land` then refused
   because the base checkout was dirty. The sync inspection already knew the
   base dirty files, but the session-hold message did not sequence that cleanup.

   **Fix implemented in this change:** when the base checkout is dirty, the hold message
   now names the base files and tells the operator to resolve them before running
   `my session finish --land` or `--publish`. Remaining follow-up: consider an
   explicit adopt/stash flow for eligible dirty base files, and per-file rather
   than whole-mount staging so unrelated work is not swept into a publish.
   Effort: follow-up M.

3. **Finished or migrated sessions can leave stale local guidance.**
   A finished session directory can remain visible even after its mount
   worktrees are removed. If the directory still contains old generated
   guidance, an agent launched from there gets a "no active session matched"
   error while reading stale instructions.

   **Fix implemented in this change:** session-root resolution now detects known inactive
   sessions and unregistered session directories, naming the session id and the
   concrete `cd <root>`, `my session status --all`, or `my doctor` next step.
   Finished sessions also replace session-local `AGENTS.md`/`CLAUDE.md` with a
   small finished-session stub that points back to the umbrella root, and
   successful publish updates the stub outcome from `landed` to `published`.

4. **Content in undeclared paths never publishes, and one path can block a whole mount.**
   Content commands can write records into a path that is not part of a mount's
   declared publish set. `my sync --push` then reports a content-only hold, but
   the write-time command did not make the publish problem obvious. Because
   content-only evaluation is mount-wide, one undeclared sibling can hold
   otherwise publishable files.

   **Fix partially implemented in this change:** handbook mounts now include `customers`
   and `fleet` in their default publish paths, including the legacy fallback
   used by older session records, matching `my customers add` and `my fleet add`
   when they write records into a handbook mount. Record creation also warns
   immediately when the created file is outside the mount's declared publish
   paths, naming the `include_paths` addition needed before `my sync --push`
   will publish it. Remaining follow-up: hold or publish per file rather than
   per mount. Effort: follow-up M.

### Tier 2 - legibility and registry gaps

5. **`held back` is one opaque status for many distinct sync gates.**
   `syncer.Result` exposes a single status plus free-text message, so agents must
   reverse-engineer which gate tripped and which next command is correct.

   **Fix implemented in this change:** held-back sync results now include a
   stable `reason_code` field such as `active_session`, `unadopted_content`,
   `outside_content_paths`, `local_mount_urls`, or
   `gnit_not_control_workspace`, plus `next_command` for classified actionable
   gates. Human output also prints `next=...` on those held-back rows, and
   `my doctor` freshness/last-sync detail lines preserve the code and next
   command when present. Remaining follow-up: consider a dedicated explain
   command that summarizes code plus remedy per mount. Effort: follow-up M.

6. **`my sync` does not self-heal dirty or diverged inbound state.**
   The inbound path fast-forwards only clean behind repositories. Dirty,
   ahead-behind, or diverged mounts require manual git choreography, even when a
   safe autostash/rebase/pop flow would be possible.

   **Partial fix implemented in this change:** dirty-behind and diverged holds
   now get distinct `reason_code` values (`dirty_behind`, `diverged`) and
   `next_command` values that name the real first recovery step instead of
   sending the operator back to a `my sync` invocation that cannot progress.
   Full auto-reconcile remains follow-up.

   **Fix:** add an opt-in reconcile mode that reports each step, autostashes,
   rebases, pops, and holds only on real conflict. Keep publishing deliberate.
   Effort: M.

7. **No CLI verb creates a customer record.**
   Customer resolution can warn and keep a literal unknown value, but the CLI has
   no `my customers add` counterpart to the mounted customer record model.

   **Fix implemented in this change:** `my customers add <domain|slug>` now scaffolds a
   mounted `customers/<id>.md` record, supports `--name`, `--domain`,
   `--domain-confirmed`, repeatable `--alias` and `--partner`, and marks the
   created record with Git intent-to-add like the other record writers. Unknown
   customer warnings now point at the scoped `my customers add ...` command.
   Dedicated `kind: customers` mounts now default to the `customers/` publish
   path, so new customer records are eligible for explicit publish without a
   redundant `include_paths` declaration.

8. **Role guidance can duplicate global guidance in generated prompts.**
   A manifest can declare one guidance fragment globally and also select the
   same fragment through a role. The launch/compile model should tolerate that
   overlap, but generated `AGENTS.md` repeated the section, adding prompt noise
   and making drift harder to reason about.

   **Fix implemented in this change:** guidance composition now dedupes manifest and
   role-selected guidance paths while preserving first occurrence order, so
   global guidance stays first and role-only guidance still follows. Launch
   projection now applies the same rule, including duplicate top-level manifest
   guidance entries. Covered by
   `TestComposeWithOptionsDedupesRoleGuidancePaths` in
   `internal/guidance/guidance_test.go` and
   `TestCompileDedupesRoleGuidancePaths` in
   `internal/launchplan/launchplan_test.go`; the setup-to-launch stale-loop
   path is covered by `TestSetupThenLaunchDedupesGlobalRoleGuidance` in
   `internal/cli/launch_test.go`.

### Tier 3 - architecture/design issues to file separately

9. Skill materialization remains hard to reason about across global installs,
   launch-scoped installs, selected profiles, and removed skills.
10. Control-plane and data-plane boundaries need stronger guardrails so private
   operator data cannot be accidentally filed into public issue trackers.
11. Installed binary drift still makes agents attempt commands that exist in
    current guidance/source but not in the local binary.
12. "Session" remains overloaded between My AI worktree sessions and harness
    chat sessions.

## Already fixed

- **`my ai` guidance-stale loop / launch self-heal** shipped in v0.34.0.
- **Pull-only default for bare sync** shipped in v0.32.0; publishing is now
  explicit.
- **Record commands target the active session worktree** shipped in v0.17.0.
- **Backend banner noise is verbose-gated and clearer** shipped in v0.32.0.

These should stay in the audit as validation that previous fixes addressed real
recurring pain, but they should not be reimplemented.

## Rejected or weak findings

- Local-only mount publish holds are correct-by-design. The CLI should keep
  requiring an explicit publish path instead of silently creating or pushing new
  remotes.
- Legacy on-disk state from an older umbrella tool was not supported by
  transcript evidence as an active my-cli guidance-confusion source. At most, it
  merits a cleanup advisory.

## Changes implemented in this change

- **Manifest-scoped init follow-ups:** `my init` now prints scoped next commands
  for setup, launch, and publish (`--manifest <org-id>`) even when the registry
  currently contains only one manifest. The generated manifest README uses the
  same scoped commands, so a later registry default change cannot make a copied
  publish/setup command target the wrong organization. Covered by
  `TestInitCreatesManifestRepoAndRegisters`,
  `TestInitNextCommandsUseManifestWhenRegistryHasSeveralManifests`, and
  `TestInitScaffoldREADMETeachesTeammateFirstRun` in
  `internal/cli/init_test.go`.
- **Manifest control-plane direct publish:** `my publish --manifest NAME` and
  `my sync --publish direct --scope manifest` can now commit and push dirty
  manifest control-plane files without routing them through content auto-publish.
  Auto `--push` still holds manifest entries, and dirty files outside the
  control-plane allowlist still hold. Covered by
  `TestPublishManifestCommitsControlPlaneChanges`,
  `TestPublishPrintPlansKnownMountRewriteCommit`,
  `TestPublishManifestCommitsControlPlaneRename`,
  `TestPublishManifestHoldsRenameFromOutsideControlPaths`,
  `TestPublishPrintPlansManifestControlPlaneCommitWithoutChanges`,
  `TestPublishManifestHoldsFilesOutsideControlPaths`,
  `TestSyncPushHoldsManifestControlChangesInAutoMode`,
  `TestSyncDirectPublishesManifestControlChanges`,
  `TestSyncDirectPublishesUntrackedManifestControlChanges`, and
  `TestSyncDirectHoldsManifestFilesOutsideControlPaths` in
  `internal/cli/init_test.go` and `internal/cli/sync_test.go`.
- **#28 self-heal:** `sessionHoldMessage` now receives the base checkout's dirty
  file list and, when non-empty, sequences the guidance so the operator resolves
  base dirt before running `my session finish --land` or `--publish`.
  Guidance-only; no sync gating behavior changed. Covered by
  `TestSessionHoldMessageSequencesBaseDirtyBeforeFinish` in
  `internal/syncer/sessionhold_test.go`.
- **Stale session diagnostics and closed-session stub:** `activeSessionForPath`
  now distinguishes known inactive sessions from unregistered session
  directories and gives concrete recovery commands. Land/publish also replaces
  session-local guidance with a finished-session stub so a later harness launch
  does not read active-session instructions. Covered by
  `TestActiveSessionForPathExplainsFinishedSession` and
  `TestActiveSessionForPathExplainsUnregisteredSessionDirectory` in
  `internal/cli/work_test.go`,
  `TestLaunchFromInsideFinishedSessionExplainsRecovery` in
  `internal/cli/launch_test.go`, and worksession land/outcome coverage in
  `internal/worksession/worksession_test.go`.
- **Handbook record publish defaults:** handbook mounts now treat `customers/`
  and `fleet/` as default content paths, so customer and fleet records written
  by `my customers add` or `my fleet add` are eligible for normal explicit
  publish. The older-session fallback table now matches that default. Covered
  by `TestSyncContentPathsIncludesRecordDefaults`,
  `TestSyncDirectPublishesCustomerFromHandbookDefault`, and
  `TestSyncDirectPublishesFleetFromHandbookDefault` in
  `internal/cli/sync_test.go`.
- **Record write-time publish warning:** `my customers add`, `my meetings add`,
  `my support add`, and `my fleet add` now warn on stderr when the created file
  is outside the mount's declared publish paths and would be held by
  `my sync --push`. Covered by
  `TestRecordAddWarnsWhenCreatedFileIsOutsidePublishPaths` in
  `internal/cli/content_test.go`.
- **Sync hold reason codes and next commands:** held-back sync results now carry
  stable `reason_code` values and common `next_command` remedies so agents can
  branch on the gate without parsing free text. Human output also appends
  `next=...` when available, and `my doctor` carries those fields in
  freshness/last-sync details while suppressing self-referential
  `next_command=my doctor` detail noise. Covered by assertions in
  `internal/syncer/syncer_test.go`, `internal/syncer/sessionhold_test.go`, and
  `internal/cli/sync_test.go`, with doctor propagation covered in
  `internal/cli/doctor_test.go`.
- **Customer record creation:** `my customers add <domain|slug>` creates mounted
  customer identity records, dedicated customers mounts publish `customers/` by
  default, and unknown-customer warnings now point at that scoped command.
  Covered by `TestCustomersAddMarksCreatedRecordIntentToAddAndResolvesAlias` in
  `internal/cli/content_test.go`,
  `TestSyncDirectPublishesCustomerFromCustomersDefault` in
  `internal/cli/sync_test.go`, and customer package tests in
  `internal/customers/customers_test.go`.
- **Duplicate role/global guidance dedupe:** guidance composition now renders a
  guidance path once even when it appears in both global manifest guidance and
  selected role guidance; compile projections use the same dedupe rule. Covered
  by `TestComposeWithOptionsDedupesRoleGuidancePaths` in
  `internal/guidance/guidance_test.go` and
  `TestCompileDedupesRoleGuidancePaths` in
  `internal/launchplan/launchplan_test.go`, with setup-to-launch coverage in
  `internal/cli/launch_test.go`.
