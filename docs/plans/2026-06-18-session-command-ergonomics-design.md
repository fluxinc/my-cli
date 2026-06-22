# Session command ergonomics: one noun, a collaboration hint, full consolidation

- Date: 2026-06-18
- Status: Implemented; dual sign-off complete; awaiting release
- Authors: Claude + Codex (Talking Stick coordination)
- Scope: CLI vocabulary + launch UX + **on-disk layout consolidation and migration**.

## Problem

The same underlying object - a set of git **worktrees** under the umbrella content
mounts (`internal/worksession`) - is reached through two inconsistent vocabularies:

- A **command** noun: `my work start|status|list|resume|finish`.
- A set of **flags** on a different command: `my ai --new-session | --session ID |
  -r/--resume | --no-session`.

So `my ai --new-session` ("session" vocab) creates a `my work` unit ("work" vocab).
The nouns don't line up. The operator's first complaint:

> "I don't like that new session makes new work, and so the CLI commands don't line
> up with the arguments."

Second, `my ai --new-session <harness>` **silently execs** the harness
(`internal/cli/launch.go:239`). The freshly created session is started lazily inside
`launchTargetDir` (`launch.go:359-378`) and the returned `session` object is
discarded - so the id, path, and (crucially) *how to bring a second harness into the
same session* are never surfaced. The operator's second complaint:

> "When you run a command like `my ai new session`, it should return with the
> session, but also give you a hint about how to start another harness in the same
> session."

That second harness story is the multi-agent / Talking Stick collaboration path:
two agents sharing one session's worktrees.

Third - surfaced in review - the word **"work" is baked into the filesystem**, not
just the command: the worktree dir is `<umbrella>/work/<id>` and the branch is
`my/work/<id>`. The operator does not want that residue:

> "I don't like mixing names. What if we implement the noun work in some other
> context?"

So "work" must be freed everywhere - command, code constants, and on-disk layout -
leaving the noun available for a future unrelated feature.

## Decision

**Consolidate fully on `session`** (operator-approved 2026-06-18) - vocabulary *and*
filesystem - and **migrate existing sessions** so nothing keeps the `work` name.

Rationale:

- `session` is the noun the operator picked and reached for naturally ("the same
  session"). It is already the registry dir (`.my-cli/sessions/`), the launch
  guidance noun, and the human output noun (`my work start` already prints
  `started session %s`).
- The only conceptual cost is overload with the harness's own conversation/thread.
  `my` does not track harness threads today (YAGNI), so within `my`'s surface
  "session" is unambiguous. Docs say **"work session"** on first mention.
- Freeing `work` from code constants and on-disk paths means a future `work` concept
  can take the name with no collision.

We do **not** add a natural-language `my ai new session` subcommand. `ai` launches;
`session` manages sessions. The mnemonic path is `my session start codex`.

## Mental model (for docs)

- **work session** = the git worktrees you do a unit of work in; created, joined,
  finished. Managed by `my session ...`.
- **harness session** = the agent's own conversation/thread (a Claude Code session,
  a Codex thread). `my` does not manage these; "session" in `my` always means the
  work session.

## Command surface

### Primary: `my session` (human + bot)

```
my session start  [--slug SLUG] [--json] [--print] [harness] [-- harness args...]
my session join   <id> <harness> [-- harness args...]
my session resume [id] [harness] [-- harness args...]
my session list   [--all] [--json]
my session status [--all] [--json]
my session finish <id> --land | --publish | --discard [--message TEXT] [--json]
```

- **start** - create the work session (worktrees). Then:
  - print id + path + mounts and the two hints (see "The hint" below);
  - if a `harness` is supplied, print the hint to **stderr** and `exec` the harness
    into the session;
  - if no harness, just create and print (pure "return with the session").
  - `--print` creates the session but does not exec; it prints the launch command
    (or `cd <path>` when no harness is supplied) to stdout and sends hints to stderr.
  - `--json` creates the session but does not exec; it writes the structured report
    to stdout, including the concrete `launch_command` when a harness was supplied.
- **join** `<id> <harness>` - launch *another* harness into an existing active
  session. The collaboration verb that answers the operator's core ask. Exactly
  equivalent to `my ai --session <id> <harness>` but named for intent.
- **resume** `[id] [harness]` - single-person continuation alias. Auto-picks the sole
  active session, prompts on a TTY when several exist (reuses
  `selectLaunchResumeSessionID` / `promptLaunchResumeSession`). With no harness it
  prints `cd <path>`; with a harness it launches.
- **list / status / finish** - delegate to the existing `runWorkStatus` /
  `runWorkFinish` handlers.

`join` vs `resume` map to the same mechanic (launch a harness into an existing
session) but name two intents: *add a collaborator* vs *continue my work*. Both are
cheap (one code path) and intent-revealing.

### Launch shortcuts: `my ai` (kept, bot-stable)

```
my ai --new-session [harness]     create + launch (now ALSO prints the hint)
my ai --session <id> [harness]    launch into an existing session
my ai -r/--resume [id] [harness]  resume (auto-pick / prompt)
my ai --no-session [harness]      launch from the base umbrella
```

The only behavior change: `--new-session` now emits the id/path + join/finish hint to
**stderr** before exec.

### `my work`: deprecated alias, scheduled for removal

`my work start|status|list|resume|finish` keep working via the same handlers for now,
so upgrades are not abrupt, but the group is marked **deprecated** in help and slated
for removal in a later release - this is what ultimately frees the `work` *command*
noun too (consistent with freeing it in code and on disk). No noisy per-invocation
deprecation banner on stdout/stderr (bots that parse output must not get surprise
noise); the deprecation lives in `--help`. Also fix the current `my work --help` bug:
it errors instead of printing group help. Both `my session` and `my work` print
proper group help.

## Filesystem layout: rename + migrate

Current layout (`internal/worksession/worksession.go`):

| Thing | Constant | Today | After |
|---|---|---|---|
| Session dir | `WorkDirName` | `<umbrella>/work/<id>` | `<umbrella>/sessions/<id>` |
| Branch | `BranchPrefix` | `my/work/<id>` | `my/session/<id>` |
| Default id slug | (literal `"work"`) | `2026-06-18-work-ab12` | `2026-06-18-ab12` (no baked-in noun) |
| Registry record | `RegistryDir` | `.my-cli/sessions/<id>.json` | unchanged (already `session`) |

The default id slug **drops the word entirely** rather than swapping `work`->`session`:
the containing dir already says `sessions/`, so repeating the noun in the id is
redundant, and a noun-free id (`<date>-<rand>`) can never collide with a future noun.
A custom `--slug foo` still yields `2026-06-18-foo-ab12`.

Key enabling fact: the registry stores each session's `path` and per-mount `branch` /
`worktree_path` **as data** in the record. Resolution reads those fields; only
*creation* reads the layout constants. So new sessions get the clean layout by
changing constants alone, and existing sessions keep resolving from their stored
paths until migrated or finished.

### Migration (lazy, idempotent, safe)

Grounded in dogfood state seen during design: an existing umbrella can contain an
**active** dirty legacy session, tracked finished/discarded records, and **orphaned**
on-disk `work/<id>` dirs with no registry record (including custom slugs). Migration
must handle each class without assuming a clean laboratory layout.

- **Trigger:** run on `my session` / `my work` subcommands and `my doctor --fix`.
  Plain `my doctor` remains diagnostic: it reports legacy sessions that would be
  migrated, plus orphan `work/<id>` dirs. If migration is wired through a broader
  maintenance hook, that hook must explicitly skip `my ai`, `my onboarding`, plain
  `my doctor`, and any path that may exec a harness. **Never** migrate during `my ai`
  launch/exec, so a directory is not moved out from under a harness we are about to
  start.
- **Legacy current-directory compatibility:** because `my ai` does not migrate,
  `currentActiveSession` / `activeSessionForPath` must not rely only on the new
  `sessions/<id>` constant. It should first match the current directory against
  stored active session paths from the registry, then use both the new
  `sessions/` root and the legacy `work/` root only for the "inside a sessions
  area but no active record matched" error. This preserves plain `my ai <harness>`
  from inside an old, unmigrated `work/<id>` session.
- **Active sessions** (per registry record whose path is under `work/` or branch under
  `my/work/`): for each mount, `git -C <repo> branch -m my/work/<id> my/session/<id>`
  (works while checked out in the linked worktree), then
  `git -C <repo> worktree move <old> <new>` (same-filesystem; preserves uncommitted
  changes and keeps the inode, so a running shell's CWD follows the move). Create the
  target `sessions/<id>/` directory first, move each mount worktree to
  `sessions/<id>/<mount-id>`, then move the loose session files (`SESSION.md`,
  `AGENTS.md`, `CLAUDE.md`, `scratch/`) into `sessions/<id>/`. Rewrite the record's
  `path`, and each mount's `worktree_path` and `branch`; save.
- **Partial-state repair:** migration must be stepwise and idempotent, not one
  all-or-nothing assumption. For each mount, detect the actual branch and worktree
  path before acting: old path + old branch needs both changes; new path + old branch
  needs only branch rename; old path + new branch needs only worktree move; new path +
  new branch is already migrated. If both old and new branches exist, or both old and
  new worktree paths exist, skip that mount with a warning instead of guessing. Save a
  rewritten record only after the session's observed paths and branches are
  consistent; otherwise leave the record pointing at the still-valid observed state
  and report the remediation.
- **Finished / discarded records:** worktrees are already removed; leave records as
  history (their stored paths are historical). No move.
- **Orphan dirs** (on-disk `work/<id>` with no registry record): **not** moved -
  source data (branch, base) is unknown. `my doctor` reports them for manual cleanup.
- **Safety:** idempotent (records already under `sessions/` are skipped). If a move
  fails (locked worktree, cross-device, missing), skip that session, emit a warning
  with manual remediation, and continue - never abort the whole migration. Remove the
  old `work/` dir only when it is empty (no orphans).

## The hint (the core ask)

After a session is created - by `my session start` or `my ai --new-session` - surface
both the collaboration path and the finish path.

Human output of `my session start` (no harness -> create only):

```
started session 2026-06-18-ab12
  path: /path/to/umbrella/sessions/2026-06-18-ab12
  content-a -> my/session/2026-06-18-ab12 (from main)
  join (another harness): my session join 2026-06-18-ab12 <harness>
  finish:                 my session finish 2026-06-18-ab12 --land | --publish | --discard
```

`my session start codex` and `my ai --new-session codex` (create + launch) print the
same block to **stderr** first, then exec:

```
started session 2026-06-18-ab12 (path: .../sessions/2026-06-18-ab12)
  join (another harness): my session join 2026-06-18-ab12 <harness>
  finish:                 my session finish 2026-06-18-ab12 --land|--publish|--discard
launching codex...
```

Stderr is correct for the launch path: stdout is handed to the harness's TTY, and
hints are diagnostics, not data.

## Human vs bot ergonomics

Operator principle: "Humans get short and memorable, bots get wider. All need to be
intuitive and consistent."

- **Humans (short, memorable):** plain-English verbs - `my session start codex`,
  `my session join <id> claude`, `my session finish <id> --land`. No cryptic
  single-letter command aliases (`my s` collides with setup/sync/skills/services and
  is rejected). Verbs read as sentences.
- **Bots (wide, explicit):** JSON on reporting/create-only modes, long flags, and
  copy-paste-ready command strings so an agent never assembles a command. Launching
  verbs stay launch verbs; bots that need data use `--json` / `--print` (no exec):

```json
{
  "id": "2026-06-18-ab12",
  "path": "/path/to/sessions/2026-06-18-ab12",
  "status": "active",
  "mounts": [{"id": "content-a", "branch": "my/session/2026-06-18-ab12", "base_branch": "main"}],
  "launch_command": "my ai --session 2026-06-18-ab12 <harness>",
  "join_command":   "my session join 2026-06-18-ab12 <harness>",
  "finish_command": "my session finish 2026-06-18-ab12 --land|--publish|--discard"
}
```

- **Consistency:** the `my session` verbs and the `my ai` flags name the same
  lifecycle. `--new-session`<->`start`, `--session`<->`join`, `-r/--resume`<->`resume`.

## Implementation map (for the build round)

- `internal/cli/cli.go` - add `case "session": return a.runSession(args[2:])` beside
  `ai` (`cli.go:153`) and `work` (`cli.go:175`); add `my session ...` to `printUsage`;
  keep `my work ...` listed but annotate as deprecated.
- `internal/cli/work.go` - add `runSession` dispatching `start|join|resume|list|
  status|finish`; reuse `runWorkStatus`/`runWorkFinish`; extend `runWorkStart` to
  accept an optional harness + emit the join/finish hint and the JSON fields; add
  group help for both `work` (deprecated) and `session` (fixes the `my work --help`
  error).
- `internal/cli/launch.go` - in `runLaunchWithInitialPrompt`, when
  `launchCreatesNewSession(opts)`, capture the started `worksession.Session` (today
  discarded in `launchTargetDir`, `launch.go:359-378`) and print the hint to stderr
  before `runHarness`. Add `my session join`/`resume`/`start <harness>` launch paths
  that funnel through the existing `--session`/`-r` machinery.
- `internal/worksession/worksession.go` - change layout constants:
  `WorkDirName "work" -> "sessions"`, `BranchPrefix "my/work/" -> "my/session/"`, and
  the default slug from `"work"` to empty (id = `<date>-<rand>`; both slug-default
  sites, ~lines 169 and 204). Keep the Go identifiers/package name (`worksession`,
  `WorkDirName`) - internal, not user-facing. Add a `Migrate(root)` that performs the
  lazy, idempotent, safe migration above.
- Migration wiring - call `worksession.Migrate(root)` from session/work command
  entry and `my doctor --fix`, plus a narrowly scoped post-root maintenance helper
  for non-launch commands if useful. Do **not** put migration in the current
  unconditional `runStartupMaintenance(args)` path unless that hook explicitly
  excludes `my ai`, `my onboarding`, plain `my doctor`, and every harness-exec path.
  Plain `my doctor` reports legacy sessions and orphan `work/<id>` dirs without
  mutating.
- Docs: `site/guide/sessions.md`, `site/guide/cli-reference.md`,
  `skills/my-cli/SKILL.md` (bot-facing guidance), and any AGENTS/quickstart mention -
  teach `my session start codex` / `my session join <id> claude-code` as primary; show
  `my ai --session`/`--new-session` as the bot-stable launch shortcuts; mark
  `my work ...` deprecated; document the layout (`sessions/<id>`, `my/session/<id>`) and
  the one-time migration.

## Testing plan

- `internal/cli/work_test.go` - `start` prints id/path/join/finish hints (human +
  `--json` fields); `start <harness>` execs with hint on stderr (assert via the
  `execHarness`/`lookPath` seams); `start --json <harness>` and `start --print
  <harness>` create without execing; group help renders; `my work` help shows
  deprecated.
- `internal/cli/launch_test.go` - `my ai --new-session` prints the hint to stderr
  before exec; `--session`/`-r` unchanged; `my session join <id> <harness>` resolves
  to the same target dir as `my ai --session <id> <harness>`.
- `internal/worksession/worksession_test.go` - new sessions use `sessions/<id>` +
  `my/session/<id>` + noun-free id; **migration tests**: an active session with a
  dirty file migrates (worktree moved, branch renamed, dirty change preserved, record
  rewritten); finished/discarded records left as history; orphan dirs untouched;
  idempotent on second run; failed-move is skipped with a warning, not fatal.
- `internal/cli/contentroots_test.go` / launch tests - current-directory detection
  still recognizes an unmigrated active session whose registry path is under
  `work/<id>` when running plain `my ai <harness>`; migration hooks are not invoked
  by `my ai` or other harness-exec paths.
- `internal/cli/doctor_test.go` - plain `my doctor` reports legacy session layout
  and orphan `work/<id>` dirs without moving anything; `my doctor --fix` performs
  the same migration as `my session status`.
- `my work ...` parity tests stay green (deprecated alias must not regress behavior).
- `go test ./...`, `go vet ./...`, `git diff --check`; `cd site && npm run build` if
  nav/frontmatter changes.

## Out of scope / YAGNI

- Tracking harness conversation/threads as a first-class object (the "two distinct
  concepts" option). Deferred; "session" stays the work-session noun. Revisit the
  `resume` overload then.
- Auto-relocating **orphan** (untracked) `work/<id>` dirs - reported by `my doctor`
  for manual cleanup, not moved (unknown branch/base).
- Renaming the internal Go identifiers/package (`worksession`, `WorkDirName`) - only
  their *values* change; identifiers stay for a minimal diff.

## Settled implementation answers

- **Full consolidation:** rename the on-disk layout to `sessions/<id>` +
  `my/session/<id>`, drop the noun from the default id (`<date>-<rand>`), and migrate
  existing tracked sessions. "work" is freed in code, on disk, and (after the
  deprecation window) in the command surface.
- **Migration is in scope** and runs lazily/idempotently outside the launch path;
  same-filesystem `git worktree move` preserves uncommitted work; unsafe moves are
  skipped with a warning, never fatal; orphans go to `my doctor`.
- `my session start` with no harness is always create-only (no hidden default-harness
  launch). Use `my session start <harness>` or `my ai --new-session <harness>` to
  launch.
- `--json` and `--print` never exec; they create and return data / a shell command,
  keeping bot and shell output deterministic.
