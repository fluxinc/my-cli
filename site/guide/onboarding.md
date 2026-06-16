# Onboarding

`my` has two setup-shaped commands with different jobs:

- `my onboard` is the human walkthrough. It explains the model, then offers to
  run setup interactively.
- `my onboard --agent` launches a harness with bundled onboarding guidance so
  the model can run an adaptive AUTHOR or JOIN flow through validated `my`
  commands.
- `my setup` is the machine configurator. It remains deterministic and safe
  for scripts; add `--interactive` only when you want prompts.

## First Run

```sh
my onboard
```

If no manifest is registered, the tour prints the registration command to run
once you have the manifest URL:

```sh
my manifests add <name> <git-url>
```

No umbrella-local tour state is written until setup has created or loaded the
umbrella. Once a manifest is available, onboarding explains the control plane
and data plane, shows what setup will change, and offers to run:

```sh
my setup --interactive
```

## Agent-Operated Onboarding

Use model-driven onboarding when a person wants help creating the first control
plane or joining an existing one:

```sh
my onboard --agent --harness codex
```

With no registered manifest, the selected harness starts from the current
directory and takes the AUTHOR branch. The model interviews the operator, asks
for approval before `my init`, then builds the local manifest/workspace through
validated commands such as `my admin services add`, `my admin roles add`,
`my setup`, `my doctor`, and `my compile`.

When a manifest is already registered, the launcher reuses the normal
`my ai --setup --no-session` path and takes the JOIN branch. The model helps
pick a role, runs setup, pulls workspace content, and points the person at
`my ai <harness>` for daily work.

The launcher itself does not publish anything. The onboarding guidance keeps
publish at the end: the model must run `my publish --print`, show the planned
remotes and pushes, and get explicit human approval before the real
`my publish`.

## Reconfigure Later

Use setup directly:

```sh
my setup --interactive
```

The interactive path can choose among registered manifests and select a role.
Enter `none` at the role prompt to clear the selected role and return to
unscoped guidance/services. Plain `my setup` never prompts, including on a
TTY.

## Repeat Onboarding

After the tour is complete, `my onboard` becomes a read-only review: it reports
the umbrella, selected role, and next commands. It does not silently re-run
setup.
