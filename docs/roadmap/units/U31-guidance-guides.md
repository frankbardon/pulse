---
id: U31
slug: guidance-guides
title: "A developer can start from a question and find the right analysis without knowing statistics"
track: Guided analysis
size: M
status: not-started
depends_on: [U21, U24, U28]
soft_depends_on: []
blocks: [U32]
todo_items: [100, 101, 102, 103, 250, 251, 252]
branch: guidance-guides
---

# U31 — guidance-guides

**Outcome:** A developer can start from a question and find the right analysis without knowing statistics.

**Track:** Guided analysis · **Size:** M · **Depends on:** [U21](U21-guidance-generated-docs.md), [U24](U24-matrix-operators.md), [U28](U28-matrix-overlays.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

Hand-written prose around generated blocks: the "What can Pulse answer?" landing page, one question guide per intent, story examples (`_meta.intent/question/interpretation`), and the vector & matrix concept primers.

## References

**Theme documents (read before starting):**
- [guided-analysis 02 — Documentation](../v1.0.0-guided-analysis/02-documentation.md) — D1 landing page, D2 question guides, D6 story examples, D8 primers

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#100** (7. Guided analysis — docs, API & MCP › G6 — Guides) "What can Pulse answer?" landing page
- [ ] **#101** (7. Guided analysis — docs, API & MCP › G6 — Guides) Question guides, one per intent: describe, compare groups, relationships, drivers, change over time, composition, benchmark, distribution, segment, measure construct, flows, data quality
- [ ] **#102** (7. Guided analysis — docs, API & MCP › G6 — Guides) Story examples: `_meta.intent` / `question` / `interpretation`, at least one per intent
- [ ] **#103** (7. Guided analysis — docs, API & MCP › G6 — Guides) Vector & matrix concept primers: correlation matrix, PCA, distance vs similarity, perceptual maps
- [ ] **#250** (7. Guided analysis › Follow-ups from U23) Fuzzy (typo-tolerant) search over examples and skills: a final `matchTier` after the literal and `KnownAs`/Sound tiers (the seam in `internal/examples/search.go`), edit distance scaled by length, ranked below exact and synonym hits
- [ ] **#251** (7. Guided analysis › Follow-ups from U23) Align the story-example metadata spelling: #102 says `_meta.intent`, examples carry `_meta.intents` (a list); pick one and make the item text, the harness and the docs agree
- [ ] **#252** (7. Guided analysis › Follow-ups from U23) `pulse examples search` has no `--intent` flag (the MCP tool and the facade `ExamplesSearchWith` take one); add it and a `docs/src/cli/flags.md` row

## Scope

**In scope**
- Landing page
- 12 question guides
- Story examples ≥1 per intent
- 4 primers

**Out of scope**
- SPSS/R/pandas/SQL translation tables (stretch)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(guidance-guides/E<n>-S<m>): …`; close each epic with `milestone(guidance-guides/E<n>): vertical slice complete — <epic title>`.

### E1 — Start from a question
- S1: landing page + guides batch 1 (describe, compare, relationships, drivers, change, composition)
- S2: guides batch 2 (benchmark, distribution, segment, measure construct, flows, data quality)

### E2 — Learn by example
- S1: `_meta` extension + stories per intent (golden outputs)
- S2: four concept primers

## Acceptance criteria

- [ ] Every intent has a guide whose generated blocks are current
- [ ] Every story's output is produced by the examples harness (no hand-typed numbers)
- [ ] Guides name only real CLI leaves (`TestSkillsCoverAllCliLeaves` passes)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestDocsGeneratedCurrent`
- `TestExamples_*`

## Update Demand companions

- `docs/src/SUMMARY.md`
- `examples/README.md` (`_meta` fields)

## Human inputs & decisions

- Review for plain-language clarity (a non-statistician reader is ideal)
