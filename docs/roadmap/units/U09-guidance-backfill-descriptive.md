---
id: U09
slug: guidance-backfill-descriptive
title: "Every operator carries guidance, and the guidance gates are binding"
track: Guided analysis
size: L
status: not-started
depends_on: [U08]
soft_depends_on: []
blocks: [U10, U21, U22]
todo_items: [46, 47, 48, 49]
branch: guidance-backfill-descriptive
---

# U09 — guidance-backfill-descriptive

**Outcome:** Every operator carries guidance, and the guidance gates are binding.

**Track:** Guided analysis · **Size:** L · **Depends on:** [U08](U08-guidance-backfill-inferential.md) · **Unblocks:** [U10](U10-skill-ontology.md), [U21](U21-guidance-generated-docs.md), [U22](U22-recommend-explain.md)

## Summary

Finish the back-fill (aggregators, attributes, filterers, groupers, windows, features, synth distributions). Then flip the guidance gates from report-only to failing, so every future operator must ship with guidance.

## References

**Theme documents (read before starting):**
- [guided-analysis 01 — Purpose metadata & intent taxonomy](../v1.0.0-guided-analysis/01-purpose-metadata.md) — P2, P3, P6
- [guided-analysis 04 — Phasing](../v1.0.0-guided-analysis/04-phasing.md) — G2 — Back-fill

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#46** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Aggregators (`AGG_*`)
- [ ] **#47** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Attributes, filterers, groupers, windows and features
- [ ] **#48** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Synth distributions
- [ ] **#49** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Gates flipped from report-only to failing

## Scope

**In scope**
- Purpose (+ Interpretation where an output needs reading) for the remaining categories
- Flip the U07 gates to failing

**Out of scope**
- Rendering (U21)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(guidance-backfill-descriptive/E<n>-S<m>): …`; close each epic with `milestone(guidance-backfill-descriptive/E<n>): vertical slice complete — <epic title>`.

### E1 — Descriptive operators explain themselves
- S1: `AGG_*`
- S2: attributes, filterers, groupers
- S3: windows, features, synth distributions

### E2 — Guidance becomes a contract
- S1: flip the gates to failing; Update Demand row enforced
- S2: reviewer sign-off on the whole back-fill

## Acceptance criteria

- [ ] All guidance gates fail CI on a missing Purpose
- [ ] The full suite passes with the gates failing-mode
- [ ] Reviewer sign-off recorded
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- U07 gates switched to failing

## Update Demand companions

- CLAUDE.md gate list unchanged in names, but note in `guided-analysis.md` that they are now binding

## Human inputs & decisions

- Statistics reviewer sign-off before the flip

## Inherited from U07

- **Tag examples with `_meta.intents` before the flip.** U07 made the field optional on examples (`internal/examples/library.go`; values validated against the taxonomy by `TestExamples_IntentsFromTaxonomy`, binding) and no example carries it yet. `TestPurposeQuestionsResolve`'s coverage half logs every intent with fewer than three declaring operators or no `_meta.intents`-tagged example (`.claude/reference/guided-analysis.md`), so flipping it to failing (#49) needs every intent to have at least one tagged example. U23's `pulse_examples_search {intent}` (#96) reads the same tags. The landed spelling is `_meta.intents` (a list); TODO #102 ([U31](U31-guidance-guides.md)) still writes `_meta.intent`, so U31 should use the landed field.
