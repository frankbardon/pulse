---
name: op-overlay-chisq-matrix
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_MATRIX
description: Whole-matrix χ² independence test across the host crosstab's row × column contingency table.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` (enum, required) — must be `matrix`. `Ref` (object, empty) — implicit-margin — leave empty. `Level`/`Within` must be `0`.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family, alpha}`; adds `p_adjusted` figures, raw p untouched (`multiplicity-correction`).
<!-- /feature -->

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). SCALAR-payload pattern shared by sibling χ² / post-test overlays.

## Output

SCALAR — `OverlayLayer.Payload.Shape = "scalar"`. `Payload.Scalar` carries χ²; `OverlaySummary{Statistic, PValue, Parameters["df"]}` where `df = (rows-1)*(cols-1)`. Layer `Baseline` unset (inferential — no ratio centerpoint).

<!-- generated: reading-the-output -->

## Gotchas

- Weighted host (both kinds, any source): cells are Σw. Frequency: Pearson on the Σw table. Probability: the table scaled to its Kish `n_eff` first (`PULSE_OVERLAY_EXPECTED_LOW` on the scaled expected) — a first-order Kish approximation, not Rao-Scott. `Summary.Parameters` adds `sum_weights` (+ `n_eff`). Probability host with components disabled ⇒ `PROCESSING_CONFIG` (no `n_eff` to scale by; predict AND runtime).
- Expected cell formula: `row_margin × col_margin / grand_total`. p-value via `chiSquareSurvival` — byte-equal to the χ² test on the same contingency.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer.
- Absent host cell treated as observed count of 0.
- Scope MUST be `matrix`. Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent — margins recomputed from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-row`, `op-overlay-chisq-col`, `op-test-chisq`.
