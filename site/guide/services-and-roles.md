# Services and Roles

Services and roles are manifest vocabulary for the organization's remote
surfaces and operating profiles. They are declared in `manifest.json`,
edited through `my admin`, inspected with read-only verbs, and materialized
locally as `.mcp.json` — never the other way around.

```sh
my admin services add|edit|remove ...
my admin roles add|edit|remove ...
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
at a checked-in descriptor file instead. An `http` service's `connection` is
only its API base, `{ "type": "http", "url": "https://…" }`; a fleet data
binding uses it to read and write records live (see
[Records](./records.md#service-backed-fleet)). `broker://` is not supported for
service-backed records.

Create or update service declarations from a maintainer checkout:

```sh
my admin services add docs-search \
  --manifest-dir ~/src/acme-manifest \
  --kind mcp \
  --purpose "Search the handbook" \
  --auth-ref env://ACME_DOCS_TOKEN \
  --connection-type stdio \
  --connection-command acme-docs-mcp \
  --connection-arg --stdio \
  --connection-env ACME_DOCS_TOKEN='${ACME_DOCS_TOKEN}'
```

Inline connection env values must be exact `${VAR}` placeholders. Header values
may include protocol syntax, but must include a `${VAR}` placeholder; literal
credentials are rejected before the manifest is saved.

## Roles

A role is a named local loadout that selects mounts, skills, tools, services,
and optional guidance fragments. Roles do not grant authority; the backing Git
host or service enforces access. Select one locally:

```sh
my admin roles add operator \
  --manifest-dir ~/src/acme-manifest \
  --purpose "Default operator role" \
  --service docs-search

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

`my doctor` checks every declared MCP service: missing connection data is an
error (an `http` service bound to fleet without `connection.url` is reported
when a fleet command first uses it); an unset `env://` variable, an `op://` reference without the `op` CLI,
or a URL-only description each get a warning naming the service and the fix.
Skills can declare `requires: ["service:<id>"]`; `my skills show` surfaces
those requirements.

## Personal API access

For an HTTP record service, the manifest can declare `auth_setup_url`, the web
page where members create personal API keys. During onboarding, open that page,
create your key, and save it in a private password-manager item. Keep the
organization's shared credential unchanged.

```sh
my services auth set registry --ref op://Private/Registry/credential
my services auth status registry
my services auth unset registry
```

`set` resolves the reference and verifies it with `GET <connection.url>/me`
before saving. The service must return `issuer: {name, email}` and
`token: {name, scopes, expires_at}`; expiry is an RFC3339 timestamp. Rejected or
expired credentials leave the previous configuration intact.

Only the `op://` or `env://` reference and verified expiry are saved locally,
under `~/.config/my-cli/service-auth.json` with mode 0600. The secret stays in
your password manager or environment. Overrides apply only to that manifest,
service and exact API URL. `unset` returns to the manifest's `auth_ref`.
`my doctor` checks the effective reference and warns before the verified expiry,
without resolving secrets or making network requests. `status` checks live
access whenever you need to verify or renew it.

Admins can publish the setup link with:

```sh
my admin services edit registry --manifest-dir /path/to/manifest \
  --auth-setup-url https://registry.example.com/settings/keys
```

## Conversation comments

A record service supporting `POST records/:id/comments` can accept a comment
from the CLI, including mention text supported by that service:

```sh
my fleet comment registry:box:123 --body-file comment.txt \
  --idempotency-key incident-2026-10-20-followup-1
```

Use a stable submission key when retrying. The command resolves the reference
uniquely, reads current revisions, and sends the comment with those revisions.
A stale write is refused; ambiguous identifiers never post. `--body-file -`
reads stdin. Bodies are plain text of 1–2,000 characters. The service owns
mention parsing and notifications. This command requires a service-backed
record and never changes local Markdown.
