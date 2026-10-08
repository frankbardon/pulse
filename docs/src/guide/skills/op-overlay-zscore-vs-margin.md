```yaml
name: op-overlay-zscore-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_MARGIN
description: Per-cell standardized-margin z-score — (cell − margin) / sd where sd is the population SD within the same margin slice.
type: reference
applies_to: process, compose
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

`Scope` must be `cell`. `Ref.Margin.Axis` (enum, required) — `row` / `column` / `grand`. `Level`/`Within` must be `0`.

## Host shape

MATRIX crosstab. First non-ratio overlay — output is unitless deviation, not ratio or percentage. Sibling of the margin index (ratio) and the margin delta (additive).

## Output

MATRIX — `Cells[r][c].Value = (cell - margin) / sd`. Mirrors host RowKeys / ColumnKeys. Absent cells stay absent. Layer `Baseline = 0` (z-score centerpoint).

## Reading the output

- `cells.value`: How far each cell sits from its row, column or grand margin figure, in standard deviations of the cell values across that slice (dividing by the number of cells). 0 means the cell equals the margin figure.
  - Sign: positive means the value sits above its centre; negative means the value sits below its centre.
  - Caveat: The spread is that of the cells in the slice, not the sampling error of one cell, so a cell built from few rows can sit far out without being unusual.

## Gotchas

- Per-slice population SD via Welford recurrence. Supports all three axes (`row` / `column` / `grand`).
- `sd == 0` (constant slice) → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected slice.
- Empty `Ref.Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Population SD convention (divide by N, not N-1) — like the row z-score attribute and the total z-score overlay.
- Buffered (host crosstab path + per-slice Welford recurrence both need materialised matrix).

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-zscore-vs-total`](op-overlay-zscore-vs-total.md), [`op-overlay-index-vs-margin`](op-overlay-index-vs-margin.md).
