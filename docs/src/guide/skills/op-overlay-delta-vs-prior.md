```yaml
name: op-overlay-delta-vs-prior
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_PRIOR
description: Per-point streamable windowed additive delta against the immediately preceding point of an ordered SERIES host.
type: reference
applies_to: process, compose
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

`Scope` must be `group`. `Ref.Prior` (object, empty) — implicit-default — empty `Ref` also accepted. `Level`/`Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES — ordered grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Family: windowed prior (`Ref.Prior`).

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `delta = point − prior` on `Summary.Statistic`, in the metric's own units. Layer `Baseline = 0`.

## Choosing between this and the index twin

Absolute-difference twin of the prior-period index, as the baseline delta is to the baseline index. Same host, same ref arm, same carrier — subtraction instead of division.

Pick this one for "how much did it change"; pick the index for "what proportion of the prior is this". An index of `101.6` and a delta of `+1.5` answer different questions and are not interchangeable in a report.

## Gotchas

- Single-state lag carrier (one `float64`) — streamable inside the streaming Process fold.
- First ordinal → NaN, no warning. A delta of `0` would CLAIM nothing changed about a comparison that was never made.
- Absent host point → NaN + carrier does NOT advance (next present point still subtracts the last present value).
- **NEVER emits `PULSE_OVERLAY_REF_ZERO`** — unlike the index twin. A prior of exactly zero is a valid subtrahend (`value − 0 = value`); there is no degenerate-denominator branch to hit. Its absence is deliberate, not an omission.
- `Ref.Prior.Lag` reserved for future window-N priors; v1 ships lag-1 only.
- Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-prior`](op-overlay-index-vs-prior.md), [`op-overlay-delta-vs-baseline`](op-overlay-delta-vs-baseline.md).
