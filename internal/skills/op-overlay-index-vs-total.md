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

`Scope` must be `group`. `Ref` (object, empty) — implicit-grand-total — leave empty. `Level`/`Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES — grouped Process host. Over two or more `groups` the total is each partition's own — the rows sharing the leading keys `params.partition_by` names (default all but the last; `[]` = the grand total over every row), matched by key equality. First streamable SERIES-host overlay with a streaming finalize hook. Sibling to the grand-total share (SERIES arm) — same accumulator, different scale.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `index = group_val / total × 100` (`total` = the partition's) on `Summary.Statistic`. Layer `Baseline = 100`.

<!-- generated: contract -->

## Gotchas

- Zero total → NaN entries + ONE `PULSE_OVERLAY_REF_ZERO` per layer (`details.partitions` = raising count).
- Absent host group → `SeriesEntry` with unset `Statistic` and does NOT contribute to grand total.
- Populated `Ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Streamable — one `float64` grand-total accumulator carried alongside per-group accumulators inside the streaming Process fold. Post-host finalize is the divide step.
- Sum semantics — counts post-filter rows, not pre-filter row count.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `op-overlay-share-of-total`, `op-overlay-zscore-vs-total`.
