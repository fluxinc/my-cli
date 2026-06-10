# Acme Workspace Example

This is a neutral organization workspace fixture for local development and docs.
It demonstrates the same shape a private organization workspace would use:

- `manifest.json` declares the org, skills, mounts, catalog, and tools.
- `skills/` contains static agent skills.
- `catalog/products.json` lists opt-in products.
- `catalog/customers.json` lists canonical customer IDs and aliases.
- `meetings/`, `support/`, `decisions/`, `projects/`, `policy/`, and `people/`
  are handbook content directories that can be mounted into an umbrella.

The handbook mount uses `git_url: "."`, which means "clone from the URL or
local path this manifest was registered with." That keeps the fixture usable as
a local Git repo and as a published private template without editing
`manifest.json`.

Useful checks:

```sh
our manifests validate examples/acme-workspace
our skills list --source examples/acme-workspace/skills
```
