---
name: op-overlay-zscore-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_MARGIN
description: Per-cell standardized-margin z-score — (cell − margin) / sd where sd is the population SD within the same margin slice.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, outlier-detection]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`scope` must be `cell`. `ref.margin.axis` (enum, required) — `row` / `column` / `grand`. `level`/`within` optional (default `0`): truncate the margin the cell is centred on (the SD still folds the full slice), each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Any other `ref` arm beside `margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab; output is a unitless deviation. Use a mean-like cell (mean, rate): its margin is the slice's own mean, so z says how far a cell sits from it. On a count or sum cell the margin is the slice TOTAL and every z is large and negative; take the margin index (a share) instead. Siblings: margin index (ratio), margin delta (additive).

## Output

MATRIX — `payload.matrix.cells[r][c].value` = `(cell − margin) / sd`, on the host's keys; absent cells stay absent. Layer `summary.baseline` = `0`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of its slice SD.
- `sd == 0` (constant slice) → `null` cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected slice.
- Empty `ref.margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Population SD of the slice's cells (divide by N, not N − 1), as the total z-score overlay.
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a mean cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-zscore-vs-total`, `op-overlay-index-vs-margin`.
