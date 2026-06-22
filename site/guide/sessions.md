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

- record commands (`my meetings add`, `my support add`, `my fleet add`)
  write to the session's mount worktrees, and
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

## Guard rails

`my sync` holds outbound publish of any mount that has a dirty or unlanded
active session, naming the session and the finish command — half-done session
work cannot leak into the published workspace. `my session status` shows what is
active; `my doctor` reports session health (active state, missing worktrees,
archived counts) alongside workspace diagnostics.

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
