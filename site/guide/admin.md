# Admin

`our admin` groups commands that mutate shared or workspace configuration.
Operational reads stay top-level. The manifest is the control plane in its
own private repository — admin commands are the intended way to change it,
since it never appears inside the day-to-day workspace.

## Manifest skill authoring

Admin skill commands write a maintainer checkout through `--manifest-dir`
(your own clone of the manifest repo, or the registered checkout printed by
`our manifests list`):

```sh
our admin skills add ./my-skill \
  --id acme:my-skill \
  --manifest-dir ~/src/acme-manifest

our admin skills remove acme:my-skill \
  --manifest-dir ~/src/acme-manifest
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
  --manifest-dir ~/src/acme-manifest \
  --mode required \
  --purpose "Multi-repo workspace publishing" \
  --install-command "curl -fsSL https://raw.githubusercontent.com/mostlydev/gnit/master/install.sh | sh" \
  --docs-url https://github.com/mostlydev/gnit

our admin tools edit gnit \
  --manifest-dir ~/src/acme-manifest \
  --purpose "Gnit workspace publishing"

our admin tools remove gnit \
  --manifest-dir ~/src/acme-manifest
```

Tool removal refuses declarations still referenced by manifest skills.

`add` and `edit` also accept skill-install hints — `--skill-install-command`
and repeatable `--skill-install-arg` — for tools that materialize their own
skills; `edit` clears them with `--clear-skill-install` (and install commands
or docs URLs with the matching `--clear-*` flags).

## Services and roles

Manifest `services` and `roles` are shared control-plane configuration. There
are inspection verbs (`our services list|get`, `our roles list|get`) but no
admin writer yet, so edit them in a maintainer checkout, validate, commit, and
push:

```json
{
  "services": [
    {
      "id": "docs-search",
      "kind": "mcp",
      "purpose": "Search workspace docs",
      "auth_ref": "env://ACME_DOCS_TOKEN",
      "connection": {
        "type": "stdio",
        "command": "acme-docs-mcp"
      }
    }
  ],
  "roles": [
    {
      "id": "operator",
      "purpose": "Default operator role",
      "guidance_paths": ["agent-guidance/operator.md"],
      "services": ["docs-search"]
    }
  ]
}
```

`our setup --role operator` stores the selected role locally, appends role
guidance to generated `AGENTS.md`, and materializes umbrella-root `.mcp.json`
for locally described MCP services selected by that role. `our doctor` reports
URL-only descriptors, missing checked-in descriptors, unset environment
variables, and missing optional resolver tools such as `op`.

## Contract rules

The organization contract — short, binding rules rendered into generated
`AGENTS.md` — is edited through the same manifest-admin review flow as tools:

```sh
our admin contract add "Record decisions in the handbook before acting on them." --manifest-dir ~/src/acme-manifest
our admin contract remove 2 --manifest-dir ~/src/acme-manifest
```

`remove` accepts the 1-based index shown by `our contract list` or the exact
rule text. Validation rejects empty, multiline, and duplicate rules. See
[Guidance and Contract](./guidance-and-contract.md).

## Admin aliases

Use `our init` to create a new local manifest repo. Mutating or configuration
commands for an existing source are reachable under admin:

```sh
our admin setup
our init acme --name "Acme"
our admin manifests add acme <git-url>
our admin manifests sync acme
our admin manifests validate acme
our admin meetings add sampleco-followup --workspace handbook
our admin support add routing-timeout --workspace handbook
our admin tools add qmd --manifest-dir ~/src/acme-manifest --mode optional --purpose "Markdown search"
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
our services list
our services get docs-search
our roles list
our roles get operator
our contract list
our meetings list
our meetings search cleanup
our meetings get 2026-05-13-sampleco-followup
our support list
our support search timeout
our support get 2026-06-10-routing-timeout
```

If a read command is invoked through `our admin`, the CLI points back to the
top-level form.
