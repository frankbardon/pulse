---
name: op-overlay-share-of-row
kind: operator
category: OVERLAY
operator: OVERLAY_SHARE_OF_ROW
description: Per-cell share-of-row ratio (cell / row_margin) — raw share, sums to 1.0 per row.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, proportion-analysis]
---

Overlays decorate the host; no `Response.Components`.

## Params

`Scope` must be `cell`. `Ref.Margin.Axis` (enum, required) — must be `row`. `Level`/`Within` must be `0`.

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: explicit-margin (`Ref.Margin`). Structural twin of the column and grand-total shares. Compatible with any cell aggregator.

## Output

MATRIX — `Cells[r][c].Value = cell / row_margin` (raw ratio, no ×100). Cells along a single row sum to 1.0 in the absence of missing cells. Renderers present as 100%-stacked horizontal projection. Layer `Baseline = 1`.

## Gotchas

- A share, not an index (×100): the kind names are kept distinct so `share` is never read as `index/100`.
- `row_margin == 0` → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected row.
- Absent host cells stay absent on the overlay.
- Scope MUST be `cell`. Empty `Ref.Margin` or non-row Axis → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (host crosstab always recomputes margins from raw rows).
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-col`, `op-overlay-share-of-total`.
