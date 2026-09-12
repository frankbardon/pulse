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

Overlays decorate the host; no `Response.Components`.

## Params

`Scope` required — `cell` (MATRIX) or `group` (SERIES). `Ref.Margin.Axis` required on MATRIX dispatch (grand-axis-locked, value ignored), empty on SERIES. `Level` / `Within` must be `0`.

## Host shape

Dual-shape: **MATRIX** crosstab (`Ref.Margin` required, grand-axis) → per-cell `cell / grand_total`; **SERIES** grouped Process host (implicit grand-total, empty `Ref`) → per-group `group_val / grand_total`.

## Output

MATRIX or SERIES — raw share (no ×100). Whole matrix sums to 1.0; complete partition sums to 1.0 within ULP. Layer `Baseline = 1` (raw-share centerpoint).

## Gotchas

- Streamable via SERIES dispatch — same `computeSeriesGrandTotal` accumulator as `OVERLAY_INDEX_VS_TOTAL`. MATRIX is buffered.
- Empty `Ref.Margin` on MATRIX, or any populated `Ref` arm on SERIES → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- `grand_total == 0` → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- Absent host coordinate → unset entry, no contribution to the grand total.
- Distinct from `OVERLAY_INDEX_VS_TOTAL` (×100); the kind names are kept distinct.

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-row`, `op-overlay-index-vs-total`.
