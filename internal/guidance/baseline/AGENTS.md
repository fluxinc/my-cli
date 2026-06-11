# Our AI Workspace

This directory is an Our AI umbrella workspace. Treat it as the local operating
context for one organization, not as a product repository or a monorepo.

Use the `our` CLI before falling back to ad hoc file searches:

- `our products list` shows the organization's products (business catalog
  entries, with their linked repos).
- `our repos list` shows the organization's repositories and their clone
  state; `our repos add <id>` clones one under `repos/<id>`.
- `our customers list` shows canonical customer IDs and aliases.
- `our mounts list` shows mounted handbook content and selected repos.
- `our meetings list`, `our meetings search <text>`, and
  `our meetings get <id>` query local meeting notes; use `--customer`,
  `--partner`, and `--product` filters when those axes are known.
- `our support list`, `our support search <text>`, and `our support get <id>`
  query anonymized support records; use `our support add <slug>` after
  resolving substantive support problems, recording the customer, any device,
  order, or asset identifiers, and the org members involved (`claimed_by`,
  `observed_by`) in frontmatter so records link later. Leave `approved_by` for
  explicit operator sign-off.
- `our fleet list`, `our fleet search <text>`, and
  `our fleet get <id|identifier>` query the deployed-instance registry; `get`
  resolves any identifier (order, location, serial) and lists related support
  records. Update workflow state with `our fleet set <id> status=<value>` and
  publish each meaningful transition with the suggested `our sync --message`
  command.
- Add `--json` when a harness needs structured output.

Default layout:

- `.our/` contains workspace identity and local state.
- `handbook/` and other mounts contain scoped organization content.
- `repos/` contains detached clones of catalog repositories.
- `work/` contains isolated Our AI work sessions created by `our ai` and
  `our work start`.
- `personal/` is durable local-only scratch for the current user.

Operating orientation:

- Launch agent harnesses with `our ai <harness>`. By default it creates a
  fresh work session under `work/` and starts the harness there; finish or
  discard session work with `our work finish --land|--publish|--discard`.
- Inspect active sessions with `our work status` or `our work list`; `our
  doctor` also reports session health.
- Treat this base umbrella as inspection/admin space. Do not draft, edit, or
  create shared workspace content directly in base mounts unless the operator
  explicitly asks for a base edit.
- Use `our ai --session <id> <harness>` to resume a known active session, or
  `our ai --no-session <harness>` only for base inspection/admin/debug.
- For repository work, use `repos/<id>` under this umbrella. Clone catalog
  repos with `our repos add <id>` and reorient with `our root --repo <id>`;
  do not switch to a standalone clone when umbrella context matters. Repo
  launches currently require `our ai --no-session --repo <id> <harness>`.
- `CLAUDE.md` is a generated alias of this file. Do not edit either generated
  file directly; update the public baseline or manifest guidance fragments and
  rerun `our setup`.
- If you are unsure where you are, run `our root` or `our doctor`.
