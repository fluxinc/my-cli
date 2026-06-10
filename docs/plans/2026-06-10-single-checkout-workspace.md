# Single-checkout workspace

Status: draft, converging under review (Claude drafting, Codex reviewing).

## Problem

A self-hosted organization repository (the `our init` default, and the layout
real deployments use) serves two roles at once: it is the **manifest** source
and the **content** mount. The CLI currently maintains a separate checkout per
role:

- the manifest registry clone under `<data>/our/manifests/<name>`
- the umbrella mount clone under `<umbrella>/<mount-id>`

and for `our init` users a third copy: the original scaffold directory.

Multiple checkouts of the same remote diverge locally. The syncer's
duplicate-remote guard then holds back publishing whenever the *other*
checkout has pending changes — and because admin edits intentionally sit
uncommitted in the manifest checkout, content publishing is blocked in the
default layout as a normal state. The hold message names neither the sibling
checkout nor the offending files.

Operator direction: do not patch the guard; remove the duplication. One
remote, one local checkout. File locations are in scope.

## Design

### One canonical checkout per remote

A mount is a **self-mount** when its resolved git URL points at the same
remote as the manifest itself (`normalizeRemote(mount) == normalizeRemote(ref)`).
This covers the `git_url: "."` marker and an explicit URL equal to the
manifest's.

For a manifest with a self-mount, the canonical checkout lives **inside the
umbrella, visibly**, at:

```
<umbrella>/workspace
```

named `workspace` because the checkout holds the manifest, catalog, and
content together — it is the org workspace, not just a handbook. The target
layout:

```
~/acme/                 umbrella
  .our/                 umbrella state
  AGENTS.md             generated guidance
  workspace/            THE checkout: manifest.json, catalog/, skills/, content dirs
  repos/                product/code clones
```

- The manifest registry's `LocalPath` points at the canonical checkout.
- `workspace.ListMounts` resolves every self-mount's `LocalPath` to the
  canonical checkout instead of `MountPath(root, id)`.
- The registry cache clone is **not** created for self-mounted manifests; for
  manifests without a self-mount (externally consumed orgs) the cache clone
  remains the only checkout, unchanged.

### Migration

`our setup`, `our mounts sync`, and `our doctor --fix` converge existing
installs through one helper (`ensureCanonicalWorkspace`), which considers the
candidate paths {registry cache, legacy mount path, canonical path}:

- only the canonical checkout exists → repoint the registry, done.
- exactly one checkout exists elsewhere → move it to the canonical path
  (rename, copy fallback), repoint.
- multiple checkouts exist → keep the one with local work (dirty or ahead),
  move it to the canonical path, delete the others **only after verifying**
  they are clean, not ahead, and on the same remote.
- two checkouts both carry local work → `setup`/`mounts sync` refuse and name
  the pending files in both checkouts with exact remediation commands.
  Disjoint-diff consolidation (apply the secondary checkout's uncommitted
  diff and untracked files onto the winner, `git apply --check` first, then
  delete the secondary) lives **only behind `our doctor --fix`**, with the
  dry-run `our doctor` naming exactly what would move. Overlapping paths
  always refuse.
- nothing exists → clone the remote at the canonical path, repoint.

Deletion never targets the path the registry will point to, and never touches
a checkout that fails the clean/not-ahead/same-remote verification.

After migration, derived state is reconciled (skills symlinks, guidance) since
installed skill links may reference the deleted cache path.

### Registry behavior changes

- `manifest.Add` preserves an existing ref's `LocalPath` when re-adding the
  same name (today it silently resets to the cache path, which would undo
  migration) — but only when the new `GitURL` matches the existing checkout's
  remote (normalized) or the checkout does not exist; re-adding with a
  different remote resets to the default cache path rather than keeping an
  incompatible checkout.
- Origin adoption is conservative: when the checkout's `origin` URL and the
  registry `GitURL` diverge, sync/doctor adopt `origin` as the registry
  `GitURL` (with a notice) **only when the registered URL is a local path**
  (the `our init` → publish flow). When a hosted registry URL diverges from a
  hosted origin, report the mismatch and hold; never silently trust either
  side.
- A checkout with no `origin` remote is reported as `local only; publish with
  gh repo create ...`, not as an error.

### Sync semantics for the merged checkout

`collectSyncEntries` emits **one** entry for a self-mounted manifest: role
`workspace`, with `ContentPaths` from the self-mount(s) (union when several
self-mounts declare different include paths). The separate `manifest` entry is
dropped for that remote.

The syncer treats `workspace` entries with path-scoped publish:

- dirty files within `ContentPaths` auto-publish exactly as content does today
  (stage only those files);
- dirty files outside `ContentPaths` (manifest.json, catalog/, skills/) are
  left uncommitted and reported by name as held admin changes, with the
  explicit publish command;
- **ahead** commits cannot be path-scoped (they are immutable), so auto
  publish holds the push when any ahead commit touches files outside
  `ContentPaths`;
- inbound fast-forward applies when the incoming files do not overlap local
  dirty files.

CLI follow-through for the `workspace` role: derived-state reconciliation
(`changedManifestForDerived`) must notice inbound/pushed manifest changes on
workspace entries, `our doctor --fix` must treat workspace checkouts as
fixable, and `--scope manifest` / `--scope content` both select the merged
entry (with tests pinning those semantics).

The duplicate-remote guard remains for genuinely distinct checkouts (e.g.
product clones), but the manifest-vs-mount sibling case disappears
structurally.

### `our init`

`our init` scaffolds the repository directly at the canonical path
(`<umbrella>/workspace`, with `--path` as an override), registers it with
`LocalPath` = the scaffold, and performs no cache clone. One repo, one
checkout, from the first command. Teammate flow is unchanged on the surface
(`our manifests add <name> <url>` then `our setup`); their cache clone is
migrated into the umbrella on first `our setup`.

## Slicing

1. Claude: registry/self-mount canonicalization, `ensureCanonicalWorkspace`
   migration, `manifest.Add` preservation, origin adoption, init relocation —
   with tests (this turn).
2. Codex: syncer `workspace` role path-scoped publish + held-admin naming,
   doctor/setup migration surfacing, review of slice 1.
3. Docs sweep (quickstart, the-model, manifest-and-mounts, admin,
   cli-reference, self-skill), changelog, release, live migration of the
   operator's workspace.
