---
name: op-overlay-index-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_MARGIN
description: Per-cell index against the matching axis margin (100 × cell / margin).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, comparison]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `cell`, `row`, or `column`. |
| `Ref.Margin.Axis` | enum | (required) | `row` / `column` / `grand`. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: explicit-margin (`Ref.Margin`). Ratio sibling of `OVERLAY_DELTA_VS_MARGIN` + `OVERLAY_ZSCORE_VS_MARGIN`. Foundational kind establishing the share-margin pattern.

## Output

MATRIX (cell scope) or SERIES (row/column scope). `Cells[r][c].Value = 100 × cell / margin`. Mirrors host RowKeys / ColumnKeys. Absent host cells stay absent. Layer `Baseline = 100` (ratio family centerpoint).

## Gotchas

- `margin == 0` → NaN cell + ONE `PULSE_OVERLAY_REF_ZERO` warning per affected slice (not per cell).
- All three axes supported. `Axis = grand` mirrors `OVERLAY_SHARE_OF_TOTAL × 100`.
- Empty `Ref.Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Distinct from `OVERLAY_SHARE_OF_*` (raw ratio, no ×100). Kind names kept distinct — don't authoring-confuse.
- Buffered (host crosstab always recomputes margins from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-row`, `op-overlay-delta-vs-margin`.
