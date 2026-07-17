# Governed organizations: policy, authorization, acceptance, and retention

Status: accepted for implementation after first security review

## Objective

Add a generic governance layer to My AI so an organization can:

- extend its existing manifest contract with short behavioral rules and
  versioned policy documents;
- make administrator-only operations fail closed against a real identity and
  authorization provider;
- require a human operator to read and accept current policy documents before
  onboarding completes or a harness launches;
- refuse to clone, mount, or use a managed repository unless the current
  identity still has access, and automatically remove managed checkouts after
  confirmed revocation without prompting;
- prevent non-administrators from deleting protected operational records or
  rewriting acceptance evidence; and
- produce evidence that a security reviewer can reproduce independently.

The public CLI must contain mechanism and public examples only. Customer names,
private repository names, policy text, member identities, and operational data
remain in private organization repositories.

## Threat model and trust boundaries

The CLI is not a security boundary by itself. A user who can edit a checkout
can bypass a local executable, a Git hook, local role state, or generated
guidance. Enforcement therefore has two matching halves:

1. `my` provides early local refusal, deterministic validation, policy
   acceptance, and a CI verifier.
2. The Git host protects the authoritative repositories and requires the CI
   verifier before protected branches can advance.

GitHub is the first identity and authorization provider. The effective actor is
the authenticated GitHub login. Administrator authority is the actor's
repository permission on the manifest repository, not a self-selected My AI
role and not a username copied into local state.

Gnit remains useful as the dependency and publication coordinator. A Gnit
control workspace can pin the exact manifest, policy, records, and attestation
commits that form one operating-plane projection. Gnit does not grant access;
the independent repository ACLs and protected branches do.

The model protects against accidental or malicious non-administrator changes
that must reach the organization's authoritative Git repositories. It cannot
prevent a repository administrator or GitHub organization owner from using
their provider-level override. Those actions remain visible in GitHub's audit
log and repository history, and enterprise deployments should export audit
events to their normal retention system.

Access loss is handled as a confidentiality event, not an ordinary sync error.
The CLI distinguishes `allowed`, `unknown`, `revocation-pending`, and
`revoked`. `unknown` (for example an outage, SSO authorization requirement,
rate limit, or insufficient token scope) blocks use after the last positive
decision expires but never destroys data. An ambiguous GitHub 404 is not by
itself proof of revocation because repository rename, transfer, deletion, token
scope, and access loss can all produce it. `revoked` requires
provider-positive evidence tied to the repository's cached immutable node id,
or repeated independent denials separated by the configured confirmation
interval. Once revocation is confirmed, active mount removal is immediate and
has no prompt.

## Repository model

Use separate repositories when confidentiality or write authority differs:

| Repository | Typical readers | Typical writers | Required protection |
| --- | --- | --- | --- |
| Manifest/control plane | organization members | administrators | PR, admin review, no force-push or branch deletion |
| Policy documents | organization members | policy owners | PR, policy-owner review, no force-push |
| Operational records | scoped operators | operators | required governance check; protected records cannot be deleted by non-admins |
| Acceptance ledger | auditors and organization admins | each operator for his or her own additions | append-only check; no force-push |

Small organizations may place policy, records, and attestations in one private
content repository. The path protections below preserve the same semantics.
Repository separation is recommended when distinct GitHub teams need distinct
read access.

Every manifest cache, content mount, and selected catalog repository is tracked
as a My AI-managed checkout with its canonical local path and repository
identity. Access cleanup is limited to those exact registered paths after
verifying that each path is below the expected My AI manifest cache or umbrella
root. Standalone clones and paths reached only through symlinks are never
cleanup targets.

The managed-path inventory is created at clone time and stored outside the
organization-controlled manifest. It records the canonical path, repository
node id, owner/name at last positive verification, organization, mount kind,
and owning umbrella. A later manifest edit cannot redirect cleanup at an
arbitrary path. Shared checkouts carry owner references and are removed only
when no still-authorized organization references them.

### Domain routing and publication

Governed organizations may opt in to manifest-declared record domains. Each
domain maps a record kind (for example support work, customer-asset
investigation, decision, bug report, or software change) to one existing mount
and path, a retention rule, a required review class, and a publish rule. This
extends the current mount model; it does not replace mounts or change
ungoverned behavior. Domains sharing readers normally remain path scopes in one
records repository. A separate repository is used only when readership or
confidentiality differs.

Governed automatic publication is pull-request-backed. The operator's own
provider identity creates one batched PR per sync; the required governance
check classifies the complete base-to-head tree diff and auto-merges routine
additive changes when green. Domains requiring an administrator add the
declared admin review. A shared bot must not author these PRs because that would
launder the human identity used by policy and authorization checks. `auto` must
never silently fall back to direct push. Existing ungoverned direct publication
remains backward compatible.

Automatic publication uses a durable local outbox. A successful record command
first commits its canonical local record and outbox entry, then attempts the
declared publication. Network or provider failure leaves an explicit pending
item for the access monitor or a later CLI invocation. It does not discard the
record or claim that it was synchronized. Additive versus mutation is always a
tree-diff property, never inferred from the CLI verb that produced the change.
Mixed cross-domain commits are held with split/remediation guidance. CI can
require a linked record for governed source changes, since My AI cannot observe
or constrain Git operations performed outside the CLI.

Mounts remain independent Git repositories. GitHub repository permissions,
rulesets, required checks, and review requirements provide authorization.
Gnit may pin and order a multi-repository publication, but it does not grant
rights. Git submodules are not used in staff umbrellas: they add detached-HEAD,
nested-auth, worktree, and sparse-checkout failure modes without improving the
provider authorization boundary. An administrative control repository may use
submodules when its operators accept that complexity. A control workspace may
represent mounts as Gnit dependencies when atomic coordination is useful,
while the manifest remains the portable source of the intended graph.

Administrative and staff planes are separate organizations and manifests. An
executive-only administrative mount is never inherited merely because a staff
manifest references the same company. Every downward-visible mount must be
declared explicitly in the staff manifest and pass the current user's provider
access check before it is materialized. Generated guidance and skills retain
source provenance and may include only manifest-approved external namespaces;
this prevents an administrator-only source from being copied into a staff
artifact by generation. Manifest roles are UX projections, not grants. The
governance audit compares declared role/team mappings with live repository
permissions and reports drift across every domain repository.

## Manifest model

The existing `contract: []string` remains the simplest way to append binding
default behavior such as release gates, decision conventions, and sign-off
requirements. It stays backward compatible and continues to render into
generated guidance.

A new optional top-level `governance` object adds machine-enforced behavior:

```json
{
  "contract": [
    "Do not release without the approvals required by the release policy."
  ],
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example-corp/my-manifest",
      "admin_permission": "admin"
    },
    "policies": [
      {
        "id": "release-policy",
        "title": "Release policy",
        "mount": "policy",
        "path": "policy/release.md",
        "version": "2026-01",
        "sha256": "sha256:<64 lowercase hex characters>",
        "acceptance": "required",
        "roles": ["operator"]
      }
    ],
    "attestations": {
      "mount": "compliance",
      "path": "attestations",
      "identity": "github"
    },
    "protections": [
      {
        "mount": "workspace",
        "paths": ["fleet", "support", "services"],
        "mode": "no-delete",
        "admin_override": true
      },
      {
        "mount": "compliance",
        "paths": ["attestations"],
        "mode": "append-only",
        "admin_override": false
      }
    ]
  }
}
```

Rules and documents have different jobs. Contract strings are concise agent
obligations. Policy documents are human-readable, versioned sources whose exact
bytes are bound to an acceptance by SHA-256. A policy revision changes its
digest and therefore requires a new acceptance without erasing the old one.

`roles` on a policy control which selected local roles require acceptance; an
empty list means every operator. Roles still do not grant authority.

## CLI surface

Read-only policy inspection:

```text
my policy list [--manifest NAME] [--json]
my policy show <id> [--manifest NAME]
my policy status [--manifest NAME] [--json]
```

Human acceptance:

```text
my policy accept <id> --yes [--manifest NAME] [--json]
```

`show` reads the declared mount/path, verifies the digest, and prints the
policy. `accept` refuses when the digest does not match, resolves the
authenticated GitHub login, requires an explicit human confirmation, and
creates a canonical JSON attestation at:

```text
<attestation path>/<github numeric user id>/<policy id>/<sha256>.json
```

The attestation contains schema version, organization, policy id/version and
digest, subject provider, immutable provider user id, display login, acceptance
time, and the manifest commit used.
It is marked with Git intent-to-add. A later policy revision produces a new
file. There is no normal delete or edit command. Administrative revocation or
supersession is a new append-only event, never silent replacement.

Admin authoring remains under `my admin`. When governance authorization is
configured, all manifest authoring helpers call one fail-closed guard before
writing. The guard asks GitHub for the current login and his or her permission
on `authorization.manifest_repository`; `--force` may bypass a dirty-tree
check but never authorization.

The existing contract verbs remain the low-friction policy-rule path:

```text
my admin contract add "RULE" --manifest-dir DIR
my admin contract remove <index|RULE> --manifest-dir DIR
```

Policy document authoring gains digest-safe verbs:

```text
my admin policy add <id> --title TEXT --mount ID --path PATH --version VERSION \
  --acceptance required|optional [--role ID] --manifest-dir DIR
my admin policy remove <id> --manifest-dir DIR
```

`add` hashes the current mounted policy bytes when an umbrella is supplied, or
accepts an explicit `--sha256` for an independently prepared manifest.

## Onboarding and launch gates

Onboarding enumerates required policies after setup and before marking the tour
complete. For each missing current acceptance it:

1. prints the policy title, version, source, and verified digest;
2. displays the document through the same `my policy show` path;
3. asks the human to confirm that he or she read and accepts that exact
   version; and
4. records the attestation through `my policy accept`.

EOF, decline, identity failure, digest mismatch, or ledger write failure leaves
onboarding incomplete. Non-interactive onboarding reports the exact
`my policy show` and `my policy accept` commands and does not mark completion.

`my ai` checks policy status before launching. It fails with the missing policy
IDs and remediation commands. This prevents an old local tour marker from
bypassing a newly required policy version.

Governed launch uses a positively refreshed manifest or a cached manifest whose
last verified refresh is inside the governance TTL. A stale or
freshness-unknown manifest blocks launch. Direct harness execution can bypass
local UX, so the authoritative content-repository check also requires the
pull-request author (not commit author, committer, pusher, or merger) to have
current acceptances. An administrator merging another person's attestation does
not change its subject: the attestation subject must still equal the
attestation pull-request author.

## Repository access and revocation

All clone, setup, sync, root, and launch paths call one access resolver before
using a managed repository. For GitHub repositories the resolver authenticates
the current login, identifies the canonical repository, and checks that the
actor has at least read access. Public repositories remain readable without an
authenticated grant but still receive normal repository-existence checks.

The access resolver applies to HTTPS and SSH GitHub remotes equally and returns:

- `allowed`: repository access is positively established;
- `revocation-pending`: authenticated checks deny access but the result is not
  yet sufficiently strong to trigger destructive cleanup;
- `revoked`: provider-positive or independently repeated checks prove that the
  cached repository identity is no longer readable by this actor; or
- `unknown`: access could not be established because credentials, network, or
  provider state could not be verified.

`allowed` is required before a new clone. A recent positive decision may be
used inside a short administrator-configured TTL to avoid making every command
depend on network latency or API rate limits. Once that TTL expires,
`revocation-pending`, `revoked`, and `unknown` block mount use and harness
launch. Only `revoked` removes local material.

Existing umbrellas enter enforcement through `my access check --dry-run`, a
permanent zero-write live-rights command. It resolves every managed repository
through the provider API and prints its immutable identity, current
authorization, exact active paths, derived session worktrees, and the future
quarantine plan. A successful activation records a positive baseline per
repository outside the manifest. A mount added later requires its own positive
baseline; cleanup is forbidden for a repository that has never had one on that
machine.

On confirmed revocation, cleanup runs without a prompt even when the checkout
is dirty. This is an explicit confidentiality invariant. Active removal is an
atomic move into a restrictive, non-mounted quarantine. If the filesystem
cannot perform a verified lossless move, cleanup blocks use and reports an
error; it never falls back to recursive deletion. Quarantine preserves local
work without leaving the repository mounted or usable by My AI. Cleanup:

1. resolves and validates the exact registered checkout path;
2. refuses to follow symlinks or remove a path outside its manifest cache or
   umbrella-owned mount/repo directories;
3. inventories branch, HEAD, upstream reachability, tracked changes, untracked
   files, worktrees, size, and file hashes;
4. writes and verifies a mode-0600 recovery capsule containing a binary full-
   index working-tree patch, a Git bundle for local-only commits, an archive of
   untracked files, metadata, and exact restore commands;
5. round-trip verifies the recovery capsule against a pristine base, including
   every branch, tag, stash, ignored file, and untracked file;
6. moves the complete checkout and related session worktrees into mode-0700
   quarantine under the owning umbrella on the same volume and verifies the
   inventory before clearing the active path;
7. removes its local state entry; and
8. removes derived organization guidance, MCP configuration, and managed skill
   materializations that depended on the revoked source.

Any inventory, capsule, or verification failure blocks the checkout in place
and makes it unusable to My AI; it never authorizes byte removal. Cleanup uses a
crash-safe intent/move/complete journal. Windows open-handle failures leave the
same blocking marker and retry the move later. A copy-and-hash-verify fallback
may be used across filesystems only when every byte is verified before the
source is removed; the normal quarantine location is selected to avoid that
case.

Cleanup holds a per-umbrella single-instance lock and writes state atomically.
It removes or invalidates session worktrees derived from the revoked mount,
prunes the source repository's Git worktree metadata, and observes shared
checkout reference counts before removing common material.

Purge eligibility is computed and recorded in the quarantine manifest at move
time, while positive remote evidence is still available. Scheduled purge is
permitted only for a clean checkout whose branches, tags, stashes, and all local
refs are provably reachable from the last positively verified remote. Dirty,
untracked, ignored-but-present, unpushed, unverifiable, or interrupted
quarantines are never purged automatically. `my access status` and `my doctor`
list their age and retention reason so they cannot grow silently. Recovery
capsules are checksummed and retained inside the quarantine because the capsule
is itself sensitive data. This treats automatic removal as an access-path
operation, never as authorization to discard a person's local work. A QMS-
controlled record is purge-eligible only after its authoritative remote copy is
positively established; quarantine must never contain the sole quality record.

If access to the manifest repository itself is revoked, the CLI first snapshots
the managed-path inventory in memory, then applies the same verified quarantine
workflow to the manifest cache and every mount/repository derived solely from
that organization. Personal scratch and unmanaged repositories are left alone.
The umbrella is marked inaccessible so stale guidance cannot be launched.

Every automatic removal writes a local security event containing timestamp,
organization, repository identifier, provider, actor, reason, and removed path.
The log contains no repository content and uses a local append-only file with
restrictive permissions. Provider audit logs remain the authoritative external
record.

To make revocation proactive rather than dependent on the next manual command,
setup installs a small per-user access monitor using the operating system's
service mechanism (launchd on macOS, systemd user units on Linux, Task Scheduler
on Windows). It periodically runs the same resolver and cleanup logic. Every
CLI invocation also performs the check synchronously, so a stopped monitor
cannot permit use. `my access status` reports monitor health and last checks;
`my doctor` treats a missing or stale monitor as a governed-workspace failure.
Doctor reports the platform's actual availability boundary: launchd agents run
only in a login session; systemd user monitoring after logout requires linger;
Windows scheduling depends on the configured user task.

## Retention enforcement

`no-delete` permits additions and modifications but rejects deletion or rename
away from protected paths by non-administrators. This fits fleet and service
records whose state legitimately evolves while their history must remain.

`append-only` permits new files only. Existing files cannot be modified,
renamed, or deleted. This fits acceptance evidence. Administrative correction
is represented by a new revocation/supersession event, so the audit trail stays
append-only.

Deletion is defined by tree membership: a protected path exists in the trusted
base tree and does not exist in the proposed head tree. The verifier compares
the complete `base...head` result, including merge commits, and does not rely
on Git rename heuristics or a provider's truncated pull-request file list.
`no-delete` intentionally does not stop a writer from replacing record
content; its durability property comes from retained Git history plus disabled
force pushes. Record types may additionally declare immutable identity fields
when a stronger semantic invariant is required.

Enforcement runs in three places:

1. record-writing commands reject unauthorized operations early;
2. `my sync --push` checks dirty and ahead changes against the upstream base
   before committing or pushing; and
3. `my governance check --base REF --head REF --actor LOGIN` runs in required
   GitHub CI and applies the same rules to every proposed change, including
   commits created without `my`.

For manifest changes, the CI verifier requires the actor to have the configured
administrator permission. The verifier always loads governance configuration,
administrator requirements, and protections from the trusted base ref, never
from untrusted pull-request head content. A head change that weakens or removes
governance is therefore evaluated under the old rules. For protected content,
the verifier rejects disallowed tree changes. `admin_override` means the
actor's permission on the repository containing the protected paths, not the
manifest repository. For attestations, the verifier also requires the pull
request author to match the immutable subject id and canonical path.

## GitHub enforcement profile

The documented baseline is deliberately reproducible rather than magical:

- protected default branch using GitHub rulesets where available;
- pull requests required;
- required `my-governance` status check supplied by an organization-required
  or ruleset-pinned workflow where available;
- CODEOWNERS review for manifest, policy, and `.github/workflows` changes;
- force pushes and branch deletion disabled;
- dismissal of stale approvals after new commits;
- conversation resolution required;
- no bypass actors, including administrators, unless the customer explicitly
  documents an emergency-access exception; and
- organization audit-log retention appropriate to the customer's obligations.

`my governance audit --json` reports whether the current GitHub repository
settings satisfy the baseline. A future `my admin governance apply --github`
may create rulesets, but the first release must not silently mutate enterprise
repository settings.

Governed content repositories publish through pull requests. Implemented PR
mode creates a topic branch, pushes it, opens or updates a GitHub pull request,
and returns the exact check and merge state. It does not silently bypass review
or required checks. Optional auto-merge is requested only when repository rules
permit it and the operator selected that policy. Retention protections must not
ship before this path works, because today's direct content push and stub PR
mode are incompatible with required-check branches.

## Security properties and limitations

- A local role cannot promote an operator; GitHub permission is authoritative.
- A managed repository is never cloned or used without a positive current
  access decision, and a positively confirmed revocation removes the managed
  checkout without an operator prompt.
- A local manifest edit cannot pass the required remote check when its actor is
  not an administrator.
- A protected record cannot be removed through `my sync` or merged through a
  protected branch by a non-administrator.
- An acceptance identifies the exact policy digest and authenticated subject.
- Old acceptances remain evidence after policy revision or revocation.
- Separate repositories enforce least privilege and confidentiality before the
  operating plane clones or composes them.
- Gnit pins the dependency graph and coordinates publication, but GitHub ACLs,
  rulesets, required checks, and audit logs provide the enforcement boundary.

An administrator or GitHub organization owner remains capable of changing
repository rules or history. That provider-level authority must be governed by
the customer's identity lifecycle, MFA/SSO, privileged-access management, and
audit-log controls; My AI reports those dependencies instead of claiming to
replace them.

## Implementation slices

1. Manifest types, validation, public fixture, and backward-compatible load.
2. Hermetic test infrastructure: isolate home and umbrella discovery in every
   filesystem-mutating test, and reject destructive test paths outside the
   declared temporary root.
3. GitHub identity/permission resolver, managed-path inventory, fail-closed
   admin authoring guard, and automatic revocation cleanup.
4. Cross-platform access monitor installation, synchronous startup checks, and
   `my access status`/doctor reporting.
5. Policy list/show/status/accept and canonical attestation storage.
6. Onboarding and launch acceptance gates.
7. Working PR publish mode for governed content repositories.
8. Manifest-declared domain routing, durable publication outbox, and additive
   record/change linkage without changing legacy record commands.
9. Protected-path diff validator integrated with sync and exposed as
   `my governance check` for CI.
10. GitHub governance audit, workflow example, documentation, and self-skill.
11. Full tests, destructive-path safety tests, threat-model review,
   public-data scan, and release readiness.

The feature is complete only when local bypass attempts and direct-Git pull
requests are both covered by tests; a green unit test for the CLI alone is not
sufficient evidence of repository-level enforcement.

## Design-review dispositions

The first Claude security review produced nine findings. All are incorporated:

- F1: CI trusts the base-ref governance document and protects the workflow.
- F2: 404 is ambiguous; scope and SSO failures are unknown; destructive cleanup
  requires strong or repeated evidence and immediately unmounts via quarantine.
- F3: attestations use immutable provider user ids and pull-request author
  identity.
- F4: launch requires fresh governance and CI enforces acceptance for writers.
- F5: TTL caching, cleanup locking, atomic state, platform monitor limits,
  Windows deletion, session worktrees, and shared-reference handling are
  explicit requirements.
- F6: tree-based deletion, full-range diffing, repository-local admin override,
  rulesets, and the exact no-delete property are defined.
- F7: functional pull-request publishing is a prerequisite for protected
  content repositories.
- F8: access checks use the provider API regardless of Git transport.
- F9: filesystem-mutating tests must isolate both home and umbrella discovery;
  destructive operations additionally require an explicit test-root guard.
