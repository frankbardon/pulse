---
name: op-overlay-index-vs-total
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_TOTAL
description: Per-group streamable ratio index against the SERIES host's grand total (×100).
type: reference
applies_to: process
examples_tags: [overlay, comparison, streaming-friendly]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`scope` must be `group`. `ref` (object, empty) — implicit-grand-total — leave empty. `level`/`within` must be `0`.

## Host shape

SERIES host. Over two or more `groups` the total is each partition's own — the rows sharing the leading keys `params.partition_by` names (default all but the last; `[]` = the grand total over every row), matched by key equality. The grand-total share scaled ×100.

## Output

SERIES — one entry per host group key in host order, `summary.statistic` = `group / total × 100`. Layer `summary.baseline` = `100`.

<!-- generated: contract -->

## Gotchas

- Zero total → `null` entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer (`details.partitions` = raising count).
- Absent host group → no `statistic`, and no part of the total.
- Populated `ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Streamable — the total accumulates inside the Process fold.
- `total` = the sum of the scored group values in the partition (over a mean, a sum of means), after filters.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `op-overlay-share-of-total`, `op-overlay-zscore-vs-total`.
