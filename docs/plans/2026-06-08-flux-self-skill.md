# Flux Self-Skill Installation

Status: implemented plan.

## Goal

Flux should install and keep current a generic skill that teaches agent
harnesses how to use the `flux` CLI itself. This is separate from
organization-specific skills declared by manifests.

## Decisions

- Ship `skills/flux/SKILL.md` in the public repository. The content is
  organization-neutral and safe to embed in the release binary.
- Materialize the embedded skill tree under `~/.local/share/flux/skills`, the
  existing Flux-managed source root.
- Add `flux skills self install|uninstall|status` for explicit lifecycle
  management of the bundled self-skill.
- Run `flux skills self install --all` from `install.sh` after the binary is
  installed. This is best-effort and non-fatal.
- Include self-skill installation during `flux onboard` so selected harnesses
  get both the organization skills and the Flux CLI skill.
- Quietly refresh already-installed filesystem copies/symlinks on normal human
  CLI runs. Missing harnesses, missing installs, tests, and harness-invoked
  runs are skipped. Gemini remains registry-managed through explicit install.

## Non-Goals

- Do not move organization skills into the public Flux repository.
- Do not overwrite unmanaged `~/.claude`, `~/.codex`, or OpenCode skill
  directories unless the operator passes `--force`.
- Do not implement GitHub PR creation in this slice; `flux sync --publish pr`
  still reports that PR mode is pending.
