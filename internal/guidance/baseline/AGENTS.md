# Our AI Workspace

This directory is an Our AI umbrella workspace. Treat it as the local operating
context for one organization, not as a product repository or a monorepo.

Use the `our` CLI before falling back to ad hoc file searches:

- `our products list` shows available source repositories.
- `our customers list` shows canonical customer IDs and aliases.
- `our mounts add product:<id>` clones a product source under `products/<id>`.
- `our mounts list` shows mounted handbook content and selected products.
- `our meetings list`, `our meetings search <text>`, and
  `our meetings get <id>` query local meeting notes; use `--customer`,
  `--partner`, and `--product` filters when those axes are known.
- Add `--json` when a harness needs structured output.

Default layout:

- `.our/` contains workspace identity and local state.
- `handbook/` and other mounts contain scoped organization content.
- `products/` contains detached product repository clones.
- `personal/` is local scratch for the current user and agent work.

Operating orientation:

- Run agent harnesses from this umbrella root so they load this guidance.
- For product work, use `products/<id>` under this umbrella. Add products with
  `our mounts add product:<id>` and reorient with `our root --product <id>`;
  do not switch to a standalone clone when umbrella context matters.
- `CLAUDE.md` is a generated alias of this file. Do not edit either generated
  file directly; update the public baseline or manifest guidance fragments and
  rerun `our setup`.
- If you are unsure where you are, run `our root` or `our doctor`.
