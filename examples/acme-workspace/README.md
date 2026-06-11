# Acme Workspace Example

This is a neutral organization workspace fixture for local development and docs.
It demonstrates the same shape a private organization workspace would use:

- `manifest/` is the private control-plane repo shape: `manifest.json`,
  static skills, catalog files, and agent guidance.
- `content/` is the separate handbook content repo shape: meetings, support
  records, decisions, projects, policy, people, and fleet records.

The handbook mount points at a separate content repository URL. For local
experiments, replace that URL with the absolute path to `content/`, the same
shape `our init` creates before `our publish` rewrites the mount to a hosted
private repository.

Useful checks:

```sh
our manifests validate examples/acme-workspace/manifest
our skills list --source examples/acme-workspace/manifest/skills
```
