---
name: op-overlay-zscore-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_MARGIN
description: Per-cell standardized-margin z-score — (cell − margin) / sd where sd is the population SD within the same margin slice.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, outlier-detection]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `cell`. `Ref.Margin.Axis` (enum, required) — `row` / `column` / `grand`. `Level`/`Within` must be `0`.

## Host shape

MATRIX crosstab. First non-ratio overlay — output is unitless deviation, not ratio or percentage. Sibling of the margin index (ratio) and the margin delta (additive).

## Output

MATRIX — `Cells[r][c].Value = (cell - margin) / sd`. Mirrors host RowKeys / ColumnKeys. Absent cells stay absent. Layer `Baseline = 0` (z-score centerpoint).

<!-- generated: reading-the-output -->

## Gotchas

- Per-slice population SD via Welford recurrence. Supports all three axes (`row` / `column` / `grand`).
- `sd == 0` (constant slice) → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected slice.
- Empty `Ref.Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Population SD convention (divide by N, not N-1) — like the row z-score attribute and the total z-score overlay.
- Buffered (host crosstab path + per-slice Welford recurrence both need materialised matrix).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-zscore-vs-total`, `op-overlay-index-vs-margin`.
