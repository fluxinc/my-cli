# What is Our AI?

Our AI is a small Go CLI, `our`, that bootstraps AI agent workspaces from an
organization manifest.

It installs declared skills into supported AI harnesses, creates a local
umbrella workspace, writes generated root guidance, syncs content mounts, and
reports missing tools. The result is a repeatable operating envelope for agents
on a fresh machine.

## The problem

AI harnesses drift. A team may use Claude Code, Codex, OpenCode, and Gemini,
but each surface has its own skill location, project context rules, and local
setup habits. Without one source of truth, agents see different knowledge and
different capabilities.

`our` makes the setup deterministic:

```sh
our setup --manifest acme
```

The command converges local state. Re-run it when the manifest changes.

## What our owns

- Manifest registration and sync.
- Harness skill materialization.
- Umbrella workspace creation.
- Generated agent guidance.
- Git-backed content mounts.
- Product and customer catalog inspection.
- Meeting-note operations.
- Tool diagnostics and install hints.

## What our does not own

- Private organization knowledge in this public repo.
- Silent installation of external tools.
- Human workflow state such as approvals or assignments.
- A daemon, MCP server, or second API surface.

The CLI is the mechanism. The manifest repo is the source of truth.
