# Commit pins for projected mounts in `my compile`

Status: **active**. Extends the
[compile launch plan](2026-06-14-compile-launch-plan.md).

## Why

A contained runner built from `my compile --role` output needs to know which
commit of each content mount it should get, so two runs of the same role see
the same content and a run can be reproduced. The projection carried the
mount's `git_url` and nothing about a revision.

## What ships

`my compile --role <id> --umbrella DIR` adds a `pin` to each projected mount,
read from that umbrella's base checkouts with local Git only.

```json
{ "id": "handbook", "git_url": "…", "pin": { "commit": "<full id>", "source": "umbrella" } }
```

Without `--umbrella` the output is byte-for-byte what it was, `compile_version`
stays 1, and no pin field appears.

### What a pin means

The commit was observed as HEAD of the mount's base checkout in the named
umbrella, and it is an ancestor of that checkout's **locally cached** upstream
ref. That is all. It does not say anyone else can fetch the commit: a
force-push, a deleted branch or a permission change can make it unfetchable.

The consumer's rule: materialize exactly that commit or fail. Never fall back
to a branch tip.

`"dirty": true` beside a pin discloses that the checkout had uncommitted
changes. They are not part of the commit and a consumer does not get them. It
is a best-effort observation taken beside the commit, not atomically with it.
`include_paths` and policy digests keep their existing meaning; a runtime
still verifies them.

### When there is no pin

The mount carries `"pin": {"unavailable": "<reason>"}`. A consumer treats that
as no pin, by its own policy.

| Reason | Meaning |
|---|---|
| `missing_checkout` | The mount is not cloned there, or the path is an ordinary directory, even one inside another repository. |
| `no_upstream` | Detached HEAD, no upstream, or an upstream that is a local branch. |
| `repository_mismatch` | The upstream's remote does not identify the manifest's `git_url` for this mount. |
| `ahead_of_upstream` | HEAD is not an ancestor of the cached upstream. |
| `error` | The repository could not be inspected, or history needed to answer is not present locally. |

Reasons are codes. A configured remote URL is never printed, since it can hold
a credential.

## Rules the implementation keeps

- **Identity.** `--umbrella` must be the umbrella's own directory, carrying
  `.my-cli/workspace.json` with a readable schema and the same organization as
  the manifest being compiled. It is never found by walking up, so a session
  directory is refused, not relabelled. When `--manifest` is omitted the
  umbrella's recorded manifest is used.
- **Base mounts only.** Session worktrees are never read.
- **The right repository.** The mount must be its own repository top level,
  and its upstream must be a remote-tracking ref whose remote matches the
  projected `git_url` under the manifest's existing comparison. That
  comparison is conservative: the same repository over another transport
  reads as a mismatch.
- **One HEAD.** HEAD is resolved once to a full commit id, and every later
  check uses that literal id.
- **Offline and read-only by construction.** Every probe runs with
  `protocol.allow=never`, `GIT_NO_LAZY_FETCH=1`, optional locks and terminal
  prompts off, no fsmonitor hook, and discovery stopped at the mount's parent.
  A partial clone cannot fetch a missing object to answer a probe; the answer
  is `error`.

## Not in this slice

A harness type per role (the runner chooses its harness), pins declared in the
manifest, and any remote lookup at compile time.

## Tests

Default output unchanged; a pinned mount; dirty disclosed; every unavailable
reason, including an unrelated repository under the mount's name, an upstream
on another remote, a local-branch upstream, a detached HEAD, a plain directory
inside an ancestor repository and an uninspectable repository; a stale cached
upstream, asserting nothing is said about fetchability; a configured remote
holding a credential, asserting it is never printed; HEAD resolved exactly
once; a session directory and another organization's umbrella refused; an
unknown workspace schema refused; missing history as `error` with nothing
fetched; and the probe runner asked for an object only a reachable promisor
remote has, with a control showing the same command fetching when unprotected.
