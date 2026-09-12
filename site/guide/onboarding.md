# Onboarding

`my` has two setup-shaped commands with different jobs:

- `my onboarding` is the guided first-run experience. In an interactive
  terminal it launches a harness and starts a learn-by-example walkthrough. The
  assistant runs routine validated `my` commands while explaining, and pauses
  for authentication, privileges, policy acceptance, and meaningful decisions.
  A manual walkthrough is available when the operator wants to practice.
- `my onboarding --no-agent` is the deterministic walkthrough. It explains the
  model and points at `my setup --interactive` without launching a harness.
- `my setup` is the machine configurator. It remains deterministic and safe for
  scripts; add `--interactive` only when you want prompts.

`my onboard` remains available as a compatibility alias.

## First Run

Install and authenticate a supported agent first, then use the stable installer:

```sh
curl -fsSL https://my-cli.com/install.sh | sh
```

For an existing organization, the same command can register the manifest before
the guide starts:

```sh
curl -fsSL https://my-cli.com/install.sh | sh -s -- --manifest acme <git-url>
```

The installer requires neither Go nor Node and persists `~/.local/bin` in the
shell profile. The commands below remain available to resume or revisit the
guide directly.

```sh
my onboarding
```

When a single logged-in harness is detected, `my` launches it. If none is
logged in but exactly one supported harness is installed, `my` launches that
harness. If the choice is ambiguous, `my` asks which harness to use. Pass
`--harness codex`, `--harness claude-code`, `--harness opencode`,
`--harness antigravity`, `--harness grok`, or `--harness cursor` to skip
detection.

The assistant starts in a fresh temporary workspace with the bundled My CLI
skill. This happens before Git, `gh`, manifest sync, or setup is required, even
when resuming with a cached manifest. The temporary directory is retained for
scratch files and harness conversation resumption. My does not enter the umbrella
or materialize organization skills/MCP here; global harness settings still apply.

The model greets the operator, explains each meaningful step, runs routine local
commands, inspects the results, and retries recoverable failures. The operator
handles login, privilege prompts, policy acceptance, and material choices. The
model never asks for passwords in chat or accepts policies on the operator's
behalf. Optional tools remain opt-in. Ask for a manual walkthrough to type and
practice commands instead.

The flow adapts to local state:

- **AUTHOR:** no manifest is registered. The assistant confirms the intent to
  create an organization before running `my init`, then verifies local setup.
- **JOIN_BOOTSTRAP:** a manifest is registered but not cloned. The assistant
  resolves missing Git/GitHub CLI, guides authentication, and runs
  `my manifests sync <name>` before setup.
- **JOIN:** a readable manifest exists. The assistant helps choose a role,
  configures the workspace, resolves required tools, and verifies with doctor.
- **JOIN_REPAIR:** the local checkout cannot be read. The assistant preserves
  recoverable state, diagnoses the failure, and retries the named continuation.

Normal organization work starts through `my ai`, with its existing access and
policy gates. The guide teaches that first launch, work sessions, sync, and
publication previews after setup. Moving onboarding earlier does not bypass
those checks.

When the operator confirms the walkthrough worked, the guide finishes with
`my onboarding --complete` and verifies `my onboarding --status`. Completion is
local state only; it does not publish, accept policy, or install anything, and
it is refused while a required tool is missing. The stable installer uses this
state to resume interrupted onboarding while keeping completed updates quiet.

Onboarding deliberately avoids teaching the full CLI. For meeting transcripts,
fleet/support context, notes, screenshots, or issue details, the human should
paste the raw context into the harness chat. Agents are the primary operators
and can choose the right deeper `my` commands when records need to be created.

The launcher does not publish anything. Publication remains a separate step:
the assistant shows `my publish --print` and obtains explicit approval before
running `my publish`.

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
