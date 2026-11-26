---
name: op-overlay-share-of-col
kind: operator
category: OVERLAY
operator: OVERLAY_SHARE_OF_COL
description: Per-cell share-of-column ratio (cell / col_margin) — raw share, sums to 1.0 per column.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, proportion-analysis]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`scope` must be `cell`. `ref.margin.axis` (enum, required) — must be `column`; any other axis → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime). `level`/`within` optional (default `0`): nested-axis prefix truncation of the margin denominator, each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Any other `ref` arm beside `margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab (`crosstab.matrix`). Any cell aggregator; twin of the row and grand-total shares.

## Output

MATRIX — `payload.matrix.cells[r][c].value` = `cell / col_margin` (raw ratio, no ×100); a column sums to 1 when no cell is missing. Layer `summary.baseline` = `1`.

<!-- generated: contract -->

## Gotchas

- A share, not an index (×100): the kind names are kept distinct so `share` is never read as `index/100`.
- `col_margin == 0` → `null` cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected column.
- An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of any `level` / `within` bucket sum.
- Weighted host → weighted figure (reads the host payload).
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-row`, `op-overlay-share-of-total`.
