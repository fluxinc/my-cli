# Release, Install, Update, and Site (clawdapus-style)

Status: in execution. Mirrors the clawdapus ergonomics for a Go CLI: GoReleaser
releases on tag, a `curl | sh` installer, and a VitePress GitHub Pages site.

## Goal

Make `flux` install/release/update as ergonomic as clawdapus, publish to GitHub,
and ship a VitePress GitHub Pages site under `site/`.

## Blueprint (from ~/dev/ai/clawdapus)

- `.goreleaser.yml` v2: build the CLI for linux/darwin x amd64/arm64, CGO off,
  ldflags inject the version, `tar.gz` archives + `checksums.txt`.
- `.github/workflows/release.yml`: on tag `v*`, run `goreleaser release --clean`.
- `install.sh`: `curl | sh` — detect os/arch, resolve the latest release tag via
  the GitHub API, download the tarball + checksums, verify sha256, install to
  `~/.local/bin`. Re-running it is the update path (clawdapus has no self-update
  subcommand).
- `.github/workflows/deploy-site.yml`: on push to the default branch touching
  `site/**`, build VitePress and deploy to GitHub Pages.
- `site/`: VitePress (`.vitepress/config.mts` + theme, `index.md` home hero +
  features, `guide/*.md`, `changelog.md`, `public/` favicons + optional CNAME,
  local search).

## Resolved decisions (Claude/Codex, defaults pending operator override)

- No `flux update` self-update subcommand in pass 1 — re-running `install.sh`
  updates, exactly like clawdapus. (Fast-follow candidate.)
- First release is `v0.2.0` (the skills/admin bundle).
- GitHub Pages default `fluxinc.github.io/flux`, no CNAME unless the operator
  supplies a custom domain.
- Publish (push + tag) only happens after cross-review on a clean tree. The task
  ("publish to github") is the push approval; flux-ai is the public repo.

## Split

- Claude (A–E infra): version wiring, `.goreleaser.yml`, `release.yml`,
  `install.sh`, then the publish (commit skills bundle, push, tag `v0.2.0`).
- Codex (F site): `site/` VitePress + `deploy-site.yml` + the README
  install/site snippet. README is the only overlap — Codex reconciles the
  install block after the site pass.

## Done (Claude, this pass)

- `internal/version/version.go`: `const Version` -> `var Version` so goreleaser
  injects the tag (`-X github.com/fluxinc/flux/internal/version.Version`).
  `bundle.Version()` already falls back to this var for goreleaser builds.
- `.goreleaser.yml`: flux build (`./cmd/flux` -> `flux`), before-hooks
  `go mod verify` / `go vet ./...` / `go test ./...`, archives
  `flux_{version}_{os}_{arch}.tar.gz`, `checksums.txt`.
- `.github/workflows/release.yml`: tag `v*` -> goreleaser.
- `install.sh`: `REPO=fluxinc/flux`, `FLUX_INSTALL_DIR`, suggests `flux doctor`.
- Verified: `go build ./...`, `go test ./...`, `go vet ./...` green;
  `flux version` -> `0.1.0`; `sh -n install.sh` clean. (`goreleaser` is not
  installed locally, so the config is validated against clawdapus's known-good
  shape and by CI on tag.)

## Done (Codex, this pass)

- Built `site/` VitePress docs with local search, GitHub social link, default
  `fluxinc.github.io/flux` base path, `v0.2.0` nav/changelog, guide pages, and
  public-safe Flux branding.
- Added `.github/workflows/deploy-site.yml` for GitHub Pages deployment on
  `master` pushes touching `site/**`.
- Added the README install block
  (`curl -sSL https://raw.githubusercontent.com/fluxinc/flux/master/install.sh | sh`)
  and docs-site link.
- Verified `npm ci && npm run build`, desktop/mobile screenshots via Playwright
  using system Chrome, static output link sanity, and public-content scan.
  `npm audit --audit-level=high` reports no high/critical findings, but prints
  the known Vite/VitePress dev-server moderate advisory with no fix available.

## Remaining

- Cross-review, then publish: commit the skills bundle + release/site infra, push
  `origin/master`, tag `v0.2.0` to fire the first release, and enable Pages
  (Settings -> Pages -> Source: GitHub Actions).

## Open questions for the operator

1. Confirm `v0.2.0` for the first tagged release.
2. Pages domain: default `fluxinc.github.io/flux`, or a CNAME?
3. `flux update` self-update subcommand now or as a fast-follow?
