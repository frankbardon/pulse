```yaml
name: op-overlay-delta-vs-sibling
kind: operator
category: OVERLAY
operator: OVERLAY_DELTA_VS_SIBLING
description: Per-group additive delta against a sibling group named by (Field, Value) on the SERIES host.
type: reference
applies_to: process
examples_tags: [overlay, comparison, before-after]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each group's value minus one named group's value, in the value's own units: how far every group sits from, say, the control group.

Questions it answers:

- How much more or less revenue than the flagship store does each store bring in?
- How many points above or below the control arm is each arm's average?

Use something else:

- `OVERLAY_INDEX_VS_SIBLING` when you want a ratio rather than a gap.

## Params

`Scope` must be `group`. `Ref.Sibling.Field` (string, required) — grouper field on the host. `Ref.Sibling.Value` (string, required) — axis-key value identifying the sibling group. Other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES — grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment; the sibling resolves inside each partition. Subtractive twin of the sibling index.

## Output

SERIES — one `SeriesEntry` per host group, carrying `delta = group - sibling` on `Summary.Statistic`. Sibling group itself emits `0.0` (self-vs-self). Layer `Baseline = 0`.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- Unknown `(Field, Value)` pair → ONE `PULSE_OVERLAY_REF_UNKNOWN` per layer + NaN across entries.
- Zero sibling → no warning (subtraction is defined; delta becomes the raw group value), unlike the ratio twin.
- Absent host group → `SeriesEntry` with unset `Statistic`.
- Buffered — the sibling resolver needs materialised per-group accumulators (`ApplyOverlaysSeries`).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-sibling`](op-overlay-index-vs-sibling.md), [`op-overlay-delta-vs-margin`](op-overlay-delta-vs-margin.md).
