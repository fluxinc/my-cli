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

## Register a manifest

```sh
our manifests add acme https://github.com/example/acme-workspace.git
our manifests sync acme
```

Private GitHub manifests use your normal Git credentials. For HTTPS private
repos, authenticate with `gh auth login` before a real fetch.

## Onboard the workspace

```sh
our setup --manifest acme
```

Onboarding is safe to re-run. It validates the manifest, installs declared
skills, creates the umbrella, writes generated guidance, and syncs default
content.

## Start an agent from the umbrella

```sh
cd "$(our root --manifest acme)"
claude
```

Or let `our` resolve and verify the launch point:

```sh
our ai --manifest acme codex
```

Use `--print` when you want the command without executing it:

```sh
our ai --manifest acme --print codex
```

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
