# Bundled Skills

This directory holds the **`my` CLI self-skill** (`my/SKILL.md`): a generic,
public-safe skill that teaches a harness how to operate inside a My AI workspace.
It is embedded in the `my` binary, installed into supported harnesses during
installation, and kept aligned with the running CLI version. It carries no
organization-specific content.

Organization-specific agent skills do **not** live here. They belong in an
organization's manifest repository. Managed launches receive them from `my ai`
as launch-scoped skills where the harness supports that; `my skills install`
and `my skills sync` are explicit manual user-global materialization commands.
