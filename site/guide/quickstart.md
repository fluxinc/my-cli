# Quickstart

## Prerequisites

Start with a POSIX shell and standard utilities (including `tty`), `curl`,
gzip-capable `tar` (`gzip` on GNU systems), and a SHA256 verifier (`sha256sum`
or `shasum`). Install and authenticate a supported agent harness first, such as
Codex, Claude Code, Grok, or Cursor. It performs and explains the remaining setup.

Git is needed for manifest/workspace operations. Authenticated GitHub CLI (`gh`)
is additionally required for GitHub-backed repositories and `my publish`.
Neither Git nor `gh` is needed to start the onboarding assistant. The guide
checks them before private Git work and helps resolve missing installations.
Authentication, privilege prompts, policy acceptance, and material choices
remain human steps.

On Windows, use [WSL](./windows-wsl): install and run the Linux `my` binary,
Git, and your harness inside the same WSL distribution. Keep the umbrella
under `/home/<user>`, not `/mnt/c`, and do not mix Windows Git metadata with
WSL Git.

## Install My AI

Install the latest release:

```sh
curl -fsSL https://my-cli.com/install.sh | sh
```

No Go or Node installation is required. On a fresh interactive install, the
script persists `~/.local/bin` in the current shell's profile, installs the
bundled agent skill, and starts `my onboarding`. The current shell may need to
load that profile once after onboarding returns; the installer prints the exact
`source` command when needed.

For an automation-only install, disable the agent handoff explicitly:

```sh
curl -fsSL https://my-cli.com/install.sh | sh -s -- --no-onboarding
```

Verify the binary:

```sh
my version
my doctor
```

`my doctor` reports machine prerequisites (`git`, `gh` login, the `my`
binary directory on PATH, installed harnesses), manifest validity, generated
guidance/MCP drift, legacy global org-skill drift,
service materialization health, local Git freshness, and the last
`.my-cli/last-sync.json` audit when an umbrella is present. Add `--no-fetch` for
an offline freshness check, or `--fix` to fast-forward clean stale
manifest/content checkouts and reconcile derived guidance, MCP config, and
legacy global org-skill cleanup.

## Choose a manifest path

### Produce a new organization manifest

```sh
my init acme --name "Acme"
my setup --manifest acme
```

`my init` creates two local repositories and registers the organization:

- a **private manifest repo** — the control plane (manifest, catalog, skills,
  agent guidance), kept at the registry path out of the workspace; admins
  change it through `my admin` commands;
- a **content repo** at `~/acme/workspace` — the actual workspace content:
  meetings, support records, fleet records, decisions, policy, people.

Everything works offline immediately and reports `local-only` until you
publish. Move into the generated umbrella and prove it is operational:

```sh
cd "$(my root)"
my doctor
my ai codex
```

When you are ready to share, preview the outward action and review it before
running the real publish:

```sh
my publish --manifest acme --print
my publish --manifest acme
```

The real publish creates the two private GitHub repos (`acme-manifest` and
`acme-workspace`), points the manifest's mount at the published content repo,
pushes both, and prints the join command for teammates. Because the manifest
and the workspace are separate repos, you can restrict manifest pushes to
admins while the whole team pushes content.

### Install an existing organization manifest

If your team already has a manifest repo, its onboarding handout should be one
command. This installs My AI, registers the manifest, and starts the agent:

```sh
curl -fsSL https://my-cli.com/install.sh | sh -s -- --manifest acme <git-url>
```

The same join, step by step, works without the installer: `my manifests add
acme <git-url>` then `my setup --manifest acme` — setup clones a
registered-but-unsynced manifest itself, and `my manifests sync` is only
needed to pull later updates.

The JOIN bootstrap is resumable. If the manifest is registered but not synced,
the agent begins with Git and GitHub CLI availability, `gh auth status`/login,
and `my manifests sync acme`, then continues through role selection, setup,
required organization tools, `my doctor`, and first launch. For GitHub HTTPS,
My AI supplies `gh auth git-credential` to the Git child process only for that
invocation; it does not rewrite global Git configuration. SSH URLs continue to
use the user's normal SSH keys.

## Optional guided onboarding

```sh
my onboarding
```

Without an agent, or to override choices directly:

```sh
my onboarding --no-agent
my setup --manifest acme
my setup --role operator
my setup --interactive
```

`my onboarding` launches guided onboarding in a harness when run interactively.
Use `my onboarding --no-agent` for the deterministic walkthrough: it explains
the model, offers to run `my setup --interactive`, and if no manifest is
registered yet, prints the `my manifests add <name> <git-url>` next step while
leaving the tour unmarked. Run `my setup` after the deterministic walkthrough
or whenever you want to converge the workspace without an onboarding
conversation.
Plain `my setup` stays deterministic and scriptable. With one registered
manifest, every command defaults to it. Setup is safe to re-run: it validates
the manifest, installs the bundled self-skill, creates the umbrella, writes
generated guidance, and syncs default content. Organization skills are composed
by `my ai` into the launch root. Opted-in catalog repo clones live under
`repos/<id>` in the umbrella.

## Operate from the umbrella

```sh
my ai codex
```

That's it: `my ai` verifies generated guidance and launches the harness from
the base umbrella. For isolated content work, use `my ai --new-session codex`
or create one with `my session start codex`; use
`my session join <id> claude-code` to add another harness to the same session.
When the work is done, `my session finish --land | --publish | --discard` is
how it leaves the session. Pass `--session <id>` or `-r <id>` to launch into a
known active session, or `-r codex` to select the only active session or pick
one in an interactive terminal. Use
`--no-session` to ignore a current session for base inspection or admin,
`--print` to see the command without executing it, or `--setup` to reconcile
the umbrella first. Use `my session status` or `my session list` to inspect active
sessions; `my doctor` also reports session health.

Harnesses sometimes create raw Git worktrees outside My AI's session registry.
Before reporting repository work complete, inspect them:

```sh
my session leftovers
my session close-worktree <exact-listed-path> --yes
```

The close command only accepts an inventoried, clean, unlocked, named-branch
leftover. It uses ordinary non-force Git removal and preserves the branch.
Dirty, detached, missing, locked, base, and active-session worktrees are
reported with instructions and left untouched.

At startup, `my root`, `my ai`, and `my setup` print stderr-only `notice`
lines for checkouts auto-refresh cannot converge (dirty, ahead, behind, or
diverged), each naming the repository and the command to run, such as
`my sync` or `my doctor`. Stdout stays clean, so `cd "$(my root)"` is safe.

## Update my

Use the self-update command:

```sh
my update --check
my update
```

`my update` downloads the latest GitHub release, verifies the checksum, and
replaces the local binary. It refuses package-managed or non-writable installs
and prints the right follow-up command.

Re-running the installer still works as a fallback:

```sh
curl -fsSL https://my-cli.com/install.sh | sh
```

The installer also refreshes the bundled `my-cli` self-skill in existing harnesses
so agents keep current CLI guidance.
