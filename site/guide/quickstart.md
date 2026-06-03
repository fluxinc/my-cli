# Quickstart

Install the latest release:

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/flux/master/install.sh | sh
```

If the install directory is not on your path, add it:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Verify the binary:

```sh
flux version
flux doctor
```

## Register a manifest

```sh
flux manifest add acme https://github.com/example/acme-workspace.git
flux manifest sync acme
```

Private GitHub manifests use your normal Git credentials. For HTTPS private
repos, authenticate with `gh auth login` before a real fetch.

## Onboard the workspace

```sh
flux onboard --manifest acme
```

Onboarding is safe to re-run. It validates the manifest, installs declared
skills, creates the umbrella, writes generated guidance, and syncs default
content.

## Start an agent from the umbrella

```sh
cd "$(flux root --manifest acme)"
claude
```

Or let `flux` resolve and verify the launch point:

```sh
flux launch --manifest acme codex
```

Use `--print` when you want the command without executing it:

```sh
flux launch --manifest acme --print codex
```

## Update flux

Re-run the installer:

```sh
curl -sSL https://raw.githubusercontent.com/fluxinc/flux/master/install.sh | sh
```

The installer downloads the latest GitHub release, verifies the checksum, and
replaces the local binary.
