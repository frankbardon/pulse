# Pulse Roadmap

Planning documents for upcoming Pulse releases. These are **design intent, not contract** — nothing here is binding until it lands in code, a skill file, CLAUDE.md and (where applicable) `.claude/reference/`. When a planned feature ships, the authoritative description moves to those places and the roadmap entry is marked done.

This directory sits outside the mdBook source (`docs/src/`) on purpose: planned CLI leaves, operators and codes named here do not exist yet, and must not trip the coverage gates (`TestSkillsCoverAllCliLeaves`, `TestSkillsCoverAllCrossReferences`).

## v1.0.0

Progress checklist for every committed v1.0.0 feature: [`TODO.md`](TODO.md). Work is planned as 41 Flow units in [`units/`](units/README.md); each TODO item links to its unit.

| Theme | Index |
|---|---|
| API surface & release — public Go surface audit, release pipeline and versioning, stability policy, [embedder migration guide](v1.0.0-api-and-release/03-embedder-migration.md) | [`v1.0.0-api-and-release/`](v1.0.0-api-and-release/00-public-surface.md) |
| Feature profiles — instance-wide allowlist configs; hidden features fully invisible to every MCP and embedder surface (manifest, skills incl. exact-name fetch, examples, generated docs) | [`v1.0.0-feature-profiles/`](v1.0.0-feature-profiles/00-feasibility.md) |
| Guided analysis — purpose metadata, question-first docs, Recommend / Explain, MCP prompts | [`v1.0.0-guided-analysis/`](v1.0.0-guided-analysis/00-overview.md) |
| Statistical integrity — first-class weighting, multiple-comparison correction | [`v1.0.0-statistical-integrity/`](v1.0.0-statistical-integrity/00-overview.md) |
| Time zones — UTC inside, zones only at the edges through one adapter | [`v1.0.0-time-zones/`](v1.0.0-time-zones/00-design.md) |
| Vector & matrix math | [`v1.0.0-vector-matrix/`](v1.0.0-vector-matrix/00-overview.md) |
| Response shaping — callers select what a response carries; unrequested parts are never computed | [`v1.0.0-response-shaping/`](v1.0.0-response-shaping/00-design.md) |
| Embedder operations — resource limits with high defaults; logger, timing hooks, opt-in metrics | [`v1.0.0-embedder-operations/`](v1.0.0-embedder-operations/00-overview.md) |
| Documentation audit — a final pass over every human- and agent-facing surface before the release candidate | [`v1.0.0-docs-audit/`](v1.0.0-docs-audit/00-plan.md) |

**Cross-theme ordering** (mirrored by the section order in `TODO.md`):
1. The public-surface audit lands before other v1 work touches the same packages. The release pipeline can land early, so release candidates are possible.
2. Feature profiles FP1–FP3 and guided-analysis G1 come next, so every new surface is profile-filtered and carries purpose metadata from its first commit.
3. Statistical integrity and the time-zone consolidation (step 0) land before the vector & matrix operators, so those operators are born weight-, multiplicity- and zone-aware.
4. Response shaping and embedder operations are largely independent and can run in parallel with the vector & matrix work.
5. The documentation audit runs after every feature unit and before the release candidate.
6. `STABILITY.md` is published last, once the public surface is final.

### Branching & release strategy

Decided 2026-10-01, in the [U01](units/U01-release-pipeline.md) interview.
- **Trunk on `main`. There is no long-lived `v1` branch.** Every unit branches from `main` and merges back through its own PR. Go consumers pin tags, not `main`, so `main` can be "v1-in-progress" without affecting anyone. A `v1` branch would mean forward-merging every hotfix and dependabot bump, retargeting CI, branch protection and the Flow tooling, and one large merge right before the release candidate.
- **No `v0.x` releases from `v0.39.0` onward.** By default, every unit records its release intent as *none — rolls into v1.0.0*, and finalize merges without tagging.
- **Pre-release tags on demand.** Tags are `v1.0.0-alpha.N` at meaningful checkpoints, chiefly after U02 / U02b / U02c so the downstream embedder can try the migration, and `v1.0.0-rc.N` after U32. The release workflow (U01) marks any `-` suffixed tag as a pre-release. `go get …@latest` ignores pre-releases, so nobody receives one without asking. `v1.0.0-alpha.0` is cut from U01's merge commit to validate the pipeline end to end.
- **Exception: `v0.39.1` is a feature release.** `release/v0.39` carries `v0.39.1` (weighted variance and Kish `n_eff` Components on `AGG_WEIGHTED_MEAN`, plus `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`) as an explicit, deliberate exception to "no `v0.x` releases" — it is not an emergency fix. The same contract was forward-ported to `main` in the same effort (`weighted-variance-z`), so v1.0.0 ships it too. The `v0.39.1` tag lives on the `release/v0.39` merge commit only; `main` is not tagged for it.
- **Emergency fixes for current users:** create `release/v0.39` from the `v0.39.0` tag only when a fix is needed, cherry-pick the fix, and tag `v0.39.x`. Nothing is maintained there otherwise.
- v1 needs no module-path change; the `/vN` suffix starts at v2.
