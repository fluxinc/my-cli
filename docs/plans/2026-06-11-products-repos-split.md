# Products are not repos: split the catalog from code checkouts

Status: draft under debate (operator-directed 2026-06-11); design converging
in-room between Claude and Codex.

## Problem

The catalog's **product** concept is conflated with **git repo checkouts**:

- `catalog/products.json` entries carry a `git_url`, and opting in
  (`our mounts add product:<id>`) clones that URL under `repos/<id>`.
- Umbrella state tracks `selected_products`; sync gives those checkouts the
  `product` role; the sync scope `repos` is internally aliased to `products`.
- `our root --product` and `our ai --no-session --product` treat a product id
  as a directory.

The operator's objection: **repos aren't all products, and not all products
are repos.** An organization has code repositories that are not sellable
products (configs, infra, tooling), and products that have zero or many
repositories (services, hardware, bundles). Meanwhile support and meeting
records already use `--product` correctly — as a pure business reference —
which is the only meaning "product" should keep.

## Design

Two concepts, two inventories:

- **Product (catalog, business plane).** `catalog/products.json` keeps
  id, name, description, purpose, related_skills. **`git_url` is removed.**
  A product may declare `repos: ["repo-id", ...]` linking to the repos that
  implement it (zero or many). Records (`our support/meetings ... --product`)
  keep referencing products; nothing changes there.
- **Repo (catalog, code plane).** New `catalog/repos.json`: an inventory of
  the organization's repositories — per entry: `id`, `git_url`,
  `description`, optional `default` (clone at setup). Opting in clones under
  `<umbrella>/repos/<id>`, exactly where clones live today. Repos are not
  mounts: they never inherit content-mount semantics (auto-publish policy,
  include_paths, session worktrees); their sync surface stays
  inspect/ff-pull/hold, never auto-push.

Why a catalog file and not mounts of kind `repo`: mounts carry content
semantics — auto-publish gating, declared content paths, session worktrees —
that code repositories must not inherit. A `repos.json` inventory mirrors
`products.json` mechanically, keeps `manifest.json` mounts content-only, and
gives repos their own lifecycle verbs. The existing mount kind `repo` is
removed from the valid kinds (pre-alpha; no compatibility shims).

### CLI surface

```
our repos list [--json]            # inventory + which are cloned locally
our repos add <id>                 # opt in: clone under repos/<id>
our repos remove <id>              # drop the local clone selection
our root --repo <id>               # print repos/<id>
our ai --no-session --repo <id>    # launch from a repo checkout
our sync --scope repos             # real scope (internal 'products' alias dies)
our products list                  # pure catalog: business entities + linked repos
```

Removed: `our mounts add/remove product:<id>`, `--product` on `root`/`ai`
(records keep `--product`). Sync entry role `product` becomes `repo`.

### State and migration

- `selected_products` in `.our/state.json` becomes `selected_repos`;
  `LoadState` migrates the key silently. Mount-status entries with kind
  `product` migrate to `repo` the same way.
- Clones already live under `repos/<id>`; no filesystem moves. The legacy
  `products/` migration shipped in v0.10 stays as-is.
- Manifest validation: a product entry with `git_url` becomes a validation
  error naming this migration ("move git_url to catalog/repos.json and link
  via repos: [...]"). Only the fluxinc manifest is affected; it is migrated
  alongside the release (operator-side, private).

### Slices

1. **A — manifest schema:** `catalog/repos.json` load/validate, Product loses
   `git_url` gains `repos[]`, validation errors with remediation, example
   fixtures.
2. **B — umbrella + plumbing:** `selected_repos` state with migration,
   `RepoPath` (rename of `ProductPath`), `our repos` noun, sync role/scope
   rename, removal of `mounts add product:`.
3. **C — surfaces:** `--repo` on `root`/`ai`, guidance baseline, bundled
   skill, site docs, README, examples.
4. **D — fluxinc migration:** update the private manifest (products.json /
   repos.json), verify `~/flux` resolves all four existing clones.

## Open questions (in debate)

- Does `our repos add <id>` clone immediately or only select for the next
  `setup`/`sync`? (Lean: clone immediately; selection-only adds a surprising
  delay.)
- Should `repos.json` support `default: true` entries cloned at `our setup`,
  mirroring mount modes? (Lean: yes, minimal — a bool, not the full mode
  enum.)
- Future: sessions may include repo worktrees (`our ai --repo` inside a
  session) — out of scope here, tracked by the execution-plane plan.
