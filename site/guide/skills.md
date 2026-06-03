# Skills

Skill commands answer what capabilities an agent can use locally.

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

`install`, `uninstall`, `sync`, and `purge` operate on local harness
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
