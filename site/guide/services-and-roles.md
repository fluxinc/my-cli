# Services and Roles

Services and roles are manifest vocabulary for the organization's remote
surfaces and operating profiles. They are declared in `manifest.json`,
inspected with read-only verbs, and materialized locally as `.mcp.json` —
never the other way around.

```sh
my services list [--manifest NAME] [--json]
my services get <id> [--manifest NAME] [--json]
my roles list [--manifest NAME] [--json]
my roles get <id> [--manifest NAME] [--json]
my setup --role <id>
my compile --role <id> [--manifest NAME] [--home DIR]
```

## Services

A service describes one remote surface — an HTTP API or an MCP server — with
reference-first auth. Secret material is never stored in the manifest; only
references:

```json
{
  "services": [
    {
      "id": "docs-search",
      "kind": "mcp",
      "purpose": "Search the handbook",
      "auth_ref": "env://ACME_DOCS_TOKEN",
      "connection": {
        "type": "stdio",
        "command": "acme-docs-mcp",
        "args": ["--stdio"],
        "env": { "ACME_DOCS_TOKEN": "${ACME_DOCS_TOKEN}" }
      }
    }
  ]
}
```

`auth_ref` accepts `env://VAR`, `op://vault/item` (1Password), `broker://`
(reserved for the gated-broker runtime), or `none`. The optional inline
`connection` uses MCP `server.json` field names; a `describe_ref` can point
at a checked-in descriptor file instead.

## Roles

A role is a named local loadout that selects mounts, skills, tools, services,
and optional guidance fragments. Roles do not grant authority; the backing Git
host or service enforces access. Select one locally:

```sh
my setup --role operator
```

The choice persists in `.my-cli/state.json` and affects two derived outputs:
role guidance fragments are appended to generated `AGENTS.md`, and the
umbrella-root `.mcp.json` is scoped to MCP services visible to that role.
Role selection never prunes mounts or hides commands.

A role's `skills` also drive the base-umbrella launch-scoped loadout. With no
`--skills`/`--profile` selector, `my ai` uses the selected role's skills for a
base umbrella launch; session launches may also include skills whose workspace
requirements are satisfied, and repo launches intentionally receive no org
skills yet. A `profile` (from the manifest's `profiles` list) is a separate
named skill loadout selected with `my ai --profile <id>`; profiles select
skills only and do not contribute guidance. See
[Skills](/guide/skills#launch-scoped-skill-selection).

`my compile --role <id>` uses the same role vocabulary for contained runners:
it prints a deterministic manifest-to-Clawdapus launch projection JSON artifact
and writes nothing. It does not launch containers, call services, resolve
credentials, or fetch `describe_ref` targets. A manifest with roles requires
`--role`; a manifest with no roles compiles an unscoped projection.

## MCP materialization

`my setup` and the derived reconcile write an umbrella-root `.mcp.json` for
MCP services that have *local* connection data — an inline `connection` or a
checked-in descriptor. Nothing is fetched from the network, and env
references stay references (`${VAR}` is resolved by the harness at runtime,
not baked in). A service whose only description is a remote URL is valid but
not materializable offline; `my doctor` calls that out.

`my` keeps a sidecar copy of what it last generated, so it can tell its own
`.mcp.json` from a hand-written one — it refuses to overwrite a file it did
not produce unless forced.

## Doctor checks

`my doctor` checks every declared service: missing connection data is an
error; an unset `env://` variable, an `op://` reference without the `op` CLI,
or a URL-only description each get a warning naming the service and the fix.
Skills can declare `requires: ["service:<id>"]`; `my skills show` surfaces
those requirements.
