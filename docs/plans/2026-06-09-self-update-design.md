# `flux update` self-update + new-release notice — design

Status: design / proposed. Authored by Claude; Codex implements; both test thoroughly.
Operator decisions: automatic behavior = **non-blocking notice** (not a prompt);
managed installs = **refuse + guide**.

## Goal

1. `flux update` — replace the running binary with the latest GitHub release
   (or a pinned `--version`), verified against `checksums.txt`.
2. An automatic, non-blocking notice when a newer release exists, surfaced on
   routine commands — agent/script-safe.

## Constraints (non-negotiable)

- **Zero third-party Go deps** — stdlib only (`net/http`, `crypto/sha256`,
  `archive/tar`, `compress/gzip`, `encoding/json`, `os`, `runtime`).
- **Agent/script-safe**: the notice goes to **stderr only**, never blocks, never
  pollutes stdout (`$(flux root)` stays path-pure), and is opt-out.
- **Reuse proven patterns**: TTL-gated + best-effort, like `maybeAutoRefresh`.

## A. `flux update [--check] [--version X.Y.Z] [--json] [--yes]`

### Resolve
- `GET https://api.github.com/repos/fluxinc/flux/releases/latest` → `tag_name`
  (e.g. `v0.5.0`); strip leading `v`. `--version X.Y.Z` targets a specific
  release tag (pin/downgrade). Base URL injectable for tests.
- Compare to `internal/version.Version` with a small stdlib semver compare
  (numeric major.minor.patch; pre-release suffix sorts lower). If current ≥
  latest and no `--version`: print `already up to date (0.5.0)`, exit 0.

### Download + verify (security-critical)
- Asset name: `flux_<ver>_<runtime.GOOS>_<runtime.GOARCH>.tar.gz`
  (goreleaser `{{.Os}}/{{.Arch}}` == GOOS/GOARCH; darwin/linux × amd64/arm64).
  Missing asset for this os/arch → clear error.
- Download the tarball **and** `checksums.txt` from
  `releases/download/<tag>/<asset>`. Compute sha256 of the tarball; it MUST match
  its line in `checksums.txt`. **Abort on mismatch** (no replace).
- Extract the `flux` entry from the gzip+tar into a temp file in the **same
  directory** as the target (needed for atomic rename), mode 0755.

### Atomic self-replace
- `target` = `filepath.EvalSymlinks(os.Executable())`.
- **Managed/writable guard (refuse + guide):** refuse when the target or its dir
  is not writable, OR the path matches a package manager: Homebrew (`/Cellar/` or
  under `brew --prefix`), `go install` (under `go env GOBIN` / `GOPATH/bin`).
  Refusal message names the right command (`brew upgrade flux`,
  `go install github.com/fluxinc/flux/cmd/flux@latest`, or re-run `install.sh`).
  Primary signal = write permission; path heuristics only sharpen the message.
- `os.Rename(temp, target)` — atomic on the same filesystem; replacing a running
  binary is safe on POSIX (open inode survives). Targets are darwin/linux only.
- Report `updated flux 0.5.0 -> 0.6.0` (stdout; `--json` structured). `--check`
  reports latest-vs-current and exits without downloading. `--yes` reserved for a
  future confirm; v1 is non-interactive (explicit command = intent).

## B. Automatic new-release notice

- **Trigger**: alongside `maybeAutoRefresh` (flux `root`, `launch`, `onboard`),
  plus a `version` item in `flux doctor`. One stderr line:
  `a newer flux (v0.6.0) is available — run \`flux update\``.
- **Cache (USER-level, NOT umbrella `.flux/`)**:
  `~/.local/share/flux/update-check.json` (`resolveHome` + share base) holding
  `{schema_version, last_checked RFC3339, latest_version}`. The binary is
  per-user and the check can run outside any umbrella.
- **TTL**: default 24h (`FLUX_UPDATE_CHECK_TTL` override). Within TTL → use cached
  `latest_version`, no network. Stale → fetch latest tag (short ~3s client
  timeout), refresh cache.
- **Best-effort**: any network/parse/cache error → silently skip; never fail or
  block the host command.
- **Opt-out**: `FLUX_NO_UPDATE_CHECK=1` env + `--no-update-check` flag on the
  trigger commands. Independent of `FLUX_NO_AUTO_REFRESH` (distinct concern).
- Notify only when `latest > current`.

## Security

- sha256 `checksums.txt` verification mandatory; abort on mismatch. HTTPS only.
  No code execution from the download (extract a binary, atomic rename). Signature
  verification is out of scope until goreleaser signing exists.

## Testing (thorough — operator emphasis)

Inject a seam: a release source (fetch-latest / download-asset) backed by an
`*http.Client` + base URL, defaulting to real GitHub, overridden in tests with
`httptest.Server` serving a fake tarball + `checksums.txt`. Make the target path
and share/home dir injectable.

Cover: version compare (`<`,`=`,`>`, pre-release, malformed); update happy path
(asserts the target file's bytes are the new binary); **checksum mismatch aborts,
target unchanged**; missing os/arch asset; `--check` (no replace); `--version`
pin; **managed/non-writable install refuses with guidance, target unchanged**;
notice TTL gating (recent cache → no network; stale → fetch); opt-outs (env +
flag) skip; best-effort (network error → silent, command still succeeds);
**`flux root` stdout == path only even when a notice prints**; notice only when
`latest > current`; corrupt cache tolerated.

## Files

- `internal/selfupdate/` (new, stdlib only): resolve, download, verify, extract,
  replace, version-compare, install-method detection.
- `internal/cli/cli.go`: `runUpdate`, `maybeUpdateNotice`, doctor `version` item,
  flag wiring on root/launch/onboard.
- Lockstep: `skills/flux/SKILL.md` (flux update + notice + opt-outs), `README.md`
  (self-update now exists; install.sh re-run still works), `site/guide/*`,
  `CHANGELOG.md` + `site/changelog.md` (Unreleased). `.goreleaser.yml` /
  `install.sh` unchanged.

## Out of scope (YAGNI)

Pre-release channels, signature verification, Windows, auto-applying updates
without `flux update`, interactive prompts.
