# Design: Startup and Context Ergonomics

Status: converged design (Claude + Codex), public-safe. Ready for Go
implementation.

## Problem

An agent harness (Claude Code, Codex) only knows the Flux operating context when
it is launched from a directory that contains the generated `AGENTS.md`. Today
`flux onboard` already produces that context correctly: it resolves or creates
the umbrella, composes a managed root `AGENTS.md` from the embedded baseline plus
the manifest's `agent_guidance.paths` fragments, makes `CLAUDE.md` an alias of
it, syncs mounts, and installs skills. The live umbrella matches that output.

So generation is not the gap. Three ergonomic gaps remain:

- **Launch location.** Nothing helps a human or agent get *into* the umbrella
  before launching a harness. Starting in the CLI repo, the manifest repo, or a
  standalone product clone yields zero Flux context. This is the easiest and
  most common human error.
- **Silent drift.** `AGENTS.md` is generated once at onboard. If the manifest
  guidance fragments or the embedded baseline change later, the on-disk copy
  drifts with no signal. `flux doctor` does not detect this.
- **Missing orientation.** The baseline `AGENTS.md` explains the CLI and the
  directory layout but never tells the agent it is inside an umbrella, how to do
  product work without leaving it, that `CLAUDE.md` is a generated alias, or how
  to reorient if lost.

## Non-goals

- **No generated `AGENTS.md` in the manifest repository.** The manifest repo is
  a source repository, not an operating workspace. A one-time committed note
  there is a documentation choice for that repo's maintainers; `flux` does not
  own or generate it.
- **No per-product-subdirectory generated files.** Keep exactly one managed pair
  (`AGENTS.md` + `CLAUDE.md`) at the umbrella root. Per-subdir generated files
  multiply the staleness surface. Product context is handled by
  `flux root --product` and the umbrella orientation rule below. Revisit only if
  dogfooding shows agents are routinely launched directly inside product dirs.
- **No changes to `flux skills install`.** It stays skill-only and continues to
  point at `flux onboard` for guidance and mounts.

## Design

### 1. `guidance.Check` — the shared freshness primitive

`internal/guidance` already exposes `Compose(manifestRoot, doc)`, a pure
function returning the expected `AGENTS.md` bytes. Add:

```go
type CheckResult struct {
    AgentsPath string `json:"agents_path"`
    ClaudePath string `json:"claude_path"`
    Status     string `json:"status"`  // ok | stale | missing | unmanaged | alias-broken
    Message    string `json:"message,omitempty"`
}

func Check(root, manifestRoot string, doc manifest.Document) (CheckResult, error)
```

Semantics:

- `missing` — `AGENTS.md` does not exist.
- `unmanaged` — `AGENTS.md` exists without the `flux:generated` marker.
- `stale` — managed `AGENTS.md` exists but does not byte-equal `Compose(...)`.
- `alias-broken` — `AGENTS.md` is fine but `CLAUDE.md` is missing, is not the
  symlink to `AGENTS.md`, and is not a managed copy.
- `ok` — managed, byte-equal, alias intact.

Remedy in `Message` is always `run flux onboard` (or `--force` for `unmanaged`).
`Check` performs no writes. This is the single source of truth for both `doctor`
and `launch`.

### 2. `flux doctor` — surface drift

Wire `guidance.Check` into `doctorUmbrella` (`internal/cli/cli.go`). Add a
`doctorItem` named `guidance` under the umbrella section carrying the
`CheckResult` status and remedy. JSON consumers get it automatically through the
existing `doctorReport.Umbrella` slice. This makes drift visible — the "proof"
half of the original question.

### 3. `flux root [--product <id>]` — the path primitive

Print the resolved umbrella root to stdout and exit zero. With `--product <id>`,
print `<umbrella>/products/<id>` (resolving the product-under-umbrella path that
prevents agents from opening a standalone clone). Reuse `umbrella.ResolveRoot`
and the existing `--home`/`--manifest`/`--umbrella` resolution flags.

This composes with any shell and any harness:

```sh
cd "$(flux root)" && claude
cd "$(flux root --product sample-product)" && codex
```

It is also the agent's reorientation primitive: a confused agent runs
`flux root` / `flux doctor` to find where it should be.

### 4. `flux launch [harness] [--product <id>] [--onboard] [--print]`

Verify-then-exec. It never silently mutates state.

1. Resolve the target directory via the same resolver as `flux root`
   (honoring `--product`).
2. Run `guidance.Check` on the resolved umbrella.
3. If guidance is `stale` / `missing` / `unmanaged` / `alias-broken`:
   - default: print the problem and the remedy to stderr and exit nonzero —
     do **not** launch into stale context;
   - with `--onboard`: run the full `onboard` reconcile first, then continue.
4. Resolve the harness binary from a data-driven harness list (the same list
   used for skill install). No flag translation — extra args after the harness
   name pass through verbatim.
5. If the harness binary is on PATH: `exec` it with cwd set to the resolved
   directory.
6. If the harness binary is **not** on PATH: print the exact fallback line
   (`cd <dir> && <harness>`) to stderr and **exit nonzero**. It did not launch,
   so it must not report success.
7. `--print`: do not exec; print the resolved `cd <dir> && <harness>` line to
   stdout and exit zero. This is the guaranteed-success command printer for
   scripting, alongside `flux root`.

Rationale for verify-by-default rather than reconcile-by-default: a launch that
re-syncs mounts and clones on every start is surprising, touches the network,
and can fail for reasons unrelated to launching. Reconciliation is `onboard`'s
job; `launch` only guarantees you never start an agent against stale guidance.

### 5. Guidance content — split by audience

Launch *examples* are pre-launch information. By the time an agent reads
`AGENTS.md` it has already launched, so launch examples in the always-loaded
body are circular and add bloat. Therefore:

- **Pre-launch (human-facing):** put exact launch examples in `flux onboard`
  stdout, in `flux root --help` / `flux launch --help`, and in the README.
- **Post-launch (agent-facing):** add a short, stable orientation section to the
  baseline `AGENTS.md`:
  - you are inside a Flux umbrella; run agents from here;
  - for product work use `products/<id>` under *this* umbrella (via
    `flux mount add product:<id>` and `flux root --product <id>`), not a
    standalone clone;
  - `CLAUDE.md` is a generated alias of this file — do not edit either; change
    the baseline or manifest guidance fragments and re-run `flux onboard`;
  - if you are unsure where you are, run `flux root` or `flux doctor`.

Keep the baseline minimal so it does not churn across versions.

## Build sequence (single bundle)

1. `guidance.Check` + unit tests (pure function; covers all five statuses).
2. `flux doctor` wiring + test.
3. `flux root [--product]` + test.
4. `flux launch [...]` + test (verify gate, `--onboard`, `--print`, missing-
   harness nonzero exit, arg pass-through).
5. Baseline `AGENTS.md` orientation section; launch examples in onboard stdout,
   `--help`, and README.

All steps land together. `go test ./...` and `go vet ./...` must pass. Tests use
neutral `acme` / `sampleco` fixtures only.

## Ownership

Design: Claude. Go implementation: Codex, after operator sign-off on this doc.
