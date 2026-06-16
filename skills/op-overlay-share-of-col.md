---
name: op-overlay-share-of-col
kind: operator
category: OVERLAY
operator: OVERLAY_SHARE_OF_COL
description: Per-cell share-of-column ratio (cell / col_margin) — raw share, sums to 1.0 per column.
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, proportion-analysis]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Ref.Margin.Axis` | enum | (required) | Must be `column`. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: explicit-margin (`Ref.Margin`). Structural twin of `OVERLAY_SHARE_OF_ROW` / `OVERLAY_SHARE_OF_TOTAL`. Compatible with any cell aggregator.

## Output

MATRIX — `Cells[r][c].Value = cell / col_margin` (raw ratio, no ×100). Cells along a single column sum to 1.0 in the absence of missing cells. Renderers present as 100%-stacked vertical projection. Layer `Baseline = 1` (raw-share centerpoint).

## Gotchas

- Distinct from `OVERLAY_INDEX_VS_MARGIN` (×100). Kind names kept distinct — author doesn't confuse `share` with `index/100`.
- `col_margin == 0` → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` warning per affected column.
- Absent host cells stay absent on the overlay.
- Scope MUST be `cell`. Empty `Ref.Margin` or non-column Axis → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (host crosstab always recomputes margins from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-row`, `op-overlay-share-of-total`.
