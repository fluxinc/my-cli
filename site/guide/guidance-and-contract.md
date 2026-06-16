# Guidance and Contract

Every umbrella has one generated instruction file for agents: `AGENTS.md` at
the umbrella root, with `CLAUDE.md` pointing at the same content where the
harness supports it. Both files are generated — never edit them directly. They
are rebuilt by `my setup`, refreshed by derived reconcile after manifest
changes, and checked for drift by `my doctor`.

## How AGENTS.md is composed

Generated guidance stacks five layers, in order:

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
   selected role (`my setup --role <id>`), from that role's
   `guidance_paths`.
5. **Domain notes** — per-data-binding norms from `data_bindings[*].guidance`,
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
  state, updated with `my fleet set` only for meaningful transitions.
- Publish the resulting content with `my sync --push`.

`my fleet get` reinforces this at the moment it matters: its human output
ends with a ready-to-run `my support add` command seeded with the customer
and every identifier from the fleet record.

## The organization contract

Organizations add their own binding rules on top. Each rule is one sentence;
longer material belongs in guidance fragments.

Inspect the rules in force:

```sh
my contract list [--manifest NAME] [--json]
```

```
acme	1	Continue an existing relevant support record or create a new dated record when working on any fleet member.
acme	2	Record decisions in the handbook before acting on them.
```

Edit the contract through the admin surface, which works against a maintainer
checkout of the manifest repo:

```sh
my admin contract add "Record decisions in the handbook before acting on them." --manifest-dir DIR
my admin contract remove 2 --manifest-dir DIR        # by list index
my admin contract remove "RULE TEXT" --manifest-dir DIR  # by exact text
```

Like every admin edit, this writes `manifest.json` locally and prints the
review-commit-push follow-up; the rule reaches teammates after the manifest
change is pushed and their workspaces reconcile (`my sync` or
`my manifests sync`). Validation rejects empty, multiline, and duplicate
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

- **One-sentence obligation every agent must follow** → `contract` rule.
- **Narrative context, playbooks, org background** → `agent_guidance.paths`
  fragment.
- **Role-specific instructions** → role `guidance_paths`, activated by
  `my setup --role`.
- **Norms for one data type, tied to its backing surface** →
  `data_bindings[*].guidance` domain notes.
- **Generic My AI workflow** → already in the baseline; if something generic
  is missing, it belongs upstream in the public CLI, not in your manifest.

## Drift and reconcile

Because contract rules change the composed bytes, the existing machinery
covers them with no extra steps: `my doctor` reports guidance drift after a
manifest change, `my doctor --fix` and `my sync` regenerate, and
`my manifests sync` reconciles derived state when the manifest cache
changes. Hand-written `AGENTS.md` files are never overwritten: generation
refuses to clobber a file it did not produce.
