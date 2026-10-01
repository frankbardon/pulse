# Pulse Roadmap

Planning documents for upcoming Pulse releases. These are **design intent, not contract** — nothing here is binding until it lands in code, a skill file, CLAUDE.md and (where applicable) `.claude/reference/`. When a planned feature ships, the authoritative description moves to those places and the roadmap entry is marked done.

This directory sits outside the mdBook source (`docs/src/`) on purpose: planned CLI leaves, operators and codes named here do not exist yet, and must not trip the coverage gates (`TestSkillsCoverAllCliLeaves`, `TestSkillsCoverAllCrossReferences`).

## v1.0.0

Progress checklist for every committed v1.0.0 feature: [`TODO.md`](TODO.md).

| Theme | Index |
|---|---|
| API surface & release — public Go surface audit, release pipeline and versioning, stability policy | [`v1.0.0-api-and-release/`](v1.0.0-api-and-release/00-public-surface.md) |
| Feature profiles — instance-wide allowlist configs; hidden features fully invisible to every MCP and embedder surface (manifest, skills incl. exact-name fetch, examples, generated docs) | [`v1.0.0-feature-profiles/`](v1.0.0-feature-profiles/00-feasibility.md) |
| Guided analysis — purpose metadata, question-first docs, Recommend / Explain, MCP prompts | [`v1.0.0-guided-analysis/`](v1.0.0-guided-analysis/00-overview.md) |
| Statistical integrity — first-class weighting, multiple-comparison correction | [`v1.0.0-statistical-integrity/`](v1.0.0-statistical-integrity/00-overview.md) |
| Time zones — UTC inside, zones only at the edges through one adapter | [`v1.0.0-time-zones/`](v1.0.0-time-zones/00-design.md) |
| Vector & matrix math | [`v1.0.0-vector-matrix/`](v1.0.0-vector-matrix/00-overview.md) |
| Response shaping — callers select what a response carries; unrequested parts are never computed | [`v1.0.0-response-shaping/`](v1.0.0-response-shaping/00-design.md) |
| Embedder operations — resource limits with high defaults; logger, timing hooks, opt-in metrics | [`v1.0.0-embedder-operations/`](v1.0.0-embedder-operations/00-overview.md) |

**Cross-theme ordering** (mirrored by the section order in `TODO.md`):
1. The public-surface audit lands before other v1 work touches the same packages. The release pipeline can land early, so release candidates are possible.
2. Feature profiles FP1–FP3 and guided-analysis G1 come next, so every new surface is profile-filtered and carries purpose metadata from its first commit.
3. Statistical integrity and the time-zone consolidation (step 0) land before the vector & matrix operators, so those operators are born weight-, multiplicity- and zone-aware.
4. Response shaping and embedder operations are largely independent and can run in parallel with the vector & matrix work.
5. `STABILITY.md` is published last, once the public surface is final.
