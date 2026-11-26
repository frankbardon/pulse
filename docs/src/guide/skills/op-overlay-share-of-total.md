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

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`scope` required — `cell` (MATRIX) or `group` (SERIES). `ref.margin` required on a MATRIX host (the axis value is ignored: the grand total), an empty `ref` on SERIES. `level` / `within` are accepted and inert.

## Host shape

Dual-shape: **MATRIX** crosstab (`ref.margin` required, grand-axis) → `cell / grand_total`; **SERIES** host (a grouped Process) (implicit total, empty `ref`) → `group_val / total`. Over two or more `groups` the total is each partition's own — the rows sharing the leading keys `params.partition_by` names (default all but the last; `[]` = the grand total over every row), matched by key equality.

## Output

MATRIX or SERIES — raw share (no ×100); a whole matrix / complete partition sums to 1. Layer `summary.baseline` = `1`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- MATRIX: An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of any bucket sum.
- The SERIES form streams (the total index's accumulator); the MATRIX form buffers.
- `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`: MATRIX without `ref.margin` or with any other `ref` arm; SERIES with any `ref`.
- Zero total → `null` + ONE `PULSE_OVERLAY_REF_ZERO` per layer (`details.partitions` on SERIES).
- Absent host coordinate → no `statistic`, no part of the total.
- A share, not an index (×100).
- Weighted host → weighted figure (reads the host payload).
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.

## See

- Skills: [`overlay-system`](overlay-system.md), [`crosstab-guide`](crosstab-guide.md), [`op-overlay-share-of-row`](op-overlay-share-of-row.md), [`op-overlay-index-vs-total`](op-overlay-index-vs-total.md).
