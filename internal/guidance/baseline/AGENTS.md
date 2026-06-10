# Our AI Workspace

This directory is an Our AI umbrella workspace. Treat it as the local operating
context for one organization, not as a product repository or a monorepo.

Use the `our` CLI before falling back to ad hoc file searches:

- `our products list` shows available source repositories.
- `our customers list` shows canonical customer IDs and aliases.
- `our mounts add product:<id>` clones a product source under `repos/<id>`.
- `our mounts list` shows mounted handbook content and selected products.
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
- `repos/` contains detached product repository clones.
- `personal/` is local scratch for the current user and agent work.

Operating orientation:

- Run agent harnesses from this umbrella root so they load this guidance.
- For product work, use `repos/<id>` under this umbrella. Add products with
  `our mounts add product:<id>` and reorient with `our root --product <id>`;
  do not switch to a standalone clone when umbrella context matters.
- `CLAUDE.md` is a generated alias of this file. Do not edit either generated
  file directly; update the public baseline or manifest guidance fragments and
  rerun `our setup`.
- If you are unsure where you are, run `our root` or `our doctor`.
