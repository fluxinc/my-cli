# Bundled Skills

This directory holds the **`flux` CLI self-skill** (`flux/SKILL.md`): a generic,
public-safe skill that teaches a harness how to operate inside a Flux workspace.
It is embedded in the `flux` binary, installed into supported harnesses during
installation, and kept aligned with the running CLI version. It carries no
organization-specific content.

Organization-specific agent skills do **not** live here. They belong in an
organization's manifest repository and are installed through
`flux skills install --manifest <name>` or `flux onboard`.
