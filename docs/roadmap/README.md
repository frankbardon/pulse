# Pulse Roadmap

Planning documents for upcoming Pulse releases. These are **design intent, not contract** — nothing here is binding until it lands in code, a skill file, CLAUDE.md and (where applicable) `.claude/reference/`. When a planned feature ships, the authoritative description moves to those places and the roadmap entry is marked done.

This directory sits outside the mdBook source (`docs/src/`) on purpose: planned CLI leaves, operators and codes named here do not exist yet, and must not trip the coverage gates (`TestSkillsCoverAllCliLeaves`, `TestSkillsCoverAllCrossReferences`).

## v1.0.0

| Theme | Index |
|---|---|
| Vector & matrix math | [`v1.0.0-vector-matrix/`](v1.0.0-vector-matrix/00-overview.md) |
| Guided analysis — purpose metadata, question-first docs, Recommend / Explain, MCP prompts | [`v1.0.0-guided-analysis/`](v1.0.0-guided-analysis/00-overview.md) |
