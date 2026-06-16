# Skills

Skill commands answer what capabilities an agent can use locally.

## Two sources of skills

Skills reach a harness from two places, and the difference is the public/private
boundary:

- **The `my` self-skill** ships *inside the CLI binary*. It is
  organization-neutral — it only teaches harnesses how to drive `my` itself —
  so it is safe to install anywhere and carries no company content. The binary
  owns its lifecycle (`install.sh`, `my setup`, `my ai`, and a quiet refresh
  on human CLI runs, including after `my update`); manage it explicitly with
  `my skills self ...`.
- **Organization skills** are declared in an org's *manifest repo* and land only
  once you `my manifests add` / `my manifests sync` that manifest. Because they
  live in a repo you control — typically private — they can carry
  organization-specific guidance. `my ai` composes them into the launch root's
  `.agents/skills` tree for harnesses with a project-local skill seam, with
  harness mirrors where needed. OpenCode is currently compatibility-global:
  present or explicit OpenCode setup/launch keeps org skills in
  `~/.config/opencode/skills`, and `my ai opencode --skills/--profile` is
  rejected until a project-local seam is proven. `my skills install` / `my
  skills sync` remain explicit manual commands for user-global materializations.

Nothing organization-specific is baked into the public CLI: the self-skill stays
generic, and everything particular to your organization lives in a manifest you
own.

## Launch-scoped skill selection

`my ai` composes the organization skill loadout for a launch and materializes it
into the launch root (`.agents/skills/`, plus a `.claude/skills/` mirror for
Claude Code). Choose the loadout with mutually exclusive selectors:

```sh
my ai                                       # default loadout for this launch target
my ai --skills all                          # every declared organization skill
my ai --skills none                         # no organization skills (self-skill only)
my ai --skills acme:handbook,acme:support   # an explicit set of skill ids
my ai --profile support                     # a named loadout from the manifest `profiles` list
```

A `profile` is a named skill loadout declared in the manifest's `profiles` list,
distinct from a role. With no selector, `my ai` uses the selected role's skills
for a base umbrella launch, includes workspace-satisfied skills for session
launches, uses all org skills for an unscoped umbrella, and intentionally uses no
org skills for repo launches. These selectors compose for harnesses with a
project-local skill seam (Claude Code, Codex, Antigravity). OpenCode is
compatibility-global: it keeps organization skills in
`~/.config/opencode/skills` and rejects `--skills`/`--profile` until a
launch-root seam is proven. The `my skills` commands below manage user-global
materializations manually and are no longer the automatic setup path.

## My AI self-skill

```sh
my skills self status --all
my skills self install --all
my skills self uninstall codex
```

The `my` self-skill is bundled with the CLI and teaches harnesses how to use
My AI itself. `install.sh` installs it into existing harnesses, `my setup`
refreshes it with the selected harnesses, `my ai` ensures it exists for the
selected filesystem harness before launch, and normal human CLI runs quietly
align already-installed file-based copies with the running binary.

## Inspect declared skills

```sh
my skills list
my skills show acme:handbook
my skills status
```

Use `--json` when an agent needs machine-readable output. `my skills show`
also surfaces manifest `requires` entries such as `service:<id>`, which name
services the skill expects the workspace to provide.

## Install and reconcile

```sh
my skills install --all
my skills install codex --skill acme:handbook
my skills sync --all
my skills purge --all
```

Manifest `install`, `uninstall`, `sync`, and `purge` operate on explicit local
harness materializations. They do not edit the manifest and are no longer the
automatic setup path for organization skills.

`sync` installs or updates declared skills and prunes stale My AI-managed
manifest targets by default. It leaves the bundled `my` self-skill to
`my skills self ...`. Only the canonical id `my:self` is protected from
pruning; a manifest-declared skill that happens to be named `my` is ordinary
and removable. Pass `--no-prune` to only install or update. When the manifest
itself changes, `my manifests sync` refreshes generated guidance, umbrella
MCP config, and launch-scoped skill reconciliation notices for an existing
matching umbrella unless `--no-derived` is passed.

## Provenance

`my` records what it installed. It refuses to clobber a directory it did not
place unless the command explicitly allows replacement.

By default, filesystem harnesses receive symlinks. Use `--copy` to vendor a
copy into the harness skill directory.

## Supported harnesses

| Harness | Install path |
|---|---|
| Claude Code | `~/.claude/skills/<skill>` |
| Codex | `~/.codex/skills/<skill>` |
| OpenCode | `~/.config/opencode/skills/<skill>` |
| Antigravity | `~/.agents/skills/<skill>` |

Managed launches use those paths differently from manual skill commands: Claude
Code receives a launch-root `.claude/skills` mirror, Codex and Antigravity read
launch-root `.agents/skills`, and OpenCode stays global as a compatibility
exception.
