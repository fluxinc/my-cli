# Work Sessions

A session is an isolated unit of work under `<umbrella>/work/<id>`: a git
worktree of each writable content mount on a fresh `my/work/<id>` branch,
session-local `scratch/`, a `SESSION.md` summary, and generated session
guidance. Sessions exist so concurrent agents — or one risky multi-file
edit — cannot trample the base workspace or each other.

```sh
my work start [--slug SLUG]
my work status [--all]
my work list [--all]
my work resume [session-id]
my work finish [session-id] --land|--publish|--discard [--message TEXT]
```

## When to use one

The base umbrella plus content verbs plus `my sync` is the default flow.
Reach for a session when:

- multiple agents work the same workspace concurrently,
- a change spans many files and you want an atomic land-or-discard,
- you are experimenting and may throw the work away.

## Lifecycle

Start one explicitly with `my work start`, or launch a harness straight into
a fresh session with `my ai --new-session <harness>`. Resume with
`my ai --session <id>` or `my work resume`. While your current directory is
inside `work/<id>`:

- record commands (`my meetings add`, `my support add`, `my fleet add`)
  write to the session's mount worktrees, and
- plain `my ai` resumes that session instead of the base umbrella. Use
  `my ai --no-session` to deliberately ignore it for base inspection.

Work leaves a session only through `my work finish`:

- `--land` merges the session branches into the base mounts locally,
- `--publish` lands and publishes through the normal sync policy,
- `--discard` drops the worktrees and branches.

## Guard rails

`my sync` holds outbound publish of any mount that has a dirty or unlanded
active session, naming the session and the finish command — half-done session
work cannot leak into the published workspace. `my work status` shows what is
active; `my doctor` reports session health (active state, missing worktrees,
archived counts) alongside workspace diagnostics.

Sessions are harness-agnostic by design: they are plain git worktrees and
directories, no hooks into any harness's internal state.
