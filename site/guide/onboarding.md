# Onboarding

`my` has two setup-shaped commands with different jobs:

- `my onboard` is the human walkthrough. It explains the model, then offers to
  run setup interactively.
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
