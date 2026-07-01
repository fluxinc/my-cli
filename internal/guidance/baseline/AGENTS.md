# My AI Workspace

This directory is a My AI umbrella workspace. Treat it as the local operating
context for one organization, not as a product repository or a monorepo.

Use the `my` CLI before falling back to ad hoc file searches:

- `my products list` shows the organization's products (business catalog
  entries, with their linked repos).
- `my repos list` shows the organization's repositories and their clone
  state; `my repos add <id>` clones one under `repos/<id>`.
- `my customers list` shows canonical customer IDs and aliases; use
  `my customers add <domain|slug>` to create a mounted customer record.
- `my mounts list` shows mounted handbook content and selected repos.
- `my roles list` / `my roles get <id>` show manifest-declared operating
  roles. `my services list` / `my services get <id>` show declared remote
  surfaces such as HTTP APIs and MCP servers. `my contract list` shows the
  organization's binding contract rules.
- `my compile --role <id>` prints the deterministic contained-runner launch
  projection JSON for that role. It does not launch containers, fetch service
  descriptors, or resolve credentials.
- `my meetings list`, `my meetings search <text>`, and
  `my meetings get <id>` query local meeting notes; use `--customer`,
  `--partner`, and `--product` filters when those axes are known.
- `my support list`, `my support search <text>`, and `my support get <id>`
  query anonymized support records; use `my support add <slug>` after
  resolving substantive support problems, recording the customer, any device,
  order, or asset identifiers, and the org members involved (`claimed_by`,
  `observed_by`) in frontmatter so records link later. Leave `approved_by` for
  explicit operator sign-off.
- `my fleet list`, `my fleet search <text>`, and
  `my fleet get <id|identifier>` query the deployed-instance registry; `get`
  resolves any identifier (order, location, serial) and lists related support
  records. Update workflow state with `my fleet set <id> status=<value>` and
  publish each meaningful transition with the suggested `my sync --push --message`
  command.
- Add `--json` when a harness needs structured output.

Fleet work contract:

- Before substantive work on a deployed instance, run
  `my fleet get <id|identifier>` so you start from the registry record and
  see related support history.
- Continue an existing relevant support record when one is listed, or create a
  new dated anonymized record with `my support add` for a distinct incident or
  work session.
- Put the fleet record id and every useful fleet identifier on the support
  record with repeated `--identifier` flags, plus customer, product, and area
  frontmatter when known.
- Treat support records as the incident/work log. Fleet records hold registry
  state; use `my fleet set` only for meaningful state transitions.
- Publish the resulting content with `my sync --push`.

Default layout:

- `.my-cli/` contains workspace identity and local state.
- `handbook/` and other mounts contain scoped organization content.
- `repos/` contains detached clones of catalog repositories.
- `sessions/` contains isolated My AI sessions created by `my session start`
  or `my ai --new-session`.
- `personal/` is durable local-only scratch for the current user.

Operating orientation:

- Launch agent harnesses with `my ai <harness>`. By default it starts from
  the base umbrella, or from the current active session when run inside
  `sessions/<id>`. Use `my ai --new-session <harness>` for isolated content work;
  join another harness with `my session join <id> <harness>`, and finish or
  discard session work with `my session finish --land|--publish|--discard`.
- If the umbrella has a selected role, it is stored in `.my-cli/state.json`.
  Change it with `my setup --role <id>`; this regenerates role-specific
  guidance and umbrella-root `.mcp.json` for MCP services visible to that
  role.
- Inspect active sessions with `my session status` or `my session list`; `my
  doctor` also reports session health.
- Treat this base umbrella as inspection/admin space. Do not draft, edit, or
  create shared workspace content directly in base mounts unless the operator
  explicitly asks for a base edit. When your current directory is inside a
  session, record commands such as `my meetings add`, `my support add`,
  `my fleet add`, and `my customers add` write to that session's mount
  worktree.
- Use `my ai --session <id> <harness>` to resume a known active session, or
  `my ai --no-session <harness>` to ignore a current session for base
  inspection/admin/debug.
- For repository work, use `repos/<id>` under this umbrella. Clone catalog
  repos with `my repos add <id>` and reorient with `my root --repo <id>`;
  do not switch to a standalone clone when umbrella context matters. Repo
  launches use `my ai --repo <id> <harness>`.
- `CLAUDE.md` is a generated alias of this file. Do not edit either generated
  file directly; update the public baseline or manifest guidance fragments and
  rerun `my setup`.
- If you are unsure where you are, run `my root` or `my doctor`.
