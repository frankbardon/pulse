---
id: U31
slug: guidance-guides
title: "A developer can start from a question and find the right analysis without knowing statistics"
track: Guided analysis
size: M
status: done
depends_on: [U21, U24, U28]
soft_depends_on: []
blocks: [U32]
todo_items: [100, 101, 102, 103, 250, 251, 252, 269, 377, 556]
branch: guidance-guides
---

# U31 — guidance-guides

**Outcome:** A developer can start from a question and find the right analysis without knowing statistics.

**Track:** Guided analysis · **Size:** M · **Depends on:** [U21](U21-guidance-generated-docs.md), [U24](U24-matrix-operators.md), [U28](U28-matrix-overlays.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

Hand-written prose around generated blocks: the "What can Pulse answer?" landing page, one question guide per intent, story examples (`_meta.intents` / `question` / `interpretation`), and the vector & matrix concept primers.

## References

**Theme documents (read before starting):**
- [guided-analysis 02 — Documentation](../v1.0.0-guided-analysis/02-documentation.md) — D1 landing page, D2 question guides, D6 story examples, D8 primers

**TODO items delivered by this unit** (ticked in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#100** (7. Guided analysis — docs, API & MCP › G6 — Guides) "What can Pulse answer?" landing page
- [x] **#101** (7. Guided analysis — docs, API & MCP › G6 — Guides) Question guides, one per intent: describe, compare groups, relationships, drivers, change over time, composition, benchmark, distribution, segment, measure construct, flows, data quality
- [x] **#102** (7. Guided analysis — docs, API & MCP › G6 — Guides) Story examples: `_meta.intents` (a list) / `question` / `interpretation`, at least one per intent
- [x] **#103** (7. Guided analysis — docs, API & MCP › G6 — Guides) Vector & matrix concept primers: correlation matrix, PCA, distance vs similarity, perceptual maps
- [x] **#250** (7. Guided analysis › Follow-ups from U23) Fuzzy (typo-tolerant) search over examples (examples only: no free-text skill search exists): a final `matchTier` after the literal and `KnownAs`/Sound tiers (the seam in `internal/examples/search.go`), edit distance scaled by length, ranked below exact and synonym hits
- [x] **#251** (7. Guided analysis › Follow-ups from U23) Align the story-example metadata spelling on `_meta.intents` (a list); the item text, the harness and the docs agree
- [x] **#252** (7. Guided analysis › Follow-ups from U23) `pulse examples search` has no `--intent` flag (the MCP tool and the facade `ExamplesSearchWith` take one); add it and a `docs/src/cli/flags.md` row
- [x] **#269** (10. Vector & matrix — operators › Follow-ups from U24) A rating-battery demo cohort (Likert items on one latent trait) so `MAT_RELIABILITY` and `MAT_PCA` examples show a defined omega and real loadings; `08_reliability.json` runs on `experiment.pulse` and shows a Heywood null omega

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

- [x] Every intent has a guide whose generated blocks are current
- [x] Every story's output is produced by the examples harness (no hand-typed numbers)
- [x] Guides name only real CLI leaves (`TestSkillsCoverAllCliLeaves` passes)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestDocsGeneratedCurrent`
- `TestExamples_*`

## Update Demand companions

- `docs/src/SUMMARY.md`
- `examples/README.md` (`_meta` fields)

## Human inputs & decisions

- Review for plain-language clarity (a non-statistician reader is ideal)

## Inherited from U41

Filed from [U41 handed on](U41-derived-cohort.md#handed-on); ticked in [`TODO.md`](../TODO.md).

- [x] **#377** — Recommend drafts a materialise step: an intent route (e.g. a derived column a later model reads) that proposes `pulse_materialize` before the step-2 request. Found in U41 (E2-S2).

## Decisions

- **Review for plain-language clarity:** done without a human reader. A critical non-statistician review of the landing page, the 12 guides and the 4 primers ran in E3-S2; its findings (location → problem → fix) are in that commit's body and every one is fixed or justified there. The generated decision lists stay full length (owner decision).
- **`FEAT_POLY` gets no materialise plan** (`Recommendation.Steps`, #377): its output column set depends on the degree the caller picks, so a drafted `REG_OLS` cannot name the columns ahead of time (owner decision). `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`, `ATTR_SCALE_SCORE`, `ATTR_REG_PREDICT`, `FEAT_LOG`, `FEAT_SQRT` and `FEAT_FREQUENCY_ENCODE` do.
- **#250 narrowed to examples:** Pulse has no free-text skill search, so the fuzzy tier lives in `internal/examples/search.go` only.

## Handed on

| Item | To | Why |
|---|---|---|
| #504 a runnable, golden-pinned perceptual-map example chain behind `docs/src/concepts/perceptual-maps.md` | [U32](U32-docs-audit.md) | the primer describes the recipe (PCA loadings → `ATTR_PC_SCORE` → group averages → `MAT_DISTANCE`) but no example or story golden runs it, so its steps are unchecked |
| #505 `skills/session-bootstrap.md` line for the CLI `pulse examples search --intent` | [U32](U32-docs-audit.md) | the skill is at its 6,000-byte topical budget, and the MCP `intent` argument it would echo predates U31; the docs audit owns skill trims |
