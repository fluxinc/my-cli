# Launch attempts: local evidence of every `my ai` launch

Status: **active**. Decision: [ADR 0003](../decisions/0003-launch-evidence-and-work-tracking.md).

## What ships in this slice

`my ai` records one launch record per harness launch under
`.my-cli/attempts/<id>/` in the umbrella:

- `lock`: held by the launcher for its whole life. The kernel releases it when
  the launcher dies. The harness never inherits it.
- `record.json`: schema version 1, replaced atomically (temp file, then
  rename). Directories are `0700`, files `0600`.

The record holds the OS user and host, the harness, the working directory and
session id, an optional opaque `--goal` reference, the launcher's and child's
process identity (pid and start time), two observations of nearby Git
checkouts, and how the launch closed.

| Field | Meaning |
|---|---|
| `observed_exit` | Set only when the launcher saw its child end. Never invented. A child ended by a signal has code -1, as `my ai` has always reported it, plus the signal's name. |
| `closure_reason` | `exited`, `start_failed`, or `launcher_lost`. Absent while unfinalized. |
| `reconciled_at`, `child_state` | Set by a reader that found the launcher gone. `child_state` is `possibly_running` when pid and start time both match on the same host. The start time has one-second resolution, so this is plausible identity, not proof, and nothing should derive authority from it. It is `not_found` only when the start time differs, or when both `ps` and the kernel say there is no such process. Any other probe failure is `unknown`. |
| `before`, `after` | One local `git status` per checkout: branch, HEAD, dirty (tracked changes and untracked files), upstream present, ahead count. No fetch. No upstream means ahead is unknown. Failures are stored per checkout. |
| `local_change_observed` | True on positive evidence: a checkout that ends dirty or ahead, or whose HEAD, branch, dirty state or ahead count differs between the two observations. False only when every checkout has complete evidence at both ends and nothing differs. Absent when unknown. |

Observed checkouts are the umbrella root, its mounts, opted-in repos, and the
worktrees of the session the launch runs in. `my`'s own state directory is
excluded from the dirty check.

Read side:

- `my attempts list [--running|--unclosed|--local-changes] [--json]`.
  A record carrying any error (unreadable, incomplete, a checkout that could
  not be observed, a wait error) always shows, whatever the filter.
- One stderr notice before a launch when launches from the last seven days
  observed local changes. It is worded as history, and says no work-service
  tracking was established.
- A `my doctor` summary row, plus one bounded warning row counting the
  records that carry errors. Doctor inspects and never reconciles.

## Rules the implementation keeps

- A record whose lock is held is never modified by anyone but its launcher.
- A reader takes the lock before it reads the record it may finalize.
- `--print` creates no record, reconciles nothing and prints no notice.
- A resumed session is a new launch record with the same session id.
- Recording never changes what `my ai` returns. A failure to observe is stored
  in the record; a failure to save is one stderr line.
- No network call is added for the actor, the observations or reconciliation.
- Native Windows: `my ai` launches as before and records nothing;
  `my attempts list` says capture is unsupported there.

## Limits, stated plainly

- Before and after observations describe checkouts, not this launch's work.
  Agents sharing a checkout all observe each other's changes.
- "Ahead" is against the cached upstream ref, so it is evidence about this
  clone, not proof of what the remote holds.
- A harness that hands work to a background process and returns is recorded
  as exited. The record claims nothing about work that continues.
- Records are never pruned in this slice.

## Not in this slice

Publishing launch records to a bound work service, a goal noun, claims, and a
launch briefing. Those depend on a service that accepts sessions a person
starts, and get their own plan.

## Tests

Launcher-evidence tests, not an end-to-end proof of intent, effect and receipt:
two live launches in competing processes; launcher killed while its child
runs; launcher killed after its child ended; child recorded without a start
time; another host's record; the directory-then-lock-then-record race;
interrupted write; corrupt record; six concurrent reconcilers; start failure;
non-zero exit; a real child's pid and exit status; a real child ended by a
signal; a resumed session; `--print`; no upstream; untracked files;
unreadable checkout; a failed process probe; errors behind every list filter
and in doctor.
