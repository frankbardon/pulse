---
name: op-overlay-chisq-row
kind: operator
category: OVERLAY
operator: OVERLAY_CHISQ_ROW
description: Per-row χ² goodness-of-fit test across the host crosstab's contingency table.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`scope` (enum, required) — must be `row`. `ref` (object, empty) — implicit-margin — leave empty. `level`/`within` must be `0`.


## Host shape

MATRIX crosstab (`crosstab.matrix`). Any cell aggregator; row-axis twin of the column χ².

## Output

SERIES — `payload.shape` `"series"`. One `payload.series.entries[]` entry per row key carrying `summary.statistic` (row χ²), `summary.p_value`, `summary.parameters.df` = `cols - 1`. Entries align with the host's `row_keys`. `summary.baseline` unset.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) host cell is MISSING: read as an absent cell (an observation of 0), never into a total.
- Weighted host (both kinds, any source): cells are Σw; under probability each row is scaled to its margin's Kish `n_eff` (expected-low on the scaled row) — first-order Kish, not Rao-Scott. Each entry's `summary.parameters` adds that row's `sum_weights` (+ `n_eff`). Probability host with components disabled ⇒ `PROCESSING_CONFIG` (no `n_eff` to scale by; predict AND runtime).
- p-values equal every χ² test's and overlay's on the same contingency.
- Any `expected < 5` in a row emits ONE `PULSE_OVERLAY_EXPECTED_LOW` per offending row.
- A populated `ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-chisq-col`, `op-test-chisq`.
