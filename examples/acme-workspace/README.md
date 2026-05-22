# Acme Workspace Example

This is a neutral organization workspace fixture for local development and docs.
It demonstrates the same shape a private organization workspace would use:

- `manifest.json` declares the org, skills, mounts, catalog, and tools.
- `skills/` contains static agent skills.
- `catalog/products.json` lists opt-in products.
- `catalog/customers.json` lists canonical customer IDs and aliases.
- `meetings/`, `decisions/`, `projects/`, `policy/`, and `people/` are handbook
  content directories that can be mounted into an umbrella.

The `git_url` values intentionally use placeholder `github.com/example/...`
URLs. For a real local onboarding smoke, copy this directory into a temporary
Git repo and update `manifest.json` to point its `handbook` mount at that repo.

Useful checks:

```sh
flux manifest validate examples/acme-workspace
flux skills list --source examples/acme-workspace/skills
```
