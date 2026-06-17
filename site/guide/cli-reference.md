# CLI Reference

Run `my --help` for the authoritative surface. This page groups the current
commands by job.

## Which command do I run?

Three commands sound alike; the split is converge vs. diagnose vs. plumbing:

- **`my sync`** converges the whole workspace. It pulls every registered
  repository (manifest cache, content mounts, catalog repo clones), reconciles
  generated guidance, umbrella MCP config, and launch-scoped skill notices when
  the manifest changed, and never publishes local changes unless the operator
  passes `--push` or an explicit `--publish` mode. This is the one routine verb
  for stale inbound state; use `my sync --push --print` then `my sync --push`
  when local changes should be shared.
- **`my doctor`** is the dry run for installation and workspace repair: it
  diagnoses manifest validity, per-checkout Git freshness, derived
  guidance/MCP drift, legacy global org-skill drift, service materialization
  health, work-session health, and the last sync audit,
  marking every repairable finding with `would ...` and a closing fixable
  count. Nothing changes until you re-run with `--fix`, which applies exactly
  that plan; findings `--fix` cannot repair (dirty, diverged, repo checkouts,
  session work) keep their explanatory remediation text instead.
- **`my manifests sync`** refreshes the registered manifest cache. You need
  it before an umbrella exists (bootstrap) or when managing several
  registered manifests; when exactly one manifest changes and an umbrella is
  known, it also reconciles generated guidance, umbrella MCP config, and
  launch-scoped skill reconciliation notices. Once an umbrella is set up, plain
  `my sync` is still the routine command.

## Setup and launch

```sh
my init <org-id> [--name NAME] [--path DIR] [--umbrella DIR] [--home DIR] [--setup] [--json]
my publish [--manifest NAME] [--home DIR] [--print] [--json]
my onboarding [--agent|--no-agent] [--harness NAME] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
my setup [harness...] | --all [--interactive] [--print] [--copy] [--link] [--force] [--verbose] [--role ROLE] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
my root [--repo ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
my ai [--new-session|--session ID|--resume [ID]|--no-session] [--repo ID] [--skills all|none|ID,...] [--profile ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]
my sync [--backend auto|gnit|builtin] [--push|--publish auto|never|direct|pr] [--scope all|local|content|manifest|repos] [--manifest NAME] [--home DIR] [--umbrella DIR] [--message TEXT] [--no-derived] [--print] [--verbose] [--json]
my doctor [--no-fetch] [--fix] [--json]
my update [--check] [--version X.Y.Z] [--json] [--yes]
my version
```

`my init` creates two local repositories — a private manifest repo at the
registry path (the control plane) and a content repo at
`<umbrella>/workspace` (`--path` overrides) — commits and registers them, and
prints the follow-up `my setup`, `my ai`, and `my publish` commands. Both
repos work offline and report `local-only` until published.

`my onboarding` is the guided first-run path. In an interactive terminal it
launches a harness with the bundled Agent-Operated Onboarding guidance. The
model greets the operator, starts a split-pane learn-by-example walkthrough, and
has the operator run small sets of validated `my` commands with a pause after
each set. A harness is auto-detected when the choice is unambiguous; pass
`--harness NAME` to choose. `--agent` forces the harness path from
non-interactive contexts.

`my onboarding --no-agent` and non-interactive runs use the deterministic
walkthrough: with no registered manifest it prints the
`my manifests add <name> <git-url>` next step and writes no state; with a
manifest it explains the model, offers `my setup --interactive`, and records
tour completion only after setup actually runs. Plain `my setup` remains
non-interactive and scriptable; `--interactive` prompts for manifest and role
selection. `my onboard` remains a compatibility alias.

`my publish` takes the organization online idempotently: it creates private
remotes (`<org>-workspace`, `<org>-manifest`) via `gh`, or adopts existing
origins and pushes (verifying GitHub remotes are private), rewrites local
mount URLs to the published repositories in a commit scoped to
`manifest.json`, updates the registry, and prints the teammate join command.
`my sync` refuses to publish a manifest that still references local mount
paths, and `my doctor` names each such mount with the `my publish`
remediation.

## Skills

```sh
my skills self install [harness...] | --all [--home DIR] [--copy] [--link] [--force] [--json]
my skills self uninstall [harness...] | --all [--home DIR] [--force] [--json]
my skills self status [harness...] | --all [--home DIR] [--json]

my skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]
my skills show <id|slug> [--json] [--source DIR] [--manifest NAME] [--home DIR]
my skills status [--skill ID_OR_SLUG] [--json] [--source DIR] [--manifest NAME] [--home DIR]
my skills install [harness...] | --all [--skill ID_OR_SLUG] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
my skills uninstall <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
my skills sync [harness...] | --all [--skill ID_OR_SLUG] [--no-prune] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
my skills purge <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
```

`--manifest NAME` reads skills from a synced manifest and overrides the
current/default manifest; `--source DIR` reads them from a local directory
instead. With no harness arguments, install targets every supported harness and
silently skips ones that are not present.

## Admin

```sh
my admin skills add <skill-dir> --id namespace:name --manifest-dir DIR [--install-slug SLUG] [--keep-original|--remove-original] [--force] [--json]
my admin skills remove <id|slug> --manifest-dir DIR [--delete-source] [--prune-related] [--prune-orphans] [--force] [--json]
my admin setup ...
my admin manifests add|sync|validate ...
my admin mounts add|remove|sync ...
my admin meetings add ...
my admin support add ...
my admin tools add|edit|remove <id> --manifest-dir DIR [--mode required|optional] [--purpose TEXT] [--install-command CMD] [--docs-url URL] [--skill-install-command CMD] [--skill-install-arg ARG] [--force] [--json]
my admin contract add "RULE TEXT" --manifest-dir DIR [--force] [--json]
my admin contract remove <index|"RULE TEXT"> --manifest-dir DIR [--force] [--json]
```

See the [admin guide](./admin.md) for the full flag set and the
review-commit-push workflow that follows every admin edit.

## Manifests, mounts, and workspace

`my manifests default [<name>]` shows or repoints the global default manifest
(initially the first one added; `--clear` reverts to it). When `--manifest` is
omitted, commands prefer the current umbrella's manifest, then fall back to this
registry default.

```sh
my manifests add <name> <git-url>
my manifests list
my manifests default [<name>] [--clear] [--home DIR] [--json]
my manifests sync [name...] | --all [--home DIR] [--umbrella DIR] [--no-derived] [--print] [--json]
my manifests validate <name|path>

my mounts list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
my mounts add <kind:id|id> [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
my mounts sync <mount...> | --all [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
my mounts remove <mount...> [--home DIR] [--umbrella DIR] [--print] [--force] [--json]

my workspaces list [--manifest NAME]
my workspaces sync <workspace...> | --all [--manifest NAME] [--print]

my work start [--slug SLUG] [--json]
my work status [--all] [--json]
my work list [--all] [--json]
my work resume [session-id] [--json]
my work finish [session-id] --land|--publish|--discard [--message TEXT] [--verbose] [--json]
```

With no `--manifest`, commands prefer the manifest recorded by the current
umbrella or `--umbrella DIR`. Outside an umbrella, they use the registry default,
which is the first manifest added unless the registry names another default.

## Content and diagnostics

```sh
my meetings list
my meetings search <text>
my meetings get <id|path>
my meetings add <slug>

my support list
my support search <text>
my support get <id|path>
my support add <slug>

my fleet list
my fleet search <text>
my fleet get <id|identifier|path>
my fleet add <id>
my fleet set <id> KEY=VALUE...

my record adopt <path>

my customers list                     # mounted customer identity records
my products list
my repos list [--json]
my repos add <id> [--print] [--json]
my repos remove <id> [--force] [--json]
my tools list
my tools info <name>
my services list [--manifest NAME] [--home DIR] [--json]
my services get <id> [--manifest NAME] [--home DIR] [--json]
my roles list [--manifest NAME] [--home DIR] [--json]
my roles get <id> [--manifest NAME] [--home DIR] [--json]
my contract list [--manifest NAME] [--home DIR] [--json]
my compile --role <id> [--manifest NAME] [--home DIR]
```

`my sync` is the routine pull/reconcile command. Bare `my sync` never publishes
local changes. `--backend auto` prefers Gnit when the umbrella is initialized as
a Gnit control workspace; My AI keeps the bootstrap, policy, duplicate-remote,
and PR layers. `my sync --push` publishes eligible local changes per manifest
policy; `--publish direct` can publish existing local commits directly, but
dirty non-content/admin files are still held back for explicit admin or review
handling. Plain untracked (`??`) files under declared content paths are also
held; create records with `my meetings add`, `my support add`, or
`my fleet add`, or run `my record adopt <path>` to mark a manually created file
as intentional publish content. A manifest can set top-level
`sync.publish_policy` to `auto`, `never`, or `pr` as the mode for `--push`; an
explicit `--publish` flag always wins. Non-print syncs write
`.my-cli/last-sync.json`; `my doctor`
reports that audit, per-checkout Git freshness, active and archived work
sessions, service health, derived guidance/MCP drift, and legacy global
org-skill drift. Doctor fetches
refs before behind/ahead checks unless `--no-fetch` is passed for an offline
view. `doctor --fix` fast-forwards only clean stale manifest/content checkouts
and reconciles generated guidance, umbrella `.mcp.json`, and legacy global
org-skill cleanup. Sync performs the same derived reconcile after manifest
checkout changes unless `--no-derived` is passed.

`my root`, `my ai`, and `my setup` run a best-effort, TTL-gated
refresh for clean manifest/content checkouts before using workspace context.
They leave dirty, diverged, repo, and remote-unknown checkouts untouched.
`my ai` also ensures the bundled `my` self-skill exists for the selected
filesystem harness before launching it. By default it launches from the base
umbrella, or from the current active session when run inside `work/<id>`. Use
`--new-session` to create a fresh isolated session, `--session <id>` or
`-r <id>` to resume a known active session, `-r <harness>` to select the only
active session or pick one in an interactive terminal, or `--no-session` to
ignore a current session for base inspection/admin/debug. Repo launches use
`--repo <id>` and are not included in work sessions yet. Use `--no-refresh` for
one command, `MYCLI_NO_AUTO_REFRESH=1` globally, or `MYCLI_REFRESH_TTL=30m` to
tune the default six-hour window.

Manifest roles are selected locally with `my setup --role <id>`. The choice
is stored in `.my-cli/state.json`, appends that role's guidance fragments to
`AGENTS.md`, and scopes generated `.mcp.json` to MCP services selected by the
role. Services and roles are manifest vocabulary: inspect them with
`my services list|get` and `my roles list|get`; they do not prune mounts.
`my compile --role <id>` prints the deterministic contained-runner launch
projection JSON for that role without launching containers, resolving
credentials, or fetching service descriptors.

Those startup commands also emit a stderr-only notice when a newer My AI release
is available. Stdout remains clean for command substitutions such as
`cd "$(my root)"`. The check follows GitHub's public release redirect rather
than the rate-limited REST API. Use `--no-update-check`,
`MYCLI_NO_UPDATE_CHECK=1`, or `MYCLI_UPDATE_CHECK_TTL=12h` to suppress or tune
that check.

`my update` downloads the selected GitHub release tarball, verifies it against
`checksums.txt`, and atomically replaces the running binary when the install is
writable and not package-managed. Use `my update --check` for a read-only
version comparison, or `my update --version X.Y.Z` to install a specific
release.
