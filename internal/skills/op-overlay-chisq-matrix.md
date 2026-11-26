---
name: op-overlay-chisq-matrix
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_MATRIX
description: Whole-matrix χ² independence test across the host crosstab's row × column contingency table.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`scope` (enum, required) — must be `matrix`. `ref` (object, empty) — implicit-margin — leave empty. `level`/`within` must be `0`.

| Name | Type | Default | Description |
|---|---|---|---|
| `correct` | bool | `false` | Yates' correction on a 2 × 2 host (the χ² test's rule); `summary.parameters` adds `continuity_correction`. Inert on larger tables. |


## Host shape

MATRIX crosstab (`crosstab.matrix`), any cell aggregator.

## Output

SCALAR — `payload.shape` `"scalar"`. `payload.scalar` carries χ²; the layer `summary` holds `statistic`, `p_value`, `parameters.df` = `(rows-1)*(cols-1)` (+ `min` / `max` / `count` of the cells). `summary.baseline` unset (inferential).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) host cell is MISSING: read as an absent cell (an observation of 0), never into a total.
- Weighted host (both kinds, any source): cells are Σw. Frequency: Pearson on the Σw table. Probability: the table scaled to its Kish `n_eff` first (`PULSE_OVERLAY_EXPECTED_LOW` on the scaled expected) — a first-order Kish approximation, not Rao-Scott. `summary.parameters` adds `sum_weights` (+ `n_eff`). Probability host with components disabled ⇒ `PROCESSING_CONFIG` (no `n_eff` to scale by; predict AND runtime).
- Expected cell formula: `row_margin × col_margin / grand_total`. p-value equals the χ² test's on the same contingency.
- Any `expected < 5` → ONE `PULSE_OVERLAY_EXPECTED_LOW` per layer.
- A populated `ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-row`, `op-overlay-chisq-col`, `op-test-chisq`, `op-overlay-symmetry`.
