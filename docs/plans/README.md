# Design plans index

Long-form designs and decision records for the public `our` CLI, newest
first. Statuses: **active** (being implemented or next up), **shipped**
(implemented; kept for rationale), **superseded** (kept for history; the
noted successor wins on conflict).

| Plan | Status | Notes |
|---|---|---|
| [2026-06-12-v018-scope](2026-06-12-v018-scope.md) | shipped (v0.18.0) | Manifest `services` + `roles`, inspection verbs, `our setup --role`, umbrella-root `.mcp.json` from local/inline connection data only, doctor service checks. Session GC and Mode B compile deferred. |
| [2026-06-11-products-repos-split](2026-06-11-products-repos-split.md) | shipped (v0.15.0) | Catalog products are business entities linking repos; `catalog/repos.json` + `our repos` noun + `--repo` launch flags. Private fluxinc manifest migrated in step. |
| [2026-06-10-execution-plane](2026-06-10-execution-plane.md) | active | Mode A sessions shipped v0.14.0–v0.17.0 (opt-in launches, session-aware content commands); manifest `roles`/`services` with Mode A MCP materialization shipped in v0.18.0. Contained runners (Mode B compile) remain active. |
| [2026-06-10-single-checkout-workspace](2026-06-10-single-checkout-workspace.md) | shipped (v0.13.0) | Control/data-plane split: private manifest repo + workspace content repo, `our publish`. The file's earlier visible-single-checkout draft is superseded by its final two-repo form. |
| [2026-06-10-fleet-registry](2026-06-10-fleet-registry.md) | shipped (v0.9.0) | `our fleet` registry: one record per deployed instance, identifier resolution. |
| [2026-06-10-support-knowledgebase](2026-06-10-support-knowledgebase.md) | shipped (v0.9.0) | `our support` anonymized problem-to-solution records. |
| [2026-06-09-workspace-currency](2026-06-09-workspace-currency.md) | shipped | TTL-gated auto-refresh and stderr freshness notices for root/ai/setup. |
| [2026-06-09-self-update-design](2026-06-09-self-update-design.md) | shipped (v0.10.x) | `our update` from GitHub releases with checksum verification. |
| [2026-06-08-overarching-sync](2026-06-08-overarching-sync.md) | partially superseded | Gnit-as-spine sync design still governs the Gnit backend; its same-remote canonicalization direction is superseded by the two-repo split (the duplicate-checkout class no longer exists in the default layout). |
| [2026-06-08-flux-self-skill](2026-06-08-flux-self-skill.md) | shipped | Bundled organization-neutral `our` self-skill. |
| [2026-06-03-release-install-site](2026-06-03-release-install-site.md) | shipped | Releases, install script, docs site. |
| [2026-06-02-admin-operational-split](2026-06-02-admin-operational-split.md) | shipped | Admin vs operational command surface. |
| [2026-05-28-startup-context-ergonomics](2026-05-28-startup-context-ergonomics.md) | shipped | Startup context and guidance ergonomics. |
