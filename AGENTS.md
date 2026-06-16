# Repository Guidelines

## Project Structure & Module Organization

This repository builds the public `my` CLI. The executable entrypoint is
`cmd/my/main.go`; implementation lives in `internal/<package>/` with tests
co-located as `*_test.go`. Bundled agent guidance and the public self-skill live
in `internal/guidance/baseline/` and `skills/my/`. Public fixture data is under
`examples/acme-workspace/`. Long-form design notes and plans are in `docs/`.
The documentation site is a VitePress project in `site/`, with static assets in
`site/public/`.

## Build, Test, and Development Commands

- `go run ./cmd/my --help`: run the local CLI without installing it.
- `go build ./cmd/my`: build the CLI binary for the current platform.
- `go test ./...`: run the full Go test suite.
- `go vet ./...`: run Go static checks.
- `git diff --check`: catch whitespace errors before commit.
- `cd site && npm ci`: install the docs-site dependencies from the lockfile.
- `cd site && npm run dev`: serve the docs site locally.
- `cd site && npm run build`: produce the static docs build.
- Install a dev binary: `go build -ldflags "-X github.com/fluxinc/my-cli/internal/version.Version=<X.Y.Z>" -o ~/.local/bin/my ./cmd/my` (unstamped builds report `dev`; `my update` can hit anonymous GitHub rate limits).

## Coding Style & Naming Conventions

Use `gofmt` on Go changes and keep packages small, lowercase, and scoped under
`internal/` unless they are command entrypoints. Prefer standard-library Go; this
module currently has no third-party Go dependencies. Keep CLI output explicit
and agent-friendly, especially remediation text and JSON fields. Markdown should
be concise, public-safe, and free of organization-private names or operational
details.

## Testing Guidelines

Add focused Go tests next to the package being changed, using names like
`TestSyncPlansDirtyDuplicateCheckout`. Broaden tests when changing shared CLI
behavior, manifest parsing, guidance generation, skill installation, or sync
policy. For docs-site or Markdown changes, run `cd site && npm run build` when
navigation, frontmatter, or rendered content might be affected. Smoke-test
publish/sync flows in a /tmp sandbox with a stub `gh` on PATH that creates
local bare repos — never against real GitHub.

## Commit & Pull Request Guidelines

History uses short, imperative commit subjects such as `Add bundled my
self-skill and installation` or release subjects like `Release v0.4.0: ...`.
Keep commits scoped, include tests or verification in the PR description, and
link issues or plans when applicable. Add screenshots only for visible site/UI
changes. Do not add agent signatures or co-author trailers to commit messages.

## Releasing

Cut `vX.Y.Z` in one commit: stamp `## Unreleased` → `## X.Y.Z - YYYY-MM-DD` in
**both** `CHANGELOG.md` and `site/changelog.md`, bump the nav version in
`site/.vitepress/config.mts`, commit `Release vX.Y.Z: ...`, `git tag vX.Y.Z`,
then push `master` **and** the tag. Pushing a `v*` tag runs `release.yml`
(goreleaser → platform tarballs + GitHub release); pushing `master` under
`site/**` runs `deploy-site.yml` (GitHub Pages at https://my-cli.com/).
The binary version comes from the git tag via goreleaser ldflags
(`internal/version.Version`); the `VERSION` file is vestigial — do not bump it.
This repo is plain Git, not a Gnit workspace — `gnit`/`.gnit` do not apply here.

## Roadmap & Plans Upkeep

README.md carries a Roadmap section (brief phases + links to detailed plans).
Whenever a release ships, a plan's status changes, or direction changes, update
the README Roadmap and the status index in `docs/plans/README.md` in the same
commit. Never let them drift from reality.

## Agent-Specific Instructions

This workspace uses Talking Stick (`tt`) coordination with other agents. Hold
the stick (`tt wait`) before shared edits, commits, or releases.

This public repo must remain generic mechanism code. Do not commit private
manifest content, customer names, meeting notes, or company-specific skills.
