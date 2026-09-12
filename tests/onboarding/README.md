# First-run container matrix

Run the public-safe onboarding stories in fresh Ubuntu containers:

```sh
tests/onboarding/run.sh
```

The default matrix covers `linux/arm64` and `linux/amd64`. Every scenario gets a
new container and a new home directory. Fixtures use local bare Git repositories
and fake harness/provider adapters; the matrix never reads a private manifest or
writes to GitHub.

The scenarios cover agent-backed AUTHOR and JOIN through setup, required and
optional tool decisions, doctor, root, first launch, and explicit completion;
same-URL continuation after missing prerequisites; HTTPS provider checks and
global Git-config preservation; durable PATH; manifest conflicts; corrupt-state
repair; the deterministic no-agent path (installer `--no-onboarding`, `my setup`
self-heal of an unsynced manifest, doctor `prereq` rows, `my onboarding
--no-agent`); and wrapper download failure.

Raw console logs and structured per-command JSONL are generated under
`transcripts/` and ignored by Git. `transcripts/summary.json` is the small,
reviewable result index retained with the candidate.

Override the matrix when diagnosing one seam:

```sh
MYCLI_ONBOARDING_PLATFORMS=linux/arm64 \
MYCLI_ONBOARDING_SCENARIOS='join resume' \
tests/onboarding/run.sh
```

The `minimal-join`, `minimal-cached`, and `minimal-governed` scenarios remove
Git and GitHub CLI before the installer starts. The harness adapter asserts both
are absent at entry, supplies the offline dependencies, and completes setup,
required-tool remediation, doctor, first-launch adapter, and tour state. Every
scenario runs with Docker networking disabled; the GitHub/provider fixtures
never send credentials or writes to a real provider. These are command-contract
tests, not proof of a live model conversation or human acceptance.

For actual terminal descriptor/event-reader coverage on macOS and Linux:

```sh
go build -o /tmp/my-terminal-candidate ./cmd/my
python3 tests/onboarding/terminal.py --binary /tmp/my-terminal-candidate
```

This uses the public wrapper and canonical installer with offline downloads,
a real My binary, a sparse PATH without Git/GitHub CLI, and a harness that
registers terminal input with kqueue/epoll and reads an operator response.
It covers piped/direct installation, output redirection (including write-only
terminal outputs), unavailable `tty`, no terminal, and a crashing harness with
explicit recovery choices. The both-outputs-logged case checks event registration;
the visible-terminal cases also read an operator response. Python is a test dependency only.

Red control: in an isolated scratch checkout, keep the current `terminal.py` and
candidate binary but replace `install.sh` with its `v0.41.1` contents. The `pipe`
case fails on macOS at kqueue registration with EINVAL. The current installer
passes. Do not replace the installer in a live shared checkout to run this check.
