# Accumulated organization contract

Status: active. Drafted by Claude and adversarially reviewed by Codex.
Phase 1 (file-backed contract) shipped in v0.47.0. Phases 2–4 are authorized
and follow as their own releases. Phase 5 and the evidence generator remain
optional and later.

## Goal (operator)

Organizations need a growing list of things that are always true and that
every agent knows at startup. Examples: where logs live, which conventions
to follow, matching technical or clinical language to the person being
answered, and always asking before touching external assets.

- **Authority.** Only someone with authority over the organization's
  contract and policies can change the list.
- **Storage.** The list is a plain text file with one item per line, so a
  git diff of a change reads naturally. It does not live inside
  `manifest.json` and it does not live in the content repo, which everyone
  can write to.
- **Delivery.** Every `my` launch loads the list. Long-running sessions pick
  up changes too, and `my sync` shows changes as a diff.
- **Review.** A capable model reviews the whole document after each change,
  finds conflicts, and proposes resolutions as PR notes for an administrator
  to review.
- **Scope.** This is a universal feature, not specific to one organization.

## What exists today (origin/master fc48a29, v0.46.0)

- The manifest field `contract: []string` holds short binding rules. They
  render as `## Organization Contract` in the generated `AGENTS.md`
  (`internal/guidance/guidance.go` `ComposeWithOptions`), right after the
  baseline and before `## Organization Policies` and the manifest guidance
  fragments.
- `my contract list` exists, and so does `my admin contract add|remove`. For
  a registered manifest, add/remove goes through an authorized, clean,
  trusted-upstream checkout and opens a PR with `PreserveCheckout`
  (`internal/cli/contract.go`).
- Governed manifests require the provider's `PermissionAdmin` for admin
  authoring (`loadAuthorizedAdminManifestCheckout`). Ungoverned manifests
  have no such check: anyone who can push to the manifest repo is
  effectively an admin.
- `manifestControlPaths()` already allows `guidance/**` to publish from the
  manifest repo.
- `governance.policies` are separate. They are versioned Markdown documents
  with digest-verified reads and an acceptance record. They are the
  heavyweight, authoritative tier.
- `my sync` regenerates derived guidance only when
  `changedManifestForDerived(report)` sees a manifest change, and only for
  the resolved umbrella root. Session worktrees carry their own `AGENTS.md`
  (`internal/worksession` `writeSessionGuidance`).
- Manifest JSON decoding ignores unknown keys. An older `my` therefore
  silently ignores any new field. `min_my_version` (v0.46.0) is the guard
  against that.
- Rewriting `AGENTS.md` alone does not establish that a running harness
  reloaded it.

## Converged design

### 1. One concept: extend the contract, don't add a knowledge subsystem

The operator's examples include facts (logs live at X), conventions (match
technical or clinical register) and obligations (ask before using external
assets). All three are always-true operating context that every agent loads.
Splitting them into "knowledge", "ontology" and "contract" would create three
lifecycles for one need. The contract becomes file-backed and is allowed to
grow. The heading in `AGENTS.md` stays `## Organization Contract`.

Policies stay separate and authoritative. A contract line is a short
always-loaded statement. A policy is a long document that agents consult and
operators accept. When the two conflict, the policy wins and the conflict is
surfaced (see section 6). Neither side silently overwrites the other.

### 2. Storage: `guidance/contract.md` in the manifest repo

```json
"contract_file": "guidance/contract.md"
```

- `contract_file` is a new top-level manifest key holding a path relative to
  the manifest root. It must be a relative path, not a URL, and after
  symlinks are resolved it must still be a regular file inside the manifest
  repo.
  - **As shipped (v0.47.0), narrower:** the path must be a clean `.md` path
    under `guidance/`, which is already a manifest control path, so normal
    sync and admin PRs publish it with no extra rule. A symlink anywhere on
    the path is refused rather than resolved, so reads and writes can never
    leave the repo.
- It is a new key rather than a change to the type of `contract`. An older
  binary would fail to decode a `[]string` field that now holds an object,
  and it would fail before `min_my_version` is ever read.
- The manifest repo is the authority boundary, as the operator suggested. It
  is already the protected control plane and already carries `guidance/**`.
  The content repo is rejected because every member can write to it.
- File format:

  ```markdown
  # Organization contract

  ## Logs
  - Service logs live under the install's `logs/` directory.

  ## Communication
  - Answer technically able people in technical language.
  - Answer clinical staff in clinical language.

  ## External assets
  - Ask permission before accessing any external asset.
  ```

  - Each `- ` bullet is one entry and fits on one line.
  - `#`/`##` headings are optional free-form groups and are kept in the
    rendered output. The category names are not fixed.
    - **As shipped:** `## ` headings are the groups. They must be unique and
      render as `### ` under `## Organization Contract`, and a heading with
      no rules renders nothing. One `# ` title may open the file. It names
      the file for people and is not rendered, because `AGENTS.md` already
      carries the section heading. `migrate` writes that title, and the
      rendered output stays byte-identical. A tab inside a rule is allowed,
      as it was inline. Other control characters, invalid UTF-8, a
      byte-order mark and stray carriage returns are errors. Edits rewrite
      only the lines they touch and keep every other line's bytes and line
      ending.
  - Blank lines are allowed.
  - Anything else is a validation error: prose paragraphs, nested bullets,
    continuation lines. This keeps the file a list, with one diff line per
    change.
  - Duplicate entries are a validation error, as they are in today's
    `contract`.
- **No stable line IDs in v1.** The operator asked for a text dump where a
  git diff makes sense.
  - The text is the identity. A replacement shows as `-old` / `+new`, and a
    retraction as `-old`.
  - v0.20.0 already rejected IDs as ceremony.
  - Edit handles stay a 1-based position or the exact text, always guarded
    by the expected base (section 3).
  - IDs come back only if conflict notes turn out to need a handle that
    survives rewording.
- **One source.** A manifest sets either `contract_file` or the inline
  `contract`, never both. Setting both is a validation error, so there is
  never a question of which one wins.
- **Feature floor.** `contract_file` requires the release that ships it.
  `migrate` and `EncodeDocument` stamp `min_my_version` automatically. A
  v0.46+ binary older than the floor asks for `my update` instead of
  launching without the contract. Releases older than v0.46 ignore the key
  and launch without the contract. The release notes and the migration
  output say so, and tell the admin to confirm that members run v0.46 or
  later before migrating.

### 3. Authority and authoring

- **Activation.** A change takes effect when it merges to the manifest
  repo's trusted default branch.
  - The real authority boundary is the provider: GitHub branch protection,
    required reviews and repo permissions. My AI never claims a restriction
    it cannot enforce.
  - In an ungoverned manifest, whoever can push to the default branch can
    change the contract, and the docs say so plainly.
  - Local admin naming or roles are not security.
- **Governed manifests** keep the existing `PermissionAdmin` gate on every
  contract authoring command.
- **Commands.** The first release that ships `contract_file` also ships the
  file-source authoring commands, so migrating never leaves a manifest
  without a way to edit:
  - `my admin contract add "TEXT" [--under HEADING]`
  - `my admin contract remove <index|"TEXT">`
  - `my admin contract replace <index|"OLD"> "NEW"`
  - `my admin contract migrate`

  `replace` is one atomic edit. Moving a log path is one PR, with no
  in-between state where both rules, or neither, are active.
- **Routes.** These follow the existing admin pattern:
  - `--manifest NAME` (a registered manifest) works in an authorized, clean,
    trusted-upstream checkout and opens one PR.
  - `--manifest-dir DIR` (the maintainer route) writes uncommitted changes
    and never commits on its own.
- **Expected base, from the first release.** Every edit records the manifest
  upstream commit and the contract file blob SHA it was computed against.
  - If either has moved by the time it publishes, the edit refuses with a
    rebase hint instead of overwriting a concurrent change.
  - This matters because a position handle (`remove 3`) is unsafe once the
    file has changed underneath it.
- **Proposals (later, optional).** v1 authoring is admin-only. A later phase
  lets members without manifest push rights propose a change:
  - `my contract propose "TEXT" [--replace N|"OLD"] [--retract N|"OLD"]`
    writes a proposal bundle (section 11) against the current revision,
    which the member sends to an admin.
  - The admin applies it with `my admin contract apply <bundle>`.
  - It needs no repo push and adds no content-repo proposal domain.
  - A proposal never activates anything, never raises anyone's
    permissions, and never enters launch context.

### 4. Revisions, reconciliation and `my sync`

- **Contract revision.** The pair (manifest commit, contract file blob SHA).
  - Ordering comes from manifest commit ancestry, not from content.
  - A rollback to older text is a new commit, so it is a new revision, and
    sessions are told about it like any other change.
- **Three cursors**, kept per manifest in
  `~/.local/share/my-cli/state/contract/<manifest>.json` and session state:
  1. **active**: the revision of the locally synced manifest checkout.
  2. **installed**, per output root (the umbrella and each open session
     root): the revision whose `AGENTS.md` was last written successfully
     there.
  3. **emitted**, per launch instance: the last revision a hook printed
     into that harness conversation (section 5).
- **Pending reconciliation.** When active differs from a root's installed
  revision, that root is pending.
  - The marker clears only after that root's `AGENTS.md` is written
    successfully.
  - Each root is retried on its own, so one failing session root does not
    block the others or the umbrella.
  - `--no-derived` and a crash mid-reconcile leave the marker set. The next
    sync, or any command that already reconciles derived guidance, retries
    even if the pull fetched nothing.
- **`my sync` diff.** When the active revision moves, sync prints the line
  diff on stderr, grouped by heading (`+ added`, `- removed`). The JSON
  report carries it as `contract_change`. The diff is computed from the
  previous active revision, which is recorded only once the new one is
  checked out.
- **Commands.**
  - `my contract status` shows:
    - the active revision and the time of the last successful remote check;
    - each root's installed revision;
    - each session's state: `current`, `pending`, `emitted <rev>`,
      `untrusted`, `unsupported` or `stale`.

    It does not claim to know whether the machine is online without a
    check. It reports the last check time.
  - `my contract diff [--from REV] [--to REV]` prints the line diff.
  - `my contract show` prints the active file.
  - `my contract list` keeps working and reads from the file.

### 5. Long-running sessions: honest live delivery

The design does not claim universal hot reload. Rewriting `AGENTS.md` alone
is not evidence that a running session sees the change, and delivery depends
on the harness.

- **Discovery is not push.** New contract text reaches a machine only
  through `my sync` or the normal freshness auto-refresh. Hooks never fetch.
  They only compare local revisions, so with no network nothing propagates
  across the organization at prompt time. That is the stated limit.
- **Launch instances.** Receipts are keyed by launch instance, not by `my`
  workspace session id. One workspace session can hold several harness
  processes, and launches from the base umbrella or a repo have no
  workspace session at all.
  - Every `my ai` launch registers an instance: a unique id, the manifest,
    the harness, the launch root, the start revision, and an optional parent
    workspace session.
  - The registry covers base, repo, session and contained launches.
  - On startup the harness's `SessionStart` hook binds the instance to the
    harness's native conversation id.
  - A resume keeps the native conversation id and so keeps its instance
    cursor, with a full snapshot on resume. A fork gets a new instance that
    starts from its parent's emitted revision, also with a full snapshot.
  - A Claude Code update therefore never suppresses a Codex update in the
    same workspace session.
- **Sessions opened before this ships.** A harness that was already running
  before the adapter shipped cannot gain hooks retroactively. Status shows it
  as `unsupported`. The fix is a manual reload (paste `my contract diff`) or
  a one-time relaunch, and the docs say so.
- **Receipts.**
  - Each launch instance records the revision it started with.
  - When a hook prints an update, `my` records an emission receipt. That
    proves only that `my` printed the update into the harness's hook path.
    It does not prove the harness accepted it, or that the model read or
    followed it.
  - Delivery is at-least-once. Every update carries its revision, so a
    repeated update is harmless.
  - Status never reports a session as having read or accepted an update.
- **Update content.** The update block:
  - says the contract changed;
  - lists the added lines;
  - lists removed lines as explicitly `RETRACTED`;
  - says the new text supersedes earlier context.

  The old text cannot be erased from the model's context, so the update has
  to say what no longer applies. When nothing changed, the hook prints
  nothing.
- **Removing the last rule is still an update.** When the contract becomes
  empty or the file is removed, existing sessions still receive an explicit
  empty snapshot or a list of retractions. Only after that do hooks and state
  go quiet. "No noise" applies only to organizations that never configured a
  contract.
- **Claude Code (live candidate).**
  - `my ai` adds a generated settings layer at launch
    (`claude --settings <generated file>`) and never edits the user's own
    settings.
  - The layer registers `my contract pending --instance <id> --hook claude`
    on `UserPromptSubmit` and on `PostToolUse`. `PostToolUse` covers long
    autonomous runs between user prompts.
  - It also registers the command on `SessionStart` for the `compact` and
    `resume` sources. After compaction or resume, earlier updates may be gone
    from context, so this re-injects the full active snapshot, not a diff.
  - The hot path is a local revision comparison only.
  - Context added around a tool call may not make the model reconsider an
    action it has already issued, so nothing here is claimed as mid-action
    enforcement.
  - Ships only after a live integration proof. Merging the settings layer
    and the behavior of each hook event were not tested independently while
    planning.
- **Codex (live candidate).** Codex CLI 0.163.0 ships hooks.
  - Per the [official hooks docs](https://learn.chatgpt.com/docs/hooks),
    these events accept `additionalContext`:
    `SessionStart` (startup, resume, clear, compact and fork, including
    automatic mid-turn compaction), `UserPromptSubmit`, `PreToolUse` and
    `PostToolUse`.
  - Non-managed hooks need trust for the exact definition hash, and a
    changed hook is skipped until it is reviewed (`/hooks`).
  - The adapter keeps existing hooks and never bypasses hook trust. A hook
    that is installed but not yet trusted reports `untrusted`.
- **Context limits.** Codex's default additional-context limit is about 2500
  tokens.
  - A trusted adapter configures a limit large enough for the full snapshot
    (`additionalContextLimit`, where 0 means unlimited).
  - If it cannot, the session reports `incomplete` and stays stale. It never
    emits a partial snapshot marked current.
  - The phase gate is a live integration proof, not an assumption.
- **Grok, Cursor and other harnesses.**
  - Each harness's hook support is checked against its current docs and
    live behavior when its adapter is built. Where a prompt-time hook
    exists, the adapter matches Claude Code.
  - Otherwise the session is `unsupported`. Any `my` command run inside it
    prints the pending diff on stderr.
  - That fallback never advances the session's emission cursor, because `my`
    cannot tell whether the model saw stderr.
  - Status shows the session as stale, with "restart or paste the diff".
- **Baseline fallback.** The baseline guidance gains one rule:
  - if a `my` command prints a contract update, follow it from then on;
  - before starting a new task in a long session, run `my contract status`.

  This is a soft fallback, not a guarantee.
- **Emergency tightening.** Once a machine has synced, an urgent rule
  reaches hook-capable sessions at their next prompt or tool boundary, and
  other sessions at their next `my` command. Killing sessions is out of
  scope.

### 6. Model conflict review (advisory only)

- **Command.** `my admin contract review [--harness claude|codex] [--model
  M] [--json]` runs the admin's own harness headless, with the admin's own
  credentials.
- **Input.** The full contract, the pending diff, and the exact text of
  every applicable policy. Summaries are not enough. If a policy source is
  missing, the result is `incomplete`, never clean.
- **Findings.** Conflicts between lines, conflicts with policies, duplicates
  in different words, stale facts, and entries too vague to act on. Each
  finding suggests a concrete `-`/`+` edit.
- **Sandbox.**
  - The review harness gets no write, shell, MCP or network tools, and runs
    with a bounded timeout and cost.
  - The contract and policy text are passed as data, with an explicit
    instruction not to follow them.
  - The model is chosen explicitly, with `--harness`/`--model` or manifest
    `contract_review.harness`/`model`.
  - If no harness is available, the result is a distinct `unavailable`
    status, never an empty "no conflicts".
- **When it runs.** Authoring commands run a review only with `--review` or
  when the manifest opts in. There is no model call on every edit by
  default.
- **Output.** The findings go into the PR body under "Conflict review" for
  the approving admin.
  - A finding never blocks, rewrites, merges or auto-resolves anything.
  - A clean review is not a certificate of consistency, and the PR text says
    so.
- **Deterministic lint** always runs first and checks only format,
  duplicates and the size budget. Contradiction detection needs semantics,
  so it belongs to the advisory model review.
- **Stale reviews.**
  - Review evidence binds the candidate contract head, the policy digests it
    read, the prompt version and the model.
  - If any of these changes, `my admin contract review --check` and the PR
    body report the review as stale. That includes a policy update with no
    contract change, which also earns a doctor note on the active contract.
  - This is advisory, not a merge gate. Branch protection stays the
    organization's choice.
- **CI example (optional).** An example GitHub workflow in `examples/` runs
  the same review on manifest-repo PRs that touch the contract file and posts
  a comment.
  - The organization supplies its own model secret.
  - `my` itself holds no API keys.

### 7. Visibility and contained launches

- Every member loads the contract, so it may hold only information every
  member is allowed to see. Material for some roles only stays in
  role-scoped policies or guidance fragments. The review step and the docs
  say so.
- `my compile` and contained (Mode B) launches carry the contract content
  and revision, not only the local `AGENTS.md`.

### 8. Freshness and offline

- The existing governed freshness and acceptance gates stay as they are.
  For example, `requireGovernedManifestFreshness` can fail a launch closed
  after its TTL.
- A launch uses the cached contract only where those existing rules already
  allow a cached manifest.
- Status reports the local revision and the last successful remote check.

### 9. Size budget

- Every line goes into every session's context. `my doctor` and validation
  warn above 150 entries or 16 KiB.
- The review step flags entries that read like policy and belong in a
  `governance.policies` document.
- Role-scoped contract sections are deferred until a manifest needs them.

### 10. Migration (the smooth path)

`my admin contract migrate` runs on `--manifest NAME` (one PR) or on
`--manifest-dir DIR` (uncommitted changes). It does the following:

- refuses if the target file already exists;
- writes the inline `contract` entries to `guidance/contract.md`, in order
  and under no heading;
- removes `contract` and sets `contract_file` and `min_my_version` in one
  targeted multi-key JSON patch. This generalizes the v0.46.0 single-key
  patch so that unknown keys and key order survive;
- validates the result before writing.

The rendered `AGENTS.md` is byte-identical before and after migration, and a
golden test proves it. Inline `contract` stays supported indefinitely.

### 11. Extension point: evidence-driven proposals (later, optional)

The operator's follow-up idea is a post-processing step that reads recent
traces and session or incident logs, then proposes new contract rules,
policies or skills, or changes to existing ones. Non-admins could run it as
an incident closeout, or it could run alongside sync once issue records are
published.

Nothing in this section is built now. The plan only keeps the seams so the
step can be added later without rework. Even the proposal bundle is a
versioned design seam, implemented with the phase 5 proposals work rather
than as extra infrastructure in phase 1.

- **A versioned proposal bundle that covers every target.** The phase 5
  `propose` command emits this format rather than a bare patch, so a later
  generator produces the same artifact a person does. A bundle carries:
  - `schema_version`.
  - `target` (`contract`, `policy`, `skill` or `guidance`) and `op` (`add`,
    `replace`, `retract` or `amend`).
  - The proposed text or patch, and `base` (manifest commit plus file blob
    or policy digest).
  - `strength`: `recommendation` or `obligation`.
  - `proposed_scope`: all members, a role, or a specific policy or skill.
  - `rationale`, plus any known exceptions or conflicting evidence.
  - `evidence` references (see below).
  - `submitted_by`, the identity the caller claims. This is kept separate
    from the verified reviewer identity, which only provider review
    supplies.
  - `generator`: `human`, or harness + model + prompt version.
  - A normalized `dedupe_key`.
- **Intake through existing authority.**
  - Phase 5 ships `my admin contract apply <bundle>` for the contract
    target. It enforces the expected base and opens the same reviewed PR an
    admin edit would.
  - The phase 4 review accepts bundles as input.
  - A generic `my admin proposal apply` that routes bundles to other
    targets' authoring paths is a candidate future surface, not a
    commitment.
- **The generator is a skill or workflow, not a core CLI model
  dependency.** It runs on the caller's own harness, and non-admins can run
  it.
  - Inputs are explicitly selected records, sessions or traces within a
    bounded time window.
  - Access is caller-scoped. It reads only what the caller may already read.
    That includes the approved, published contract, policies and skills,
    which it needs in order to propose amendments.
  - It never receives manifest write access or admin credentials.
  - It is sandboxed like the review (section 6) and can only write bundle
    files. It cannot grant itself admin rights, apply or merge anything, or
    put text into launch context.
- **Evidence and privacy.**
  - **Published sources.** Each evidence reference names an immutable
    source revision or digest and the time window it covers.
  - **Local sources.** Unpublished traces may be analyzed too. Their
    provenance is marked `local/unpublished`, and they are never uploaded
    automatically.
  - **No raw text.** Bundles never copy raw trace or log text.
  - **References can leak too.** Names and paths can be private, so
    references stay access-controlled and rationale is redacted.
  - **What a model may see.** An explicit organization policy states which
    evidence may be sent to a model at all. Secrets and unnecessary
    sensitive content, such as customer or patient identifiers, are
    excluded.
- **One incident is a hypothesis, not a universal rule.**
  - The generator states whether a candidate is a recommendation or an
    obligation, notes exceptions and conflicting evidence, and proposes a
    scope.
  - The admin decides the audience. A candidate may belong in the
    all-member contract (section 7 visibility applies), or in a scoped
    policy or skill instead. Visibility is judged per target, not as a
    blanket all-member requirement.
- **No repeated suggestions, no lost evidence.**
  - A candidate whose `dedupe_key` matches active text or a known pending
    bundle is not re-proposed. Its distinct supporting evidence is attached
    to the existing bundle instead of being discarded.
  - An admin therefore sees a cluster, such as ten incidents behind one
    rule, as a single item with all of its evidence.
- **Triggers and transport are deliberately open.**
  - A future adapter consumes only successful sync results and resolves the
    record revisions that were actually published. It never treats an
    attempted or failed sync as published. Whether today's `my sync --json`
    report carries complete published-record ids is checked when that
    adapter is designed, not assumed here.
  - The later phase decides how bundles travel: as a file, as a
    manifest-repo PR, or through a declared content-repo inbox.
  - However bundles travel, a proposal never renders into launch context and
    never raises permissions.

## Scenarios

1. **A log path moves.** An admin runs `replace`, which is one PR with a
   `-old +new` diff. After sync, hook sessions get an update with the old
   line marked `RETRACTED`. Unsupported sessions see the diff at their next
   `my` command.
2. **A clinical-register rule lands while ten sessions are open.**
   - Synced Claude Code sessions get it at their next prompt or tool
     boundary.
   - An untrusted Codex hook shows `untrusted`.
   - A harness without hooks shows `stale`.
   - The baseline rule makes the agent check before its next task.
3. **A non-admin suggests a rule** (later phase). `propose` writes a patch
   file. It is in nobody's context until an admin applies it and the PR
   merges.
4. **Two admins edit at the same time.** The second edit's expected base
   (commit and blob) no longer matches, so it refuses. The admin rebases,
   and any review re-runs against the new base.
5. **A contract line contradicts a policy.** The review flags it in the PR.
   If it merges anyway, the policy stays authoritative under the existing
   consult contract, and `my doctor` keeps reporting the recorded conflict
   until a later review clears it.
6. **Rollback.** Reverting to last week's text is a new manifest commit, so
   sessions are notified. None is left on a "newer" revision that is
   actually wrong.
7. **`my sync --no-derived`, or a crash mid-reconcile.** The pending marker
   survives, and the next reconcile retries every pending root even when
   nothing new was pulled.
8. **An old binary.** v0.46+ older than the floor reports
   `requires my vX or later; run my update`. Releases before v0.46 ignore
   `contract_file`, which is documented and flagged at migration.
9. **An offline laptop.** The existing freshness rules decide whether the
   launch may proceed on the cached manifest. Status shows the last
   successful check time.
10. **The last rule is removed.** Open sessions receive an explicit empty
    snapshot or retractions before going quiet.
11. **An organization that never had a contract.** No section, no hooks, no
    state, no noise.
12. **Incident closeout (later phase).** After syncing an incident's
    records, a non-admin runs the closeout skill on those records.
    - It writes one bundle proposing a scoped recommendation and cites the
      published record revisions.
    - If a pending bundle already makes the same proposal, the new evidence
      is added to that bundle.
    - An admin judges the audience. In this case the rule fits better in a
      role policy than in the all-member contract.
    - The admin then applies the bundle through the reviewed path.
    - Until it merges, no session sees the rule.

## Phases (1 shipped; 2–4 authorized; 5 optional)

1. **File-backed contract.** *Shipped in v0.47.0.* The compile block
   carries `revision: {manifest_commit, blob}`.
   - `contract_file` schema, parser and validation.
   - Feature floor and rendering from the file.
   - `list` and `show`.
   - File-source `add`, `remove` and `replace` with the expected base.
   - `migrate`, with the golden byte-identity test.
   - `my compile` and contained projections carry the contract content and
     revision, so migrating to a file never drops the contract from them.
   - Docs and changelogs.
2. **Revisions and sync diff.**
   - The three cursors and per-root pending reconciliation.
   - The sync `contract_change` diff.
   - Fan-out to session roots.
   - `my contract status` and `my contract diff`.
3. **Live delivery.**
   - The launch-instance registry, native-id binding, and receipts.
   - `my contract pending`, explicit retraction and empty snapshots, and
     the stderr fallback.
   - The baseline rule.
   - The Claude Code and Codex adapters, each gated on a live integration
     proof (for Codex: hook trust and context limit).
   - Other harnesses as their hook support is verified.

   This phase is part of full delivery. The operator asked for in-session
   reload, so it is not optional polish.
4. **Review.**
   - Deterministic lint and the sandboxed `my admin contract review`.
   - PR-body findings and stale-review detection.
   - The example CI workflow.
5. **Proposals (optional).**
   - The proposal bundle schema.
   - The `propose` bundle export.
   - `my admin contract apply <bundle>`.

   The evidence-driven generator (section 11) builds on this phase later,
   as its own plan.

Each phase is its own release, with sandbox smoke tests (stub `gh`, local
bare repos). The operator approved migrating their own manifest after
phase 1. It goes through the normal reviewed manifest PR.

## Tests (beyond unit coverage per package)

**File and migration**

- Migration output is byte-identical to the inline-contract `AGENTS.md`.
- Migration refuses when the target file exists, and preserves unknown keys
  and key order.
- Setting both `contract` and `contract_file` is rejected.
- Prose, nested and multi-line entries are rejected.
- Symlinks that escape the repo, and URL paths, are rejected.
- A floor-gated old binary reports the upgrade instead of an invalid
  manifest.

**Authoring**

- `replace` is a single edit.
- `add`, `remove` and `replace` refuse when the upstream commit or the blob
  has moved.

**Reconciliation**

- Each root's pending marker survives `--no-derived` and a simulated crash,
  and clears only after a successful write.
- A failing session root does not block the other roots.

**Live delivery**

- The hook prints nothing at the current revision.
- After a removal the hook prints `RETRACTED` lines, and after the last rule
  is removed it prints an empty snapshot.
- Compaction and resume re-inject the full snapshot.
- With Claude Code and Codex both running in one workspace session, each
  gets its own update.
- A base-umbrella launch with no workspace session gets updates.
- A fork starts a new instance cursor.
- A repeated update with the same revision is harmless.
- A rollback produces a new revision notification.
- A snapshot over the context limit reports `incomplete` and is never marked
  current.
- The stderr fallback never advances an emission cursor.
- Hooks do no network I/O.

**Review and other**

- `my compile` output carries the contract.
- Review findings never change the file.
- A review is reported stale when the contract head, a policy digest, the
  prompt or the model changes.
- A missing harness reports `unavailable`, and a missing policy source
  reports `incomplete`.
- Proposals never render into launch context.
- A bundle whose base has moved refuses to apply.
- A bundle with raw evidence text instead of references fails validation.
- A bundle that duplicates active or pending text is not re-proposed, and
  its distinct evidence is attached to the existing bundle.
- A bundle citing an unpublished or failed-sync source is marked
  `local/unpublished`, never `published`.

## Defaults chosen (the operator can override any of them)

1. Keep the `## Organization Contract` heading, since it is one concept.
2. Authoring is admin-only in v1. Patch proposals are a later, optional
   phase.
3. The CI review ships only as an optional example. The review on the
   admin's own harness is the primary path.
4. Role-scoped sections are deferred until a manifest needs them.
