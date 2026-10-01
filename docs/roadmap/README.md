# Pulse Roadmap

Planning documents for upcoming Pulse releases. These are **design intent, not contract** — nothing here is binding until it lands in code, a skill file, CLAUDE.md and (where applicable) `.claude/reference/`. When a planned feature ships, the authoritative description moves to those places and the roadmap entry is marked done.

This directory sits outside the mdBook source (`docs/src/`) on purpose: planned CLI leaves, operators and codes named here do not exist yet, and must not trip the coverage gates (`TestSkillsCoverAllCliLeaves`, `TestSkillsCoverAllCrossReferences`).

## v1.0.0

| Theme | Index |
|---|---|
| Vector & matrix math | [`v1.0.0-vector-matrix/`](v1.0.0-vector-matrix/00-overview.md) |
| Guided analysis — purpose metadata, question-first docs, Recommend / Explain, MCP prompts | [`v1.0.0-guided-analysis/`](v1.0.0-guided-analysis/00-overview.md) |
| Feature profiles — per-instance enable/disable with manifest, skills, MCP and docs reacting | [`v1.0.0-feature-profiles/`](v1.0.0-feature-profiles/00-feasibility.md) |

**Cross-theme ordering:** feature profiles FP1–FP3 and guided-analysis G1 land first, so every new surface in the other themes is profile-filtered and carries purpose metadata from its first commit.
