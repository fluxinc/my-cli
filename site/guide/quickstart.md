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

## Create a manifest

```sh
our init acme --name "Acme"
```

`our init` creates a small private manifest/handbook repo at
`~/acme-workspace`, commits it, registers it, syncs the manifest cache, and
prints the optional `gh repo create ... --private --source . --push` command
for publishing it later.

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
writes generated guidance, and syncs default content. Opted-in product clones
live under `repos/<id>` in the umbrella.

## Start an agent

```sh
our ai codex
```

That's it: `our ai` verifies generated guidance, then launches the harness
from the umbrella. Pass `--print` to see the command without executing it,
or `--setup` to reconcile the umbrella first. If you prefer plain shell, the
equivalent is `cd "$(our root)" && codex`.

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
