# Admin

`my admin` groups commands that mutate shared or workspace configuration.
Operational reads stay top-level. The manifest is the control plane in its
own private repository — admin commands are the intended way to change it,
since it never appears inside the day-to-day workspace.

## Manifest skill authoring

Admin skill commands write a maintainer checkout through `--manifest-dir`
(your own clone of the manifest repo, or the registered checkout printed by
`my manifests list`):

```sh
my admin skills add ./my-skill \
  --id acme:my-skill \
  --manifest-dir ~/src/acme-manifest

my admin skills remove acme:my-skill \
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
change up via `my manifests sync`, which reconciles generated guidance and
launch-scoped skill reconciliation notices automatically.

## Tool hints

Manifest tool declarations are admin-owned hints, not installers that My AI runs
silently:

```sh
my admin tools add gnit \
  --manifest-dir ~/src/acme-manifest \
  --mode required \
  --purpose "Multi-repo workspace publishing" \
  --install-command "curl -fsSL https://raw.githubusercontent.com/mostlydev/gnit/master/install.sh | sh" \
  --docs-url https://github.com/mostlydev/gnit

my admin tools edit gnit \
  --manifest-dir ~/src/acme-manifest \
  --purpose "Gnit workspace publishing"

my admin tools remove gnit \
  --manifest-dir ~/src/acme-manifest
```

Tool removal refuses declarations still referenced by manifest skills.

`add` and `edit` also accept skill-install hints — `--skill-install-command`
and repeatable `--skill-install-arg` — for tools that materialize their own
skills; `edit` clears them with `--clear-skill-install` (and install commands
or docs URLs with the matching `--clear-*` flags).

## Services and roles

Manifest `services` and `roles` are shared control-plane configuration. There
are inspection verbs (`my services list|get`, `my roles list|get`) and admin
writers for maintainer checkouts:

```sh
my admin services add docs-search \
  --manifest-dir ~/src/acme-manifest \
  --kind mcp \
  --purpose "Search workspace docs" \
  --auth-ref env://ACME_DOCS_TOKEN \
  --connection-type stdio \
  --connection-command acme-docs-mcp \
  --connection-env ACME_DOCS_TOKEN='${ACME_DOCS_TOKEN}'

my admin roles add operator \
  --manifest-dir ~/src/acme-manifest \
  --purpose "Default operator role" \
  --guidance agent-guidance/operator.md \
  --service docs-search
```

After reviewing the local manifest checkout, publish those control-plane edits
with `my publish --manifest NAME`.

`my admin services edit|remove` and `my admin roles edit|remove` update the
same declarations. Service removal refuses role-selected services unless
`--prune-roles` is supplied. Inline connection env values must be exact
`${VAR}` placeholders, and connection header values must include a `${VAR}`;
literal credentials are rejected before the manifest is saved.

`my setup --role operator` stores the selected role locally, appends role
guidance to generated `AGENTS.md`, and materializes umbrella-root `.mcp.json`
for locally described MCP services selected by that role. `my doctor` reports
URL-only descriptors, missing checked-in descriptors, unset environment
variables, and missing optional resolver tools such as `op`.

## Contract rules

The organization contract — short, binding rules rendered into generated
`AGENTS.md` — is edited through the same manifest-admin review flow as tools:

```sh
my admin contract add "Record decisions in the handbook before acting on them." --manifest-dir ~/src/acme-manifest
my admin contract remove 2 --manifest-dir ~/src/acme-manifest
```

`remove` accepts the 1-based index shown by `my contract list` or the exact
rule text. Validation rejects empty, multiline, and duplicate rules. See
[Guidance and Contract](./guidance-and-contract.md).

## Admin aliases

Use `my init` to create a new local manifest repo. Mutating or configuration
commands for an existing source are reachable under admin:

```sh
my admin setup
my init acme --name "Acme"
my admin manifests add acme <git-url>
my admin manifests sync acme
my admin manifests validate acme
my admin meetings add sampleco-followup --workspace handbook
my admin support add routing-timeout --workspace handbook
my admin tools add qmd --manifest-dir ~/src/acme-manifest --mode optional --purpose "Markdown search"
```

The top-level forms remain quiet compatibility aliases in this release.

## Operational reads stay top-level

These commands inspect local or manifest-derived state:

```sh
my skills list
my skills status
my manifests list
my mounts list
my tools list
my tools info qmd
my services list
my services get docs-search
my roles list
my roles get operator
my contract list
my meetings list
my meetings search cleanup
my meetings get 2026-05-13-sampleco-followup
my support list
my support search timeout
my support get 2026-06-10-routing-timeout
```

If a read command is invoked through `my admin`, the CLI points back to the
top-level form.
