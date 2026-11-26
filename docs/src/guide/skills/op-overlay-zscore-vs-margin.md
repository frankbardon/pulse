```yaml
name: op-overlay-zscore-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_MARGIN
description: Per-cell standardized-margin z-score — (cell − margin) / sd where sd is the population SD within the same margin slice.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, outlier-detection]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each crosstab cell's gap from its row, column or grand margin, in standard deviations of the cells in that slice: which cells stand out?

Questions it answers:

- Which segment-by-question averages sit unusually far from the question's overall average?
- Which store-by-month cells stand out from their store's typical month?

Use something else:

- `OVERLAY_DELTA_VS_MARGIN` when the gap in the cell's own units is easier to explain.

## Params

`scope` must be `cell`. `ref.margin.axis` (enum, required) — `row` / `column` / `grand`. `level`/`within` optional (default `0`): truncate the margin the cell is centred on (the SD still folds the full slice), each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Any other `ref` arm beside `margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab, any cell aggregator; output is a unitless deviation. Sibling of the margin index (ratio) and the margin delta (additive).

## Output

MATRIX — `payload.matrix.cells[r][c].value` = `(cell − margin) / sd`, on the host's keys; absent cells stay absent. Layer `summary.baseline` = `0`.

## Reading the output

- `cells.value`: How far each cell sits from its row, column or grand margin figure, in standard deviations of the cell values across that slice (dividing by the number of cells). 0 means the cell equals the margin figure.
  - Sign: positive means the value sits above its centre; negative means the value sits below its centre.
  - Caveat: The spread is that of the cells in the slice, not the sampling error of one cell, so a cell built from few rows can sit far out without being unusual.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of its slice SD.
- `sd == 0` (constant slice) → `null` cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected slice.
- Empty `ref.margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Population SD of the slice's cells (divide by N, not N − 1), as the total z-score overlay.
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-zscore-vs-total`](op-overlay-zscore-vs-total.md), [`op-overlay-index-vs-margin`](op-overlay-index-vs-margin.md).
