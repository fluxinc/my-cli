# What is Our AI?

Our AI is a small Go CLI, `our`, that bootstraps AI agent workspaces from an
organization manifest.

It creates a local umbrella workspace, writes generated root guidance, syncs
content mounts, reports missing tools, and lets `our ai` compose declared
organization skills into each launch root. The result is a repeatable operating
envelope for agents on a fresh machine.

## The problem

AI harnesses drift. A team may use Claude Code, Codex, OpenCode, and Antigravity,
but each surface has its own skill location, project context rules, and local
setup habits. Without one source of truth, agents see different knowledge and
different capabilities.

`our` makes the setup deterministic:

```sh
our setup
```

The command converges local state. Re-run it when the manifest changes.

## What our owns

- Manifest registration and sync.
- Launch-scoped organization skill materialization.
- Umbrella workspace creation.
- Generated agent guidance.
- Git-backed content mounts.
- Product catalog inspection and mounted customer-record inspection.
- Customer, meeting-note, support-record, and fleet-registry operations.
- Opt-in isolated work sessions (`our work`, `our ai --new-session`), with
  session-aware content commands inside a session.
- Tool diagnostics and install hints.
- Best-effort TTL-gated auto-refresh of clean manifest/content checkouts at
  startup (tunable via `--no-refresh`, `OUR_NO_AUTO_REFRESH`, `OUR_REFRESH_TTL`),
  with stderr freshness notices for anything it cannot converge.

## What our does not own

- Private organization knowledge in this public repo.
- Silent installation of external tools.
- Human workflow state such as approvals or assignments.
- A daemon, MCP server, or second API surface.

The CLI is the mechanism. The manifest repo is the source of truth.
