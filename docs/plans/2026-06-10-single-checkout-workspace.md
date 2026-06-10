# Control-plane manifest, data-plane workspace

Status: decided (operator-confirmed 2026-06-10); supersedes the earlier
single-visible-checkout draft in this file's history.

## Problem

A self-hosted organization repository served two roles at once — manifest
source and content mount — and the CLI kept a checkout per role (plus the
`our init` scaffold: up to three clones of one remote). The duplicate-remote
guard then blocked content publishing whenever an admin edit sat uncommitted
in the hidden manifest checkout, as a normal state.

Two interim designs were rejected by the operator:

- guard precision ("don't patch the guard; remove the duplication"),
- one visible checkout at `<umbrella>/workspace` ("users could inadvertently
  modify the manifest; the manifest isn't the workspace — the workspace is a
  mount of things it defines").

The deciding rationale: **write access differs per plane.** The manifest is
admin-writable; workspace content can be pushed by anyone. Only a repository
boundary can enforce that distinction on the hosting side.

## Design

Two repositories with a structural boundary:

- **Manifest repo (control plane, private path):** `manifest.json`,
  `catalog/`, `skills/`, `agent-guidance/`. Its only checkout is the registry
  path under `<data>/our/manifests/<name>`. Users never browse it; `our
  admin` commands target it. Hosting permissions can restrict pushes to
  admins.
- **Content repo(s) (data plane, visible):** mounted as real directories in
  the umbrella (`<umbrella>/handbook`, …). Ordinary tools (rg, ls, editors,
  git) work on real files. Hosting permissions can allow the whole org to
  push.

```
~/.local/share/our/manifests/acme/   private manifest repo (own remote)
  manifest.json  catalog/  skills/  agent-guidance/

~/acme/                              umbrella
  .our/  AGENTS.md
  handbook/                          content repo (own remote)
    meetings/ support/ fleet/ decisions/ projects/ policy/ people/
  repos/                             product clones
```

One checkout per remote everywhere; the duplicate-checkout class of problems
cannot occur. Sync keeps its original simple roles: content mounts
auto-publish private content; the manifest repo holds admin changes for
explicit publication. No path-scoped publish, no merged workspace role for
the default layout.

### `our init` (offline, two local repos)

`our init acme` creates both repos locally and registers the manifest:

- manifest repo at the registry default path; the handbook mount's `git_url`
  is the **local path** of the content repo until published;
- content repo at `<umbrella>/handbook` (`--path` overrides), containing the
  content directories and a README with joining instructions;
- `our setup` / `our ai` work immediately; everything reports `local-only`
  until published.

### `our publish` (first-class ramp step)

Replaces the printed raw `gh` commands. Idempotently:

1. creates/pushes the content remote (`gh repo create <org>-handbook
   --private`, or plain push when origin already exists);
2. rewrites manifest mount URLs from local paths to the published remote
   URLs and commits;
3. creates/pushes the manifest remote (`<org>-manifest`);
4. updates the registry GitURL to the manifest's hosted URL and prints the
   teammate instructions (`our manifests add acme <manifest-url>`).

Guard: a manifest whose mounts reference local paths is **local-only**;
sync/doctor refuse to treat it as publishable and point at `our publish`.

### Compatibility: conflated self-mount repos

Existing orgs whose manifest repo also carries content (the v0.12 layout and
fluxinc today) remain supported but are no longer the recommended layout:

- self-mounts (`"."` or an explicit URL equal to the manifest remote) resolve
  to the single registered checkout (no duplicate clone, sparse-checkout
  skipped); sync sees one merged entry;
- their umbrella clones are not auto-migrated; duplicate-remote holds name
  the sibling checkout and files;
- the migration path is a **split**: generate a slim private manifest repo
  whose handbook mount points at the existing content repo with
  `include_paths` (the vestigial manifest files never materialize in the
  umbrella mount), register the slim repo, done — no history surgery.

### Kept from the interim work (commit 4cd1be5)

Local-only reporting for origin-less checkouts (manifest sync, mounts sync,
syncer), SelfMount detection by normalized remote equality, the merged sync
entry for conflated repos, sparse-checkout skip for self-mounts,
`SetLocalPath`/`DefaultCachePath`, registry `Add` preserving re-pointed
checkouts, derived reconciliation for workspace-role results, and the Gemini
`--home` isolation fix.

### Reverted/discarded

`our init` scaffolding at `<umbrella>/workspace`, the `ensureCanonicalWorkspace`
migration to a visible workspace checkout, and the path-scoped workspace
publish (syncer scratch) — except its origin-less local-only handling and
(optionally, Codex's call) overlap-aware inbound fast-forward for behind+dirty
checkouts.

## Slices

1. Claude: design doc, init two-repo rework, `our publish`, local-URL guard
   in doctor, test updates.
2. Codex: syncer scratch cleanup (keep local-only; decide on overlap-aware
   inbound pull), sync/doctor messaging, adversarial role-play of
   founder/teammate/publish flows, review of slice 1.
3. Docs sweep, v0.13.0 release, fluxinc split guidance.
