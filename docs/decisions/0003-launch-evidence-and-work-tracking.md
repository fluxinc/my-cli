# ADR 0003: A bound service owns goals; `my` records launch evidence

Status: **proposed**. Recorded 2026-11-09. The direction was agreed in design
discussion; this text has not yet been accepted by the maintainer. Number 0002
is reserved for the service-backed fleet record, which is in review on another
branch. Implementation: [launch attempts plan](../plans/2026-11-09-launch-attempts.md).

## Context

Organizations using `my` run many agent sessions across harnesses, checkouts
and machines. Work gets dropped between them: a session ends, its purpose and
its half-finished state live in a closed terminal, and nobody notices until
someone asks again. Two facts make this worse.

- Git is the only shared medium `my` has, and it is slow by design. Another
  person sees a change only after a publish and a pull. That is the right
  medium for knowledge and the wrong one for who is doing what right now.
- [ARCHITECTURE.md](../ARCHITECTURE.md) section 2 says goals belong in content.
  That holds for goals as statements of intent. It does not hold for goals as
  live commitments with an owner, a state and something they wait on.

## Decision

1. **Goals as live commitments, and the workflow around them, belong to a
   service the organization binds**, not to Git content and not to `my`. The
   service is the authority for ownership, waiting, acceptance and history.
   `my` may become a client of such a service through the data-binding
   mechanism; it does not become the tracker.
2. **`my` records local evidence that a harness was launched.** `my ai` already
   starts the harness and waits for it. It now writes a launch record: who and
   where, an optional opaque `--goal` reference, what nearby Git checkouts
   looked like before and after, and how the launcher's child ended if that
   was observed.
3. **A launch record is an observation, never a claim.** It does not say work
   finished, was published or is tracked. A clean Git tree says nothing about
   effects outside Git. Checkouts shared by several agents are observed, not
   attributed. Unknown is recorded as unknown.
4. **Liveness is one kernel lock per launch.** The launcher holds it for its
   whole life; a reader that cannot take it leaves the record alone. Nothing
   ever infers that a launch ended from the absence of a write.

## What this does not decide

The non-goals in ARCHITECTURE.md section 9 stand. This slice adds no
human-workflow verb and no daemon. Whether `my` should later publish launch
records to a bound service, read a briefing from one, or offer any second
integration surface is future work with its own decision.

## Alternatives

- **Track work in Git content.** Rejected: claims and status need one
  authority and minutes, not a publish and a pull.
- **Have agents write their own status.** Rejected as the primary mechanism:
  a record that depends on an agent's diligence is missing exactly when the
  agent stops mid-task. The launcher can record without it.
- **Close unfinished records on the next run.** Rejected: other launches may
  be alive, and a dead launcher can leave its harness running.

## Consequences

Launch records accumulate under `.my-cli/attempts/`; pruning is not part of
this slice. Native Windows records nothing, and says so, because no lock
implementation is verified there. The record's fields are chosen so a later
slice can map them onto a bound service without changing what was recorded.
