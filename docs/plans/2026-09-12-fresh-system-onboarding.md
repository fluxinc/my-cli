# Reliable fresh-system onboarding

Status: shipped in v0.42.0.

## Contract

The operator installs and authenticates a supported agent harness first. The
one-line installer then needs only network access, a writable home, a terminal,
and the supported system's shell/download/archive/checksum utilities. Git and
GitHub CLI are workspace prerequisites that the assistant helps install, not
requirements for starting the assistant. Supported installer targets remain
macOS and Linux on arm64/amd64; Windows uses the documented WSL path.

The assistant executes routine local steps while teaching. Login, privilege
prompts, policy acceptance, meaningful configuration choices, destructive work,
and publication remain human boundaries. A manual teaching mode remains an
operator preference, and `--no-agent` remains deterministic.

## Evidence and decisions

Two separate failures were reproduced:

1. On macOS, inherited terminal stdin works with Codex 0.154.0, while reopening
   `/dev/tty` produces its exact `reader source not set` panic. The controlling
   terminal alias rejects kqueue read registration with EINVAL; the actual
   terminal device accepts it. Reopening the actual device avoids the panic.
2. On Linux arm64, a cached governed manifest with no Git/GitHub CLI fails with
   `gh_missing` before the assistant starts. The old valid-checkout path used
   normal launch with setup; the missing-checkout path already bootstrapped.
   The same fixture completes with the neutral launch candidate.

Explicit onboarding now uses a fresh temporary workspace on every branch and
retry, retaining it for scratch files and harness conversation resumption.
It does not enter the umbrella or materialize organization guidance, skills,
or MCP. Global harness configuration still applies; this is not a sandbox.
Home/manifest/umbrella selectors survive the cwd change. Normal `my ai` access,
freshness, policy, and organization skill checks are unchanged.

The installer preserves inherited terminal stdin, or obtains the actual terminal
from stdout/stderr before reconnecting piped input. No new package manager or
runtime is required. Missing terminal discovery gives an explicit resume step.

## Verification

- Native PTY tests run the public wrapper, canonical installer, real My binary,
  and an event-reading harness with Git and GitHub CLI absent. Cover pipe,
  direct invocation, redirected stdout, retained input with both outputs logged,
  absent `tty`, no controlling terminal, write-only output descriptors, and
  harness-crash recovery without an automatic manual-mode switch.
- Offline Linux arm64/amd64 matrix covers fresh, cached, governed cached, corrupt,
  resumed, and completed installs, required tool remediation, optional tool
  preservation, setup, doctor, first-launch adapter, and completion state.
- Go tests cover neutral launch before governed prerequisites, custom and
  relative umbrella paths, and unchanged normal policy gates.
- Full Go suite, vet, shell syntax/whitespace checks, and docs build.

The shell harness/provider adapters prove deterministic handoff and command
continuation, not a real model's judgment or human acceptance. Native Codex
startup spikes prove terminal initialization, not authenticated model responses.
Real operator authentication and successful first organization launch remain
required before the guide records the operator's completed tour.

## Verified candidate results (2026-09-12)

- 26/26 offline container scenarios passed across Linux arm64 and amd64.
- 8/8 native terminal scenarios passed on macOS arm64, independently repeated.
- 8/8 terminal scenarios passed on Linux arm64 with an independently rebuilt
  candidate in an offline container.
- Full `go test ./...`, `go vet ./...`, ShellCheck, shell syntax, whitespace,
  and VitePress build passed locally. CI includes the native terminal scenarios.
- Both the old-installer terminal failure and the old governed-bootstrap
  `gh_missing` failure have independent red controls and passing candidates.

Review converged on explicit retry/alternate-agent/manual choices after a
harness crash. Automatic manual fallback was rejected because it would replace
the requested agent-operated teaching flow. Duplicating output directly into
stdin was also rejected: an output descriptor can be a terminal and still be
write-only; reopening the real device yields readable input.
