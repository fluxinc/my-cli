# Admin

`our admin` groups commands that mutate shared or workspace configuration.
Operational reads stay top-level.

## Manifest skill authoring

Admin skill commands write a maintainer checkout, not the synced manifest
cache:

```sh
our admin skills add ./my-skill \
  --id acme:my-skill \
  --manifest-dir ~/src/acme-workspace

our admin skills remove acme:my-skill \
  --manifest-dir ~/src/acme-workspace
```

They refuse dirty checkouts unless `--force` is supplied, never commit or push,
and print follow-up `git status` and `git diff` commands. Removal reports
now-orphaned tools and allowed namespaces by default; add `--prune-orphans` to
remove them in the same write.

## Tool hints

Manifest tool declarations are admin-owned hints, not installers that Our AI runs
silently:

```sh
our admin tools add gnit \
  --manifest-dir ~/src/acme-workspace \
  --mode required \
  --purpose "Multi-repo workspace publishing" \
  --install-command "curl -fsSL https://raw.githubusercontent.com/mostlydev/gnit/master/install.sh | sh" \
  --docs-url https://github.com/mostlydev/gnit

our admin tools edit gnit \
  --manifest-dir ~/src/acme-workspace \
  --purpose "Gnit workspace publishing"

our admin tools remove gnit \
  --manifest-dir ~/src/acme-workspace
```

Tool removal refuses declarations still referenced by manifest skills.

## Admin aliases

Mutating or configuration commands are reachable under admin:

```sh
our admin setup --manifest acme
our admin manifests add acme https://github.com/example/acme-workspace.git
our admin manifests sync acme
our admin manifests validate acme
our admin mounts add product:sample-product --manifest acme
our admin meetings add sampleco-followup --manifest acme --workspace handbook
our admin customers add sampleco.example.com --manifest-dir ~/src/acme-workspace
our admin tools add qmd --manifest-dir ~/src/acme-workspace --mode optional --purpose "Markdown search"
```

The top-level forms remain quiet compatibility aliases in this release.

## Operational reads stay top-level

These commands inspect local or manifest-derived state:

```sh
our skills list
our skills status
our manifests list
our mounts list
our tools list
our tools info qmd
our meetings list
our meetings search cleanup
our meetings get 2026-05-13-sampleco-followup
```

If a read command is invoked through `our admin`, the CLI points back to the
top-level form.
