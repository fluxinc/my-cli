# Policy at invocation

Status: converged 2026-07-31 — joint Claude (architect/reviewer) + Codex
(implementer/tester) debate complete; implementation owned by Codex with
Claude review before each commit

Operator goal: any invoked bot must have organization policy available/loaded
at invocation, plus a standing instruction to consult the policy when the
topic demands it. Plan together, debate, edge-case, reconcile with invocation
context and user stories, implement, test, optimize, test again.

## Current state (verified 2026-07-31)

- `my policy show <id>` prints digest-verified policy content
  (`verifiedPolicyBlob`); it fails closed on digest mismatch, so a bot can
  never read an unaccepted revision silently.
- `my ai` gates interactive launches on required-policy acceptance (S8b), and
  real non-interactive launches hard-fail missing acceptance
  (`internal/cli/launch.go` runs `reviewRequiredPolicies` on non-print
  paths); only `--print` is notice-only by design. The launched agent's
  context still contains no policy inventory and no instruction to consult
  policy: generated guidance renders `## Organization Contract` and is
  otherwise policy-blind.
- `manifest.Policy` already carries `id`, `title`, `mount`, `path`,
  `version`, `sha256`, `acceptance`, and `roles` (role scoping exists).
- `guidance.Compose(manifestRoot, doc)` receives the full manifest document —
  the governance block is available at composition time. `launchplan` owns
  the `my compile` projection; `my ai --print` emits stderr governance
  notices with stdout purity.

## User stories

1. An employee runs `my ai claude` on a governed umbrella. The agent's
   generated guidance names each applicable policy with a one-line summary
   and the exact command to read it, plus a binding consult contract. When
   the conversation turns to a covered topic, the agent reads the policy
   before acting and follows it.
2. An admin adds a policy through `my admin policy add`. After the next
   sync/setup regeneration, every subsequent launch prompts for acceptance
   (existing gate) and the new policy appears in guidance automatically. No
   human runs plumbing.
3. A harness consumes `my ai --print` or `my compile --role` JSON and
   receives the same policy inventory machine-readably.
4. A non-governed organization sees zero policy noise anywhere.

## Converged design

Each point below was debated independently by both agents and reconciled;
the resolutions are final for this slice.

1. **Guidance section.** Governed manifests render a compact, always-loaded
   `## Organization Policies` section: per policy — title, id, version,
   one-line summary (derived from `title` when `summary` absent), declared
   topics, and the exact digest-verifying read action
   (`my policy show <id>`) — followed by a binding consult contract: policy
   text is authoritative over other guidance; before acting on a covered
   topic, read every matching policy and follow it. Raw sha256 stays out of
   prose; digests live in machine projections and read-time verification.
2. **Topic triggers.** `manifest.Policy` gains optional `summary` (single
   line, length-capped) and `topics` (validated string list). Topics are
   consultation triggers, not enforcement selectors; admins declare them,
   employees never author them interactively. Fallback wording derives from
   `title` when absent.
3. **Pointer-only loading (resolved).** No `inline` manifest knob and no
   size threshold. Guidance carries the compact index plus the binding
   consult contract; exact bytes stay locally available through
   digest-verified `my policy show`. Rationale: token budget, duplication
   across harnesses and session worktrees, and stale-context risk all favor
   the pointer; the operator contract — "available/loaded at invocation"
   plus "refer to it when the topic demands it" — is satisfied by guaranteed
   local availability plus a consult contract, and the consult instruction
   only makes sense when consultation is an action. If an operator ever
   requires full text in model context, that becomes an explicit cost-choice
   knob later, never a hidden heuristic.
4. **Digest binding, fail-closed at launch.** The guidance section reflects
   the accepted version; before launch, `my ai` locally proves every
   applicable policy blob readable and digest-valid — no provider call, local
   hashing only. On drift, launch fails before the harness starts with one
   agent-actionable remediation and emits no stale context. `my policy show`
   continues verifying at read time.
5. **Surfaces with contract preservation.** (a) Generated guidance via
   `internal/guidance` covers all harnesses and session worktrees.
   (b) The launch projection and `my compile` gain role-scoped `policies[]`
   refs `{id, title, version, sha256, mount, path, summary, topics}` — refs
   only, no host command strings and no embedded content; compile proves
   policy-mount visibility. (c) `my ai --print` stdout stays a shell
   command with concise stderr notices; no new non-interactive gate is
   needed because real launches already hard-fail (see current state).
6. **Role scoping.** A selected role sees universal policies plus its own,
   never another role's, identically in guidance and compile; the guidance
   section names the selected role when one is active. Required policies
   gate acceptance; optional policies remain consultable.
7. **Zero noise.** Non-governed organization ⇒ no guidance section, omitted
   JSON fields, no notices, byte-identical behavior elsewhere.

## BDD acceptance matrix

1. Given an accepted applicable policy, when the employee runs `my ai`, then
   the harness starts with the compact consultation contract in guidance and
   the exact verified policy bytes available locally without employee
   commands.
2. Given a newly required policy, when the next interactive `my ai` runs,
   then it shows the policy once, records acceptance, refreshes context, and
   launches; decline or EOF does not launch.
3. Given a conversation on a covered topic, then the agent consults every
   matching policy before acting (contract documented in guidance and the
   walkthrough; exercised in dogfood).
4. Given role scoping, then a role sees universal plus its own policies and
   never another role's, identically in guidance and `my compile`.
5. Given a non-governed configuration, then guidance, compile, and launch
   remain byte-compatible with zero policy noise.
6. Given stale, missing, or digest-mismatched policy bytes, then launch
   fails before the harness with one agent-actionable remediation and emits
   no stale context.
7. Given a session start, join, or resume after a policy change, then
   session guidance is current before exec.
8. Given multiple policies with overlapping topics, then all matching
   policies are listed; text is never merged.

## Edge cases

- Multiple policies covering overlapping topics: list all; never merge text.
- Policy revised upstream but cache stale: launch-time digest proof fails
  closed with remediation; guidance regen on sync repairs the section.
- Policy removed/superseded: section regenerates; stale acceptance handling
  already exists in the acceptance layer.
- Session worktrees: guidance regen must reach `sessions/<id>` on start,
  join, and resume (existing refresh seam) so mid-session policy changes
  surface on the next resume.
- Harness without a launch-root guidance seam: baseline guidance path already
  covers; verify parity.
- Unaccepted required policy in interactive launch: existing S8b review flow
  runs first; the section renders only accepted-current content afterward.

## Implementation tasks (Codex implements TDD; Claude reviews before each commit)

1. Manifest schema: optional `summary`, `topics` on `Policy` (no `inline`);
   strict validation + tests (reject non-list topics, oversized or
   multi-line summary).
2. Guidance: `## Organization Policies` section renderer + consult contract;
   golden tests for governed, non-governed, role-scoped, selected-role
   naming, topic fallback.
3. Launch projection: role-scoped `policies[]` refs in launchplan and
   `my compile`; mount-visibility proof; parity tests against guidance
   content; `my ai --print` JSON parity.
4. Launch-time local digest proof: fail-closed check of every applicable
   policy blob before harness start; remediation text; tests for missing,
   drifted, and healthy blobs.
5. Session guidance: verify/extend regen on start/join/resume; test
   mid-session policy addition surfaces on resume.
6. Self-skill + site docs: consultation contract documented; changelog.
7. Dogfood: governed umbrella launch shows the section (read-only checks);
   agent-side consult flow exercised live (`my policy show` under the
   instruction); non-governed umbrella shows zero noise.

## Release

Ships in the next minor release together with the governance-core beta per
the release-boundary decision of 2026-07-31 recorded in
[2026-07-21-governed-organizations-completion](2026-07-21-governed-organizations-completion.md).
