```yaml
name: op-overlay-delta-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_PRIOR
description: Per-point streamable additive delta against the immediately preceding point of an ordered SERIES host.
type: reference
applies_to: process
examples_tags: [overlay, time-series, trend-detection, streaming-friendly]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each point of an ordered series minus the point before it, in the value's own units: how much it changed from the previous period.

Questions it answers:

- How many more or fewer orders did we get than the month before?
- How many points did satisfaction move from each wave to the next?

Use something else:

- `OVERLAY_INDEX_VS_PRIOR` when you want the change as a ratio.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |

`scope` must be `group`. `ref.prior` (empty object) or an empty `ref`. `level`/`within` must be `0`.

## Host shape

Ordered SERIES host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Family: windowed prior (`ref.prior`).

## Output

SERIES — one entry per host group key in host order, `summary.statistic` = `point − prior`, in the metric's own units. Layer `summary.baseline` = `0`.

## Choosing between this and the index twin

Absolute-difference twin of the prior-period index (same host, ref arm and carrier; subtraction instead of division). This one answers "how much did it change", the index "what proportion of the prior is this": an index of `101.6` and a delta of `+1.5` are not interchangeable in a report.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Carries one prior value, so it streams inside the Process fold.
- First point → `statistic` `null`, no warning. A delta of `0` would CLAIM nothing changed about a comparison that was never made.
- Absent host point → no `statistic`; the next present point subtracts the last present value.
- **NEVER emits `PULSE_OVERLAY_REF_ZERO`** — unlike the index twin. A prior of exactly zero is a valid subtrahend (`value − 0 = value`); there is no denominator.
- A non-zero `ref.prior.lag`, or another `ref` arm → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (predict too).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-prior`](op-overlay-index-vs-prior.md), [`op-overlay-delta-vs-baseline`](op-overlay-delta-vs-baseline.md).
