# Governance <Badge type="warning" text="beta" />

Some organizations need more than shared context: they need binding policy,
proof of who accepted it, and agents that actually consult it. Governance is
the opt-in layer that adds exactly that — without adding a single command to
anyone's day.

If your manifest declares no governance, nothing on this page exists for you.
No sections in guidance, no prompts, no JSON fields, no notices. Zero noise.

## The employee experience: it's still just `my ai`

You launch the way you always launch:

```sh
my ai claude
```

If a required policy is new or changed since you last accepted it, the launch
pauses once. You see the exact policy document — the real bytes, verified
against the digest the organization published — and one question. Accept, and
your acceptance is recorded durably and the harness starts. Decline (or hit
Ctrl-D), and nothing launches and nothing is recorded.

That is the entire employee surface. No policy verbs to learn, no setup, no
plumbing. The detailed commands below exist for agents and admins.

## What your agents know

On a governed umbrella, generated guidance gains an
`## Organization Policies` section: each applicable policy's title, id,
version, a one-line summary, the topics it covers, and the exact command to
read it — plus a binding contract: **before acting on a covered topic, read
every matching policy and follow it.** Policy text is authoritative over
other guidance.

Agents read policy through a digest-verified command:

```sh
my policy show <id>
```

If the local bytes do not match the digest the organization published, the
read fails closed with remediation instead of showing stale text. The same
proof runs before every launch, so an agent never starts with a policy it
cannot verifiably consult.

The same inventory rides machine-readably in `my ai --print` and
`my compile` output as role-scoped `policies[]` references, so contained
runners and custom harnesses see exactly what interactive launches see.

## Publishing a policy (admins and their agents)

A policy is a document in a mounted content repo plus one manifest entry
binding its version to a digest. The authoring command proposes the manifest
change as an isolated PR — it never edits your synced workspace in place:

```sh
my admin policy add security-baseline \
  --title "Security Baseline" \
  --mount handbook --path policy/security-baseline.md \
  --version 2026-07 --acceptance required \
  --summary "How we handle credentials, access, and customer data." \
  --topic "credentials" --topic "customer data"
```

`--summary` and the repeatable `--topic` flags feed the agent guidance
directly: they tell every launched agent what the policy covers and when it
must be consulted. After the PR merges and umbrellas sync, the next `my ai`
on every machine shows the new document once and records acceptance before
launching. Scope a policy to a role with `--role`, or make it
consultable-but-ungated with `--acceptance optional`.

## The acceptance ledger

Acceptances are not local state. Each one is published as an append-only
attestation through a durable PR, which makes the ledger inspectable and
CI-enforceable:

```sh
my policy acceptances          # who has accepted what, at which version
my policy status               # this machine's view: current, pending, missing
```

Organizations that enforce acceptance in CI get a trusted-branch check:
a governed repository refuses changes from authors without a merged
attestation for the current required policies. Revoking an acceptance is an
append-only administrative supersession — history is never rewritten, and a
superseded acceptance permanently blocks re-accepting that same version.

## Honest limits

- **Governance is beta.** Policy, acceptance, durable publication, and
  policy-at-invocation are dogfooded and tested, and their file formats are
  append-only by design — but expect the surface to keep sharpening.
- **Automatic access revocation is experimental and off.** A separate,
  explicitly activated monitor can quarantine a workspace when provider
  access disappears. It is not enabled by governance, has to pass a
  dedicated revocation drill before we recommend it anywhere, and "immediate"
  removal always means *immediately after confirmed detection* — detection
  latency is bounded by the monitor interval and confirmation policy.
- **Role-scoped CI enforcement is limited.** Until manifests carry an
  authoritative identity-to-role mapping, CI enforces universal policies;
  role-scoped policies gate locally at launch.

## Where to go next

- [Guidance and Contract](/guide/guidance-and-contract) — how the policy
  section composes with the rest of AGENTS.md.
- [Admin](/guide/admin) — the manifest control plane that policy entries
  live in.
- [Sync, Doctor, and Updates](/guide/sync-and-doctor) — how policy changes
  reach every umbrella automatically.
