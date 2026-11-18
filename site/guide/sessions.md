# Sessions

A session is an isolated unit of work under `<umbrella>/sessions/<id>`: a git
worktree of each writable content mount on a fresh `my/session/<id>` branch,
session-local `scratch/`, a `SESSION.md` summary, and generated session
guidance with the concrete umbrella, organization, role, session, mount, and
finish/resume commands a launched harness needs at startup. Sessions exist so
concurrent agents — or one risky multi-file edit — cannot trample the base
workspace or each other.

```sh
my session start [--slug SLUG] [harness]
my session join <session-id> <harness>
my session status [--all]
my session list [--all]
my session resume [session-id] [harness]
my session finish [session-id] --land|--publish|--discard [--message TEXT]
my session leftovers [--all] [--json]
my session close-worktree <path> [--yes] [--json]
```

## When to use one

The base umbrella plus content verbs plus `my sync` and explicit
`my sync --push` publishing is the default flow.
Reach for a session when:

- multiple agents work the same workspace concurrently,
- a change spans many files and you want an atomic land-or-discard,
- you are experimenting and may throw the work away.

## Lifecycle

Start one explicitly with `my session start`, or launch a harness straight into
a fresh session with `my ai --new-session <harness>`. Resume work by launching
a harness in the session:

```sh
my session start codex
my session join <session-id> claude-code
my session resume <session-id> codex
my ai -r codex
my ai -r <session-id> codex
```

A work session is a persistent workspace (worktrees plus `scratch/`), not a
harness chat. `my session resume` and `my ai -r` put a harness back into that
workspace but start a fresh conversation; resuming an earlier conversation is
the harness's own feature (for example `claude --resume <chat-id>` or
`codex resume`), and those ids are unrelated to My AI session ids.

Use `join` when adding another harness to the same session. With one active
session, `my session resume codex` or `my ai -r codex` selects it automatically.
With multiple active sessions in an interactive terminal, the resume form
prompts for the session. In scripts or agentic runs, pass the id explicitly;
without a TTY, multiple active sessions produce an error that lists the ids
instead of prompting.

Use `my session resume [session-id]` with no harness only when you want a shell
command such as `cd <path>` for manual navigation or shell evaluation. It does
not change the parent shell by itself.

While your current directory is inside `sessions/<id>`:

- record commands (`my customers add`, `my meetings add`, `my support add`,
  `my fleet add`) write to the session's mount worktrees, and
- plain `my ai` resumes that session instead of the base umbrella. Use
  `my ai --no-session` to deliberately ignore it for base inspection.

`my ai --session <id>` and `my ai -r <id>` rewrite the session guidance before
launch, so older active sessions pick up the current startup contract. The
session guidance also embeds the generated base umbrella guidance, including
manifest contract rules and selected-role guidance.

Work leaves a session only through `my session finish`:

- `--land` merges the session branches into the base mounts locally,
- `--publish` lands and publishes through the normal sync policy,
- `--discard` drops the worktrees and branches.

## Conversation room

When Talking Stick (`tt`) is installed, every launch into a session runs
`tt room ensure <session path>`. That declares the session's own room, or finds
it when it already exists. The launched harness then gets `TT_ROOM` set to that
room, so the session's agents meet there instead of in the umbrella's room. That
holds even after they `cd` into a mount worktree. `my session start` without a
harness ensures the room too, so the commands it prints point at a real room.
People join with the `tt chat --in <room>` line from the launch output, from any
directory. Use `--in` rather than a bare path: an inherited `TT_ROOM` outranks a
path argument, but `--in` outranks `TT_ROOM`.

```text
room	/home/me/acme/sessions/2026-11-17-notes-ab12	created	people join with: tt chat --in /home/me/acme/sessions/2026-11-17-notes-ab12
```

- `my` never joins a room, takes a turn, or moves a harness that is already
  running. An agent elsewhere joins by launching with
  `my session join <id> <harness>`.
- The room is checked again on every launch and stored nowhere. The session's
  `AGENTS.md` explains this but names no room, because any reconcile rewrites
  it. `TT_ROOM` on its own is only Talking Stick's default selector: after a
  fallback it can still name a parent room.
- If `tt` is missing, nothing is printed and nothing changes. If the check
  fails, the launch still goes ahead. `my` prints why, keeps any `TT_ROOM` the
  shell already had, and claims no room.
- `--print` runs the check and puts `TT_ROOM=<room>` on the harness command.
  The printed command is a manual launch, so it carries only that.
- Every launch `my` performs into a session also sets `MYCLI_SESSION_ID` and
  `MYCLI_SESSION_PATH`. A launch outside a session drops any inherited ones.

The room routes conversation for the session, including its mount worktrees.
It is not a write boundary. Landing (`my session finish --land`) and builds in
the base checkouts change shared state, so they still need a turn in the room
that coordinates those checkouts.

## Guard rails

`my sync` holds outbound publish of any mount that has a dirty or unlanded
active session, naming the session and the finish command — half-done session
work cannot leak into the published workspace. `my session status` shows what is
active; `my doctor` reports session health (active state, missing worktrees,
archived counts) alongside workspace diagnostics.

Harnesses can also create raw Git worktrees outside My AI's session registry.
Run `my session leftovers` before stopping work to inventory every worktree on
the umbrella's content mounts and cloned catalog repositories. The report
distinguishes base checkouts, active sessions, finished-session residue,
prunable metadata, and unregistered leftovers; `--all` also inspects the
manifest registry checkout. Manifest results are inspection-only because
intentional governance/admin topic worktrees are not owned by the close verb.

Use `my session finish` for registered sessions. For an unregistered clean
named-branch worktree whose changes are already landed or deliberately kept on
its branch, run `my session close-worktree <path>` and confirm the exact path.
Scripts and non-interactive agents must pass `--yes`. The command preserves the
branch and refuses base, active-session, locked, missing, detached, dirty, or
untracked worktrees. It never passes Git's `--force`, deletes a branch, prunes
metadata, merges work, or performs garbage collection.

`my work ...` remains as a deprecated compatibility alias during the migration
window. New commands, generated guidance, and remediation text use
`my session ...`.

Sessions are harness-agnostic by design: they are plain git worktrees and
directories, no hooks into any harness's internal state.

## Catalog Repos

Work sessions currently include writable content mounts, not catalog code
repos. Launch a harness in a selected repo checkout with:

```sh
my ai --repo <repo-id> codex
```

That launch uses the base `repos/<repo-id>` checkout. Land and publish code
changes with that repository's normal Git or pull-request workflow. Do not
expect `my session finish` to land catalog repo changes until repo-inclusive
sessions are designed.
