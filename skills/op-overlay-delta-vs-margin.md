---
name: op-overlay-delta-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_MARGIN
description: Per-cell additive delta against the matching axis margin (cell − margin).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, before-after]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `cell`. |
| `Ref.Margin.Axis` | enum | (required) | `row` / `column` / `grand`. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Family: explicit-margin (`Ref.Margin`). Subtractive sibling of `OVERLAY_INDEX_VS_MARGIN` (ratio) and `OVERLAY_ZSCORE_VS_MARGIN` (standardized). Compatible with any cell aggregator.

## Output

MATRIX — `OverlayLayer.Payload.Matrix.Cells[r][c].Value` = `cell - margin`. Mirrors host RowKeys / ColumnKeys / headers. Absent host cells stay absent on the overlay. Layer `Baseline = 0` (delta family centerpoint).

## Gotchas

- Preserves host cell's units — a $-valued `AGG_SUM` cell minus a $-valued row margin yields a $-valued deviation in the same currency.
- No division — never raises `PULSE_OVERLAY_REF_ZERO`. Distinct from `OVERLAY_INDEX_VS_MARGIN` and `OVERLAY_SHARE_OF_*` triad.
- `Axis = grand` is supported (all three axes).
- Scope MUST be `cell`. Empty `Ref.Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Buffered (inherent — host crosstab path always recomputes margins from raw rows).

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-index-vs-margin`, `op-overlay-zscore-vs-margin`.
