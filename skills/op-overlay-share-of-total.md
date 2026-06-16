---
name: op-overlay-share-of-total
kind: operator
category: OVERLAY
operator: OVERLAY_SHARE_OF_TOTAL
description: Share-of-grand-total ratio — dual-shape (matrix per-cell or series per-group); raw share sums to 1.0.
type: reference
applies_to: process, compose
examples_tags: [overlay, proportion-analysis, streaming-friendly]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | `cell` (MATRIX) or `group` (SERIES). |
| `Ref.Margin.Axis` | enum | conditional | MATRIX dispatch: required (grand-axis-locked, value ignored). SERIES: leave empty. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

Dual-shape overload:
- **MATRIX** crosstab (`Ref.Margin` required, grand-axis): per-cell `cell / grand_total`.
- **SERIES** grouped Process host (implicit grand-total; empty `Ref`): per-group `group_val / grand_total`.

## Output

MATRIX or SERIES — raw share (no ×100). Whole matrix sums to 1.0; complete partition sums to 1.0 within ULP. Layer `Baseline = 1` (raw-share centerpoint).

## Gotchas

- Streamable via SERIES dispatch — same `computeSeriesGrandTotal` accumulator as `OVERLAY_INDEX_VS_TOTAL`. MATRIX is buffered.
- MATRIX dispatch: empty `Ref.Margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- SERIES dispatch: any populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- `grand_total == 0` → NaN + ONE `PULSE_OVERLAY_REF_ZERO` warning per layer.
- Absent host coordinate → unset entry; does NOT contribute to grand total.
- Distinct from `OVERLAY_INDEX_VS_TOTAL` (×100). Kind names kept distinct.

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-row`, `op-overlay-index-vs-total`.
