# Guidance and Contract

Every umbrella has one generated instruction file for agents: `AGENTS.md` at
the umbrella root, with `CLAUDE.md` pointing at the same content where the
harness supports it. Both files are generated — never edit them directly. They
are rebuilt by `my setup`, refreshed by derived reconcile after manifest
changes, and checked for drift by `my doctor`.

## How AGENTS.md is composed

Generated guidance stacks six layers, in order:

1. **The public baseline** — ships inside the CLI binary. It teaches any agent
   the workspace layout, the operational command surface, and the built-in
   fleet work contract (below). Every umbrella gets it with no configuration.
2. **The organization contract** — short, binding rules from the manifest's
   `contract` list, rendered as an `## Organization Contract` section. Present
   only when the manifest declares rules.
3. **Organization policies** — compact consultation pointers for universal
   policies plus policies matching the selected role. Each pointer carries
   title, id, version, summary, topics, and `my policy show <id>`; raw digests
   and policy bodies stay out of prose. Present only for governed manifests.
4. **Manifest guidance fragments** — longer narrative content from
   `agent_guidance.paths`, each rendered as a `## Manifest Guidance: <path>`
   section.
5. **Role guidance fragments** — appended only when the local umbrella has a
   selected role (`my setup --role <id>`), from that role's
   `guidance_paths`.
6. **Domain notes** — per-data-binding norms from `data_bindings[*].guidance`,
   rendered as labeled, source-attributed `## Domain Notes: <data type>`
   sections (below). Present only when a binding declares guidance fragments.

The composition is deterministic: same manifest plus same selected role equals
byte-identical guidance. That is what lets `my doctor` and `my sync` detect
drift and regenerate safely.

## The built-in fleet work contract

The baseline carries one universal contract that connects the fleet registry
to support records:

- Before substantive work on a deployed instance, run
  `my fleet get <id|identifier>` so you start from the registry record and
  see related support history.
- Continue an existing relevant support record when one is listed, or create a
  new dated anonymized record with `my support add` for a distinct incident.
- Put the fleet record id and every useful identifier on the support record
  with repeated `--identifier` flags.
- Treat support records as the incident/work log; fleet records hold registry
  state, updated only for meaningful transitions (`my fleet set` for markdown
  records, `my fleet transition` for service-backed ones).
- Publish the resulting content with `my sync --push`.

`my fleet get` reinforces this at the moment it matters: its human output
ends with a ready-to-run `my support add` command seeded with the customer
and every identifier from the fleet record.

## The organization contract

Organizations add their own binding rules on top. Each rule is one line;
longer material belongs in guidance fragments or governance policies. The
contract can live inline in `manifest.json` (`contract`) or, from v0.47.0, in a
contract file in the manifest repo (`contract_file`). A manifest uses one or
the other, never both.

### The contract file

```json
{
  "min_my_version": "0.47.0",
  "contract_file": "guidance/contract.md"
}
```

The file is a plain Markdown list, so a change reads as a one-line Git diff:

```markdown
# Organization contract

## Logs
- Service logs live under the install's `logs/` directory.

## Communication
- Answer technically able people in technical language.
- Answer clinical staff in clinical language.
```

- Each `- ` line is one rule. Rules must be single-line, non-blank, and
  unique.
- `## ` headings group rules. They are free-form, must be unique, and render
  as `### ` headings inside `## Organization Contract`. A heading with no rules
  renders nothing.
- One optional `# ` title may open the file. It names the file for people and
  is not rendered.
- Blank lines are allowed. Anything else (prose, indented or nested bullets,
  `###` headings) is a validation error.
- The path must be a `.md` file under `guidance/`, which already publishes with
  the manifest. Symlinks are refused.
- The contract lives in the manifest repo, not a content mount, so only people
  who can change the manifest can change it. Every member loads it, so keep it
  to material every member may see.

Validation and `my doctor` warn above 150 rules or 16 KiB, since every rule
loads into every session.

### Reading and editing

```sh
my contract list [--manifest NAME] [--json]   # rules with their position (and heading)
my contract show [--manifest NAME] [--json]   # the file as written, plus its Git blob id
```

```
acme	1	Continue an existing relevant support record or create a new dated record when working on any fleet member.
acme	2	Record decisions in the handbook before acting on them.
```

Edit through the admin surface:

```sh
my admin contract add "Rotate logs weekly." --under Logs
my admin contract remove 2                      # by list position
my admin contract remove "RULE TEXT"            # by exact text
my admin contract replace 3 "Service logs live under /var/log/acme."
```

- With a registered manifest (`--manifest NAME`, or the umbrella's manifest)
  each command opens one pull request from a clean cache at its trusted
  upstream and leaves the cache untouched. It re-fetches just before pushing
  and refuses if the upstream moved while the change was prepared.
- With `--manifest-dir DIR` it edits a maintainer checkout and never commits.
  It refuses if the files it touches changed while it was working.
- `replace` is a single edit, so there is never a moment with both rules or
  neither.
- `--under HEADING` adds to that group and creates the heading if it is
  missing. It needs a contract file.
- `--expect-blob SHA` (the `blob` from `my contract show --json`) refuses the
  edit if the file changed since you read it. A number that is also the text
  of a different rule is refused as ambiguous.
- Governed manifests require provider admin permission for every edit.
  Elsewhere, whoever can merge to the manifest's default branch can change
  the contract, so protect that branch.

A rule reaches teammates when the change merges and their workspaces sync:
regenerated `AGENTS.md` and every new launch carry it. Sync records the active
revision before writing derived files, shows a line diff on stderr, and includes
`contract_change` in JSON. Each umbrella and active session has its own installed
revision. A failed write or `--no-derived` leaves it pending; the next sync retries
without requiring another pull. Closed sessions leave the live root inventory.

```sh
my contract status [--manifest NAME] [--json]
my contract diff [--from REV] [--to REV] [--json]
```

These two commands read only local files and Git objects. Status distinguishes
unpublished local changes from the last observed active revision, and reports
the last successful remote check rather than claiming current connectivity.
Historical diffs resolve the manifest and its contract file at the same commit.
With no revisions specified, diff compares the previous active snapshot to the
active contract. Explicit `--from` and `--to` resolve local Git commits. State lives under
`~/.local/share/my-cli/state/contract/<manifest>.json`.

Guidance installation does **not** prove that a running harness read the update.
Live delivery uses a separate launch instance and an emitted snapshot for each
harness conversation. An emission receipt proves only that the CLI wrote the
complete update into the hook path, not that the harness accepted or followed it.

### Live contract delivery

New `my ai` launches register independently of workspace session IDs. The
adapters use Claude Code 2.1.310+ and Codex CLI 0.163.0+ hooks at session start,
prompt submission, and tool completion (also before tools in Codex). Startup,
resume, clear, compaction and fork deliver a full snapshot. Removed rules are
explicitly marked `RETRACTED`; removing the final rule emits an empty snapshot.
Each launch compares against its own emitted text even if it missed many updates.
A launch that resumes or forks a conversation by ID (`claude --resume ID`,
`codex resume|fork ID`) starts from what that conversation was last given, so a
fork still lists the rules retracted since. Every update says it is the complete
contract and supersedes any earlier contract text in the conversation.

Hooks read local state only. Run `my sync` or use the normal refresh path to
bring merged changes to the machine. This is context delivery, not a guarantee
that an already-issued action will reconsider a new rule.

Codex requires review of the exact hook definition with `/hooks`; the adapter
never bypasses that trust. Hook definitions stay stable across launches: instance
identity is passed through the child process environment. Codex's context limit
is disabled only for these hooks, whose output has a strict 64 KiB cap. Larger
updates remain `incomplete`; manually load the full contract before proceeding.

Existing settings remain in place. An explicit Claude `--settings` or Codex hook
configuration on the command line disables automatic adapter injection for that
launch, avoiding replacement of the caller's options. Native Windows and other
harnesses remain `unsupported` until verified. Already-running sessions cannot
acquire hooks retroactively; relaunch once, or manually supply the contract.

```sh
my contract status --json
my contract pending                   # local context; no emission receipt
my contract pending --hook codex       # native hook JSON on stdin
```

Status distinguishes `untrusted`, `stale`, `emitted`, `incomplete`, `unsupported`
and `closed` instances. It never labels a model as having accepted an update.
Commands inside a registered launch print pending changes on stderr as a fallback;
that output never advances the emission cursor. Unchanged fallback context is quiet.

Contained runtimes can register at actual process launch:

```sh
my contract register --manifest acme --harness codex --root /workspace
```

The JSON result supplies a launch ID and environment variables for that process.
The runtime must still install and trust a verified hook adapter. `my compile`
continues to carry contract content/revision without allocating an instance.
State schema 2 is introduced when a launch is registered; older clients refuse
that tracking state instead of rewriting it and losing emission receipts.

Closed launch records are kept for 30 days after their last hook. If a
long-idle process's record was pruned, or its conversation was reopened
without a session-start hook, its next hook gets a full authoritative snapshot
instead of an exact retraction list.

Live verification (v0.49.0, in a sandbox manifest):

- **Claude Code 2.1.310:** the hook delivered the contract at startup, on each
  prompt, and mid-run after a tool call that changed and synced the contract.
  Resume and fork marked the replaced rule `RETRACTED`. Compaction re-sent the
  full snapshot. Project and user hooks kept running alongside the generated
  `--settings` layer. Subagent hook calls carry `agent_id` and are skipped, so a
  subagent never consumes the parent's cursor.
- **Codex CLI 0.163.0:** until the hook definition is trusted, Codex skips it,
  the model gets nothing, and status stays `untrusted`. Your own
  `~/.codex/hooks.json` hooks still run beside the inline layer. In a test-only
  invocation with the trust bypass enabled,
  startup, prompt, and pre- and post-tool hooks delivered the contract, and a
  mid-run change arrived with its retraction. The adapter never enables that
  bypass. Normal `/hooks` trust persistence is verified from the
  [0.163.0 discovery source](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/hooks/src/engine/discovery.rs):
  session flags use a stable source path, and trust is matched by source,
  event, handler position and definition hash. The
  [TUI saves approvals](https://github.com/openai/codex/blob/rust-v0.163.0/codex-rs/tui/src/hooks_rpc.rs)
  in persistent `hooks.state`. This persistence check was source-based, not a
  live TUI approval test. Until you approve the definition, status remains
  `untrusted` and the stderr fallback remains available.

### Migrating an inline contract

```sh
my admin contract migrate [--manifest NAME | --manifest-dir DIR]
```

`migrate` writes the inline rules, in order and under no heading, to
`guidance/contract.md`. In the same change it removes `contract` and sets
`contract_file` and `min_my_version`.

- Only those keys change. Fields this `my` does not know, nested values and
  key order are kept, and a higher `min_my_version` is never lowered.
- It refuses if `guidance/contract.md` already exists.
- The generated `AGENTS.md` is byte-identical before and after.
- Inline `contract` stays supported.

Upgrade before migrating. `my` v0.46.x reads `min_my_version` and asks for
`my update`. Releases before v0.46.0 ignore both keys and launch without the
contract. Confirm every member runs v0.46.0 or later first, and `migrate`
prints this reminder.

### Rendering

Rendered in `AGENTS.md` between the baseline and any guidance fragments:

```markdown
## Organization Contract

These rules are binding in this workspace:

- Continue an existing relevant support record or create a new dated record
  when working on any fleet member.

### Logs

- Service logs live under the install's `logs/` directory.
```

`my compile` carries the same rules in its `contract` block. For a contract
file the block also records its `path`, its `sections`, and a `revision`
(manifest commit and file blob id). An emptied file still projects an empty
block.

## Organization policies

Governed manifests add a pointer-only policy index immediately after the
organization contract. Universal policies and policies matching the selected
role are listed; another role's policies are never exposed. Each entry tells
the agent when to consult the policy and gives the digest-verifying read action:

```markdown
## Organization Policies

Selected role: `operator`.

- **Release policy** (`release-policy`, version `2026-08`) — Rules for
  preparing and approving a release.
  Topics: releases, deployment approval. Read: `my policy show release-policy`.
```

The generated contract makes policy text authoritative over summaries and
requires every matching policy to be read before acting on a covered topic.
Real `my ai` launches also prove each applicable committed policy blob exists
and matches its declared digest before writing launch context or starting the
harness. See [Governance](./governance.md).

## Domain notes

A [data binding](./the-model) can attach domain-specific norms to the
surface that backs a data type. List markdown fragments under the binding's
`guidance` (paths relative to the manifest root):

```json
{
  "data_bindings": {
    "customers": {
      "surface": "mount:handbook",
      "guidance": ["agent-guidance/customers-domain.md"]
    }
  }
}
```

Each fragment renders into `AGENTS.md` as a labeled, source-attributed section:

```markdown
## Domain Notes: customers

_Source: mount:handbook_

Archive customer records instead of hard-deleting them; preserve identifiers
needed to reconcile prior meetings, support records, and fleet deployments.
```

Domain notes are deliberately **separate** from the organization contract:
the contract is the org's binding rules and is owned by `my contract`, while
domain notes are surface-contributed norms for one data type and are attributed
to the surface that supplies them. They never merge into the contract list, so
`my contract list` always reflects exactly what the org owns. This is how the
soft side of the customers story is expressed ("archive, never hard-delete")
without putting access control in the CLI — the backing surface still owns real
permissions.

## Choosing the right layer

- **One-line fact, convention, or obligation every agent must know** →
  contract rule (`contract` or `contract_file`).
- **Narrative context, playbooks, org background** → `agent_guidance.paths`
  fragment.
- **Role-specific instructions** → role `guidance_paths`, activated by
  `my setup --role`.
- **Versioned rules that must be consulted by topic and may require durable
  acceptance** → governance policy.
- **Norms for one data type, tied to its backing surface** →
  `data_bindings[*].guidance` domain notes.
- **Generic My AI workflow** → already in the baseline; if something generic
  is missing, it belongs upstream in the public CLI, not in your manifest.

## Drift and reconcile

Because contract rules and policy references change the composed bytes, the
existing machinery covers them with no extra steps: `my doctor` reports
guidance drift after a manifest change, `my doctor --fix` and `my sync`
regenerate, and
`my manifests sync` reconciles derived state when the manifest cache
changes. Hand-written `AGENTS.md` files are never overwritten: generation
refuses to clobber a file it did not produce.
