# Skills

Skill commands answer what capabilities an agent can use locally.

## Two sources of skills

Skills reach a harness from two places, and the difference is the public/private
boundary:

- **The `flux` self-skill** ships *inside the CLI binary*. It is
  organization-neutral — it only teaches harnesses how to drive `flux` itself —
  so it is safe to install anywhere and carries no company content. The binary
  owns its lifecycle (`install.sh`, `flux onboard`, and a quiet refresh on human
  CLI runs, including after `flux update`); manage it explicitly with
  `flux skills self ...`.
- **Organization skills** are declared in an org's *manifest repo* and land only
  once you `flux manifest add` / `flux manifest sync` that manifest. Because they
  live in a repo you control — typically private — they can carry
  organization-specific guidance. `flux skills install` / `flux skills sync`
  materialize them into harness skill directories.

Nothing organization-specific is baked into the public CLI: the self-skill stays
generic, and everything particular to your organization lives in a manifest you
own.

## Flux self-skill

```sh
flux skills self status --all
flux skills self install --all
flux skills self uninstall codex
```

The `flux` self-skill is bundled with the CLI and teaches harnesses how to use
Flux itself. `install.sh` installs it into existing harnesses, `flux onboard`
refreshes it with the selected harnesses, and normal human CLI runs quietly
align already-installed file-based copies with the running binary.

## Inspect declared skills

```sh
flux skills list --manifest acme
flux skills show acme:handbook --manifest acme
flux skills status --manifest acme
```

Use `--json` when an agent needs machine-readable output.

## Install and reconcile

```sh
flux skills install --all --manifest acme
flux skills install codex --skill acme:handbook --manifest acme
flux skills sync --all --manifest acme
flux skills purge --all --manifest acme
```

Manifest `install`, `uninstall`, `sync`, and `purge` operate on local harness
materializations. They do not edit the manifest.

`sync` installs or updates declared skills and prunes stale Flux-managed
targets by default. Pass `--no-prune` to only install or update.

## Provenance

`flux` records what it installed. It refuses to clobber a directory it did not
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
