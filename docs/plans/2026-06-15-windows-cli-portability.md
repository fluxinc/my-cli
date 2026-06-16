# Follow-up: Windows CLI portability

Status: **proposed** (backlog). Opened after v0.28.0 shipped Windows release
builds and the Windows `my update` fix.

## Context

v0.28.0 added `windows/amd64` + `windows/arm64` release archives and made
`my update` work on Windows (extract `my.exe`; replace the running binary via
rename-self-aside, validated on a real Windows host). That release is
deliberately scoped to the **updater and release packaging**.

It is **not** full Windows CLI portability. During the v0.28.0 windev
validation, `go test ./...` on Windows was red — but every failure was a
pre-existing test/fixture assumption unrelated to the updater patch. This note
captures those so they are not lost.

## Known Windows gaps (from the windev test run)

- **Unescaped Windows paths in JSON test fixtures.** Backslash separators in
  temp paths are written into JSON without escaping, producing invalid JSON in
  fixtures/assertions.
- **Unix path-string expectations.** Tests compare against hardcoded `/`
  separators or POSIX-style absolute paths instead of using `filepath`
  separators / normalizing before comparison.
- **Tests invoke `my` instead of `my.exe`.** Some end-to-end/CLI tests build
  or exec the binary by the bare name `my`, which does not resolve on Windows.
- **CLI print/quoting expectations.** Launch/command preview output (e.g. the
  `my ai --print` shell line) assumes POSIX shell quoting and path style.

## Scope when picked up

- Make the Go test suite pass on Windows: normalize path comparisons via
  `filepath`, escape paths in JSON fixtures, exec `my.exe` where needed.
- Decide and document the intended Windows behavior for command-preview output
  (`--print`) — POSIX-style vs `cmd`/PowerShell-style — rather than only
  matching the current POSIX assumption.
- Add a Windows job to CI so portability does not regress again.

## Not in scope

- The updater / release packaging (shipped in v0.28.0).
- `install.sh` is Unix-only; a Windows install path (script or instructions) is
  a separate question from test portability.
