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

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Ref.Margin.Axis` | enum | (required) | Must be `row`. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: explicit-margin (`Ref.Margin`). Structural twin of `OVERLAY_SHARE_OF_COL` / `OVERLAY_SHARE_OF_TOTAL`. Compatible with any cell aggregator.

## Output

MATRIX — `Cells[r][c].Value = cell / row_margin` (raw ratio, no ×100). Cells along a single row sum to 1.0 in the absence of missing cells. Renderers present as 100%-stacked horizontal projection. Layer `Baseline = 1`.

## Gotchas

- Distinct from `OVERLAY_INDEX_VS_MARGIN` (×100). Kind names kept distinct — author doesn't confuse `share` with `index/100`.
- `row_margin == 0` → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` warning per affected row.
- Absent host cells stay absent on the overlay.
- Scope MUST be `cell`. Empty `Ref.Margin` or non-row Axis → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (host crosstab always recomputes margins from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-col`, `op-overlay-share-of-total`.
