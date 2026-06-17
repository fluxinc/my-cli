# Plan: Rename bundled self-skill `my` → `my-cli` (with install migration)

Status: FINAL (Claude draft, Codex final pass) — implemented and locally
verified; awaiting peer signoff.
Branch: `rename-self-skill-my-cli` (off master).

## Goal

Rename the bundled self-skill from `my` to `my-cli`. `my` is short and easily
confused with the CLI command, the umbrella dir (`~/my`), and the word "my".
The CLI binary/command stays `my` — **only the skill renames**. On update,
existing installs migrate automatically (`my` → `my-cli`) across all supported
harnesses. Update the site, the skill content, and the docs. Test everything.

## Hard invariant (read this first)

`selfskill.CanonicalID = "my:self"` is the stable identity of the self-skill and
**must not change.** It anchors provenance, pruning protection
(`isSelfSkillMaterialization`), and upgrade safety. The rename changes the
skill's *name / install slug / directory* (`my` → `my-cli`); the canonical id
stays `my:self`. Tests that assert `my:self` stay as-is.

Other constraints:
- CLI binary/command stays `my`.
- No `/my` alias retained — this is a clean move + migration.
- Migration must be idempotent, quiet (`SyncExisting` suppresses output),
  handle both copy and symlink installs, and never touch a non-managed skill.

## Changes

### 1. Skill source
- `git mv skills/my skills/my-cli` (only `SKILL.md` lives under it).
- `skills/my-cli/SKILL.md` frontmatter: `name: my` → `name: my-cli`. Scan the
  body for self-references to "the `my` skill" / "self-skill named `my`" and fix.
- `embed.go` uses `//go:embed all:skills` — embeds the whole tree, so the
  directory rename needs **no** embed change.

### 2. Installer constant — `internal/selfskill/selfskill.go`
- Line 18: `Name = "my"` → `Name = "my-cli"`.
- Line 19: `CanonicalID = "my:self"` — **unchanged**.
- Line ~74 marker field `Installer: "my"`: set to `Name` (so the managed
  marker reflects the new slug). Existing readers continue accepting legacy
  `"my"` markers.
- Everything else in the file already derives from `Name`
  (`InstallSlug`, `Path`, target paths, uninstall, inspect), so it follows the
  constant automatically.

### 3. Migration in `SyncExisting()` — `internal/selfskill/selfskill.go:194-256`
Add a legacy constant and a migration pass.

```go
const legacyName = "my" // pre-rename self-skill slug; migrate to Name ("my-cli")
```

After `Materialize`, before the per-harness ensure loop, for each harness `h`:
1. `oldTarget := h.SkillTargetPath(home, legacyName)`
2. `newTarget := h.SkillTargetPath(home, Name)`
3. If `oldTarget` does not exist → nothing to migrate.
4. If `oldTarget` exists AND is our managed install (read
   `oldTarget/.my-cli-managed.json`; confirm `CanonicalID == CanonicalID`):
   - If `newTarget` does **not** exist → `os.Rename(oldTarget, newTarget)`
     (preserves symlink vs copy; fast).
   - Else (`newTarget` exists) → remove `oldTarget` (stale duplicate).
5. If `oldTarget` exists but is **not** managed (no marker / different canonical
   id) → leave it untouched (it's a user/other skill that happens to be named
   `my`).
6. Fall through to the existing ensure/sync flow, which makes `newTarget`
   current.

Notes:
- Idempotent: after the first run `oldTarget` is gone, so it's a no-op.
- Quiet during automatic startup sync, but record migrated harnesses in the
  result set so explicit self-skill install and tests can report `migrated`.
- Handle symlink case: a symlinked `oldTarget` should `os.Rename` cleanly; if
  rename fails (cross-device etc.), fall back to remove-old + reinstall-new.
- After harness migration, remove the stale embedded source directory
  `~/.local/share/my-cli/skills/my` only when it carries the managed
  `my:self` marker.

### 4. Tests — TEST EVERYTHING
Update existing literals (keep `my:self`):
- `internal/selfskill/selfskill_test.go`: lines ~27, 53, 81, 82, 100
  (`my` dir paths, hardcoded `name: my` fixture, `"name: my"` check).
- `internal/cli/skills_test.go`: lines ~71, 74, 85, 441, 467, 470
  (`my` dir paths, `"...\tmy\t..."` and `"skill": "my"` assertions,
  `makeCLISkill(t, "my")`). Note `TestSkillsSyncPrunesManifestSkillNamedMy`
  asserts a *manifest* skill named `my` (different canonical id) is still
  prunable while `my:self` is protected — keep that behavior; only adjust if the
  rename changes the self-skill slug it compares against.
- `internal/e2e/e2e_test.go:144`: keep `my:self`; ensure install lands at
  `my-cli`.

New migration tests (`internal/selfskill/selfskill_test.go`):
- Managed legacy install (`~/.codex/skills/my` with marker `my:self`) → migrated
  to `my-cli` on `SyncExisting`; old gone, new present + current.
- Symlink (link-mode) legacy install migrates.
- `newTarget` already present → legacy removed, new untouched.
- Non-managed `my` dir (no marker) → left alone.
- Idempotency: second `SyncExisting` is a no-op (no error, no churn).

### 5. Docs / site / skills — UPDATE SITE, SKILLS, DOCS
- `README.md`: ~7 references to the bundled `my` skill (lines ~45, 55, 208, 219,
  234, 559, 565-566) → `my-cli` (keep `my:self`).
- `site/guide/skills.md`: ~6 references (lines ~10-14, 97).
- `CLAUDE.md` + `AGENTS.md`: `skills/my/` → `skills/my-cli/`.
- `CHANGELOG.md` + `site/changelog.md`: add an `## Unreleased` entry:
  "Renamed the bundled self-skill from `my` to `my-cli`; existing installs are
  migrated automatically on the next CLI run (canonical id `my:self`
  unchanged)."
- `docs/plans/*` historical dated files that mention `skills/my`: **leave as
  historical record** (do not rewrite past plans).
- Rebuild: `cd site && npm run build`.

### 6. Verification
- `go build ./...`, `go vet ./...`, `go test ./...`, `git diff --check`.
- Dogfood the real binary: create a fake managed legacy install under a temp
  `--home` for one or more harnesses, run a `my` command (triggers
  `SyncExisting`), confirm `my/` → `my-cli/` migration; run twice for
  idempotency; confirm a non-managed `my/` is left alone.
- `cd site && npm run build`.

## Decisions (Codex final pass)

1. New managed markers use `my-cli`; readers accept both `my` and `my-cli`.
2. Historical `docs/plans/*` references are left as dated records.
3. Migration emits a `migrated` result internally and on explicit self-skill
   install output when a legacy target is moved or removed.

## Ownership / turns
Per operator: Claude drafted this plan; **Codex does the final pass on this plan
and the implementation**; then we alternate verification (TAKE TURNS). Start
from the invariant (`my:self`), then constant + dir rename, then migration, then
tests, then docs, then site build.
