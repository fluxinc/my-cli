# What is My AI?

My AI is a small Go CLI, `my`, that bootstraps AI agent workspaces from an
organization manifest.

It creates a local umbrella workspace, writes generated root guidance, syncs
content mounts, reports missing tools, and lets `my ai` compose declared
organization skills into each launch root. The result is a repeatable operating
envelope for agents on a fresh machine.

## The problem

AI harnesses drift. A team may use Claude Code, Codex, OpenCode, and Antigravity,
but each surface has its own skill location, project context rules, and local
setup habits. Without one source of truth, agents see different knowledge and
different capabilities.

`my` makes the setup deterministic:

```sh
my setup
```

The command converges local state. Re-run it when the manifest changes.

## What `my` Owns

- Manifest registration and sync.
- Launch-scoped organization skill materialization.
- Umbrella workspace creation.
- Generated agent guidance.
- Git-backed content mounts.
- Product catalog inspection and mounted customer-record inspection.
- Customer, meeting-note, support-record, and fleet-registry operations.
- Opt-in isolated sessions (`my session`, `my ai --new-session`), with
  session-aware content commands inside a session.
- Tool diagnostics and install hints.
- Best-effort TTL-gated auto-refresh of clean manifest/content checkouts at
  startup (tunable via `--no-refresh`, `MYCLI_NO_AUTO_REFRESH`, `MYCLI_REFRESH_TTL`),
  with stderr freshness notices for anything it cannot converge.

## What `my` Does Not Own

- Private organization knowledge in this public repo.
- Silent installation of external tools.
- Human workflow state such as approvals or assignments.
- A daemon, MCP server, or second API surface.

The CLI is the mechanism. The manifest repo is the source of truth.
