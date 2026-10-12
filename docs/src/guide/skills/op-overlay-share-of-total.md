```yaml
name: op-overlay-share-of-total
kind: operator
category: OVERLAY
operator: OVERLAY_SHARE_OF_TOTAL
description: Share-of-grand-total ratio — dual-shape (matrix per-cell or series per-group); raw share sums to 1.0.
type: reference
applies_to: process
examples_tags: [overlay, proportion-analysis, streaming-friendly]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each crosstab cell or group of a grouped result as a share of the grand total (0 to 1): what part of the whole each piece makes up.

Questions it answers:

- What share of all revenue does each region bring in?
- What part of all responses falls in each segment-by-answer cell?

Use something else:

- `OVERLAY_SHARE_OF_ROW` when you want the share within each row of a crosstab.

## Params

`Scope` required — `cell` (MATRIX) or `group` (SERIES). `Ref.Margin.Axis` required on MATRIX dispatch (grand-axis-locked, value ignored), empty on SERIES. `Level` / `Within` must be `0`. `params.field` (string, optional on SERIES) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

Dual-shape: **MATRIX** crosstab (`Ref.Margin` required, grand-axis) → `cell / grand_total`; **SERIES** grouped Process host (implicit total, empty `Ref`) → `group_val / total`. Over two or more `groups` the total is each partition's own — the rows sharing the leading keys `params.partition_by` names (default all but the last; `[]` = the grand total over every row), matched by key equality.

## Output

MATRIX or SERIES — raw share (no ×100). A whole matrix / complete partition sums to 1.0 within ULP. Layer `Baseline = 1`.

## Gotchas

- Streamable via SERIES dispatch — same `computeSeriesGrandTotal` accumulator as the total index; MATRIX buffers.
- Empty `Ref.Margin` on MATRIX, or a populated `Ref` on SERIES → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Zero total → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer (`details.partitions` on SERIES).
- Absent host coordinate → unset entry, no contribution to the grand total.
- A share, not an index (×100); the kind names are kept distinct.
- Weighted host → weighted figure (reads the host payload).
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-share-of-row`](op-overlay-share-of-row.md), [`op-overlay-index-vs-total`](op-overlay-index-vs-total.md).
