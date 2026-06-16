# Onboarding

`my` has two setup-shaped commands with different jobs:

- `my onboarding` is the guided first-run experience. In an interactive
  terminal it launches a harness, introduces the operator to My AI, confirms the
  session with an `OK` handshake, then configures the workspace through
  validated `my` commands.
- `my onboarding --no-agent` is the deterministic walkthrough. It explains the
  model and points at `my setup --interactive` without launching a harness.
- `my setup` is the machine configurator. It remains deterministic and safe for
  scripts; add `--interactive` only when you want prompts.

`my onboard` remains available as a compatibility alias.

## First Run

```sh
my onboarding
```

When a single logged-in harness is detected, `my` launches it. If none is
logged in but exactly one supported harness is installed, `my` launches that
harness. If the choice is ambiguous, `my` asks which harness to use. Pass
`--harness codex`, `--harness claude-code`, `--harness opencode`, or
`--harness antigravity` to skip detection.

The launched model starts by greeting the operator and asking for `OK`. After
that handshake, it detects whether this machine is authoring a new organization
or joining an existing one:

- With no registered manifest, the harness starts from the current directory
  and takes the AUTHOR branch. The model interviews the operator, asks for
  approval before `my init`, then builds the local manifest/workspace through
  validated commands such as `my admin services add`, `my admin roles add`,
  `my setup`, `my doctor`, and `my compile`.
- With a registered manifest, the launcher reuses the normal
  `my ai --setup --no-session` path and takes the JOIN branch. The model helps
  pick a role, runs setup, pulls workspace content, and points the person at
  `my ai <harness>` for daily work.

If a selected launch skill already exists from a manual install or old
workspace rename, interactive onboarding asks whether to replace or skip that
entry and then continues launching the harness.

The launcher itself does not publish anything. The onboarding guidance keeps
publish at the end: the model must run `my publish --print`, show the planned
remotes and pushes, and get explicit human approval before the real
`my publish`.

## Deterministic Walkthrough

Use the non-agent path in scripts, CI, or terminals where you only want the
fixed setup review:

```sh
my onboarding --no-agent
```

If no manifest is registered, the walkthrough prints the registration command
to run once you have the manifest URL:

```sh
my manifests add <name> <git-url>
```

No umbrella-local tour state is written until setup has created or loaded the
umbrella. Once a manifest is available, the walkthrough explains the control
plane and data plane, shows what setup will change, and offers to run:

```sh
my setup --interactive
```

## Reconfigure Later

Use setup directly:

```sh
my setup --interactive
```

The interactive path can choose among registered manifests and select a role.
Enter `none` at the role prompt to clear the selected role and return to
unscoped guidance/services. Plain `my setup` never prompts, including on a TTY.

## Repeat Onboarding

Run `my onboarding` again when you want the model to re-introduce and review
the workspace with the operator. Use `my onboarding --no-agent` for a read-only
status review that reports the umbrella, selected role, and next commands
without silently re-running setup.
