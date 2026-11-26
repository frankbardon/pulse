---
name: op-overlay-delta-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_MARGIN
description: Per-cell additive delta against the matching axis margin (cell − margin).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, before-after]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`scope` must be `cell`. `ref.margin.axis` (enum, required) — `row` / `column` / `grand`. `level`/`within` optional (default `0`): nested-axis prefix truncation of the margin denominator, each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Any other `ref` arm beside `margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab (`crosstab.matrix`). Any cell aggregator; subtractive sibling of the margin index and margin z-score.

## Output

MATRIX — `payload.matrix.cells[r][c].value` = `cell − margin`, on the host's keys and headers; absent host cells stay absent. Layer `summary.baseline` = `0`.

<!-- generated: contract -->

## Gotchas

- An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count`.
- Preserves host cell's units — a $-valued summed cell minus a $-valued row margin yields a $-valued deviation in the same currency.
- No division — never raises `PULSE_OVERLAY_REF_ZERO`.
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-index-vs-margin`.
