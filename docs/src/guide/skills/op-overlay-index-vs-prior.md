```yaml
name: op-overlay-index-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_PRIOR
description: Per-point streamable index against the immediately preceding point of an ordered SERIES host (×100).
type: reference
applies_to: process
examples_tags: [overlay, time-series, trend-detection, streaming-friendly]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each point of an ordered series as an index value against the point before it (point / prior x 100): period-on-period change.

Questions it answers:

- By how much did sales grow or shrink from each month to the next?
- Is this week's volume above or below last week's, scaled to 100?

Use something else:

- `OVERLAY_DELTA_VS_PRIOR` when you want the change in the value's own units.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`scope` must be `group`. `ref.prior` (empty object) or an empty `ref`. `level`/`within` must be `0`.

## Host shape

Ordered SERIES host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment.

## Output

SERIES — one entry per host group key in host order, `summary.statistic` = `point / prior × 100`. Layer `summary.baseline` = `100`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Carries one prior value, so it streams inside the Process fold.
- First point → `statistic` `null`, no warning (no comparison, not a zero denominator).
- Absent host point → no `statistic`; the next present point divides by the last present value.
- Zero prior → `null` + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- A non-zero `ref.prior.lag`, or another `ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict too).
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-yoy`](op-overlay-yoy.md), [`op-overlay-index-vs-rolling-mean`](op-overlay-index-vs-rolling-mean.md).
