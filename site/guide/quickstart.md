# Quickstart

Install the latest release:

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/my-cli/master/install.sh | sh
```

If the install directory is not on your path, add it:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Verify the binary:

```sh
my version
my doctor
```

`my doctor` reports manifest validity, generated guidance/MCP drift, legacy
global org-skill drift,
service materialization health, local Git freshness, and the last
`.my-cli/last-sync.json` audit when an umbrella is present. Add `--no-fetch` for
an offline freshness check, or `--fix` to fast-forward clean stale
manifest/content checkouts and reconcile derived guidance, MCP config, and
legacy global org-skill cleanup.

## Create an organization

```sh
my init acme --name "Acme"
```

`my init` creates two local repositories and registers the organization:

- a **private manifest repo** — the control plane (manifest, catalog, skills,
  agent guidance), kept at the registry path out of the workspace; admins
  change it through `my admin` commands;
- a **content repo** at `~/acme/workspace` — the actual workspace content:
  meetings, support records, fleet records, decisions, policy, people.

Everything works offline immediately and reports `local-only` until you
publish. When you're ready to share:

```sh
my publish
```

One command creates the two private GitHub repos (`acme-manifest` and
`acme-workspace`), points the manifest's mount at the published content repo,
pushes both, and prints the join command for teammates. Because the manifest
and the workspace are separate repos, you can restrict manifest pushes to
admins while the whole team pushes content.

If your team already has a manifest repo, register that instead:

```sh
my manifests add acme <git-url>
my manifests sync acme
```

Private GitHub manifests use your normal Git credentials. For HTTPS private
repos, make sure `gh auth login` (or your usual Git credentials) works before
running `my manifests sync` against a private repo.

## Onboard the workspace

```sh
my onboarding
# or: my onboarding --no-agent, then my setup
# my setup --manifest acme    # only needed when several manifests are registered
# my setup --role operator    # optional: select role-specific guidance/services
# my setup --interactive      # prompt for manifest/role choices
```

`my onboarding` launches guided onboarding in a harness when run interactively.
Use `my onboarding --no-agent` for the deterministic walkthrough: it explains
the model, offers to run `my setup --interactive`, and if no manifest is
registered yet, prints the `my manifests add <name> <git-url>` next step while
leaving the tour unmarked. Run `my setup` after the deterministic walkthrough
or whenever you want to converge the workspace without an onboarding
conversation.
Plain `my setup` stays deterministic and scriptable. With one registered
manifest, every command defaults to it. Setup is safe to re-run: it validates
the manifest, installs the bundled self-skill, creates the umbrella, writes
generated guidance, and syncs default content. Organization skills are composed
by `my ai` into the launch root. Opted-in catalog repo clones live under
`repos/<id>` in the umbrella.

## Start an agent

```sh
my ai codex
```

That's it: `my ai` verifies generated guidance and launches the harness from
the base umbrella. For isolated content work, use `my ai --new-session codex`
or create one with `my work start`; when the work is done,
`my work finish --land | --publish | --discard` is how it leaves the session.
Pass `--session <id>` to resume an active session, `--no-session` to ignore a
current session for base inspection or admin, `--print` to see the command
without executing it, or `--setup` to reconcile the umbrella first. Use
`my work status` or `my work list` to inspect active sessions; `my doctor`
also reports session health.

At startup, `my root`, `my ai`, and `my setup` print stderr-only `notice`
lines for checkouts auto-refresh cannot converge (dirty, ahead, behind, or
diverged), each naming the repository and the command to run, such as
`my sync` or `my doctor`. Stdout stays clean, so `cd "$(my root)"` is safe.

## Update my

Use the self-update command:

```sh
my update --check
my update
```

`my update` downloads the latest GitHub release, verifies the checksum, and
replaces the local binary. It refuses package-managed or non-writable installs
and prints the right follow-up command.

Re-running the installer still works as a fallback:

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/my-cli/master/install.sh | sh
```

The installer also refreshes the bundled `my` self-skill in existing harnesses
so agents keep current CLI guidance.
