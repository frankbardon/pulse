```yaml
name: op-overlay-delta-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_MARGIN
description: Per-cell additive delta against the matching axis margin (cell − margin).
type: reference
applies_to: process
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

`scope` must be `cell`. `ref.margin.axis` (enum, required) — `row` / `column` / `grand`. `level`/`within` optional (default `0`): nested-axis prefix truncation of the margin denominator, each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Any other `ref` arm beside `margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab (`crosstab.matrix`). Any cell aggregator; subtractive sibling of the margin index and margin z-score.

## Output

MATRIX — `payload.matrix.cells[r][c].value` = `cell − margin`, on the host's keys and headers; absent host cells stay absent. Layer `summary.baseline` = `0`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count`.
- Preserves host cell's units — a $-valued summed cell minus a $-valued row margin yields a $-valued deviation in the same currency.
- No division — never raises `PULSE_OVERLAY_REF_ZERO`.
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-index-vs-margin`](op-overlay-index-vs-margin.md).
