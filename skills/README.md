# Bundled Skills

This directory holds the **`our` CLI self-skill** (`our/SKILL.md`): a generic,
public-safe skill that teaches a harness how to operate inside an Our AI workspace.
It is embedded in the `our` binary, installed into supported harnesses during
installation, and kept aligned with the running CLI version. It carries no
organization-specific content.

Organization-specific agent skills do **not** live here. They belong in an
organization's manifest repository and are installed through
`our skills install --manifest <name>` or `our setup`.
