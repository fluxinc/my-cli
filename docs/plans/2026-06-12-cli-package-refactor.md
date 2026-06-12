# CLI package refactor

Status: shipped in v0.19.0.

## Context

Before v0.19.0, `internal/cli/cli.go` held nearly the entire command surface:
dispatch, app setup, usage text, command parsing, command implementations,
derived reconcile, content roots, admin helpers, and many shared utilities.
`internal/cli/cli_test.go` mirrored the problem by holding most CLI tests.

The package already had the right local pattern in `work.go` and `repos.go`.
The refactor extends that pattern across the rest of the command surface.

## Scope

This was a pure declaration-move refactor. It did not intentionally change CLI
behavior, output, flags, manifest semantics, sync policy, or release behavior.

The production package is now split by responsibility:

- `cli.go`: app core, dispatcher, top-level usage, version/update.
- `util.go`: shared flag, JSON, path, and small utility helpers.
- `init.go`, `launch.go`, `sync.go`, `refresh.go`, `doctor.go`, `setup.go`.
- Domain nouns: `meetings.go`, `support.go`, `fleet.go`, `record.go`,
  `customers.go`, `catalog.go`, `services.go`, `manifests.go`, `mounts.go`,
  `admin.go`, `skills.go`, plus existing `repos.go` and `work.go`.
- Shared content plumbing: `contentroots.go` and `qmd.go`.

The test package mirrors the split with per-domain `_test.go` files while
leaving shared helpers and cross-cutting tests in `cli_test.go`.

## Verification

The refactor was reviewed and tested as a behavior-preserving move:

- top-level declaration signatures were compared before and after the split;
- CLI test count was conserved after splitting tests;
- `gofmt`, `go vet`, `git diff --check`, and `go test ./...` passed;
- a built binary was smoke-tested through `init`, `setup`, `root`,
  `meetings add/list`, `doctor`, `sync --print`, `skills list`, and
  `work start/status`.

## Follow-Up

The larger files that remain (`admin.go`, `skills.go`, `doctor.go`, and
`mounts.go`) are cohesive enough for this release. Future work should extract
domain packages only when behavior changes create real reuse pressure.
