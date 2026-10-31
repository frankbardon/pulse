---
name: op-overlay-share-of-total
kind: operator
category: OVERLAY
operator: OVERLAY_SHARE_OF_TOTAL
description: Share-of-grand-total ratio — dual-shape (matrix per-cell or series per-group); raw share sums to 1.0.
type: reference
applies_to: process
examples_tags: [overlay, proportion-analysis, streaming-friendly]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`Scope` required — `cell` (MATRIX) or `group` (SERIES). `Ref.Margin.Axis` required on MATRIX dispatch (grand-axis-locked, value ignored), empty on SERIES. `Level` / `Within` must be `0`.

## Host shape

Dual-shape: **MATRIX** crosstab (`Ref.Margin` required, grand-axis) → `cell / grand_total`; **SERIES** grouped Process host (implicit total, empty `Ref`) → `group_val / total`. Over two or more `groups` the total is each partition's own — the rows sharing the leading keys `params.partition_by` names (default all but the last; `[]` = the grand total over every row), matched by key equality.

## Output

MATRIX or SERIES — raw share (no ×100). A whole matrix / complete partition sums to 1.0 within ULP. Layer `Baseline = 1`.

<!-- generated: contract -->

## Gotchas

- MATRIX: An undefined (NaN) host cell — a mean over no value, an unanswered element — is MISSING, as an absent one: the overlay cell stays absent, no warning, out of `summary.count` and of any Level / Within bucket sum.
- Streamable via SERIES dispatch — same `computeSeriesGrandTotal` accumulator as the total index; MATRIX buffers.
- `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`: MATRIX without `Ref.Margin` or with any other `Ref` arm; SERIES with any `Ref`.
- Zero total → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer (`details.partitions` on SERIES).
- Absent host coordinate → unset entry, no contribution to the grand total.
- A share, not an index (×100).
- Weighted host → weighted figure (reads the host payload).
<!-- feature: AGG_MODE -->
- Reads cell values: a code cell (`AGG_MODE` over `categorical_*` / `packed_bool` — the modal dictionary index / truth value) → `PROCESSING_CONFIG`, reason `categorical_code`, `slot: "crosstab.cell"` (+ `field`, `field_type`), predict and runtime. Use a count cell.
<!-- /feature -->

## See

- Skills: `overlay-system`, `crosstab-guide`, `op-overlay-share-of-row`, `op-overlay-index-vs-total`.
