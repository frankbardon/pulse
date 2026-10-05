---
id: U13
slug: multiplicity
title: "Analysts can correct for multiple comparisons in one consistent way"
track: Statistical integrity
size: M
status: done
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

- [x] **#60** (4. Statistical integrity › Multiple comparisons) `processing/multiplicity`: Bonferroni, Holm, BH, BY
- [x] **#61** (4. Statistical integrity › Multiple comparisons) `multiplicity {method, family}` on Request / OverlaySpec / Test / MatrixSpec; `Options.DefaultMultiplicity` (shipped `none`; correction is opt-in)
- [x] **#62** (4. Statistical integrity › Multiple comparisons) Families `layer` / `row` / `column` / `request` / `matrix`, incl. across Compose slots
- [x] **#63** (4. Statistical integrity › Multiple comparisons) Additive `p_adjusted` / `significant_adjusted` / `multiplicity` outputs
- [x] **#64** (4. Statistical integrity › Multiple comparisons) Advisory + Explain hooks; glossary terms
- [x] **#65** (4. Statistical integrity › Multiple comparisons) Reference-value, identity and family-boundary gates; `multiple-comparisons.md` skill

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

- [x] Adjusted values match R `p.adjust` on reference vectors for every method
- [x] Absent `multiplicity` (the default) is byte-identical to today
- [x] A `layer` family never mixes p-values across layers; `row` never across rows
- [x] Raw p-values are never modified
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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

## Shipped: deviations and decisions

Contract: `.claude/reference/execution-modes.md` (Multiplicity), `.claude/reference/predict-inspect.md` (Predict); agent skill `skills/multiple-comparisons.md`; wire shape `docs/src/contract/payload-schema.md` (Multiplicity slots); user guide `docs/src/library/multiplicity.md`; embedder rows `03-embedder-migration.md` (Changes from U13).

- **Families.** `layer`, `row`, `column`, `request`, `compose` — the planned `matrix` family waits for the matrix units (U28), and `MatrixSpec.multiplicity` is not shipped (no `MatrixSpec` exists yet).
- **A Compose family.** `compose` is new here: it pools every `compose`-family member across all slots and the Compose-host overlay layers. Every other family stays per slot.
- **ProcessChain is wired.** Each stage folds its own plan; no family spans stages and the whole-chain `ChainOverlaySpec` has no slot. A v1 stage reaches no p-value site yet (tests gated out, crosstab cannot be a stage), so the wiring is exercised by tests only.
- **Regressions out.** Coefficient p-values are not corrected; `TEST_TUKEY_HSD` is already family-wise and never joins a family (an explicit block on it is refused, an inherited one skipped).
- **Advisory is data, not a code.** `PULSE_ADVISORY_MANY_TESTS` is not added; predict reports `p_values {total, uncorrected, basis, threshold}` with `descriptor.MultiplicityTriggerThreshold` = 10, for U22 to read.
- **Row and column are coordinate-based** on the layer's own matrix (a pairwise overlay's rows are the compared groups); panel vector elements each join their cell's family; the echo carries `m_per`.
- **Reference values.** R 4.6.1 `p.adjust` fixtures (generator under `internal/processing/multiplicity/testdata/padjust_reference/`); BY to 1e-13.
- **Hidden by profile.** Feature `capability:multiplicity` hides the keys, the schema entries and predict's `p_values`.

## Handed on

| Item | Owner |
|---|---|
| Recommend / Explain consume `p_values` and `descriptor.MultiplicityTriggerThreshold` (field `p_values`, no advisory code) | U22 |
| `MatrixSpec.multiplicity` and the `matrix` family | U28 |
| Hand-built MCP tool input schemas in `internal/mcp/bind.go` do not name `multiplicity` (the payload schema does) | U34 / new (on demand) |
