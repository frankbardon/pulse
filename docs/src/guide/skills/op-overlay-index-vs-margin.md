```yaml
name: op-overlay-index-vs-margin
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_MARGIN
description: Per-cell index against the matching axis margin (100 × cell / margin).
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, comparison]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Index value of each crosstab cell against its row, column or grand margin (cell / margin x 100): which cells over- or under-index?

Questions it answers:

- Which segments over-index on each answer compared with the total?
- Which product-by-region cells are well above their region's average?

Use something else:

- `OVERLAY_SHARE_OF_ROW` when you want the raw share (0 to 1) of the row.

## Params

`scope` must be `cell`; any other scope → `PULSE_OVERLAY_SCOPE_UNSUPPORTED` (predict and runtime). `ref.margin.axis` (enum, required) — `row` / `column` / `grand`. `level`/`within` optional (default `0`): nested-axis prefix truncation of the margin denominator, each in `[0, axis depth)` else `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Any other `ref` arm beside `margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict and runtime).

## Host shape

MATRIX crosstab (`crosstab.matrix`). Any cell aggregator; ratio sibling of the margin delta and margin z-score.

## Output

MATRIX — `payload.matrix.cells[r][c].value` = `100 × cell / margin`, on the host's keys; absent host cells stay absent. Layer `summary.baseline` = `100`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- MATRIX: An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of any `level` / `within` bucket sum.
- `margin == 0` → `null` cell + ONE `PULSE_OVERLAY_REF_ZERO` per affected slice (not per cell).
- `axis: grand` is the grand-total share × 100.
- Empty `ref.margin` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Distinct from `OVERLAY_SHARE_OF_*` (raw ratio, no ×100).
- Weighted host → weighted figure (reads the host payload).
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-share-of-row`](op-overlay-share-of-row.md), [`op-overlay-delta-vs-margin`](op-overlay-delta-vs-margin.md).
