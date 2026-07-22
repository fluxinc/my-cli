# Policy at invocation

Status: draft — Claude skeleton 2026-07-22; Codex debate pending before any
implementation

Operator goal: any invoked bot must have organization policy available/loaded
at invocation, plus a standing instruction to consult the policy when the
topic demands it. Plan together, debate, edge-case, reconcile with invocation
context and user stories, implement, test, optimize, test again.

## Current state (verified)

- `my policy show <id>` prints digest-verified policy content
  (`verifiedPolicyBlob`); it fails closed on digest mismatch, so a bot can
  never read an unaccepted revision silently.
- `my ai` gates interactive launches on required-policy acceptance (S8b), but
  the launched agent's context contains no policy content and no instruction
  to consult policy. Generated guidance renders `## Organization Contract`
  and is otherwise policy-blind.
- `manifest.Policy` already carries `id`, `title`, `mount`, `path`,
  `version`, `sha256`, `acceptance`, and `roles` (role scoping exists).
- `guidance.Compose(manifestRoot, doc)` receives the full manifest document —
  the governance block is available at composition time. `launchplan` owns
  the `my compile` projection; `my ai --print` emits stderr governance
  notices with stdout purity.

## User stories

1. An employee runs `my ai claude` on a governed umbrella. The agent's
   generated guidance names each required policy with a one-line summary and
   the exact command to read it, plus a binding instruction. When the
   conversation turns to a covered topic, the agent reads the policy before
   acting and follows it.
2. An admin adds a policy through `my admin policy add`. After the next
   sync/setup regeneration, every subsequent launch prompts for acceptance
   (existing gate) and the new policy appears in guidance automatically. No
   human runs plumbing.
3. A harness consumes `my ai --print` or `my compile --role` JSON and
   receives the same policy inventory machine-readably.
4. A non-governed organization sees zero policy noise anywhere.

## Design position (Claude; each point OPEN for Codex debate)

1. **Guidance section.** Governed manifests render a generated
   `## Organization Policies` section: per policy — id, version, one-line
   summary, sha256, and `my policy show <id>` — followed by a binding
   instruction block: policy text is authoritative over other guidance; before
   acting on a covered topic, read the policy and follow it.
2. **Topic triggers.** `manifest.Policy` gains optional `summary` (one line)
   and `topics` (string list) so the instruction states *when* consultation is
   required. Fallback wording derives from `title` when absent. The operator's
   "when the topic demands it" is unimplementable without declared topics.
3. **Hybrid loading (sharpest debate point).** Full text is inlined into
   guidance only for small policies (threshold ~4 KB) or when the manifest
   marks a policy inline; larger policies ride as pointer + read command.
   Rationale: token budget, and a digest-verified read at consult time beats
   bake-time inlining that can go stale across cache refreshes. OPEN: does
   pointer + summary + instruction satisfy "loaded at invocation", or must
   full text always be in context?
4. **Digest binding.** The guidance section records the same version+digest
   the operator accepted; `my policy show` verifies at read time and fails
   closed with precise remediation when the cache blob drifts.
5. **Surfaces.** (a) Generated AGENTS.md via `internal/guidance` — covers all
   harnesses and session worktrees; (b) launch projection JSON gains
   `policies[]` `{id, version, sha256, summary, topics, command,
   inline_content?}` for `my ai --print` and `my compile`; (c) stderr notice
   parity for non-interactive launches stays as-is (S8b). OPEN: should
   non-interactive launches gain a harder gate than notice-only?
6. **Role scoping.** Respect existing `roles` semantics: a role-scoped launch
   renders only universal policies plus those matching the selected role.
7. **Zero noise.** No governance block, or an empty policy list ⇒ no section,
   no JSON field, no notices.

## Edge cases

- Large policy documents (threshold behavior; never truncate silently — a
  truncated policy is worse than a pointer).
- Multiple policies covering overlapping topics: list all; never merge text.
- Policy revised upstream but cache stale: freshness TTL + `my policy show`
  digest failure with remediation; guidance regen on sync repairs the section.
- Policy removed/superseded: section regenerates; stale acceptance handling
  already exists in the acceptance layer.
- Session worktrees: guidance regen must reach `sessions/<id>` on start,
  join, and resume (existing refresh seam) so mid-session policy changes
  surface on the next resume.
- Harness without a launch-root guidance seam: baseline guidance path already
  covers; verify parity.
- Unaccepted required policy in interactive launch: existing S8b review flow
  runs first; the section renders only accepted-current content afterward.

## Implementation tasks (owner decided after debate)

1. Manifest schema: optional `summary`, `topics`, `inline` on `Policy`;
   validation + tests (reject non-list topics, oversized summary).
2. Guidance: `## Organization Policies` section renderer + instruction block;
   golden tests for governed, non-governed, role-scoped, inline vs pointer.
3. Launch projection: `policies[]` in launchplan projection and `my ai
   --print` JSON; tests for parity with guidance content.
4. Session guidance: verify/extend regen on start/join/resume; test
   mid-session policy addition surfaces on resume.
5. Self-skill + site docs: document the consultation contract; changelog.
6. Dogfood: governed umbrella launch shows the section; agent-side consult
   flow exercised live (`my policy show` under the instruction); non-governed
   umbrella shows zero noise.

## Release

Ships as a minor release after joint review and dogfood; independent of the
governed-organizations completion gates (this is guidance/projection surface,
not new enforcement).
