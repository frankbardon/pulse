---
id: U13
slug: multiplicity
title: "Analysts can correct for multiple comparisons in one consistent way"
track: Statistical integrity
size: M
status: not-started
depends_on: [U04]
soft_depends_on: [U07]
blocks: [U22, U28]
todo_items: [60, 61, 62, 63, 64, 65]
branch: multiplicity
---

# U13 — multiplicity

**Outcome:** Analysts can correct for multiple comparisons in one consistent way.

**Track:** Statistical integrity · **Size:** M · **Depends on:** [U04](U04-profiles-model.md) · **Soft:** [U07](U07-guidance-metadata.md) · **Unblocks:** [U22](U22-recommend-explain.md), [U28](U28-matrix-overlays.md)

## Summary

One correction core (Bonferroni, Holm, BH, BY) with explicit test families, exposed as opt-in `multiplicity` on Request / OverlaySpec / Test / MatrixSpec. Raw p-values stay unchanged; `p_adjusted` rides beside them. The default is `none`, so today's numbers are byte-identical.

## References

**Theme documents (read before starting):**
- [statistical-integrity 02 — Multiple comparisons](../v1.0.0-statistical-integrity/02-multiple-comparisons.md) — whole document (Decisions: default `none`, `OVERLAY_CORR_PVALUE` dropped)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#60** (4. Statistical integrity › Multiple comparisons) `processing/multiplicity`: Bonferroni, Holm, BH, BY
- [ ] **#61** (4. Statistical integrity › Multiple comparisons) `multiplicity {method, family}` on Request / OverlaySpec / Test / MatrixSpec; `Options.DefaultMultiplicity` (shipped `none`; correction is opt-in)
- [ ] **#62** (4. Statistical integrity › Multiple comparisons) Families `layer` / `row` / `column` / `request` / `matrix`, incl. across Compose slots
- [ ] **#63** (4. Statistical integrity › Multiple comparisons) Additive `p_adjusted` / `significant_adjusted` / `multiplicity` outputs
- [ ] **#64** (4. Statistical integrity › Multiple comparisons) Advisory + Explain hooks; glossary terms
- [ ] **#65** (4. Statistical integrity › Multiple comparisons) Reference-value, identity and family-boundary gates; `multiple-comparisons.md` skill

## Scope

**In scope**
- `processing/multiplicity`
- Families layer/row/column/request/matrix incl. Compose
- Additive output fields
- Advisory + Explain hook points (consumed by U22)
- Glossary terms
- Gates + topical skill

**Out of scope**
- Matrix wiring (`MatrixSpec.multiplicity` is reserved here and implemented in U28)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(multiplicity/E<n>-S<m>): …`; close each epic with `milestone(multiplicity/E<n>): vertical slice complete — <epic title>`.

### E1 — One correction core
- S1: methods + reference-value gate (R `p.adjust` fixtures)
- S2: family resolution incl. across Compose slots

### E2 — Every p-value family can opt in
- S1: `multiplicity` on Request/OverlaySpec/Test (+ reserved on MatrixSpec); `Options.DefaultMultiplicity`
- S2: additive `p_adjusted` / `significant_adjusted` / `multiplicity` outputs
- S3: advisory trigger data + glossary terms; topical skill

## Acceptance criteria

- [ ] Adjusted values match R `p.adjust` on reference vectors for every method
- [ ] Absent `multiplicity` (the default) is byte-identical to today
- [ ] A `layer` family never mixes p-values across layers; `row` never across rows
- [ ] Raw p-values are never modified
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestMultiplicityReferenceValues`
- `TestMultiplicityNoneIsIdentity`
- Family-boundary tests

## Update Demand companions

- Payload-schema golden
- CLAUDE.md Output Format Contract (additive fields)
- `skills/multiple-comparisons.md`; overlay and test atomic skills `## Params`
- `update-demand.md` row for `Multiplicity`

## Human inputs & decisions

- None.
