# Quickstart

Install the latest release:

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/our-ai/master/install.sh | sh
```

If the install directory is not on your path, add it:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Verify the binary:

```sh
our version
our doctor
```

`our doctor` reports manifest validity, generated guidance/skill drift, local
Git freshness, and the last `.our/last-sync.json` audit when an umbrella is
present. Add `--no-fetch` for an offline freshness check, or `--fix` to
fast-forward clean stale manifest/content checkouts and reconcile derived
skills/guidance.

## Create an organization

```sh
our init acme --name "Acme"
```

`our init` creates two local repositories and registers the organization:

- a **private manifest repo** — the control plane (manifest, catalog, skills,
  agent guidance), kept at the registry path out of the workspace; admins
  change it through `our admin` commands;
- a **content repo** at `~/acme/workspace` — the actual workspace content:
  meetings, support records, fleet records, decisions, policy, people.

Everything works offline immediately and reports `local-only` until you
publish. When you're ready to share:

```sh
our publish
```

One command creates the two private GitHub repos (`acme-manifest` and
`acme-workspace`), points the manifest's mount at the published content repo,
pushes both, and prints the join command for teammates. Because the manifest
and the workspace are separate repos, you can restrict manifest pushes to
admins while the whole team pushes content.

If your team already has a manifest repo, register that instead:

```sh
our manifests add acme <git-url>
our manifests sync acme
```

Private GitHub manifests use your normal Git credentials. For HTTPS private
repos, make sure `gh auth login` (or your usual Git credentials) works before
running `our manifests sync` against a private repo.

## Onboard the workspace

```sh
our setup
# our setup --manifest acme    # only needed when several manifests are registered
```

With one registered manifest, every command defaults to it. Onboarding is safe to re-run.
It validates the manifest, installs declared skills, creates the umbrella,
writes generated guidance, and syncs default content. Opted-in catalog repo clones
live under `repos/<id>` in the umbrella.

## Start an agent

```sh
our ai codex
```

That's it: `our ai` verifies generated guidance, creates a fresh work session
under `work/<id>` (a git worktree per content mount, isolated from the base
umbrella), and launches the harness there. When the work is done,
`our work finish --land | --publish | --discard` is how it leaves the
session. Pass `--session <id>` to resume an active session, `--no-session`
to launch from the base umbrella for inspection or admin, `--print` to see
the command without executing it, or `--setup` to reconcile the umbrella
first.

At startup, `our root`, `our ai`, and `our setup` print stderr-only `notice`
lines for checkouts auto-refresh cannot converge (dirty, ahead, behind, or
diverged), each naming the repository and the command to run, such as
`our sync` or `our doctor`. Stdout stays clean, so `cd "$(our root)"` is safe.

## Update our

Use the self-update command:

```sh
our update --check
our update
```

`our update` downloads the latest GitHub release, verifies the checksum, and
replaces the local binary. It refuses package-managed or non-writable installs
and prints the right follow-up command.

Re-running the installer still works as a fallback:

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/our-ai/master/install.sh | sh
```

The installer also refreshes the bundled `our` self-skill in existing harnesses
so agents keep current CLI guidance.
