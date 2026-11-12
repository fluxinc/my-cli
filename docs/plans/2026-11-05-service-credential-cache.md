# Service credential cache

Status: **shipped (v0.52.0)**; effective eligibility in cache status and doctor shipped in v0.53.0.

## Problem

`my` resolved an `op://` service key on every command, so an agent's routine
fleet lookups each ran `op read`. The manifest shape was right; the reuse was
missing. That 1Password then prompts on each of an agent's fresh shells is an
inference: its authorization is per terminal session, and how a harness's shells
map onto that varies.

## Design

- One lazy, per-user, in-memory broker (`my services auth broker`), started by
  the first command that needs it and gone after idling empty.
- The command that misses runs `op read` itself, where 1Password expects a
  prompt, and publishes the key. The broker never runs a credential helper.
- Keys are filed under manifest, service, URL, reference and resolved 1Password
  account. The account UUID is also passed to `op read`, so the two cannot
  disagree. Ambiguous accounts stay uncached, with a notice.
- Default on, because an opt-in would leave the complaint in place behind a
  setting nobody finds. Lifetimes are visible: 1 hour idle, 8 hours at most, 24
  hours the ceiling, and never past a personal key's recorded expiry.
- A cached key deliberately outlives a 1Password lock. The notice, status output
  and docs say so; there is no lock detection.
- Same-user is the broker's trust boundary. A 0700 directory with safe ancestors
  and peer credentials checked both ways keep other users out; nothing separates
  one process of a user from another. This is deliberately broader than
  1Password's own per-terminal approval, and the docs say so.

## Failure behavior

- Concurrent misses share one fetch. A waiter that times out errors and never
  runs `op`, so prompts cannot fan out.
- A refused or failed prompt sets a 30 second cooldown in the broker.
- `lock` advances an epoch and an unconditional invalidate removes the lease, so
  a fetch already under way cannot publish.
- A fetch publishes only on the connection that took its lease, so a broker
  restart also fences it.
- A 401 drops only the generation the command saw, refreshes once and retries
  reads only. Writes are never replayed; a 403 never refreshes.
- A lost broker gets one restart, then an actionable error naming `--no-cache`.
  There is no automatic direct fallback: it would restore the original prompts
  with no shared cooldown or single-flight, looking like the bug it replaces.
- The broker lock is held for the broker's whole life, and only its holder may
  unlink a leftover socket.

## Compatibility

No manifest change and no `min_my_version`. Older releases keep resolving on
every command. Direct mode is unchanged for `env://`, service accounts, Connect,
`--no-cache` and `MYCLI_NO_CRED_CACHE=1`.

## Limits

Windows runs in direct mode with a notice: per-user IPC there needs ACL work,
either AF_UNIX with verified permissions or a named pipe with an explicit
current-user ACL and remote clients rejected. This does not fix the prompts on
Windows.

## Verification

Stub `op` and stub service only: cross-process tests with the real binary and a
detached broker, plus in-process lifecycle tests with an injected clock. The
real 1Password prompt behavior needs one manual check by a person, since agents
must not drive it in CI.
