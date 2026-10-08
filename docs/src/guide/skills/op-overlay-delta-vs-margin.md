```yaml
name: op-overlay-delta-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_MARGIN
description: Per-cell additive delta against the matching axis margin (cell − margin).
type: reference
applies_to: process, compose
examples_tags: [overlay, cross-tabulation, before-after]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each crosstab cell minus its row, column or grand margin, in the cell's own units: how far a cell sits above or below its margin.

Questions it answers:

- How many points above or below the overall average rating is each segment, per question?
- Which store-by-month cells sit furthest from their store's overall average?

Use something else:

- `OVERLAY_INDEX_VS_MARGIN` when you want a ratio rather than a gap.

## Params

`Scope` must be `cell`. `Ref.Margin.Axis` (enum, required) — `row` / `column` / `grand`. `Level`/`Within` optional (default `0`): nested-axis prefix truncation of the margin denominator, each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## Host shape

MATRIX crosstab (`Response.Crosstab.Matrix`). Subtractive sibling of the margin index (ratio) and the margin z-score (standardized). Compatible with any cell aggregator.

## Output

MATRIX — `OverlayLayer.Payload.Matrix.Cells[r][c].Value` = `cell - margin`. Mirrors host RowKeys / ColumnKeys / headers. Absent host cells stay absent on the overlay. Layer `Baseline = 0` (delta family centerpoint).

## Gotchas

- Preserves host cell's units — a $-valued summed cell minus a $-valued row margin yields a $-valued deviation in the same currency.
- No division — never raises `PULSE_OVERLAY_REF_ZERO`. Unlike the margin index and the share overlays.
- `Axis = grand` is supported (all three axes).
- Buffered (inherent — host crosstab path always recomputes margins from raw rows).

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-index-vs-margin`](op-overlay-index-vs-margin.md).
