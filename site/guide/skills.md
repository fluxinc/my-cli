# Skills

Skill commands answer what capabilities an agent can use locally.

## Two sources of skills

Skills reach a harness from two places, and the difference is the public/private
boundary:

- **The `our` self-skill** ships *inside the CLI binary*. It is
  organization-neutral — it only teaches harnesses how to drive `our` itself —
  so it is safe to install anywhere and carries no company content. The binary
  owns its lifecycle (`install.sh`, `our setup`, `our ai`, and a quiet refresh
  on human CLI runs, including after `our update`); manage it explicitly with
  `our skills self ...`.
- **Organization skills** are declared in an org's *manifest repo* and land only
  once you `our manifests add` / `our manifests sync` that manifest. Because they
  live in a repo you control — typically private — they can carry
  organization-specific guidance. `our skills install` / `our skills sync`
  materialize them into harness skill directories.

Nothing organization-specific is baked into the public CLI: the self-skill stays
generic, and everything particular to your organization lives in a manifest you
own.

## Our AI self-skill

```sh
our skills self status --all
our skills self install --all
our skills self uninstall codex
```

The `our` self-skill is bundled with the CLI and teaches harnesses how to use
Our AI itself. `install.sh` installs it into existing harnesses, `our setup`
refreshes it with the selected harnesses, `our ai` ensures it exists for the
selected filesystem harness before launch, and normal human CLI runs quietly
align already-installed file-based copies with the running binary.

## Inspect declared skills

```sh
our skills list --manifest acme
our skills show acme:handbook --manifest acme
our skills status --manifest acme
```

Use `--json` when an agent needs machine-readable output.

## Install and reconcile

```sh
our skills install --all --manifest acme
our skills install codex --skill acme:handbook --manifest acme
our skills sync --all --manifest acme
our skills purge --all --manifest acme
```

Manifest `install`, `uninstall`, `sync`, and `purge` operate on local harness
materializations. They do not edit the manifest.

`sync` installs or updates declared skills and prunes stale Our AI-managed
manifest targets by default. It leaves the bundled `our` self-skill to
`our skills self ...`. Only the canonical id `our:self` is protected from
pruning; a manifest-declared skill that happens to be named `our` is ordinary
and removable. Pass `--no-prune` to only install or update. When the
manifest itself changes, `our manifests sync` refreshes generated guidance and
manifest skills for an existing matching umbrella unless `--no-derived` is
passed.

## Provenance

`our` records what it installed. It refuses to clobber a directory it did not
place unless the command explicitly allows replacement.

By default, filesystem harnesses receive symlinks. Use `--copy` to vendor a
copy into the harness skill directory.

## Supported harnesses

| Harness | Install path |
|---|---|
| Claude Code | `~/.claude/skills/<skill>` |
| Codex | `~/.codex/skills/<skill>` |
| OpenCode | `~/.config/opencode/skills/<skill>` |
| Gemini | via `gemini skills link` |
