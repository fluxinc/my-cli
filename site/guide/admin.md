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

On `add`, `--install-slug SLUG` renames the install directory inside the
manifest, and `--keep-original`/`--remove-original` decides whether the
imported source directory stays or is deleted. On `remove`, `--delete-source`
also deletes the skill's source directory; `--prune-related` drops catalog
`related_skills` references to the removed skill; `--prune-orphans` removes
tools and allowed namespaces left orphaned by the removal.

They refuse dirty checkouts unless `--force` is supplied, never commit or push,
and print follow-up `git status` and `git diff` commands. Removal reports
now-orphaned tools and allowed namespaces by default; add `--prune-orphans` to
remove them in the same write.

After an admin edit, only the maintainer checkout has changed: review with
`git status` and `git diff`, then commit and push yourself. Teammates pick the
change up via `our manifests sync`, which reconciles generated guidance and
manifest skills automatically.

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

`add` and `edit` also accept skill-install hints — `--skill-install-command`
and repeatable `--skill-install-arg` — for tools that materialize their own
skills; `edit` clears them with `--clear-skill-install` (and install commands
or docs URLs with the matching `--clear-*` flags).

## Admin aliases

Mutating or configuration commands are reachable under admin:

```sh
our admin setup
our admin manifests add acme https://github.com/example/acme-workspace.git
our admin manifests sync acme
our admin manifests validate acme
our admin mounts add product:sample-product
our admin meetings add sampleco-followup --workspace handbook
our admin support add routing-timeout --workspace handbook
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
our support list
our support search timeout
our support get 2026-06-10-routing-timeout
```

If a read command is invoked through `our admin`, the CLI points back to the
top-level form.
