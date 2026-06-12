# Guidance and Contract

Every umbrella has one generated instruction file for agents: `AGENTS.md` at
the umbrella root, with `CLAUDE.md` pointing at the same content where the
harness supports it. Both files are generated — never edit them directly. They
are rebuilt by `our setup`, refreshed by derived reconcile after manifest
changes, and checked for drift by `our doctor`.

## How AGENTS.md is composed

Generated guidance stacks four layers, in order:

1. **The public baseline** — ships inside the CLI binary. It teaches any agent
   the workspace layout, the operational command surface, and the built-in
   fleet work contract (below). Every umbrella gets it with no configuration.
2. **The organization contract** — short, binding rules from the manifest's
   `contract` list, rendered as an `## Organization Contract` section. Present
   only when the manifest declares rules.
3. **Manifest guidance fragments** — longer narrative content from
   `agent_guidance.paths`, each rendered as a `## Manifest Guidance: <path>`
   section.
4. **Role guidance fragments** — appended only when the local umbrella has a
   selected role (`our setup --role <id>`), from that role's
   `guidance_paths`.

The composition is deterministic: same manifest plus same selected role equals
byte-identical guidance. That is what lets `our doctor` and `our sync` detect
drift and regenerate safely.

## The built-in fleet work contract

The baseline carries one universal contract that connects the fleet registry
to support records:

- Before substantive work on a deployed instance, run
  `our fleet get <id|identifier>` so you start from the registry record and
  see related support history.
- Continue an existing relevant support record when one is listed, or create a
  new dated anonymized record with `our support add` for a distinct incident.
- Put the fleet record id and every useful identifier on the support record
  with repeated `--identifier` flags.
- Treat support records as the incident/work log; fleet records hold registry
  state, updated with `our fleet set` only for meaningful transitions.
- Publish the resulting content with `our sync`.

`our fleet get` reinforces this at the moment it matters: its human output
ends with a ready-to-run `our support add` command seeded with the customer
and every identifier from the fleet record.

## The organization contract

Organizations add their own binding rules on top. Each rule is one sentence;
longer material belongs in guidance fragments.

Inspect the rules in force:

```sh
our contract list [--manifest NAME] [--json]
```

```
acme	1	Continue an existing relevant support record or create a new dated record when working on any fleet member.
acme	2	Record decisions in the handbook before acting on them.
```

Edit the contract through the admin surface, which works against a maintainer
checkout of the manifest repo:

```sh
our admin contract add "Record decisions in the handbook before acting on them." --manifest-dir DIR
our admin contract remove 2 --manifest-dir DIR        # by list index
our admin contract remove "RULE TEXT" --manifest-dir DIR  # by exact text
```

Like every admin edit, this writes `manifest.json` locally and prints the
review-commit-push follow-up; the rule reaches teammates after the manifest
change is pushed and their workspaces reconcile (`our sync` or
`our manifests sync`). Validation rejects empty, multiline, and duplicate
rules, and `add` refuses a rule that already exists.

In the manifest the contract is a plain list of strings:

```json
{
  "contract": [
    "Continue an existing relevant support record or create a new dated record when working on any fleet member."
  ]
}
```

Rendered in `AGENTS.md` between the baseline and any guidance fragments:

```markdown
## Organization Contract

These rules are binding in this workspace:

- Continue an existing relevant support record or create a new dated record
  when working on any fleet member.
```

## Choosing the right layer

- **One-sentence obligation every agent must follow** → `contract` rule.
- **Narrative context, playbooks, org background** → `agent_guidance.paths`
  fragment.
- **Role-specific instructions** → role `guidance_paths`, activated by
  `our setup --role`.
- **Generic Our AI workflow** → already in the baseline; if something generic
  is missing, it belongs upstream in the public CLI, not in your manifest.

## Drift and reconcile

Because contract rules change the composed bytes, the existing machinery
covers them with no extra steps: `our doctor` reports guidance drift after a
manifest change, `our doctor --fix` and `our sync` regenerate, and
`our manifests sync` reconciles derived state when the manifest cache
changes. Hand-written `AGENTS.md` files are never overwritten: generation
refuses to clobber a file it did not produce.
