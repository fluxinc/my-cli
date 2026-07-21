# Governed organizations completion: hardening, acceptance CI, and dogfood

Status: active — S1-S4 complete; S5 umbrella-root contract authoring next

Design source of truth: [2026-07-16-governed-organizations](2026-07-16-governed-organizations.md).
This plan closes the gap between mechanism-complete code and a
product-goal-complete, released, dogfooded feature, per operator direction
(2026-07-21). It also corrects the premature "implementation complete" status:
that status line must not return until every gate below passes.

## Operator direction

1. Fix the policy race and outbox integrity/retry issues; expand missing tests.
2. Keep `manifest_commit` as provenance; stop requiring equality with the
   latest manifest when the policy digest/version still matches.
3. Implement CI enforcement of current operator acceptance.
4. Add umbrella-root governed contract authoring.
5. Route acceptances through durable PR publication; add authoritative
   acceptance reporting.
6. Add linked-record CI policy for governed software changes.
7. Dogfood policy/QMS acceptance on Flux Admin; test revocation separately
   with disposable private repositories and a second identity.
8. Only then mark the plan complete, tag a release, and consider enabling for
   the staff Flux umbrella.

Product framing: policy, acceptance, PR publication, and record routing ship
together as opt-in organization governance. Automatic revocation quarantine is
endpoint-security functionality with its own activation and release gate.
Documentation must state plainly that "immediate" removal means immediately
after confirmed detection, and detection latency is bounded by the monitor
interval, the positive-access TTL, and the denial-confirmation policy.

## Jointly agreed dispositions

- **A. Provenance-only manifest commit.** CI validates that an attestation's
  `manifest_commit` is a nonempty full OID and that its policy id, version,
  and digest match the trusted base manifest. Equality with the current
  manifest head is dropped. Regression: manifest advances after acceptance;
  the attestation PR still passes.
- **B. Remote acceptance enforcement (implement, don't reword).** For
  universally required policies (empty `roles`), the content-repo CI check
  requires a merged attestation for the pull-request author in the
  authoritative attestation mount, read from that repository's trusted
  default-branch tip, never from PR head. An attestation PR bootstraps by
  validating its own additions for its own author only. The required workflow
  token needs read scope on the attestation repository; this is documented in
  `examples/governance/`. Role-scoped policies remain locally gated until
  manifests carry an authoritative identity-to-role mapping; this limitation
  is documented.
- **C. Durable acceptance publication.** `my policy accept` queues a reserved
  policy-acceptance outbox item and attempts a governed PR containing **only**
  the queued attestation path(s) — never the batched mount sweep. The publisher
  builds its prospective commit from those paths only, preserves unrelated
  working-tree changes byte-for-byte, and verifies both the pull-request author
  and remote head. Acceptances are a reserved implicit append-only domain that
  reuses the outbox state machinery, not the Markdown record parser.
  `my policy acceptances [--json]` reports local/submitted/merged state per
  subject and policy from the attestation repository, making the ledger
  authoritative and inspectable.
- **D. Append-only administrative supersession.** Revocation/supersession of
  an acceptance is a new append-only event written by a minimal admin verb;
  CI validates the actor's admin permission on those additions. Never
  mutation, never deletion. A supersession permanently blocks re-acceptance of
  the same version and digest; reinstatement requires a new policy version.
- **E. Umbrella-root contract authoring.** `my admin contract add|remove`
  resolves the registered manifest checkout from `--manifest`/`--umbrella`
  (defaulting like other umbrella commands) and finishes with the governed PR
  publication next step. `--manifest-dir` remains as the compatibility path.
  The fail-closed GitHub admin guard applies unchanged.
- **F. Outbox integrity set.** Per-domain reconcile errors (skip and report,
  never abort all domains); `outbox.Append` recomputes the item id from
  identity fields and rejects mismatch, and freezes identity fields across an
  item's events; parent directories fsynced after event/record creation;
  content digest re-checked at submit time so modified-after-queue content
  never marks a stale item submitted; structural `pr_url`/`pr_head_sha`
  fields instead of message parsing; `merged` requires GitHub MERGED state
  plus the exact blob (digest match) present at the merge commit reachable
  from the trusted default branch; missing upstream is pending/error, never
  silently "nothing to publish"; parent-directory CODEOWNERS patterns count
  as covering a domain path; every path included in a batched PR is surfaced
  to the operator. A pending item may transition directly to `merged` only
  when the exact queued digest is already present at the fetched trusted
  upstream tip, covering publication by another machine or workflow without
  opening a redundant pull request.
- **G. Linked-record CI (separate slice, opt-in).** Manifest-declared
  `governance.change_record_rules` map a governed source surface to a record
  domain. A governed source PR must carry a `My-Record: <domain>/<record-id>`
  trailer; `my sync --record` writes that trailer, and the workflow supplies
  the pull-request identity authoritatively rather than trusting PR-body
  identity fields. CI resolves the merged record through its domain repository
  and verifies reciprocal evidence. To avoid the ordering deadlock,
  reciprocity matches `github-pr:<owner>/<repo>#<number>` in the record's
  `sources`, not a merge SHA that cannot exist yet: open source PR → add record
  citing the PR → record merges (additive, auto-PR) → source CI finds the merged
  record. Gnit may order publication but is never authority.
- **H. Revocation plane separation.** The access monitor and automatic
  quarantine remain explicitly labeled experimental and keep their own
  activation gate (`my access activate` remains explicit and is never enabled
  by policy/record dogfood) and their own release gate: a real drill against
  disposable private repositories with a second identity must pass before the
  monitor is recommended for any real umbrella. The drill includes dirty,
  untracked, ahead-of-upstream, and active-session fixtures; ambiguous 404/SSO
  denial; capsule restore proof; and proof that quarantine never purges local
  data. Docs state the detection-latency bounds in the product framing above.

## Debate resolutions (schema, deadlocks, trust, migration, breadth)

- **Schema.** The reserved acceptance domain id (`policy-acceptances`) is
  rejected in user manifests, and user record domains may not overlap the
  attestation path; manifest validation enforces both. Outbox gains additive
  structural fields (`pr_head_sha`, `merged_commit`) without a schema bump;
  events remain content-free.
- **Deadlocks.** Acceptance bootstrap: an attestation PR satisfies its own
  author's requirement for the files it adds; a new operator blocked on an
  unmerged attestation PR gets remediation text naming that PR. The
  enforcement profile documents that attestation paths should permit
  auto-merge (subject==author is already CI-enforced). Linked-record
  reciprocity uses PR URLs (G above). Reconcile skips absent mounts (F).
- **Provider trust.** Merged proof and acceptance reads prefer git evidence
  (fetch, `cat-file` at the trusted tip or merge commit) over REST file
  listings, consistent with the base plan's rejection of truncated PR lists.
- **Migration.** None required: slice 8 is uncommitted, the attestation format
  is unchanged (only CI's interpretation of `manifest_commit` relaxes), and
  umbrella contract authoring adds flags beside the compatibility path.
- **Release breadth.** Implementation proceeds in operator order, but the
  release cut after dogfood (step 7) covers steps 1–5 at minimum. If
  linked-record CI (step 6) is not yet dogfood-solid, it ships in the next
  release rather than delaying this one. The revocation monitor's
  recommendation gate is independent of both (H).

## Implementation slices

Codex drafts each slice; Claude and Codex alternate review/test turns via
Talking Stick. Tests first within each slice; `go build ./... && go vet ./...
&& go test ./...` plus `git diff --check` before every handoff. One commit per
slice, README/roadmap and `docs/plans/README.md` updated in the same commit
whenever a status changes.

### S1 — Slice-8 integrity fixes and tests (steps 1, F) — complete

Files: `internal/outbox/outbox.go`, `internal/cli/record_domains.go`,
`internal/cli/governance_pr.go`, `internal/cli/governance_audit.go`,
`internal/cli/access_monitor.go`, co-located tests.

- Append recomputes/validates item id; freezes identity fields across events.
- Digest re-check before submitted; structural PR fields end-to-end.
- Submitted→merged reconciler (git-evidence proof) run from flush/monitor and
  visible in `my record outbox`.
- Per-domain reconcile skip with per-domain error reporting.
- Parent-dir fsync; missing-upstream pending; CODEOWNERS parent patterns;
  batched-path surfacing.
- Tests: manual flush, mixed-domain hold, monitor retry, crash reconcile,
  digest drift, merged proof, absent-mount skip.
- Commit slice 8 (currently uncommitted worktree) with these fixes; drop the
  premature "implementation complete" wording from README/plan status in the
  same commit.

### S2 — Policy provenance race fix (step 2, A) — complete

Files: `internal/governance/check.go` (validateAttestationAdditions),
`internal/governance/check_test.go`.

- Replace the equality check with full-OID validation; keep the trusted-base
  policy tuple match. Regression test: manifest advances post-acceptance.

### S3 — Durable acceptance publication and reporting (step 5, C) — complete

Files: `internal/cli/policy.go`, `internal/cli/record_domains.go`,
`internal/outbox/`, tests.

- Reserved implicit append-only acceptance domain; accept queues and attempts
  an attestation-only governed PR; `my policy acceptances` report with
  local/submitted/merged; onboarding/launch remediation text gains the
  publish step.
- The prospective commit starts at the fetched trusted upstream and contains
  only the queued attestation path, excluding unrelated dirty, staged, and
  ahead state while preserving the checkout and index. Exact content already
  at trusted upstream converges pending outbox state directly to merged.
- Acceptance reporting skips and warns on an unreadable individual ledger or
  outbox item instead of hiding every valid row. The publication attempt does
  not update the manifest-freshness TTL cache; later governed operations still
  perform their own freshness gate.

### S4 — Remote acceptance CI and supersession (steps 3, B, D) — complete

Files: `internal/governance/check.go`, `internal/cli/policy.go` (admin verb),
`examples/governance/` (workflow + token scope docs), tests.

- Cross-repo merged-attestation requirement for universal policies with the
  attestation-PR bootstrap; documented role-scope limitation; minimal
  append-only supersession verb with CI admin validation.

### S5 — Umbrella-root contract authoring (step 4, E)

Files: `internal/cli/contract.go`, `internal/cli/admin.go`, tests
(`internal/cli/governance_test.go` or new `contract_admin_test.go`).

- `--manifest`/`--umbrella` resolution to the registered manifest checkout,
  governed-PR next commands, `--manifest-dir` untouched; fail-closed guard
  exercised in tests via stubbed runner.

### S6 — Linked-record CI (step 6, G)

Files: `internal/manifest/manifest.go` (change_record_rules + validation),
`internal/governance/check.go`, `examples/governance/`, tests.

- Trailer parsing, reciprocal PR-URL matching, merged-record resolution
  through the domain repository; ships only when dogfood-solid.

### S7 — Docs truth pass and revocation-plane framing (H)

Files: `README.md`, `docs/plans/README.md`, both plan docs,
`skills/my-cli/SKILL.md`, `site/` if rendered content changes.

- Revocation = endpoint-security plane with separate activation/release gate;
  "immediate after confirmed detection" latency bounds; acceptance-ledger
  authoritative reporting; role-scope limitation; and the acceptance
  publisher's deliberate non-interference with the manifest-freshness TTL.

### S8 — Dogfood package and revocation drill (step 7)

Deliverables, not mechanism code:

- Flux Admin manifest governance config draft (policies, attestations mount,
  acceptance domain) prepared for the private repo — never committed here.
- An operator-runnable command script for the flux-admin umbrella covering:
  install dev binary, `my policy list/show/status/accept`, acceptance PR and
  report, `my record add` round trip, `my governance audit --json`.
- Revocation drill runbook: disposable private repositories, second identity,
  baseline activation, revocation, quarantine verification, recovery-capsule
  restore, dirty/untracked/ahead/session fixtures, ambiguous 404/SSO handling,
  and no-purge proof — executed before any real monitor activation.

### Release gate (step 8)

- All S1–S5 merged with green CI; S6 merged or explicitly deferred; S7 docs
  accurate; S8 dogfood and drill evidence recorded.
- Then: plan statuses updated, `CHANGELOG.md` + `site/changelog.md` stamped,
  release tagged per repository release process with policy/record governance
  explicitly labeled beta, staff-Flux enablement considered separately by the
  operator, and revocation still subject to its independent experimental gate.
